// Package alerts turns the poller's unit readings into a small set of
// firing/resolved alerts, one per (rule, unit). State is in memory only: a
// restart forgets acknowledgements and mutes, and the next poll re-fires
// whatever is still wrong.
package alerts

import (
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

// Rule names, as they appear in an alert's id and rule field.
const (
	RuleDied     = "unit_died"
	RuleStuck    = "unit_stuck"
	RuleFailures = "task_failures"
	RuleLowFood  = "low_food"
	RuleSilent   = "unit_silent"
)

const (
	SevWarn = "warn"
	SevCrit = "crit"

	StateFiring   = "firing"
	StateResolved = "resolved"

	// Resolved alerts stay listed long enough to be noticed, then go, so the
	// list stays bounded by firing alerts plus a short tail.
	resolvedKeep = 10 * time.Minute
)

// Alert is one rule's verdict on one unit. It carries no coordinates and no
// operator identity, so the public console may show it.
type Alert struct {
	ID       string `json:"id"`
	Rule     string `json:"rule"`
	Unit     string `json:"unit"`
	Name     string `json:"name"`
	Severity string `json:"severity"`
	State    string `json:"state"`
	Text     string `json:"text"`
	// Since is when the alert last started firing; a re-fire after a
	// resolution moves it.
	Since      time.Time  `json:"since"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	Acked      bool       `json:"acked"`
	MutedUntil *time.Time `json:"muted_until,omitempty"`
}

func alertID(rule, unit string) string { return rule + "." + unit }

// Engine holds the alert state. Its methods are safe for concurrent use and
// are meant to be called from the poll loop, so none of them block.
type Engine struct {
	cfg Config

	mu      sync.Mutex
	started time.Time
	names   map[string]string
	alerts  map[string]*Alert
	mutes   map[string]time.Time
	// ruleMutes silence a whole alert type, including alerts that have not
	// fired yet. Memory only: a restart lifts them, the safe direction.
	ruleMutes map[string]time.Time
	// fails counts consecutive failed tasks per unit, cleared by a success.
	fails      map[string]int
	stuckSince map[string]time.Time
	version    uint64
	subs       map[chan struct{}]struct{}
}

func NewEngine(cfg Config, now time.Time) *Engine {
	return &Engine{
		cfg:        cfg,
		started:    now,
		names:      map[string]string{},
		alerts:     map[string]*Alert{},
		mutes:      map[string]time.Time{},
		ruleMutes:  map[string]time.Time{},
		fails:      map[string]int{},
		stuckSince: map[string]time.Time{},
		subs:       map[chan struct{}]struct{}{},
	}
}

// Observe counts task outcomes from the events born in one tick. Call it
// before Poll for the same tick so a failure and its alert land together.
func (e *Engine) Observe(batch []telemetry.Born) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, b := range batch {
		switch b.Kind {
		case "task_failed":
			e.fails[b.Unit]++
		case "task_done":
			delete(e.fails, b.Unit)
		}
	}
}

// Poll evaluates every rule against one tick's readings.
func (e *Engine) Poll(now time.Time, statuses []telemetry.Status, states []telemetry.UnitState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	state := make(map[string]string, len(states))
	for _, s := range states {
		state[s.ID] = s.State
	}
	changed := false
	set := func(rule string, s telemetry.Status, bad bool, sev, text string) {
		if e.setLocked(rule, s, bad, sev, text, now) {
			changed = true
		}
	}
	for _, s := range statuses {
		e.names[s.ID] = s.Name
		set(RuleDied, s, state[s.ID] == telemetry.StateDead, SevCrit, "died")

		if state[s.ID] == telemetry.StateStuck {
			if _, ok := e.stuckSince[s.ID]; !ok {
				e.stuckSince[s.ID] = now
			}
		} else {
			delete(e.stuckSince, s.ID)
		}
		since, stuck := e.stuckSince[s.ID]
		set(RuleStuck, s, stuck && now.Sub(since) >= e.cfg.StuckFor, SevWarn, "no task progress")

		n := e.fails[s.ID]
		set(RuleFailures, s, n >= e.cfg.Failures, SevCrit, strconv.Itoa(n)+" tasks failed in a row")

		low := s.Online && s.Hunger != nil && *s.Hunger <= e.cfg.HungerBelow
		food := 0
		if s.Hunger != nil {
			food = int(*s.Hunger)
		}
		set(RuleLowFood, s, low, SevWarn, "hunger low ("+strconv.Itoa(food)+")")

		seen := s.Seen
		if seen.IsZero() {
			seen = e.started
		}
		quiet := !s.Online && now.Sub(seen) >= e.cfg.SilentFor
		set(RuleSilent, s, quiet, SevWarn, "no contact for "+now.Sub(seen).Truncate(time.Second).String())
	}
	if e.pruneLocked(now) {
		changed = true
	}
	if changed {
		e.version++
		e.notifyLocked()
	}
}

// setLocked drives one (rule, unit) through firing and resolved and reports
// whether anything visible changed. A firing alert is never rewritten, which
// is what stops a flapping reading from re-announcing itself every poll.
func (e *Engine) setLocked(rule string, s telemetry.Status, bad bool, sev, text string, now time.Time) bool {
	id := alertID(rule, s.ID)
	a := e.alerts[id]
	switch {
	case bad && (a == nil || a.State == StateResolved):
		na := &Alert{ID: id, Rule: rule, Unit: s.ID, Name: s.Name, Severity: sev, State: StateFiring, Text: text, Since: now}
		if until, ok := e.mutes[id]; ok && until.After(now) {
			na.MutedUntil = &until
		}
		if until, ok := e.ruleMutes[rule]; ok && until.After(now) && (na.MutedUntil == nil || until.After(*na.MutedUntil)) {
			na.MutedUntil = &until
		}
		e.alerts[id] = na
		return true
	case !bad && a != nil && a.State == StateFiring:
		a.State, a.ResolvedAt = StateResolved, &now
		return true
	}
	return false
}

func (e *Engine) pruneLocked(now time.Time) bool {
	changed := false
	for id, a := range e.alerts {
		if a.State == StateResolved && now.Sub(*a.ResolvedAt) > resolvedKeep {
			delete(e.alerts, id)
			changed = true
		} else if a.MutedUntil != nil && !a.MutedUntil.After(now) {
			a.MutedUntil = nil
			changed = true
		}
	}
	for id, until := range e.mutes {
		if !until.After(now) {
			delete(e.mutes, id)
		}
	}
	for rule, until := range e.ruleMutes {
		if !until.After(now) {
			delete(e.ruleMutes, rule)
			changed = true
		}
	}
	return changed
}

// List returns every retained alert, firing first, then newest first.
func (e *Engine) List() []Alert {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Alert, 0, len(e.alerts))
	for _, a := range e.alerts {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].State == StateFiring) != (out[j].State == StateFiring) {
			return out[i].State == StateFiring
		}
		if !out[i].Since.Equal(out[j].Since) {
			return out[i].Since.After(out[j].Since)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Ack marks a firing alert as seen. It reports false for an unknown id.
func (e *Engine) Ack(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	a := e.alerts[id]
	if a == nil {
		return false
	}
	if !a.Acked {
		a.Acked = true
		e.version++
		e.notifyLocked()
	}
	return true
}

// Mute silences an alert's notifications until now+d; d <= 0 lifts the mute.
// The mute is kept by id, so it still holds if the alert resolves and fires
// again within the period. It reports false for an unknown id.
func (e *Engine) Mute(id string, d time.Duration, now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	a := e.alerts[id]
	if a == nil {
		return false
	}
	if d <= 0 {
		delete(e.mutes, id)
		a.MutedUntil = nil
	} else {
		if d > e.cfg.MuteMax {
			d = e.cfg.MuteMax
		}
		until := now.Add(d)
		e.mutes[id] = until
		a.MutedUntil = &until
	}
	e.version++
	e.notifyLocked()
	return true
}

var knownRules = map[string]bool{RuleDied: true, RuleStuck: true, RuleFailures: true, RuleLowFood: true, RuleSilent: true}

// SnoozeRule mutes every alert of one rule until now+d, now and as they
// fire; d <= 0 lifts it. It reports false for a rule that does not exist, so
// the set of keys stays closed.
func (e *Engine) SnoozeRule(rule string, d time.Duration, now time.Time) bool {
	if !knownRules[rule] {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if d <= 0 {
		delete(e.ruleMutes, rule)
		for id, a := range e.alerts {
			if a.Rule == rule {
				delete(e.mutes, id)
				a.MutedUntil = nil
			}
		}
	} else {
		if d > e.cfg.MuteMax {
			d = e.cfg.MuteMax
		}
		until := now.Add(d)
		e.ruleMutes[rule] = until
		for id, a := range e.alerts {
			if a.Rule == rule {
				e.mutes[id] = until
				a.MutedUntil = &until
			}
		}
	}
	e.version++
	e.notifyLocked()
	return true
}

// Snoozed returns the rules currently snoozed and when each snooze ends.
func (e *Engine) Snoozed(now time.Time) map[string]time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]time.Time{}
	for rule, until := range e.ruleMutes {
		if until.After(now) {
			out[rule] = until
		}
	}
	return out
}

// Subscribe signals whenever the list changed. The buffer holds one signal:
// a slow reader only needs to know to read the list again.
func (e *Engine) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	e.mu.Lock()
	e.subs[ch] = struct{}{}
	e.mu.Unlock()
	return ch, func() {
		e.mu.Lock()
		delete(e.subs, ch)
		e.mu.Unlock()
	}
}

func (e *Engine) notifyLocked() {
	for ch := range e.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
