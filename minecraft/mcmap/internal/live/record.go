// Package live holds where players and mobs are right now. The positions
// come from a script pack on the server, which prints them to the console
// once a second; the console bridge keeps the latest of those lines, and
// this package turns them into one current picture per dimension for the
// page to draw.
package live

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
)

// Sentinel marks a console line as one of the pack's records and names the
// format, so another pack's output and a later format are both ignored.
const Sentinel = "MCMAP1 "

// scriptTag is what the server puts in front of everything a script prints.
const scriptTag = "[Scripting] "

const (
	// maxPayload is far above what the pack writes. The server corrupts the
	// line after one longer than 4 KB, so a longer payload is not the pack
	// working as designed and is not worth decoding.
	maxPayload = 8 << 10

	// maxParts bounds what a single record can make the store hold open. At
	// the default cap a list is under twenty parts.
	maxParts = 256

	maxText = 64

	// The limits of a counts record, well past what the pack writes: it
	// keeps a line under 4 KB, which is a few dozen players.
	maxDeltas     = 256
	maxDeltaTypes = 64
	// maxCount is more than a player does between two samples, and keeps a
	// total from being overflowed by one bad line.
	maxCount = 1_000_000

	maxHealth = 1024
	maxLevel  = 1_000_000
)

type Kind string

const (
	Players Kind = "players"
	Mobs    Kind = "mobs"
	// Tick is the pack's heartbeat, sent with every sample whether or not
	// anything is in the world, so that silence means the pipeline broke.
	Tick Kind = "tick"
	// Counts is what players did since the last such record, as amounts to
	// add to running totals. It is the only kind that is not a picture of
	// now, so it is the only one that must be applied once and no more.
	Counts Kind = "counts"
)

