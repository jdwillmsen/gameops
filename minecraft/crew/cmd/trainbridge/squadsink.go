package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const (
	// A lab squad's id carries this mark; the squad service refuses a
	// profile for any other, and the sink refuses first so a mistyped
	// production squad never even gets a request.
	labSquadPrefix = "lab-"
	putTimeout     = 10 * time.Second
	// Enough of an error body to say why, never enough to echo a secret.
	maxErrBody = 256
)

// SquadSink PUTs a profile to the squad route the lab units poll. The route
// is per squad, so every unit of a squad shares one profile: Squads maps a
// unit to its squad and Default covers the rest. Clear PUTs the shipped
// default, since the route has no delete and the units read it back as the
// default behaviour.
type SquadSink struct {
	Base    *url.URL
	Default string
	Squads  map[string]string
	// Token is the shared unit token. It reaches the wire only as a bearer
	// header and no error carries it.
	Token  string
	Client *http.Client
}

func (s SquadSink) squadOf(unit string) string {
	if sq, ok := s.Squads[unit]; ok {
		return sq
	}
	return s.Default
}

// squadsOf is the distinct squads of the units, in first-seen order.
func (s SquadSink) squadsOf(units []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, u := range units {
		sq := s.squadOf(u)
		if !strings.HasPrefix(sq, labSquadPrefix) {
			return nil, fmt.Errorf("trainbridge: squad %q for unit %q is not a lab squad", sq, u)
		}
		if !seen[sq] {
			seen[sq] = true
			out = append(out, sq)
		}
	}
	return out, nil
}

func (s SquadSink) Push(ctx context.Context, units []string, p tactics.Profile) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	squads, err := s.squadsOf(units)
	if err != nil {
		return err
	}
	for _, sq := range squads {
		if err := s.put(ctx, sq, body); err != nil {
			return err
		}
	}
	return nil
}

func (s SquadSink) Clear(ctx context.Context, units []string) error {
	body, err := json.Marshal(tactics.NewProfile("default"))
	if err != nil {
		return err
	}
	squads, err := s.squadsOf(units)
	if err != nil {
		return err
	}
	var first error
	for _, sq := range squads {
		if err := s.put(ctx, sq, body); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s SquadSink) put(ctx context.Context, squad string, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, putTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		s.Base.JoinPath("v1", "squads", squad, "profile").String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	c := s.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		// A transport error names the URL, not the headers.
		return fmt.Errorf("trainbridge: put profile to squad %q: %w", squad, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
	return fmt.Errorf("trainbridge: put profile to squad %q: status %d: %s",
		squad, resp.StatusCode, strings.TrimSpace(string(msg)))
}
