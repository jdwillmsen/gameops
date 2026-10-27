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
	"sync"
	"time"
)

const (
	maxStateBody = 1 << 20
	// damageReadTimeout bounds one reading of a squad's totals; a fight is
	// scored without damage rather than held up by a slow unit.
	damageReadTimeout = 5 * time.Second
)

// DamageSource reports how much health each named unit has lost since it
// started. Totals only grow, so the damage of a fight is the difference of two
// readings. A reading may name fewer units than asked, with an error saying
// why.
type DamageSource interface {
	Taken(ctx context.Context, units []string) (map[string]float64, error)
}

// HTTPDamage reads damage taken from each unit's /v1/state, where the unit
// serves tasks.fight.total.damage_taken.
type HTTPDamage struct {
	// URL is the state endpoint with {name} standing for the unit's warrior
	// name, e.g. http://crew-control/units/{name}/v1/state.
	URL    string
	Client *http.Client
}

// NewHTTPDamage checks the template once so a typo fails at start-up.
func NewHTTPDamage(template string) (*HTTPDamage, error) {
	if !strings.Contains(template, "{name}") {
		return nil, fmt.Errorf("status url %q has no {name} placeholder", template)
	}
	u, err := url.Parse(strings.ReplaceAll(template, "{name}", "x"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("status url %q is not an http(s) url", template)
	}
	return &HTTPDamage{URL: template}, nil
}

func (h *HTTPDamage) Taken(ctx context.Context, units []string) (map[string]float64, error) {
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	out := map[string]float64{}
	var errs []error
	for _, name := range units {
		v, err := h.read(ctx, client, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		out[name] = v
	}
	return out, errors.Join(errs...)
}

func (h *HTTPDamage) read(ctx context.Context, client *http.Client, name string) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.ReplaceAll(h.URL, "{name}", url.PathEscape(name)), nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	var body struct {
		Tasks struct {
			Fight *struct {
				Total struct {
					DamageTaken float64 `json:"damage_taken"`
				} `json:"total"`
			} `json:"fight"`
		} `json:"tasks"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxStateBody)).Decode(&body); err != nil {
		return 0, err
	}
	if body.Tasks.Fight == nil {
		return 0, errors.New("the unit reports no fight stats")
	}
	return body.Tasks.Fight.Total.DamageTaken, nil
}

// FakeDamage is a DamageSource with settable totals, for tests.
type FakeDamage struct {
	mu     sync.Mutex
	totals map[string]float64
	err    error
}

// Set replaces a unit's total.
func (f *FakeDamage) Set(unit string, total float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.totals == nil {
		f.totals = map[string]float64{}
	}
	f.totals[unit] = total
}

// Add raises a unit's total.
func (f *FakeDamage) Add(unit string, amount float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.totals == nil {
		f.totals = map[string]float64{}
	}
	f.totals[unit] += amount
}

// Fail makes every following reading fail with err and name no unit.
func (f *FakeDamage) Fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *FakeDamage) Taken(_ context.Context, units []string) (map[string]float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]float64{}
	for _, u := range units {
		if v, ok := f.totals[u]; ok {
			out[u] = v
		}
	}
	return out, nil
}