// Entity is one marker. The keys are short because a list of a thousand is
// written to the server's console, and again to every browser, each second.
type Entity struct {
	ID string `json:"i"`
	// Name is a player's gamertag or a mob's name tag.
	Name string `json:"n,omitempty"`
	// Type is a mob's type without the minecraft: prefix.
	Type string  `json:"t,omitempty"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Z    float64 `json:"z"`
	// Yaw is the way a player faces, in degrees.
	Yaw *float64 `json:"r,omitempty"`
	// Surface is the y of the highest block in the column the entity is
	// over, set here and never by the pack: what the page tells how high
	// above or below the ground one stands by. It is left out where the
	// column is not known and in the nether.
	Surface *int32 `json:"s,omitempty"`
	// Health, MaxHealth and Level are a player's vitals as the game has
	// them now, in half-hearts and levels. Each is left out where the pack
	// could not read it, and an older pack never sends them.
	Health    *float64 `json:"h,omitempty"`
	MaxHealth *float64 `json:"hm,omitempty"`
	Level     *int     `json:"xl,omitempty"`
}

// Delta is what one player did since the pack's last counts record. Types
// are without the minecraft: prefix.
type Delta struct {
	ID string `json:"i"`
	// Deaths is by damage cause; Kills by the type of mob killed.
	Deaths map[string]int `json:"d,omitempty"`
	Kills  map[string]int `json:"k,omitempty"`
	// Broken and Placed are exact; the maps by block type are the pack's
	// most-counted few and so can add up to less.
	Broken      int            `json:"b,omitempty"`
	BrokenTypes map[string]int `json:"bt,omitempty"`
	Placed      int            `json:"p,omitempty"`
	PlacedTypes map[string]int `json:"pt,omitempty"`
	Joins       int            `json:"j,omitempty"`
	Leaves      int            `json:"l,omitempty"`
	// OnlineSeconds is the time of the sessions that ended in this delta.
	OnlineSeconds int `json:"t,omitempty"`
}

// Record is one line from the pack.
type Record struct {
	Gen  int64
	Kind Kind
	// Dimension is empty on a heartbeat, which describes the whole sample.
	Dimension string
	// Part of Parts: a list too long for one line is split, and only the
	// whole of it is a generation.
	Part, Parts int
	// More is how many entities the pack left out at its cap.
	More  int
	Items []Entity

	// ScanMillis and IntervalMillis are the heartbeat's report of what the
	// sample cost the server and how often the pack is now sampling.
	ScanMillis, IntervalMillis float64
	// Boot names one run of the pack's script and Seq counts the counts
	// records it has printed, from 1. Together they name a line for good,
	// which is how a repeat of it is told from a new one.
	Boot, Seq int64
	Deltas    []Delta

	// SentMillis is the server's clock when the pack wrote the record, in
	// Unix milliseconds, or zero if the pack did not say.
	SentMillis int64
}

var (
	// ErrForeign is a console line that is not one of the pack's records.
	ErrForeign = errors.New("not a map record")
	errTooLong = errors.New("record over the size limit")
)

type wireEntity struct {
	ID   string   `json:"i"`
	Name string   `json:"n"`
	Type string   `json:"t"`
	X    *float64 `json:"x"`
	Y    *float64 `json:"y"`
	Z    *float64 `json:"z"`
	Yaw  *float64 `json:"r"`
	H    *float64 `json:"h"`
	HM   *float64 `json:"hm"`
	XL   *int     `json:"xl"`
}

type wireRecord struct {
	Gen      *int64          `json:"gen"`
	Kind     string          `json:"kind"`
	Dim      string          `json:"dim"`
	Part     int             `json:"part"`
	Parts    int             `json:"parts"`
	More     int             `json:"more"`
	Items    json.RawMessage `json:"items"`
	Boot     int64           `json:"boot"`
	Seq      int64           `json:"seq"`
	Scan     float64         `json:"scan"`
	Interval float64         `json:"interval"`
	Sent     int64           `json:"ts"`
}

// Parse reads one record. It takes either the whole console line or only
// what follows the sentinel, which is how the bridge hands records over. A
// line that carries no sentinel is another pack's and is refused.
//
// The server starts the line after an over-long one with a NUL byte. That
// line is otherwise intact, so NULs are removed rather than costing a
// second record; stripped reports that one was.
func Parse(line string) (rec Record, stripped bool, err error) {
	if strings.ContainsRune(line, 0) {
		line, stripped = strings.ReplaceAll(line, "\x00", ""), true
	}
	payload := strings.TrimSpace(line)
	// A payload is taken as it is and never searched: its text includes
	// names players choose, and one spelling the sentinel must not be able
	// to cut its own record in two.
	if !strings.HasPrefix(payload, "{") {
		before, after, found := strings.Cut(payload, Sentinel)
		if !found || !strings.HasSuffix(before, scriptTag) {
			return Record{}, stripped, ErrForeign
		}
		payload = after
	}
	if len(payload) > maxPayload {
		return Record{}, stripped, errTooLong
	}

	var w wireRecord
	if err := json.Unmarshal([]byte(payload), &w); err != nil {
		return Record{}, stripped, err
	}
	if w.Gen == nil || *w.Gen < 0 {
		return Record{}, stripped, errors.New("record has no generation")
	}
	rec = Record{Gen: *w.Gen, Kind: Kind(w.Kind), Dimension: w.Dim, Part: w.Part, Parts: w.Parts, More: w.More,
		ScanMillis: w.Scan, IntervalMillis: w.Interval, SentMillis: w.Sent}

	switch rec.Kind {
	case Tick:
		if !finite(rec.ScanMillis) || !finite(rec.IntervalMillis) || rec.ScanMillis < 0 || rec.IntervalMillis < 0 {
			return Record{}, stripped, errors.New("heartbeat timings are not durations")
		}
		return rec, stripped, nil
	case Counts:
		return parseCounts(rec, w.Boot, w.Seq, w.Items, stripped)
	case Players, Mobs:
	default:
		return Record{}, stripped, fmt.Errorf("unknown kind %q", w.Kind)
	}
	if !slices.Contains(render.Dimensions, rec.Dimension) {
		return Record{}, stripped, fmt.Errorf("unknown dimension %q", w.Dim)
	}
	if rec.Parts < 1 || rec.Parts > maxParts || rec.Part < 0 || rec.Part >= rec.Parts || rec.More < 0 {
		return Record{}, stripped, fmt.Errorf("part %d of %d with %d more is not a split list", rec.Part, rec.Parts, rec.More)
	}
	var items []wireEntity
	if err := json.Unmarshal(w.Items, &items); err != nil && len(w.Items) > 0 {
		return Record{}, stripped, err
	}
	rec.Items = make([]Entity, 0, len(items))
	for _, it := range items {
		// JSON has no NaN or Infinity: a script serialising one writes null,
		// which would otherwise decode as a marker at the origin.
		if it.X == nil || it.Y == nil || it.Z == nil || !finite(*it.X) || !finite(*it.Y) || !finite(*it.Z) {
			return Record{}, stripped, errors.New("entity position is not a finite number")
		}
		if it.Yaw != nil && !finite(*it.Yaw) {
			return Record{}, stripped, errors.New("entity heading is not a finite number")
		}
		if it.ID == "" || len(it.ID) > maxText {
			return Record{}, stripped, errors.New("entity has no usable id")
		}
		e := Entity{ID: it.ID, Name: clip(it.Name), Type: clip(it.Type), X: *it.X, Y: *it.Y, Z: *it.Z, Yaw: it.Yaw}
		// Vitals are extra, so one that makes no sense is left out and does
		// not cost the record its positions.
		if it.H != nil && it.HM != nil && finite(*it.H) && finite(*it.HM) && *it.H >= 0 && *it.HM > 0 && *it.HM <= maxHealth {
			e.Health, e.MaxHealth = it.H, it.HM
		}
		if it.XL != nil && *it.XL >= 0 && *it.XL <= maxLevel {
			e.Level = it.XL
		}
		rec.Items = append(rec.Items, e)
	}
	return rec, stripped, nil
}

func parseCounts(rec Record, boot, seq int64, raw json.RawMessage, stripped bool) (Record, bool, error) {
	if boot <= 0 || seq <= 0 {
		return Record{}, stripped, errors.New("counts record has no boot and sequence")
	}
	var deltas []Delta
	if err := json.Unmarshal(raw, &deltas); err != nil {
		return Record{}, stripped, err
	}
	if len(deltas) > maxDeltas {
		return Record{}, stripped, fmt.Errorf("counts record holds %d players", len(deltas))
	}
	for i := range deltas {
		d := &deltas[i]
		if d.ID == "" || len(d.ID) > maxText {
			return Record{}, stripped, errors.New("counts have no usable player id")
		}
		for _, n := range []int{d.Broken, d.Placed, d.Joins, d.Leaves, d.OnlineSeconds} {
			if n < 0 || n > maxCount {
				return Record{}, stripped, errors.New("a count is not a plausible amount")
			}
		}
		for _, m := range []map[string]int{d.Deaths, d.Kills, d.BrokenTypes, d.PlacedTypes} {
			if len(m) > maxDeltaTypes {
				return Record{}, stripped, errors.New("counts name too many types")
			}
			for name, n := range m {
				if name == "" || len(name) > maxText || n < 0 || n > maxCount {
					return Record{}, stripped, errors.New("a count is not a plausible amount")
				}
			}
		}
	}
	rec.Boot, rec.Seq, rec.Deltas = boot, seq, deltas
	return rec, stripped, nil
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// clip keeps a name a player chose from being arbitrarily long on the way
// to every browser. It cuts on a rune boundary.
func clip(s string) string {
	if len(s) <= maxText {
		return s
	}
	n := maxText
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
