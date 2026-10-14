package operators

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrNoFile is the bridge saying permissions.json is not there. It is an
// error and not an empty list: a file deleted by hand must not make every
// admin vanish, nor be how one is removed.
var ErrNoFile = errors.New("operators: permissions.json is not there")

// Bridge reads the game's own operator list from the console bridge.
type Bridge struct {
	URL, Token string
	HTTP       *http.Client
}

func NewBridge(baseURL, token string) *Bridge {
	return &Bridge{URL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (b *Bridge) Permissions(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL+"/permissions", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNoFile
	default:
		return nil, fmt.Errorf("operators: bridge answered %d", resp.StatusCode)
	}
	var perms map[string]string
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&perms); err != nil {
		return nil, fmt.Errorf("operators: %w", err)
	}
	return perms, nil
}
