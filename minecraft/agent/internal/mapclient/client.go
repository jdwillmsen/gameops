// Package mapclient tells the world map service who typed a login code, and
// who asked to be logged out of it.
package mapclient

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

// ErrUnknownCode is the map service saying the code never existed, has
// expired, or was already used. It does not say which.
var ErrUnknownCode = errors.New("map: that code is not valid or has expired")

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

func (c *Client) post(ctx context.Context, path string, payload map[string]string) (status int, detail string, err error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, strings.TrimSpace(string(msg)), nil
}
