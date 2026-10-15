// Package xbox turns a Microsoft sign-in into the XUID and gamertag of the
// Xbox profile behind it, and keeps nothing of the tokens it passes through.
//
// The chain is the one Minecraft launchers use: an OAuth 2.0 authorization
// code with PKCE for the XboxLive.signin scope, that access token for an
// Xbox Live user token, that for an XSTS token whose display claims name
// the player. Only the last step's xid and gtg leave this package.
package xbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Why a sign-in did not yield a profile; the values are the labels of the
// login metric, so each is a closed word and never text a caller chose.
const (
	ResultExchangeFailed = "exchange_failed"
	ResultNoProfile      = "no_xbox_profile"
	ResultChild          = "child_account"
	ResultRefused        = "xbox_refused"
	ResultUnavailable    = "xbox_unavailable"
	ResultBadProfile     = "bad_identity"
)

// Failure is why Resolve gave no profile. Detail is a short class of
// cause that is safe to write down: Microsoft's error code, an XErr
// number, a status. It is never a body, a token or a message Microsoft
// wrote.
type Failure struct {
	Result string
	Detail string
}

func (f *Failure) Error() string {
	if f.Detail == "" {
		return "xbox sign-in: " + f.Result
	}
	return "xbox sign-in: " + f.Result + " (" + f.Detail + ")"
}

// Endpoints are where each step is asked. They are constants of production;
// only a test builds a Client with others.
type Endpoints struct {
	Authorize, Token, UserAuth, XSTS string
}

var production = Endpoints{
	Authorize: "https://login.microsoftonline.com/consumers/oauth2/v2.0/authorize",
	Token:     "https://login.microsoftonline.com/consumers/oauth2/v2.0/token",
	UserAuth:  "https://user.auth.xboxlive.com/user/authenticate",
	XSTS:      "https://xsts.auth.xboxlive.com/xsts/authorize",
}

// Scope asks for the Xbox sign-in and nothing else. Without offline_access
// no refresh token is issued, so there is nothing long-lived to lose.
const Scope = "XboxLive.signin"

type Config struct {
	ClientID, ClientSecret string
	// RedirectURI is the registered one: the map's origin and the callback.
	RedirectURI string
}

type Client struct {
	cfg  Config
	ep   Endpoints
	http *http.Client
}

func newHTTP() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		// A redirect from any of these is not part of the protocol, and
		// following one would send a token wherever it pointed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func New(cfg Config) *Client { return &Client{cfg: cfg, ep: production, http: newHTTP()} }

// NewWithEndpoints is for tests, which stand a fake of Microsoft and Xbox
// in place of them. Nothing that starts the service calls it.
func NewWithEndpoints(cfg Config, ep Endpoints) *Client {
	return &Client{cfg: cfg, ep: ep, http: newHTTP()}
}

// AuthorizeURL is where to send the browser. prompt=select_account makes
// someone with several accounts in one browser choose.
func (c *Client) AuthorizeURL(state, challenge string) string {
	q := url.Values{
		"client_id":             {c.cfg.ClientID},
		"response_type":         {"code"},
		"redirect_uri":          {c.cfg.RedirectURI},
		"response_mode":         {"query"},
		"scope":                 {Scope},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	return c.ep.Authorize + "?" + q.Encode()
}

// Profile is who signed in.
type Profile struct {
	XUID, Gamertag string
}

const maxBody = 64 << 10

// Resolve redeems the authorization code and walks the chain. The tokens it
// handles are locals of this call: none is returned, stored or logged.
func (c *Client) Resolve(ctx context.Context, code, verifier string) (Profile, error) {
	access, err := c.redeem(ctx, code, verifier)
	if err != nil {
		return Profile{}, err
	}
	userToken, err := c.userToken(ctx, access)
	if err != nil {
		return Profile{}, err
	}
	return c.profile(ctx, userToken)
}

func (c *Client) redeem(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {c.cfg.RedirectURI},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
		"scope":         {Scope},
	}
	status, body, err := c.post(ctx, c.ep.Token, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), false)
	if err != nil {
		return "", err
	}
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorCodes  []int  `json:"error_codes"`
	}
	_ = json.Unmarshal(body, &out)
	switch {
	case status >= 500:
		return "", &Failure{ResultUnavailable, "token " + strconv.Itoa(status)}
	case status != http.StatusOK || out.AccessToken == "":
		return "", &Failure{ResultExchangeFailed, microsoftDetail(out.Error, out.ErrorCodes)}
	}
	return out.AccessToken, nil
}

