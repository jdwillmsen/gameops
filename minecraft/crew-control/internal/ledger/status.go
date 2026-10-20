package ledger

import (
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

// UnitStatus is a unit's telemetry plus how much unfinished work it holds.
type UnitStatus struct {
	ID, Name string
	Online   bool
	Dead     bool
	Health   *float32
	Task     *telemetry.TaskSummary
	Seen     time.Time

	Queued, Delivered, Running int
}

// Free reports whether a planner may give the unit more work. Delivered work
// is busy like running: the unit has taken it and is about to start.
func (u UnitStatus) Free() bool {
	return u.Online && !u.Dead && u.Running == 0 && u.Delivered == 0 && u.Queued <= 1
}

// Statuses joins telemetry with each unit's unfinished work. Finished items
// in open are ignored, so a caller that passes a wider list stays correct.
func Statuses(units []telemetry.Status, open map[string][]work.Item) []UnitStatus {
	out := make([]UnitStatus, 0, len(units))
	for _, s := range units {
		u := UnitStatus{ID: s.ID, Name: s.Name, Online: s.Online, Dead: s.Dead, Health: s.Health, Task: s.Task, Seen: s.Seen}
		for _, it := range open[s.ID] {
			switch it.State {
			case work.Queued:
				u.Queued++
			case work.Delivered:
				u.Delivered++
			case work.Running:
				u.Running++
			}
		}
		out = append(out, u)
	}
	return out
}
