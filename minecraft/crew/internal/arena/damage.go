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

// UnitDamage is one unit's lifetime counts: health it lost and health other
// entities lost to its swings.
type UnitDamage struct {
	Taken float64
	Dealt float64
}

// TotalsSource is a DamageSource that reads dealt as well as taken in one
// request per unit. The recorder and the duel use it when the source has it
// and fall back to Taken, leaving dealt at zero, when it has not.
type TotalsSource interface {
	DamageSource
	Totals(ctx context.Context, units []string) (map[string]UnitDamage, error)
}

// readDamage reads src's totals, with dealt zero for a source that only knows
// taken.
func readDamage(ctx context.Context, src DamageSource, units []string) (map[string]UnitDamage, error) {
	if ts, ok := src.(TotalsSource); ok {
		return ts.Totals(ctx, units)
	}
	taken, err := src.Taken(ctx, units)
	out := make(map[string]UnitDamage, len(taken))
	for name, v := range taken {
		out[name] = UnitDamage{Taken: v}
	}
	return out, err
}

// damageGrowth is what each unit's counts rose by between two readings. A unit
// missing from either reading is left out, and a count that fell means the
// unit restarted mid-fight, so that part is unknown rather than negative.
func damageGrowth(before, after map[string]UnitDamage) map[string]UnitDamage {
	out := make(map[string]UnitDamage, len(before))
	for name, was := range before {
		now, ok := after[name]
		if !ok {
			continue
		}
		var g UnitDamage
		if now.Taken > was.Taken {
			g.Taken = now.Taken - was.Taken
		}
		if now.Dealt > was.Dealt {
			g.Dealt = now.Dealt - was.Dealt
		}
		out[name] = g
	}
	return out
}

// HTTPDamage reads damage taken and dealt from each unit's /v1/state, where
// the unit serves tasks.fight.total.damage_taken and damage_dealt.
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
	totals, err := h.Totals(ctx, units)
	out := make(map[string]float64, len(totals))
	for name, d := range totals {
		out[name] = d.Taken
	}
	return out, err
}

// Totals reads each unit's lifetime counts. A unit that serves no damage_dealt
// (an older crew) reads as zero dealt.
func (h *HTTPDamage) Totals(ctx context.Context, units []string) (map[string]UnitDamage, error) {
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	out := map[string]UnitDamage{}
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

func (h *HTTPDamage) read(ctx context.Context, client *http.Client, name string) (UnitDamage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.ReplaceAll(h.URL, "{name}", url.PathEscape(name)), nil)
	if err != nil {
		return UnitDamage{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return UnitDamage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return UnitDamage{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	var body struct {
		Tasks struct {
			Fight *struct {
				Total struct {
					DamageTaken float64 `json:"damage_taken"`
					DamageDealt float64 `json:"damage_dealt"`
				} `json:"total"`
			} `json:"fight"`
		} `json:"tasks"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxStateBody)).Decode(&body); err != nil {
		return UnitDamage{}, err
	}
	if body.Tasks.Fight == nil {
		return UnitDamage{}, errors.New("the unit reports no fight stats")
	}
	return UnitDamage{Taken: body.Tasks.Fight.Total.DamageTaken, Dealt: body.Tasks.Fight.Total.DamageDealt}, nil
}

// FakeDamage is a TotalsSource with settable totals, for tests.
type FakeDamage struct {
	mu     sync.Mutex
	totals map[string]float64
	dealt  map[string]float64
	err    error
}

// SetDealt replaces a unit's dealt total. A unit with only a dealt total is
// still reported, with nothing taken.
func (f *FakeDamage) SetDealt(unit string, total float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dealt == nil {
		f.dealt = map[string]float64{}
	}
	f.dealt[unit] = total
}

// AddDealt raises a unit's dealt total.
func (f *FakeDamage) AddDealt(unit string, amount float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dealt == nil {
		f.dealt = map[string]float64{}
	}
	f.dealt[unit] += amount
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

func (f *FakeDamage) Totals(_ context.Context, units []string) (map[string]UnitDamage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]UnitDamage{}
	for _, u := range units {
		t, okT := f.totals[u]
		d, okD := f.dealt[u]
		if okT || okD {
			out[u] = UnitDamage{Taken: t, Dealt: d}
		}
	}
	return out, nil
}
