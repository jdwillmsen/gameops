// Package detail serves an operator's view of one unit: what the world save
// holds of it — inventory, armour, experience and the ender chest, none of
// which the unit reports live — merged with what the live poll knows. The
// ender chest is private to its owner, so the route answers operators only.
package detail

import (
	"encoding/json"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/roster"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
	"github.com/jdwillmsen/gameops/minecraft/pkg/playerdata"
)

// Gate is the part of auth.Auth the route needs.
type Gate interface {
	Operator(r *http.Request) (auth.Operator, bool)
}

// Source reads unit player records out of the world save: what the last
// read found, and when that was.
type Source interface {
	Lookup(playerID string) (playerdata.Record, time.Time, bool)
	Scanned() (time.Time, bool)
}

// What the body says about each source of a unit's detail; the words are
// the map's own, so a page can read both the same way.
const (
	stateKnown   = "known"
	stateNone    = "none"
	statePending = "pending"
	stateOff     = "off"
)

// enderChestSlots is how many the chest holds; the save lists only the
// non-empty ones, the answer fills the rest in.
const enderChestSlots = 27

// maxBody is the cap on one answer. The decoder bounds a record far below
// it; the cap holds even a save made by hand, by dropping what shulker
// boxes in the record carry before the answer goes out.
const maxBody = 50 << 10

// DefaultEvery is both how long an answer may be reused and the least time
// between reads of the save; a save read more often than the cache would
// serve nothing the last read has not.
const DefaultEvery = 5 * time.Second

type sourceJSON struct {
	State string `json:"state"`
	// AsOf is when the source this came from was read.
	AsOf *time.Time `json:"asOf,omitempty"`
}

type xpJSON struct {
	Level    *int     `json:"level,omitempty"`
	Progress *float64 `json:"progress,omitempty"`
}

type body struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	Online bool       `json:"online"`
	Save   sourceJSON `json:"save"`
	Live   sourceJSON `json:"live"`

	Health *playerdata.Gauge `json:"health,omitempty"`
	Hunger *playerdata.Gauge `json:"hunger,omitempty"`
	Air    *int              `json:"air,omitempty"`

	XP       xpJSON `json:"xp"`
	GameMode string `json:"gameMode,omitempty"`
	// Dimension is where the save put the unit; live movement does not
	// move it, as the live poll reports a number and not a name.
	Dimension string `json:"dimension,omitempty"`

	Effects   []telemetry.LiveEffect `json:"effects"`
	Inventory []playerdata.Item      `json:"inventory"`
	Armor     []playerdata.Item      `json:"armor"`
	Mainhand  *playerdata.Item       `json:"mainhand,omitempty"`
	Offhand   *playerdata.Item       `json:"offhand,omitempty"`
	// EnderChest is every slot of the chest, index 0 to 26; an empty slot
	// is null, and the whole list is null when no record was read.
	EnderChest []*playerdata.Item `json:"enderChest"`
}

// Handler serves the unit detail route. saves may be nil, which answers
// every unit's save with state "off" rather than refusing: the view still
// shows what the live poll holds.
func Handler(units roster.Roster, live func(string) (telemetry.Live, bool), saves Source, g Gate) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ServeMux would answer an unclean path with a redirect; refuse
		// it, as the unit proxy does, so no spelling of a path becomes a
		// different one.
		if r.URL.Path != path.Clean(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if _, ok := g.Operator(r); !ok {
			writeError(w, http.StatusUnauthorized, "signed_out")
			return
		}
		unit, ok := units.Get(r.PathValue("id"))
		if !ok {
			writeError(w, http.StatusNotFound, "no such unit")
			return
		}
		writeJSON(w, detail(unit, live, saves))
	})
}

