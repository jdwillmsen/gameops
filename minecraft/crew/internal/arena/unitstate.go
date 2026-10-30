package arena

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNoTactics is what a unit answers when its state names no tactics: a
// build from before the field existed can never confirm a profile.
var ErrNoTactics = errors.New("the unit reports no tactics")

// UnitTactics is what a unit says it is fighting with.
type UnitTactics struct {
	Profile         string     `json:"profile"`
	Hash            string     `json:"hash"`
	AppliedAt       *time.Time `json:"applied_at"`
	TrainingEnabled *bool      `json:"training_enabled"`
}

// TacticsSource reads the tuning a named unit is running right now.
type TacticsSource interface {
	Tactics(ctx context.Context, unit string) (UnitTactics, error)
}

// HTTPTactics reads tactics from each unit's /v1/state through the same
// template the damage reader takes.
type HTTPTactics struct {
	// URL is the state endpoint with {name} standing for the unit's warrior
	// name, e.g. http://crew-control/units/{name}/v1/state.
	URL    string
	Client *http.Client
}

// NewHTTPTactics checks the template once so a typo fails at start-up.
func NewHTTPTactics(template string) (*HTTPTactics, error) {
	if _, err := NewHTTPDamage(template); err != nil {
		return nil, err
	}
	return &HTTPTactics{URL: template}, nil
}

func (h *HTTPTactics) Tactics(ctx context.Context, name string) (UnitTactics, error) {
	ctx, cancel := context.WithTimeout(ctx, damageReadTimeout)
	defer cancel()
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.ReplaceAll(h.URL, "{name}", url.PathEscape(name)), nil)
	if err != nil {
		return UnitTactics{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return UnitTactics{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return UnitTactics{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	var body struct {
		Tactics *UnitTactics `json:"tactics"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxStateBody)).Decode(&body); err != nil {
		return UnitTactics{}, err
	}
	if body.Tactics == nil {
		return UnitTactics{}, ErrNoTactics
	}
	return *body.Tactics, nil
}
