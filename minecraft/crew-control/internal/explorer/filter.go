// Package explorer answers the console's event explorer: filter, sort and
// search over the unit event history, saved views, and a live tail. It reads
// events only through Source, so a store that can push the filter down to a
// database replaces the in-memory scan without touching the routes.
package explorer

import (
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

const (
	// maxList bounds each multi-value filter so a crafted URL cannot make
	// every event pay for a thousand comparisons.
	maxList    = 32
	maxText    = 200
	maxRange   = events.MinRetention
	defaultLim = 100
	maxLim     = 200
)

// Sort keys. at is the default and the tiebreak of every other key.
const (
	ByAt       = "at"
	ByUnit     = "unit"
	ByKind     = "kind"
	BySeverity = "severity"
)

// Filter is what an operator narrows the history to. Each list is an OR
// within itself and the fields AND together; an empty field matches all.
type Filter struct {
	Units      []string
	Kinds      []string
	Severities []string
	Task       string
	// Text is a case-insensitive substring over unit, kind, task and payload.
	Text  string
	Since time.Time
	Until time.Time
	// Range is a window ending now. It is kept relative so a saved view
	// means "the last hour" each time it is opened, and it only applies when
	// Since is not set.
	Range time.Duration
}

type Sort struct {
	By   string
	Desc bool
}

// Params is a parsed explorer request.
type Params struct {
	Filter Filter
	Sort   Sort
	Limit  int
	Cursor string
}

func list(v url.Values, name string) ([]string, error) {
	var out []string
	for _, raw := range v[name] {
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	if len(out) > maxList {
		return nil, errors.New("too many " + name + " values")
	}
	return out, nil
}

var severityRank = map[string]int{events.SevOK: 0, events.SevInfo: 1, events.SevWarn: 2, events.SevCrit: 3}

// ParseParams reads a filter and sort from query parameters. Anything
// present but malformed is an error: a silently dropped filter would look
// like an empty history.
func ParseParams(v url.Values) (Params, error) {
	p := Params{Limit: defaultLim, Sort: Sort{By: ByAt, Desc: true}}
	var err error
	if p.Filter.Units, err = list(v, "unit"); err != nil {
		return p, err
	}
	if p.Filter.Kinds, err = list(v, "kind"); err != nil {
		return p, err
	}
	if p.Filter.Severities, err = list(v, "severity"); err != nil {
		return p, err
	}
	for _, s := range p.Filter.Severities {
		if _, ok := severityRank[s]; !ok {
			return p, errors.New("unknown severity")
		}
	}
	p.Filter.Task = strings.TrimSpace(v.Get("task"))
	p.Filter.Text = strings.TrimSpace(v.Get("q"))
	if len([]rune(p.Filter.Text)) > maxText {
		return p, errors.New("search text too long")
	}
	for name, into := range map[string]*time.Time{"since": &p.Filter.Since, "until": &p.Filter.Until} {
		if s := v.Get(name); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return p, errors.New("bad " + name)
			}
			*into = t
		}
	}
	if s := v.Get("range"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 || d > maxRange {
			return p, errors.New("bad range")
		}
		p.Filter.Range = d
	}
	if s := v.Get("sort"); s != "" {
		switch s {
		case ByAt, ByUnit, ByKind, BySeverity:
			p.Sort.By = s
		default:
			return p, errors.New("unknown sort")
		}
	}
	switch v.Get("dir") {
	case "", "desc":
		p.Sort.Desc = true
	case "asc":
		p.Sort.Desc = false
	default:
		return p, errors.New("dir must be asc or desc")
	}
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxLim {
			return p, errors.New("bad limit")
		}
		p.Limit = n
	}
	p.Cursor = v.Get("cursor")
	return p, nil
}

// Values is the canonical URL form of a filter and sort, the shape saved
// views store and the console puts in its address bar. Defaults are left out.
func (p Params) Values() url.Values {
	v := url.Values{}
	set := func(k string, l []string) {
		if len(l) > 0 {
			v.Set(k, strings.Join(l, ","))
		}
	}
	set("unit", p.Filter.Units)
	set("kind", p.Filter.Kinds)
	set("severity", p.Filter.Severities)
	if p.Filter.Task != "" {
		v.Set("task", p.Filter.Task)
	}
	if p.Filter.Text != "" {
		v.Set("q", p.Filter.Text)
	}
	if !p.Filter.Since.IsZero() {
		v.Set("since", p.Filter.Since.UTC().Format(time.RFC3339))
	}
	if !p.Filter.Until.IsZero() {
		v.Set("until", p.Filter.Until.UTC().Format(time.RFC3339))
	}
	if p.Filter.Range > 0 {
		v.Set("range", p.Filter.Range.String())
	}
	if p.Sort.By != ByAt {
		v.Set("sort", p.Sort.By)
	}
	if !p.Sort.Desc {
		v.Set("dir", "asc")
	}
	return v
}
