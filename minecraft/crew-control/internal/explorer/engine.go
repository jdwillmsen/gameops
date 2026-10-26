package explorer

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/events"
)

// Compiled is a filter prepared once per request, so per-event matching does
// no allocation beyond what the payload search needs.
type Compiled struct {
	f     Filter
	units map[string]struct{}
	kinds map[string]struct{}
	sevs  map[string]struct{}
	text  []byte
	since time.Time
}

func set(l []string) map[string]struct{} {
	if len(l) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(l))
	for _, s := range l {
		m[s] = struct{}{}
	}
	return m
}

func (f Filter) Compile(now time.Time) Compiled {
	c := Compiled{f: f, units: set(f.Units), kinds: set(f.Kinds), sevs: set(f.Severities), since: f.Since}
	if c.since.IsZero() && f.Range > 0 {
		c.since = now.Add(-f.Range)
	}
	if f.Text != "" {
		c.text = bytes.ToLower([]byte(f.Text))
	}
	return c
}

func in(m map[string]struct{}, s string) bool {
	if m == nil {
		return true
	}
	_, ok := m[s]
	return ok
}

// Match reports whether e passes every set field.
func (c Compiled) Match(e events.Event) bool {
	if !in(c.units, e.Unit) || !in(c.kinds, e.Kind) || !in(c.sevs, e.Severity) {
		return false
	}
	if !c.since.IsZero() && e.At.Before(c.since) {
		return false
	}
	if !c.f.Until.IsZero() && e.At.After(c.f.Until) {
		return false
	}
	if c.f.Task != "" && (e.TaskID == nil || e.TaskID.String() != c.f.Task) {
		return false
	}
	if len(c.text) == 0 {
		return true
	}
	return hasFold(e.Unit, c.text) || hasFold(e.Kind, c.text) ||
		(e.TaskID != nil && hasFold(e.TaskID.String(), c.text)) ||
		hasFoldBytes(e.Payload, c.text)
}

func hasFold(s string, needle []byte) bool {
	return strings.Contains(strings.ToLower(s), string(needle))
}

// hasFoldBytes folds ASCII only, which is all the payloads' keys and the
// feed's text use, and avoids copying the payload to lower-case it.
func hasFoldBytes(hay, needle []byte) bool {
	n := len(needle)
	if n == 0 {
		return true
	}
	for i := 0; i+n <= len(hay); i++ {
		j := 0
		for ; j < n; j++ {
			b := hay[i+j]
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if b != needle[j] {
				break
			}
		}
		if j == n {
			return true
		}
	}
	return false
}

// compare orders two events by the sort key, then (At, ID) in the same
// direction, so the order is total and a cursor names exactly one position.
func (s Sort) compare(a, b events.Event) int {
	c := 0
	switch s.By {
	case ByUnit:
		c = cmp.Compare(a.Unit, b.Unit)
	case ByKind:
		c = cmp.Compare(a.Kind, b.Kind)
	case BySeverity:
		c = cmp.Compare(severityRank[a.Severity], severityRank[b.Severity])
	}
	if c == 0 {
		c = a.At.Compare(b.At)
	}
	if c == 0 {
		c = cmp.Compare(a.ID, b.ID)
	}
	if s.Desc {
		return -c
	}
	return c
}

type wireCursor struct {
	U string    `json:"u,omitempty"`
	K string    `json:"k,omitempty"`
	S string    `json:"s,omitempty"`
	A time.Time `json:"a"`
	I int64     `json:"i"`
}

// EncodeCursor carries the last row's sort key, not an offset, so a page
// stays exact while new events arrive.
func EncodeCursor(e events.Event) string {
	b, _ := json.Marshal(wireCursor{e.Unit, e.Kind, e.Severity, e.At, e.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (events.Event, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return events.Event{}, err
	}
	var w wireCursor
	if json.Unmarshal(b, &w) != nil || w.A.IsZero() || w.I == 0 {
		return events.Event{}, errors.New("not a cursor")
	}
	return events.Event{Unit: w.U, Kind: w.K, Severity: w.S, At: w.A, ID: w.I}, nil
}

// Result is one page of an explorer answer. Total counts every match, not
// just this page.
type Result struct {
	Events []events.Event
	Total  int
	Next   string
}

// Run filters all, sorts the matches and returns one keyset page.
func Run(all []events.Event, f Filter, s Sort, cursor string, limit int, now time.Time) (Result, error) {
	var after events.Event
	if cursor != "" {
		var err error
		if after, err = decodeCursor(cursor); err != nil {
			return Result{}, errors.New("bad cursor")
		}
	}
	if limit < 1 || limit > maxLim {
		limit = defaultLim
	}
	c := f.Compile(now)
	hit := make([]events.Event, 0, min(len(all), 1024))
	for _, e := range all {
		if c.Match(e) {
			hit = append(hit, e)
		}
	}
	slices.SortFunc(hit, s.compare)
	start := 0
	if cursor != "" {
		start, _ = slices.BinarySearchFunc(hit, after, func(e, t events.Event) int {
			// Strictly past the cursor: an equal row is the cursor itself.
			if s.compare(e, t) <= 0 {
				return -1
			}
			return 1
		})
	}
	end := min(start+limit, len(hit))
	res := Result{Events: hit[start:end], Total: len(hit)}
	if end < len(hit) && end > start {
		res.Next = EncodeCursor(hit[end-1])
	}
	return res, nil
}
