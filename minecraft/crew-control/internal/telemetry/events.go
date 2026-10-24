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
	// Task is the work item a task event is about, when there is one. It is
	// the queue's id, which the history store keys on; the text alone cannot
	// carry it.
	Task string `json:"task,omitempty"`
	// Template and Took describe a task event for the history store only: the
	// public feed has no use for them. Took is how long the unit's own record
	// says the task ran, zero when it is not known.
	Template string        `json:"-"`
	Took     time.Duration `json:"-"`
	// Reason is the unit's own words for a failed task, untouched. Text carries
	// it too, but trimmed and behind a prefix; the fleet metrics classify this.
	Reason string `json:"-"`
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
	ID      string    `json:"id"`
	State   string    `json:"state"`
	Reason  string    `json:"reason"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// took is how long a finished task ran: its last update is the moment it
// reached its final state. Zero when the unit sent no usable times.
func (r recentTask) took() time.Duration {
	if r.Created.IsZero() || r.Updated.Before(r.Created) {
		return 0
	}
	return r.Updated.Sub(r.Created)
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
	addTask := func(kind, level, text, task, template string) {
		out = append(out, Event{At: now, Kind: kind, Level: level, Text: text, Task: task, Template: template})
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
			addTask("task_started", "info", "started "+o.curTemplate, o.curID, o.curTemplate)
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
			addTask("stuck", "warn", fmt.Sprintf("%s has not progressed for %ds", t.template, int(now.Sub(t.progressAt).Seconds())), o.curID, t.template)
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
		ev := Event{At: now, Task: id, Template: template, Took: r.took()}
		switch r.State {
		case "done":
			ev.Kind, ev.Level, ev.Text = "task_done", "ok", "finished "+template
			return ev
		case "cancelled":
			ev.Kind, ev.Level, ev.Text = "task_cancelled", "info", "cancelled "+template
			return ev
		default:
			text := "failed " + template
			if r.Reason != "" {
				text += ": " + r.Reason
			}
			ev.Kind, ev.Level, ev.Text, ev.Reason = "task_failed", "crit", text, r.Reason
			return ev
		}
	}
	return Event{At: now, Kind: "task_ended", Level: "info", Text: template + " ended, outcome unknown", Task: id, Template: template}
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

// Born is an event as it happened, with where the unit stood at the moment.
// The position is the event's context, not its content: it goes to the
// history store, never to the public feed.
type Born struct {
	Event
	Pos Position
}

// appendEventLocked sends only under p.mu, so the slot freed for a slow
// reader stays free. It returns the event as it was published, with the seq
// and identity the feed gave it, because that is the form history stores.
func (p *Poller) appendEventLocked(i int, e Event) Event {
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
	return e
}
