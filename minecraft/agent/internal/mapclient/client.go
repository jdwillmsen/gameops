// Package mapclient tells the world map service who typed a login code,
// who asked to be logged out of it, and what each online player's head
// looks like, and reads back what it knows of the world's health.
package mapclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ErrUnknownCode is the map service saying the code never existed, has
// expired, or was already used. It does not say which.
var ErrUnknownCode = errors.New("map: that code is not valid or has expired")

// ErrCountReplaced is the map refusing an acknowledgement because the count
// it names is no longer the current one. The newer count may hold losses
// nobody has looked at, so the caller has to read again before retrying.
var ErrCountReplaced = errors.New("map: a newer chunk count has replaced the one acknowledged")

// ErrNoPendingPasskey is the map saying nobody started adding a passkey
// for that player in the last two minutes.
var ErrNoPendingPasskey = errors.New("map: no passkey is waiting for that player")

var (
	passkeyCodeShape  = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{6}$`)
	passkeyPrintShape = regexp.MustCompile(`^[0-9A-F]{4}-[0-9A-F]{4}$`)
)

type Client struct {
	baseURL string
	token   string
	timeout time.Duration
	http    *http.Client
}

// New builds a client for the map service's internal API. timeout bounds
// each call by itself: commands run on the process's own long-lived
// context, which would otherwise let one call hang forever.
func New(baseURL, token string, timeout time.Duration) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, timeout: timeout, http: &http.Client{
		// The map never redirects, and following one would send the token
		// to wherever it pointed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Claim reports that the player with this XUID typed code in game chat.
func (c *Client) Claim(ctx context.Context, code, xuid, gamertag string) error {
	status, detail, err := c.post(ctx, "/internal/v1/claims", map[string]string{"code": code, "xuid": xuid, "gamertag": gamertag})
	switch {
	case err != nil:
		return fmt.Errorf("map: claim: %w", err)
	case status == http.StatusNoContent:
		return nil
	case status == http.StatusNotFound:
		return ErrUnknownCode
	}
	return fmt.Errorf("map: claim: status %d: %s", status, detail)
}

// Revoke ends every map login the player with this XUID holds.
func (c *Client) Revoke(ctx context.Context, xuid string) error {
	status, detail, err := c.post(ctx, "/internal/v1/revocations", map[string]string{"xuid": xuid})
	switch {
	case err != nil:
		return fmt.Errorf("map: revoke: %w", err)
	case status == http.StatusNoContent:
		return nil
	}
	return fmt.Errorf("map: revoke: status %d: %s", status, detail)
}

// PasskeyCode asks the map for the code to whisper to the player who typed
// !map passkey. A failure quotes nothing the map said: on this path the
// map's words are a code meant for one player.
func (c *Client) PasskeyCode(ctx context.Context, xuid string) (code, fingerprint string, err error) {
	body, err := json.Marshal(map[string]string{"xuid": xuid})
	if err != nil {
		return "", "", err
	}
	status, reply, err := c.do(ctx, http.MethodPost, "/internal/v1/passkey-codes", body)
	switch {
	case err != nil:
		return "", "", fmt.Errorf("map: passkey code: %w", err)
	case status == http.StatusNotFound:
		return "", "", ErrNoPendingPasskey
	case status != http.StatusOK:
		return "", "", fmt.Errorf("map: passkey code: status %d", status)
	}
	var out struct {
		Code        string `json:"code"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(reply, &out); err != nil || !passkeyCodeShape.MatchString(out.Code) || !passkeyPrintShape.MatchString(out.Fingerprint) {
		return "", "", errors.New("map: passkey code: an answer of the wrong shape")
	}
	return out.Code, out.Fingerprint, nil
}

// Chunk is one lost chunk, at the block coordinates of its corner.
type Chunk struct {
	Dimension string `json:"dimension"`
	X         int64  `json:"x"`
	Z         int64  `json:"z"`
}

