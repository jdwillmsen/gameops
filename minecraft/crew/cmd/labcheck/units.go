package main

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

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
)

const (
	stateReadTimeout = 5 * time.Second
	maxStateBody     = 1 << 20
)

// UnitState is the part of a unit's /v1/state the checks read. A pointer or a
// nil Seen is a field the unit's build does not serve.
type UnitState struct {
	// SprintTicks and Corrections are the unit's lifetime totals.
	SprintTicks *uint64
	Corrections *uint64
	Seen        *life.Sightings
}

// UnitSource reads a named unit's state. It is read-only: crew-control's unit
// proxy serves only GET, and so does the HTTP implementation.
type UnitSource interface {
	State(ctx context.Context, unit string) (UnitState, error)
}

// HTTPUnits reads /v1/state through a template with {name} for the unit.
type HTTPUnits struct {
	URL    string
	Client *http.Client
}

// NewHTTPUnits checks the template once, with the same rules the duel's damage
// reader applies, so a typo fails at start-up.
func NewHTTPUnits(template string) (*HTTPUnits, error) {
	if _, err := arena.NewHTTPDamage(template); err != nil {
		return nil, err
	}
	return &HTTPUnits{URL: template}, nil
}

func (h *HTTPUnits) State(ctx context.Context, unit string) (UnitState, error) {
	ctx, cancel := context.WithTimeout(ctx, stateReadTimeout)
	defer cancel()
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.ReplaceAll(h.URL, "{name}", url.PathEscape(unit)), nil)
	if err != nil {
		return UnitState{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return UnitState{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return UnitState{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	var body struct {
		Tasks struct {
			Fight *struct {
				Total struct {
					SprintTicks *uint64 `json:"sprint_ticks"`
					Corrections *uint64 `json:"movement_corrections"`
				} `json:"total"`
			} `json:"fight"`
		} `json:"tasks"`
		Seen *life.Sightings `json:"seen"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxStateBody)).Decode(&body); err != nil {
		return UnitState{}, err
	}
	if body.Tasks.Fight == nil && body.Seen == nil {
		return UnitState{}, errors.New("the unit serves neither fight stats nor sightings")
	}
	st := UnitState{Seen: body.Seen}
	if f := body.Tasks.Fight; f != nil {
		st.SprintTicks, st.Corrections = f.Total.SprintTicks, f.Total.Corrections
	}
	return st, nil
}
