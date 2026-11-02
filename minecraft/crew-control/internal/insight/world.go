package insight

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/unitlog"
)

// maxEvidence bounds the event ids a world finding carries.
const maxEvidence = 20

// worldsOf resolves every unit that appears in the input to a world.
func worldsOf(in Input) map[string]string {
	out := map[string]string{}
	for u, w := range in.Worlds {
		if w != "" {
			out[u] = w
		}
	}
	for _, e := range in.Events {
		if _, ok := out[e.Unit]; !ok && e.World != "" {
			out[e.Unit] = e.World
		}
	}
	return out
}

// onlineHours is how long a unit was connected in [start, end]. Its state at
// the start is read from the connection events; a unit with none is taken as
// connected throughout, because a connected unit with nothing to do writes no
// event at all. es must be that unit's events, oldest first.
func onlineHours(es []unitlog.Event, start, end time.Time) float64 {
	var conn []unitlog.Event
	for _, e := range es {
		if (e.Kind == unitlog.KindOnline || e.Kind == unitlog.KindOffline) && !e.At.After(end) {
			conn = append(conn, e)
		}
	}
	online, known := true, false
	for _, e := range conn {
		if e.At.After(start) {
			if !known {
				// With nothing before the window, the first change inside it
				// says what came before it.
				online = e.Kind == unitlog.KindOffline
			}
			break
		}
		online, known = e.Kind == unitlog.KindOnline, true
	}
	var total time.Duration
	at := start
	for _, e := range conn {
		if !e.At.After(start) {
			continue
		}
		if online {
			total += e.At.Sub(at)
		}
		at, online = e.At, e.Kind == unitlog.KindOnline
	}
	if online {
		total += end.Sub(at)
	}
	return total.Hours()
}

type worldRate struct {
	world string
	rate  float64
	done  int
}

// WorldDivergence is R6. Worlds are compared on one template at a time,
// completions per connected unit-hour, because a world is not the same task
// mix as another and a rate over every template would flag the mix. It is
// silent unless two other worlds qualify, so a lone world, or a pair, never
// has a verdict: there is no "the others" to diverge from.
func WorldDivergence(cfg Config, in Input) []Finding {
	worldOf := worldsOf(in)
	members := map[string][]string{}
	for u, w := range worldOf {
		members[w] = append(members[w], u)
	}
	if len(members) < 3 {
		return nil
	}
	start := in.Now.Add(-cfg.R6Window)
	units := byUnit(in.Events)

	hours := map[string]float64{}
	for w, us := range members {
		for _, u := range us {
			hours[w] += onlineHours(units[u], start, in.Now)
		}
	}

	type key struct{ world, template string }
	done := map[key]int{}
	evidence := map[key][]int64{}
	templates := map[string]bool{}
	for _, e := range in.Events {
		w, ok := worldOf[e.Unit]
		if !ok || !e.Useful() || e.Template == "" || e.At.Before(start) || e.At.After(in.Now) {
			continue
		}
		k := key{w, e.Template}
		done[k]++
		evidence[k] = append(evidence[k], e.ID)
		templates[e.Template] = true
	}

	worlds := make([]string, 0, len(members))
	for w := range members {
		worlds = append(worlds, w)
	}
	sort.Strings(worlds)
	tpls := make([]string, 0, len(templates))
	for t := range templates {
		tpls = append(tpls, t)
	}
	sort.Strings(tpls)

	type verdict struct {
		template     string
		rate, median float64
		others       int
	}
	found := map[string][]verdict{}
	for _, t := range tpls {
		for _, w := range worlds {
			if hours[w] < cfg.R6MinUnitHours {
				continue
			}
			var others []float64
			for _, o := range worlds {
				if o != w && hours[o] >= cfg.R6MinUnitHours && done[key{o, t}] >= cfg.R6MinDone {
					others = append(others, float64(done[key{o, t}])/hours[o])
				}
			}
			if len(others) < 2 {
				continue
			}
			med := median(others)
			rate := float64(done[key{w, t}]) / hours[w]
			diff := rate - med
			if diff < 0 {
				diff = -diff
			}
			if diff >= cfg.R6MinDelta && (rate < med*(1-cfg.R6Band) || rate > med*(1+cfg.R6Band)) {
				found[w] = append(found[w], verdict{t, rate, med, len(others)})
			}
		}
	}

	var out []Finding
	for _, w := range worlds {
		vs := found[w]
		if len(vs) == 0 {
			continue
		}
		names := make([]string, len(vs))
		below, above := 0, 0
		detail := make([]map[string]any, len(vs))
		var ev []int64
		for i, v := range vs {
			names[i] = v.template
			if v.rate < v.median {
				below++
			} else {
				above++
			}
			detail[i] = map[string]any{"template": v.template, "rate": v.rate, "median_of_others": v.median, "other_worlds": v.others}
			ev = append(ev, evidence[key{w, v.template}]...)
		}
		slices.Sort(ev)
		if len(ev) > maxEvidence {
			ev = ev[len(ev)-maxEvidence:]
		}
		dir := "differs from"
		switch {
		case above == 0:
			dir = "is well below"
		case below == 0:
			dir = "is well above"
		}
		out = append(out, Finding{
			Rule: RuleWorldDiverges, World: w, Severity: SevWarn, Evidence: ev,
			Detail: fmt.Sprintf("%s completions per unit-hour %s the other worlds for the last hour", strings.Join(names, ", "), dir),
			Attrs: map[string]any{
				"window_s": int(cfg.R6Window / time.Second), "unit_hours": hours[w], "templates": detail,
			},
		})
	}
	return out
}
