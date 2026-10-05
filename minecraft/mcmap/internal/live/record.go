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
)

type Kind string

const (
	Players Kind = "players"
	Mobs    Kind = "mobs"
	// Tick is the pack's heartbeat, sent with every sample whether or not
	// anything is in the world, so that silence means the pipeline broke.
	Tick Kind = "tick"
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
}

type wireRecord struct {
	Gen      *int64       `json:"gen"`
	Kind     string       `json:"kind"`
	Dim      string       `json:"dim"`
	Part     int          `json:"part"`
	Parts    int          `json:"parts"`
	More     int          `json:"more"`
	Items    []wireEntity `json:"items"`
	Scan     float64      `json:"scan"`
	Interval float64      `json:"interval"`
	Sent     int64        `json:"ts"`
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
	rec.Items = make([]Entity, 0, len(w.Items))
	for _, it := range w.Items {
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
		rec.Items = append(rec.Items, Entity{ID: it.ID, Name: clip(it.Name), Type: clip(it.Type), X: *it.X, Y: *it.Y, Z: *it.Z, Yaw: it.Yaw})
	}
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
