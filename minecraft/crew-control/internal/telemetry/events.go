package telemetry

import (
	"fmt"
	"strings"
	"time"
)

const (
	feedSize = 200
	// A running task whose progress has not moved for this long is reported
	// once as stuck. Gather and haul steps normally advance within seconds.
	stuckAfter = 90 * time.Second
	// Walking this far counts as progress, so long walks are not stuck.
	movedBlocks = 3
	lowHealth   = 6
	lowHunger   = 6
)

// Event is one line of the fleet log. It carries no coordinates, so the
// public console may show it.
type Event struct {
	Seq  uint64    `json:"seq"`
	At   time.Time `json:"at"`
	Unit string    `json:"unit"`
	Name string    `json:"name"`
	// Kind is one of online, offline, died, respawned, task_started,
	// task_done, task_failed, task_cancelled, task_ended (outcome unknown),
	// stuck, low_health, low_hunger.
	Kind string `json:"kind"`
	// Level is ok, info, warn or crit; the console colours and toasts by it.
	Level string `json:"level"`
	Text  string `json:"text"`
}

// Position is where a unit stands. It is only ever sent to signed-in streams.
type Position struct {
	ID    string  `json:"id"`
	X     float32 `json:"x"`
	Y     float32 `json:"y"`
	Z     float32 `json:"z"`
	Yaw   float32 `json:"yaw"`
	Dim   int32   `json:"dim"`
	Known bool    `json:"known"`
}

// track is what the poller remembers about a unit between polls to tell what
// changed. The zero value has seen nothing, so the first poll sets a baseline
// instead of announcing every unit as newly online.
type track struct {
	seeded     bool
	online     bool
	dead       bool
	lowHP      bool
	lowFood    bool
	taskID     string
	template   string
	progress   int
	progressAt time.Time
	// anchor is where the unit stood when progressAt last moved.
	anchor      [3]float32
	anchorKnown bool
	stuck       bool
}

type recentTask struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type observation struct {
	online      bool
	dead        bool
	health      *float32
	hunger      *float32
	curID       string
	curTemplate string
	curProgress int
	pos         [3]float32
	posKnown    bool
	recent      []recentTask
}

// observe advances t to o and returns what changed. It is pure so the rules
// are testable without a poller.
func (t *track) observe(o observation, now time.Time) []Event {
	var out []Event
	add := func(kind, level, text string) {
		out = append(out, Event{At: now, Kind: kind, Level: level, Text: text})
	}
	defer func() {
		t.seeded = true
		t.online, t.dead = o.online, o.dead
	}()
	if !t.seeded {
		t.taskID, t.template, t.progress, t.progressAt = o.curID, o.curTemplate, o.curProgress, now
		t.anchor, t.anchorKnown = o.pos, o.posKnown
		t.lowHP = o.health != nil && *o.health <= lowHealth
		t.lowFood = o.hunger != nil && *o.hunger <= lowHunger
		return nil
	}
	if t.online != o.online {
		if o.online {
			add("online", "ok", "connected")
		} else {
			add("offline", "crit", "went offline")
			t.taskID, t.template, t.stuck, t.lowHP, t.lowFood = "", "", false, false, false
			return out
		}
	}
	if !o.online {
		return out
	}
	switch {
	case !t.dead && o.dead:
		add("died", "crit", "died")
	case t.dead && !o.dead:
		add("respawned", "info", "respawned")
	}

	low := o.health != nil && *o.health <= lowHealth && !o.dead
	if low && !t.lowHP {
		add("low_health", "warn", fmt.Sprintf("health low (%d)", int(*o.health)))
	}
	t.lowHP = low
	hungry := o.hunger != nil && *o.hunger <= lowHunger
	if hungry && !t.lowFood {
		add("low_hunger", "warn", fmt.Sprintf("hunger low (%d)", int(*o.hunger)))
	}
	t.lowFood = hungry

	if o.curID != t.taskID {
		if t.taskID != "" {
			out = append(out, finished(t.taskID, t.template, o.recent, now))
		}
		if o.curID != "" {
			add("task_started", "info", "started "+o.curTemplate)
		}
		t.taskID, t.template, t.progress, t.progressAt, t.stuck = o.curID, o.curTemplate, o.curProgress, now, false
		t.anchor, t.anchorKnown = o.pos, o.posKnown
		return out
	}
	if o.curID != "" {
		switch {
		case o.curProgress != t.progress || t.moved(o):
			t.progress, t.progressAt, t.stuck = o.curProgress, now, false
			t.anchor, t.anchorKnown = o.pos, o.posKnown
		case o.posKnown && !t.anchorKnown:
			t.anchor, t.anchorKnown = o.pos, true
		case !t.stuck && now.Sub(t.progressAt) >= stuckAfter:
			t.stuck = true
			add("stuck", "warn", fmt.Sprintf("%s has not progressed for %ds", t.template, int(now.Sub(t.progressAt).Seconds())))
		}
	}
	return out
}

func (t *track) moved(o observation) bool {
	if !o.posKnown || !t.anchorKnown {
		return false
	}
	dx, dy, dz := o.pos[0]-t.anchor[0], o.pos[1]-t.anchor[1], o.pos[2]-t.anchor[2]
	return dx*dx+dy*dy+dz*dz > movedBlocks*movedBlocks
}

// finished reads how a task that left "current" ended from the unit's recent
// list. A task missing from it is reported as ended without a verdict rather
// than guessed done.
func finished(id, template string, recent []recentTask, now time.Time) Event {
	for _, r := range recent {
		if r.ID != id {
			continue
		}
		switch r.State {
		case "done":
			return Event{At: now, Kind: "task_done", Level: "ok", Text: "finished " + template}
		case "cancelled":
			return Event{At: now, Kind: "task_cancelled", Level: "info", Text: "cancelled " + template}
		default:
			text := "failed " + template
			if r.Reason != "" {
				text += ": " + r.Reason
			}
			return Event{At: now, Kind: "task_failed", Level: "crit", Text: text}
		}
	}
	return Event{At: now, Kind: "task_ended", Level: "info", Text: template + " ended, outcome unknown"}
}

// tidy keeps a unit's free-text reason to one short line before it reaches
// the feed.
func tidy(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = strings.ToValidUTF8(s[:120], "")
	}
	return s
}

// Events returns the retained log, oldest first.
func (p *Poller) Events() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Event{}, p.feed...)
}

// SubscribeEvents delivers each new event. A reader that falls behind loses
// the oldest events rather than stalling the poller.
func (p *Poller) SubscribeEvents() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	p.mu.Lock()
	p.esubs[ch] = struct{}{}
	p.mu.Unlock()
	return ch, func() {
		p.mu.Lock()
		delete(p.esubs, ch)
		p.mu.Unlock()
	}
}

// Positions returns where each unit stands, in roster order.
func (p *Poller) Positions() []Position {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Position(nil), p.positions...)
}

// appendEventLocked sends only under p.mu, so the slot freed for a slow
// reader stays free.
func (p *Poller) appendEventLocked(i int, e Event) {
	p.seq++
	e.Seq = p.seq
	e.Unit = p.units[i].ID
	e.Name = p.units[i].Name
	e.Text = tidy(e.Text)
	p.feed = append(p.feed, e)
	if len(p.feed) > feedSize {
		p.feed = append([]Event(nil), p.feed[len(p.feed)-feedSize:]...)
	}
	for ch := range p.esubs {
		select {
		case ch <- e:
		default:
			select {
			case <-ch:
			default:
			}
			ch <- e
		}
	}
}
