package events

import (
	"encoding/json"
	"fmt"
	"time"
)

// KindIdleRollup stands for a run of idle polls from one unit; see Collapser.
// It is the only kind this package defines: every other kind is whatever the
// poller derives, stored as given, so a new one is code in the producer and
// no migration (the column is deliberately not constrained).
const KindIdleRollup = "idle_rollup"

const (
	// IdleMax is how fast an idle-eligible task must finish to count as an
	// idle poll. Hauling units re-check their chests every couple of seconds
	// and, finding nothing to move, finish in a fraction of one; a haul that
	// ran longer than this moved something and is stored as itself.
	IdleMax = 2 * time.Second
	// rollupWindow bounds one roll-up row, so a unit that idles for hours
	// still produces rows a console can place on a timeline, and a crash
	// loses at most this much of a count.
	rollupWindow = time.Minute
	// holdMax is how long a task start waits for its verdict. IdleMax plus a
	// poll or two: past this the task is evidently not an idle poll.
	holdMax = 5 * time.Second
)

// idleTemplates are the task templates whose quick, successful finishes are
// polls rather than work. It is a closed list because "fast" alone is not
// idle: a goto that finishes at once did something.
var idleTemplates = map[string]bool{"haul": true}

// Hint is what the idle rule needs to know about an event beyond what a row
// stores: the task's raw id (the poller's ids are not all UUIDs, so the row's
// task_id cannot pair a start with its finish), its template, and how long it
// ran. Took is zero when unknown, and an unknown duration is never idle.
type Hint struct {
	Task     string
	Template string
	Took     time.Duration
}

func (h Hint) idleTemplate() bool { return idleTemplates[h.Template] }

// idleDone reports whether a finished-task event is an idle poll: the task
// succeeded, its template is one that polls, and it was over within IdleMax.
// A failure, a cancellation, an unknown outcome or an unknown duration is
// real information and is never folded away.
func (h Hint) idleDone(kind string) bool {
	return kind == "task_done" && h.idleTemplate() && h.Took > 0 && h.Took <= IdleMax
}

// Collapser turns repeated idle polls into one row per unit and template: the
// first poll's time as the row's time, the count, and the last poll's time in
// the payload. Without it a hauling unit would write a start and a finish
// every couple of seconds for nothing but "nothing to do", most of the table.
//
// An idle haul is only known to be idle when it finishes, so a start of an
// idle-eligible template is held until then: dropped with its finish when the
// poll was idle, released unchanged when it was not. Any other event from the
// unit closes its open roll-up first, so the stream stays in order and a
// roll-up never spans something that happened.
//
// Collapser is not safe for concurrent use; the writer's one goroutine owns it.
type Collapser struct {
	units map[string]*unitIdle
}

type unitIdle struct {
	held     *Event
	heldHint Hint
	roll     *rollup
}

type rollup struct {
	unit        string
	template    string
	first, last time.Time
	count       int
}

func NewCollapser() *Collapser { return &Collapser{units: map[string]*unitIdle{}} }

func (c *Collapser) unit(id string) *unitIdle {
	u := c.units[id]
	if u == nil {
		u = &unitIdle{}
		c.units[id] = u
	}
	return u
}

// Push takes one event and returns what is ready to store, in order. The
// event's own time (not the wall clock) places everything, so a replay of the
// same events collapses the same way.
func (c *Collapser) Push(e Event, h Hint) []Event {
	u := c.unit(e.Unit)
	var out []Event
	switch {
	case e.Kind == "task_started" && h.idleTemplate():
		// A second start while one is held means the first never finished in
		// view; it is real history, not a poll.
		out = u.release(out)
		u.held, u.heldHint = &e, h
	case h.idleDone(e.Kind):
		if u.held != nil && u.heldHint.Task == h.Task {
			u.held = nil
		} else {
			out = u.release(out)
		}
		out = u.fold(out, e, h)
	default:
		out = u.closeRoll(out)
		out = u.release(out)
		out = append(out, e)
	}
	return out
}

// Expire releases what has waited long enough as of now: a held start whose
// finish never came, and a roll-up that has run its window. The writer calls
// it on its tick, so a unit that goes quiet does not strand its last count.
func (c *Collapser) Expire(now time.Time) []Event {
	var out []Event
	for _, u := range c.units {
		if u.held != nil && now.Sub(u.held.At) >= holdMax {
			out = u.release(out)
		}
		if u.roll != nil && now.Sub(u.roll.first) >= rollupWindow {
			out = u.closeRoll(out)
		}
	}
	return out
}

// Flush releases everything still open, for shutdown.
func (c *Collapser) Flush() []Event {
	var out []Event
	for _, u := range c.units {
		out = u.closeRoll(out)
		out = u.release(out)
	}
	return out
}

func (u *unitIdle) release(out []Event) []Event {
	if u.held == nil {
		return out
	}
	// The roll-up closes before the held start, which happened after it.
	out = u.closeRoll(out)
	out = append(out, *u.held)
	u.held = nil
	return out
}

func (u *unitIdle) fold(out []Event, e Event, h Hint) []Event {
	if u.roll != nil && (u.roll.template != h.Template || e.At.Sub(u.roll.first) >= rollupWindow) {
		out = u.closeRoll(out)
	}
	if u.roll == nil {
		u.roll = &rollup{unit: e.Unit, template: h.Template, first: e.At}
	}
	u.roll.count++
	u.roll.last = e.At
	return out
}

func (u *unitIdle) closeRoll(out []Event) []Event {
	r := u.roll
	if r == nil {
		return out
	}
	u.roll = nil
	text := fmt.Sprintf("%d idle %s polls", r.count, r.template)
	payload, _ := json.Marshal(struct {
		Text     string    `json:"text"`
		Template string    `json:"template"`
		Count    int       `json:"count"`
		Last     time.Time `json:"last"`
	}{text, r.template, r.count, r.last})
	return append(out, Event{Unit: r.unit, At: r.first, Kind: KindIdleRollup, Severity: SevInfo, Payload: payload})
}
