package server

import (
	"math"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/live"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/players"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/render"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/trails"
)

// PlayerSource is the player records of the last snapshot.
type PlayerSource interface {
	Lookup(id string) (players.Record, time.Time, bool)
	Scanned() (time.Time, bool)
}

// A live player's id is a signed 64-bit number written out.
var playerID = regexp.MustCompile(`^-?[0-9]{1,20}$`)

// What the page is told about each source of a player's details.
const (
	detailKnown = "known"
	// detailNone is a source that was read and holds nothing of the
	// player: for the save, a player who joined since the snapshot.
	detailNone = "none"
	// detailPending is a source that has not been read yet.
	detailPending = "pending"
	// detailOff is a source this server does not keep.
	detailOff = "off"
)

type savedJSON struct {
	State string `json:"state"`
	// AsOf is when the snapshot the record was read from was taken. The
	// game rewrites a player's record when the world saves and when they
	// log out, so anything here may be older than what they are doing.
	AsOf   *time.Time      `json:"asOf,omitempty"`
	Record *players.Record `json:"record,omitempty"`
}

type trailStatsJSON struct {
	State string        `json:"state"`
	Stats *trails.Stats `json:"stats,omitempty"`
}

// countersJSON is what the pack's counts have added up to for a player.
// Totals is flattened in, so the page reads "since" beside the figures.
type countersJSON struct {
	State string `json:"state"`
	*live.Totals
}

// liveJSON is what the pack reported of an online player in its last
// sample, which is newer than anything in the save. Each part is left out
// where the pack did not send it, as an older pack does not.
type liveJSON struct {
	Health    *float64   `json:"health,omitempty"`
	MaxHealth *float64   `json:"maxHealth,omitempty"`
	Level     *int       `json:"level,omitempty"`
	Dimension string     `json:"dimension"`
	At        *time.Time `json:"at,omitempty"`
}

type playerJSON struct {
	ID       string         `json:"id"`
	Name     string         `json:"name,omitempty"`
	Saved    savedJSON      `json:"saved"`
	Trail    trailStatsJSON `json:"trails"`
	Counters countersJSON   `json:"counters"`
	// Live is present only for a player who is online.
	Live *liveJSON `json:"live,omitempty"`
}

// livePlayer is the player with this id in the live feed, and where.
func (s *Server) livePlayer(id string) (live.Entity, string, time.Time, bool) {
	if s.Live == nil {
		return live.Entity{}, "", time.Time{}, false
	}
	now := time.Now()
	for _, dimension := range render.Dimensions {
		frame := s.Live.Store.Snapshot(dimension, now)
		for _, p := range frame.Players {
			if p.ID == id {
				return p, dimension, frame.At, true
			}
		}
	}
	return live.Entity{}, "", time.Time{}, false
}

// liveName is the gamertag the live feed gives the player with this id.
func (s *Server) liveName(id string) string {
	p, _, _, _ := s.livePlayer(id)
	return p.Name
}

func (s *Server) counters(id string) countersJSON {
	if s.Live == nil || s.Live.Counters == nil {
		return countersJSON{State: detailOff}
	}
	// The pack counts under the signed id the game gives; the page may have
	// been handed the same number read as unsigned.
	if u, err := strconv.ParseUint(id, 10, 64); err == nil && u > math.MaxInt64 {
		id = strconv.FormatInt(int64(u), 10)
	}
	if totals, ok := s.Live.Counters.Lookup(id); ok {
		return countersJSON{State: detailKnown, Totals: &totals}
	}
	return countersJSON{State: detailNone}
}

// handlePlayer serves everything the save and the trails know of one
// player, named by the id the live feed gives them. Every logged-in player
// sees this of every other, as they see where each is.
//
// The gamertag is not in the save, so the trails, which are kept by it,
// are found by the name the live feed gives that id. A player who is not
// online has no live name, and the page then says who it is asking about.
func (s *Server) handlePlayer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !playerID.MatchString(id) {
		http.Error(w, "a player is named by the id the live feed gives them", http.StatusBadRequest)
		return
	}
	name := s.liveName(id)
	if asked := r.URL.Query().Get("name"); name == "" && len(asked) <= maxGamertag {
		name = asked
	}
	out := playerJSON{ID: id, Name: name, Saved: savedJSON{State: detailPending}, Trail: trailStatsJSON{State: detailOff}, Counters: s.counters(id)}
	if p, dimension, at, online := s.livePlayer(id); online && (p.Health != nil || p.Level != nil) {
		out.Live = &liveJSON{Health: p.Health, MaxHealth: p.MaxHealth, Level: p.Level, Dimension: dimension}
		if !at.IsZero() {
			at = at.UTC().Truncate(time.Second)
			out.Live.At = &at
		}
	}

	if s.Players != nil {
		record, at, found := s.Players.Lookup(id)
		_, scanned := s.Players.Scanned()
		switch {
		case found:
			out.Saved = savedJSON{State: detailKnown, AsOf: &at, Record: &record}
		case scanned:
			out.Saved = savedJSON{State: detailNone, AsOf: &at}
		}
	}
	if stats, ok := s.trailStats(name); ok {
		out.Trail = trailStatsJSON{State: detailKnown, Stats: &stats}
	} else if s.Trails != nil {
		out.Trail.State = detailNone
	}
	writeJSON(w, http.StatusOK, out)
}

// trailStats is the figures of a named player, if the trails keep any.
func (s *Server) trailStats(name string) (trails.Stats, bool) {
	if s.Trails == nil || name == "" {
		return trails.Stats{}, false
	}
	return s.Trails.Stats(name, time.Now())
}
