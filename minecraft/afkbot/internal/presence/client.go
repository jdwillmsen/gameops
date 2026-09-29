// Package presence asks minecraft-server-agent whether this bot should be in
// the world, and holds the connect loop out of it while the answer is parked.
package presence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/jdwillmsen/minecraft-server-agent/presenceapi"
)

// Presence and error bodies are a few hundred bytes. The cap stops a
// misbehaving endpoint from making the bot buffer whatever it sends.
const maxBody = 64 << 10

// ErrInvalidResponse marks a 2xx answer this bot cannot act on. It is kept
// apart from transport errors because it means the agent is up and wrong,
// which needs a person rather than patience.
var ErrInvalidResponse = errors.New("presence api: invalid response")

// HTTPError is any status the client does not treat as success.
type HTTPError struct {
	StatusCode int
	Body       presenceapi.Error
}

func (e *HTTPError) Error() string {
	if e.Body.Code != "" {
		return fmt.Sprintf("presence api: %d %s: %s", e.StatusCode, e.Body.Code, e.Body.Message)
	}
	return fmt.Sprintf("presence api: %d", e.StatusCode)
}

// Fetched is one answer to GET /v1/actors/{id}/presence.
type Fetched struct {
	Presence    presenceapi.Presence
	ETag        string
	NotModified bool
}

// Client speaks the two routes a bot is allowed: reading its own presence
// and reporting its own status.
type Client struct {
	base    string
	actorID string
	token   string
	http    *http.Client
}

func NewClient(baseURL, actorID, token string, hc *http.Client) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), actorID: actorID, token: token, http: hc}
}

func (c *Client) endpoint(suffix string) string {
	return c.base + "/v1/actors/" + url.PathEscape(c.actorID) + "/" + suffix
}

// Fetch returns the actor's presence, or NotModified when etag still matches.
func (c *Client) Fetch(ctx context.Context, etag string) (Fetched, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("presence"), nil)
	if err != nil {
		return Fetched{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Fetched{}, fmt.Errorf("fetch presence: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return Fetched{ETag: etag, NotModified: true}, nil
	case http.StatusOK:
	default:
		return Fetched{}, readHTTPError(resp)
	}

	var p presenceapi.Presence
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&p); err != nil {
		return Fetched{}, fmt.Errorf("%w: decode presence: %v", ErrInvalidResponse, err)
	}
	if !p.Effective.Valid() {
		return Fetched{}, fmt.Errorf("%w: effective state %q", ErrInvalidResponse, p.Effective)
	}
	return Fetched{Presence: p, ETag: resp.Header.Get("ETag")}, nil
}

// Report posts what the bot is doing. Any 2xx is success.
func (c *Client) Report(ctx context.Context, st presenceapi.Status) error {
	body, err := json.Marshal(st)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("status"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("report status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return readHTTPError(resp)
	}
	return nil
}

// readHTTPError keeps the status even when the body is not the contract's
// Error shape, since a proxy in front of the agent answers with its own.
func readHTTPError(resp *http.Response) error {
	he := &HTTPError{StatusCode: resp.StatusCode}
	_ = json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&he.Body)
	return he
}