var errorWord = regexp.MustCompile(`^[a-z_]{1,40}$`)

// microsoftDetail keeps Microsoft's error word and first AADSTS number,
// which say what to fix (invalid_client is the secret, invalid_grant the
// code or verifier) and carry nothing of the request.
func microsoftDetail(word string, codes []int) string {
	if !errorWord.MatchString(word) {
		word = "unknown"
	}
	if len(codes) > 0 && codes[0] > 0 {
		return word + " AADSTS" + strconv.Itoa(codes[0])
	}
	return word
}

type claimSet struct {
	Token         string `json:"Token"`
	DisplayClaims struct {
		XUI []map[string]string `json:"xui"`
	} `json:"DisplayClaims"`
	XErr json.Number `json:"XErr"`
}

func (c *Client) userToken(ctx context.Context, access string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"Properties":   map[string]string{"AuthMethod": "RPS", "SiteName": "user.auth.xboxlive.com", "RpsTicket": "d=" + access},
		"RelyingParty": "http://auth.xboxlive.com",
		"TokenType":    "JWT",
	})
	status, raw, err := c.post(ctx, c.ep.UserAuth, "application/json", bytes.NewReader(body), true)
	if err != nil {
		return "", err
	}
	var out claimSet
	_ = json.Unmarshal(raw, &out)
	switch {
	case status >= 500:
		return "", &Failure{ResultUnavailable, "user " + strconv.Itoa(status)}
	case status != http.StatusOK || out.Token == "":
		return "", &Failure{ResultRefused, "user " + strconv.Itoa(status)}
	}
	return out.Token, nil
}

const (
	xerrNoProfile = 2148916233
	xerrChild     = 2148916238
)

func (c *Client) profile(ctx context.Context, userToken string) (Profile, error) {
	body, _ := json.Marshal(map[string]any{
		"Properties":   map[string]any{"SandboxId": "RETAIL", "UserTokens": []string{userToken}},
		"RelyingParty": "http://xboxlive.com",
		"TokenType":    "JWT",
	})
	status, raw, err := c.post(ctx, c.ep.XSTS, "application/json", bytes.NewReader(body), true)
	if err != nil {
		return Profile{}, err
	}
	var out claimSet
	_ = json.Unmarshal(raw, &out)
	switch {
	case status >= 500:
		return Profile{}, &Failure{ResultUnavailable, "xsts " + strconv.Itoa(status)}
	case status == http.StatusUnauthorized:
		xerr, _ := out.XErr.Int64()
		switch xerr {
		case xerrNoProfile:
			return Profile{}, &Failure{ResultNoProfile, "XErr " + strconv.FormatInt(xerr, 10)}
		case xerrChild:
			return Profile{}, &Failure{ResultChild, "XErr " + strconv.FormatInt(xerr, 10)}
		}
		return Profile{}, &Failure{ResultRefused, "XErr " + strconv.FormatInt(xerr, 10)}
	case status != http.StatusOK:
		return Profile{}, &Failure{ResultRefused, "xsts " + strconv.Itoa(status)}
	}
	if len(out.DisplayClaims.XUI) == 0 {
		return Profile{}, &Failure{ResultBadProfile, "no xui claim"}
	}
	xui := out.DisplayClaims.XUI[0]
	return Profile{XUID: xui["xid"], Gamertag: xui["gtg"]}, nil
}

// post sends one request and reads a bounded answer. Its errors say what
// kind of failure it was and nothing of the request, whose body is a token.
func (c *Client) post(ctx context.Context, to, contentType string, body io.Reader, xbl bool) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, to, body)
	if err != nil {
		return 0, nil, &Failure{ResultUnavailable, "request"}
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if xbl {
		req.Header.Set("x-xbl-contract-version", "1")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		detail := "network"
		if errors.Is(err, context.DeadlineExceeded) {
			detail = "timeout"
		}
		return 0, nil, &Failure{ResultUnavailable, detail}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return 0, nil, &Failure{ResultUnavailable, "read"}
	}
	return resp.StatusCode, raw, nil
}