// World is the map's last chunk count. Lost, not missing, is the damage:
// the server regenerates a missing chunk from the seed as soon as a player
// walks near, so missing falls back to zero while the data stays gone.
type World struct {
	// Checked is false until the map has counted the world once.
	Checked   bool           `json:"checked"`
	CheckedAt time.Time      `json:"checkedAt"`
	Lost      map[string]int `json:"lost"`
	LostTotal int            `json:"lostTotal"`
	// LostSample is the lowest-positioned lost chunks, capped by the map.
	LostSample []Chunk `json:"lostSample"`
}

// World reads the map's last chunk count.
func (c *Client) World(ctx context.Context) (World, error) {
	status, body, err := c.do(ctx, http.MethodGet, "/internal/v1/world", nil)
	switch {
	case err != nil:
		return World{}, fmt.Errorf("map: world: %w", err)
	case status != http.StatusOK:
		return World{}, fmt.Errorf("map: world: status %d: %s", status, strings.TrimSpace(string(body)))
	}
	var w World
	if err := json.Unmarshal(body, &w); err != nil {
		return World{}, fmt.Errorf("map: world: %w", err)
	}
	return w, nil
}

// AcknowledgeWorld accepts the world as the count taken at checkedAt found
// it, which clears what that count reports lost. checkedAt must be the one
// World returned, unmodified: the map compares it exactly.
func (c *Client) AcknowledgeWorld(ctx context.Context, checkedAt time.Time) error {
	status, detail, err := c.post(ctx, "/internal/v1/world/acknowledge", map[string]string{"checkedAt": checkedAt.UTC().Format(time.RFC3339Nano)})
	switch {
	case err != nil:
		return fmt.Errorf("map: acknowledge world: %w", err)
	case status == http.StatusNoContent:
		return nil
	case status == http.StatusConflict:
		return ErrCountReplaced
	}
	return fmt.Errorf("map: acknowledge world: status %d: %s", status, detail)
}

// PlayerHead is one online player and the head cropped from their skin, as
// a PNG. A player with no head is still named: the map matches markers to
// heads by gamertag, and has to know every gamertag in use to tell when two
// players share one.
type PlayerHead struct {
	XUID     string `json:"xuid"`
	Gamertag string `json:"gamertag"`
	Head     []byte `json:"head,omitempty"`
}

// ReportHeads tells the map who is online now and what their heads look
// like. Each report replaces the last whole, so an empty one says nobody is
// being watched.
func (c *Client) ReportHeads(ctx context.Context, players []PlayerHead) error {
	if players == nil {
		players = []PlayerHead{}
	}
	body, err := json.Marshal(map[string][]PlayerHead{"players": players})
	if err != nil {
		return fmt.Errorf("map: report heads: %w", err)
	}
	status, reply, err := c.do(ctx, http.MethodPut, "/internal/v1/heads", body)
	switch {
	case err != nil:
		return fmt.Errorf("map: report heads: %w", err)
	case status == http.StatusOK:
		return nil
	}
	return fmt.Errorf("map: report heads: status %d: %s", status, strings.TrimSpace(string(reply[:min(len(reply), 4<<10)])))
}

func (c *Client) post(ctx context.Context, path string, payload map[string]string) (status int, detail string, err error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, "", err
	}
	status, msg, err := c.do(ctx, http.MethodPost, path, body)
	// Quoted into error text and logs, so held to what a refusal needs.
	return status, strings.TrimSpace(string(msg[:min(len(msg), 4<<10)])), err
}

// do sends one authenticated request and returns the reply, capped at what
// the map's own answers could ever need.
func (c *Client) do(ctx context.Context, method, path string, payload []byte) (status int, reply []byte, err error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	// A body cut short is left to the caller: the status is already known,
	// and a decode of half a body fails on its own.
	reply, _ = io.ReadAll(io.LimitReader(resp.Body, maxReply))
	return resp.StatusCode, reply, nil
}

const maxReply = 64 << 10
