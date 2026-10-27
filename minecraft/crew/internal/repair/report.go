package repair

import (
	"fmt"
	"io"
	"sort"
)

// Report is the per-run damage summary, JSON-ready so a run can be logged
// beside the director's result line.
type Report struct {
	Dry       bool           `json:"dry_run"`
	Status    string         `json:"status"`
	Error     string         `json:"error,omitempty"`
	Scanned   int            `json:"cells_scanned"`
	Unscanned int            `json:"cells_unscanned"`
	Damaged   int            `json:"cells_damaged"`
	Rects     int            `json:"rectangles"`
	Commands  int            `json:"commands"`
	Executed  int            `json:"commands_executed"`
	ByKind    map[string]int `json:"by_kind"`
	ByZone    map[string]int `json:"by_zone"`
	Top       []Change       `json:"top_changes"`
}

// How a run ended. Refused means nothing was sent; aborted means some commands
// ran and the arena is partly repaired.
const (
	StatusPlanned = "planned" // a report built but not yet applied
	StatusDryRun  = "dry_run"
	StatusApplied = "applied"
	StatusAborted = "aborted"
	StatusRefused = "refused"
)

// Change is how many cells found one block where another was planned.
type Change struct {
	Change string `json:"change"` // "found -> planned"
	Cells  int    `json:"cells"`
}

const topChanges = 10

// NewReport summarises damage and the plan built from it.
func NewReport(d *Damage, p Plan) Report {
	r := Report{
		Scanned: d.Scanned, Unscanned: d.Unscanned, Damaged: d.Cells,
		Rects: len(d.Rects), Commands: len(p.Commands()),
		ByKind: d.ByKind, ByZone: d.ByZone,
	}
	for c, n := range d.ByChange {
		r.Top = append(r.Top, Change{c, n})
	}
	sort.Slice(r.Top, func(i, j int) bool {
		if r.Top[i].Cells != r.Top[j].Cells {
			return r.Top[i].Cells > r.Top[j].Cells
		}
		return r.Top[i].Change < r.Top[j].Change
	})
	if len(r.Top) > topChanges {
		r.Top = r.Top[:topChanges]
	}
	return r
}

// Write prints the report for a person.
func (r Report) Write(w io.Writer) {
	mode := r.Status
	switch r.Status {
	case StatusDryRun:
		mode = "dry run, nothing sent"
	case StatusAborted:
		mode = "ABORTED, arena partly repaired"
	case StatusRefused:
		mode = "REFUSED, nothing sent"
	case "":
		mode = StatusPlanned
	}
	fmt.Fprintf(w, "repair report (%s)\n", mode)
	if r.Error != "" {
		fmt.Fprintf(w, "  error: %s\n", r.Error)
	}
	fmt.Fprintf(w, "  scanned %d cells, %d not scanned, %d damaged in %d rectangles\n", r.Scanned, r.Unscanned, r.Damaged, r.Rects)
	fmt.Fprintf(w, "  commands %d, executed %d\n", r.Commands, r.Executed)
	for _, k := range sortedKeys(r.ByKind) {
		fmt.Fprintf(w, "  %-10s %d\n", k, r.ByKind[k])
	}
	for _, z := range sortedKeys(r.ByZone) {
		fmt.Fprintf(w, "  zone %-16s %d\n", z, r.ByZone[z])
	}
	for _, c := range r.Top {
		fmt.Fprintf(w, "  %6d  %s\n", c.Cells, c.Change)
	}
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
