// Package capacity applies Little's law to the unit event stream: with tasks
// of a type arriving at rate lambda and taking W seconds on average, L =
// lambda x W units are busy on them. Summed over types and divided by the
// units that are working, that is how full the fleet is.
//
// The law needs a stationary arrival rate, so the report says when the two
// halves of the window disagree; past that point the numbers describe an
// average that never happened.
package capacity

import (
	"sort"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/unitlog"
)

// nonStationaryRatio: arrival rates in the two window halves that differ by
// more than this factor are not one steady process.
const nonStationaryRatio = 2.0

// Template is one task type's load.
type Template struct {
	Template      string  `json:"template"`
	Completed     int     `json:"completed"`
	Failed        int     `json:"failed"`
	LambdaPerMin  float64 `json:"lambda_per_min"`
	MeanDurationS float64 `json:"mean_duration_s"`
	// L is the mean number of busy units: lambda x W.
	L float64 `json:"l"`
	// FailedTimeS is time spent in failed tasks, kept apart so a flapping
	// task type shows as waste rather than as healthy load.
	FailedTimeS   float64 `json:"failed_time_s"`
	NonStationary bool    `json:"non_stationary"`
}

// Idle is polling that is not work and so never counts toward utilization.
type Idle struct {
	Template string `json:"template"`
	Count    int    `json:"count"`
}

type Report struct {
	From           time.Time  `json:"from"`
	To             time.Time  `json:"to"`
	WorkingUnits   int        `json:"working_units"`
	Templates      []Template `json:"templates"`
	Idle           []Idle     `json:"idle"`
	BusyUnits      float64    `json:"busy_units"`
	Utilization    float64    `json:"utilization"`
	NonStationary  bool       `json:"non_stationary"`
	UnknownLengths int        `json:"unknown_durations"`
	// Truncated says the window held more events than one answer reads, so
	// the newest are missing from the counts.
	Truncated bool `json:"truncated"`
}

type acc struct {
	done, failed         int
	firstHalf, secondHal int
	sumS                 float64
	known                int
	failedS              float64
}

// Compute reports the load over [from, to). Only task_done and task_failed
// events count; a unit is working if it completed anything in the window.
func Compute(from, to time.Time, es []unitlog.Event) Report {
	r := Report{From: from, To: to, Templates: []Template{}, Idle: []Idle{}}
	mid := from.Add(to.Sub(from) / 2)
	byT := map[string]*acc{}
	idle := map[string]int{}
	working := map[string]bool{}
	for _, e := range es {
		if e.At.Before(from) || !e.At.Before(to) {
			continue
		}
		if e.Kind != unitlog.KindTaskDone && e.Kind != unitlog.KindTaskFailed {
			continue
		}
		if e.Idle {
			idle[e.Template]++
			continue
		}
		a := byT[e.Template]
		if a == nil {
			a = &acc{}
			byT[e.Template] = a
		}
		if e.Kind == unitlog.KindTaskDone {
			a.done++
			working[e.Unit] = true
		} else {
			a.failed++
		}
		if e.At.Before(mid) {
			a.firstHalf++
		} else {
			a.secondHal++
		}
		if e.Duration > 0 {
			a.sumS += e.Duration.Seconds()
			a.known++
			if e.Kind == unitlog.KindTaskFailed {
				a.failedS += e.Duration.Seconds()
			}
		} else {
			r.UnknownLengths++
		}
	}
	mins := to.Sub(from).Minutes()
	r.WorkingUnits = len(working)
	for name, a := range byT {
		t := Template{Template: name, Completed: a.done, Failed: a.failed, FailedTimeS: a.failedS}
		if mins > 0 {
			t.LambdaPerMin = float64(a.done+a.failed) / mins
		}
		if a.known > 0 {
			t.MeanDurationS = a.sumS / float64(a.known)
		}
		t.L = t.LambdaPerMin * t.MeanDurationS / 60
		t.NonStationary = shifted(a.firstHalf, a.secondHal)
		r.NonStationary = r.NonStationary || t.NonStationary
		r.BusyUnits += t.L
		r.Templates = append(r.Templates, t)
	}
	sort.Slice(r.Templates, func(i, j int) bool {
		if r.Templates[i].L != r.Templates[j].L {
			return r.Templates[i].L > r.Templates[j].L
		}
		return r.Templates[i].Template < r.Templates[j].Template
	})
	if r.WorkingUnits > 0 {
		r.Utilization = r.BusyUnits / float64(r.WorkingUnits)
	}
	for name, n := range idle {
		r.Idle = append(r.Idle, Idle{name, n})
	}
	sort.Slice(r.Idle, func(i, j int) bool { return r.Idle[i].Template < r.Idle[j].Template })
	return r
}

// shifted compares counts over two equal halves, which compares rates.
func shifted(a, b int) bool {
	lo, hi := min(a, b), max(a, b)
	if hi == 0 {
		return false
	}
	if lo == 0 {
		return true
	}
	return float64(hi)/float64(lo) > nonStationaryRatio
}
