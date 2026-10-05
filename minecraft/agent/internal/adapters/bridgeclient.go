package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BridgeClient is the shared transport every mc-console-bridge-backed
// adapter (Voice, Facts, permission resolution) sits on top of. It exists
// as one place holding the base URL, bearer token, and per-call timeout,
// so those three things aren't re-threaded through every adapter's own
// constructor.
type BridgeClient struct {
	baseURL string
	token   string
	timeout time.Duration
	http    *http.Client
}

// NewBridgeClient builds a client for mc-console-bridge's HTTP API.
// timeout bounds every individual call this client makes, independent of
// whatever context the caller passes in — a caller's longer-lived context
// (e.g. the process lifetime) must not let one bridge call hang forever.
func NewBridgeClient(baseURL, token string, timeout time.Duration) *BridgeClient {
	return &BridgeClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		timeout: timeout,
		http:    &http.Client{},
	}
}

type commandRequest struct {
	Command string `json:"command"`
}

type commandResponse struct {
	Rule   string `json:"rule"`
	Output string `json:"output"`
}

// runCommand posts cmd to POST /command and returns the bridge's response.
// The bridge itself is the allowlist authority — this makes no attempt to
// pre-validate cmd, so a refusal simply comes back as an error here.
func (c *BridgeClient) runCommand(ctx context.Context, cmd string) (commandResponse, error) {
	body, err := json.Marshal(commandRequest{Command: cmd})
	if err != nil {
		return commandResponse{}, fmt.Errorf("bridge: encode command request: %w", err)
	}

	var out commandResponse
	if err := c.do(ctx, http.MethodPost, "/command", bytes.NewReader(body), &out); err != nil {
		return commandResponse{}, err
	}
	return out, nil
}

// getPermissions fetches GET /permissions: the live permissions.json
// content as an XUID-to-permission-level map.
func (c *BridgeClient) getPermissions(ctx context.Context) (map[string]string, error) {
	var out map[string]string
	if err := c.do(ctx, http.MethodGet, "/permissions", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// statusError is a non-2xx answer from the bridge, carrying the status so a
// caller can tell a refusal apart from a failure that may still have reached
// the console. The message is unchanged from the plain error it replaces.
type statusError struct {
	status int
	err    error
}

func (e statusError) Error() string { return e.err.Error() }

// refusedBeforeConsole reports whether err is the bridge declining a command
// outright — a 4xx, which its allowlist answers with before the console is
// touched. Anything else (a timeout, a transport failure, a 5xx) leaves open
// that the command ran, and must not be treated as if it had not.
func refusedBeforeConsole(err error) bool {
	var se statusError
	if !errors.As(err, &se) {
		return false
	}
	return se.status >= 400 && se.status < 500
}

// maxErrorBodyBytes caps how much of a non-2xx response body do() reads
// into an error message, so a misbehaving bridge can't make a failure
// message unboundedly large.
const maxErrorBodyBytes = 4 << 10

// do performs one bridge HTTP call, bounded by c.timeout, with the bearer
// token attached. On success, a non-nil out is JSON-decoded from the
// response body; pass nil when the caller doesn't need the body.
func (c *BridgeClient) do(ctx context.Context, method, path string, body io.Reader, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("bridge: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("bridge: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return statusError{
			status: resp.StatusCode,
			err:    fmt.Errorf("bridge: %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg))),
		}
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("bridge: %s %s: decode response: %w", method, path, err)
	}
	return nil
}