// detail joins what the save holds of the unit with what the live poll
// heard. The live poll is fresher than any save while the unit is online,
// so it wins for what it knows: vitals and the inventory. Effects live only
// in the poll; the save holds none. Everything else — armour, experience,
// the ender chest — only the save knows.
func detail(unit roster.Unit, live func(string) (telemetry.Live, bool), saves Source) body {
	out := body{ID: unit.ID, Name: unit.Name, Effects: []telemetry.LiveEffect{}, Inventory: []playerdata.Item{}, Armor: []playerdata.Item{}}

	lv, liveOK := live(unit.ID)
	if liveOK && lv.Online {
		out.Online = true
		at := lv.At
		out.Live = sourceJSON{State: stateKnown, AsOf: &at}
	} else {
		out.Live = sourceJSON{State: stateNone}
	}

	// An id in the roster is the operator's word and wins; otherwise the
	// unit's own report is used, which only an online unit has made.
	playerID := unit.Player
	if playerID == "" && liveOK {
		playerID = lv.PlayerID
	}
	var (
		record  playerdata.Record
		savedAt time.Time
		found   bool
	)
	switch {
	case saves == nil:
		out.Save = sourceJSON{State: stateOff}
	case playerID == "":
		// There is a save, but nothing yet says which record is the
		// unit's: it has not been online since crew-control started and
		// the roster does not name it.
		out.Save = sourceJSON{State: stateNone}
	default:
		record, savedAt, found = saves.Lookup(playerID)
		_, scanned := saves.Scanned()
		switch {
		case found:
			out.Save = sourceJSON{State: stateKnown, AsOf: &savedAt}
		case scanned:
			out.Save = sourceJSON{State: stateNone, AsOf: &savedAt}
		default:
			out.Save = sourceJSON{State: statePending}
		}
	}

	// An online unit's poll is an observation of now, while the record
	// was written whenever the server last saved; the time the save was
	// read says nothing about that, so it is not weighed against the poll.
	liveFresh := liveOK && lv.Online

	if lv.Online && lv.Effects != nil {
		out.Effects = lv.Effects
	}
	if liveFresh && lv.Health != nil {
		max := 20.0
		if record.Health != nil {
			max = record.Health.Max
		}
		out.Health = &playerdata.Gauge{Current: float64(*lv.Health), Max: max}
	} else {
		out.Health = record.Health
	}
	if liveFresh && lv.Hunger != nil {
		out.Hunger = &playerdata.Gauge{Current: float64(*lv.Hunger), Max: 20}
	} else {
		out.Hunger = record.Hunger
	}
	if liveFresh {
		out.Inventory = mergeInventory(lv.Inventory, record.Inventory)
	} else if found {
		out.Inventory = record.Inventory
	}

	if found {
		out.Air = record.Air
		out.XP = xpJSON{Level: record.Level, Progress: record.Progress}
		out.GameMode, out.Dimension = record.GameMode, record.Dimension
		out.Armor = record.Armor
		if len(out.Armor) == 0 {
			out.Armor = []playerdata.Item{}
		}
		out.Mainhand, out.Offhand = record.Mainhand, record.Offhand
		out.EnderChest = expand(record.EnderChest)
	}
	return out
}

// mergeInventory takes the live slots as they stand and carries over what
// the save knows about a stack it still holds: durability, a custom name,
// enchantments, and what a shulker box in it carries. A slot the save does
// not agree about is taken as the live one alone.
func mergeInventory(live []telemetry.LiveItem, saved []playerdata.Item) []playerdata.Item {
	bySlot := make(map[int]playerdata.Item, len(saved))
	for _, it := range saved {
		bySlot[it.Slot] = it
	}
	out := make([]playerdata.Item, 0, len(live))
	for _, l := range live {
		it := playerdata.Item{Slot: l.Slot, Name: l.Name, Count: l.Count}
		if s, ok := bySlot[l.Slot]; ok && s.Name == l.Name {
			it.Damage, it.MaxDamage = s.Damage, s.MaxDamage
			it.Custom, it.Enchants, it.Contents = s.Custom, s.Enchants, s.Contents
		}
		out = append(out, it)
	}
	return out
}

// expand fills the chest's slots out to all of them; the save lists only
// the ones that hold something.
func expand(items []playerdata.Item) []*playerdata.Item {
	out := make([]*playerdata.Item, enderChestSlots)
	for _, it := range items {
		if it.Slot >= 0 && it.Slot < enderChestSlots {
			it := it
			out[it.Slot] = &it
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, b body) {
	data, err := json.Marshal(b)
	if err == nil && len(data) > maxBody {
		// What boxes carry is the one part a save made by hand can grow
		// past the cap; without it the bounded lists fit.
		stripped := b
		stripped.stripBoxes()
		if again, err := json.Marshal(stripped); err == nil {
			data = again
		}
	}
	w.Header().Set("Content-Type", "application/json")
	// The ender chest is its owner's, so no share may ever hold the answer.
	w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(int(DefaultEvery.Seconds())))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (b *body) stripBoxes() {
	for i := range b.Inventory {
		b.Inventory[i].Contents = nil
	}
	for i := range b.EnderChest {
		if b.EnderChest[i] != nil {
			b.EnderChest[i].Contents = nil
		}
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
