package crewexplain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	maxReplyBytes = 1 << 20
	// DefaultMaxCalls caps model calls per process so a loop or a large
	// evaluation cannot run away.
	DefaultMaxCalls = 60
	// DefaultMaxTokens caps one answer; the schema needs far less.
	DefaultMaxTokens = 700
)

var loopback = []string{"localhost", "127.0.0.1", "::1"}

// Client talks to one OpenAI-compatible chat endpoint. It sends no
// credentials and follows no redirects, and refuses hosts outside its list.
type Client struct {
	BaseURL   string
	Model     string
	MaxTokens int
	MaxCalls  int
	HTTP      *http.Client
	calls     int
}

// NewClient checks the endpoint against the host allow-list: loopback plus
// whatever the operator named, so a typo or a pasted hosted URL cannot send
// a window anywhere unintended.
func NewClient(baseURL, model string, extraHosts []string, timeout time.Duration) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("base url %q is not an http(s) URL", baseURL)
	}
	if u.User != nil {
		return nil, errors.New("base url must not carry credentials")
	}
	host := u.Hostname()
	if !slices.Contains(loopback, host) && !slices.Contains(extraHosts, host) {
		return nil, fmt.Errorf("host %q is not allowed: loopback only unless named with --allow-host", host)
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"), Model: model, MaxTokens: DefaultMaxTokens, MaxCalls: DefaultMaxCalls,
		HTTP: &http.Client{
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Reply is one raw model answer.
type Reply struct {
	Content string
	// Model is the id the server reported, which can differ from the one asked for.
	Model string
}

type chatRequest struct {
	Model          string         `json:"model"`
	Temperature    int            `json:"temperature"`
	MaxTokens      int            `json:"max_tokens"`
	Messages       []chatMessage  `json:"messages"`
	ResponseFormat map[string]any `json:"response_format"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Ask sends one window and returns the raw content.
func (c *Client) Ask(ctx context.Context, w Window) (Reply, error) {
	user, err := UserPrompt(w)
	if err != nil {
		return Reply{}, err
	}
	if c.calls >= c.MaxCalls {
		return Reply{}, fmt.Errorf("call cap of %d reached", c.MaxCalls)
	}
	c.calls++
	body, err := json.Marshal(chatRequest{
		Model: c.Model, MaxTokens: c.MaxTokens,
		Messages: []chatMessage{{"system", SystemPrompt}, {"user", user}},
		ResponseFormat: map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": "incident_draft", "strict": true, "schema": schema(),
		}},
	})
	if err != nil {
		return Reply{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Reply{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Reply{}, fmt.Errorf("model call failed: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxReplyBytes+1))
	if err != nil {
		return Reply{}, fmt.Errorf("model reply unreadable: %w", err)
	}
	if len(b) > maxReplyBytes {
		return Reply{}, errors.New("model reply exceeds the size limit")
	}
	if resp.StatusCode != http.StatusOK {
		return Reply{}, fmt.Errorf("model endpoint answered %d", resp.StatusCode)
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil || len(out.Choices) == 0 {
		return Reply{}, errors.New("model reply is not a chat completion")
	}
	return Reply{Content: out.Choices[0].Message.Content, Model: out.Model}, nil
}
