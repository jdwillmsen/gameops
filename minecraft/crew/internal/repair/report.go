package repair

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Report is the per-run damage summary, JSON-ready so a run can be logged
// beside the director's result line.
type Report struct {
	Dry       bool   `json:"dry_run"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	Scanned   int    `json:"cells_scanned"`
	Unscanned int    `json:"cells_unscanned"`
	Damaged   int    `json:"cells_damaged"`
	Rects     int    `json:"rectangles"`
	Commands  int    `json:"commands"`
	Executed  int    `json:"commands_executed"`
	// RepairsRun counts only repair commands; Executed also counts setup and
	// teardown, which change nothing in the arena.
	RepairsRun int            `json:"repair_commands_executed"`
	ByKind     map[string]int `json:"by_kind"`
	ByZone     map[string]int `json:"by_zone"`
	Top        []Change       `json:"top_changes"`
	// ScanAt is when the world copy the scan read was finished; zero when the
	// scan came from a file.
	ScanAt time.Time `json:"scan_at,omitempty"`
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
	if !r.ScanAt.IsZero() {
		fmt.Fprintf(w, "  world copy taken %s (%s ago)\n", r.ScanAt.Format(time.RFC3339), time.Since(r.ScanAt).Round(time.Second))
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

// Summary is the report on one line of at most max bytes, worst first: the
// kinds and zones with the most damaged cells lead and the top changes follow.
// Detail goes first when the line is too long, so the totals always survive.
// It names no coordinate.
func (r Report) Summary(max int) string {
	head := fmt.Sprintf("%d of %d scanned cells damaged in %d rectangles, %d not scanned",
		r.Damaged, r.Scanned, r.Rects, r.Unscanned)
	kinds := ranked(r.ByKind)
	zones := ranked(r.ByZone)
	tops := make([]string, len(r.Top))
	for i, c := range r.Top {
		tops[i] = fmt.Sprintf("%d %s", c.Cells, c.Change)
	}
	for nTop := len(tops); ; nTop-- {
		for nZone := len(zones); nZone >= 0; nZone-- {
			s := head
			if len(kinds) > 0 {
				s += "; " + strings.Join(kinds, ", ")
			}
			if nZone > 0 {
				s += "; zones " + strings.Join(zones[:nZone], ", ")
			}
			if nTop > 0 {
				s += "; top " + strings.Join(tops[:nTop], ", ")
			}
			if len(s) <= max || (nTop == 0 && nZone == 0) {
				return clipTo(s, max)
			}
		}
	}
}

// ranked lists "name count" by count, largest first.
func ranked(m map[string]int) []string {
	keys := sortedKeys(m)
	sort.SliceStable(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] })
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = fmt.Sprintf("%s %d", k, m[k])
	}
	return out
}

func clipTo(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
