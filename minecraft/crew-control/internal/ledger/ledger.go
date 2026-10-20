// Package ledger is the crew's shared picture of the world: named places,
// the stock last seen in each chest, and which units are free to take work.
// Job planners read it; units feed it by reporting what they see.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	KindChest = "chest"
	KindArea  = "area"

	minX, maxX = -500, 499
	minY, maxY = -64, 320
	minZ, maxZ = -500, 499

	// Large enough to be useful, small enough that a typo cannot make a
	// "place" the size of the world.
	maxAreaSpan = 64
	maxItems    = 64
	maxName     = 64
)

var (
	ErrSeeded   = errors.New("ledger: place comes from configuration")
	ErrNotFound = errors.New("ledger: no such place")
	ErrInvalid  = errors.New("ledger: invalid place")
	// ErrConflict: another chest already occupies the position.
	ErrConflict = errors.New("ledger: a chest is already there")
	// ErrUnavailable: the database cannot be reached right now.
	ErrUnavailable = errors.New("ledger: store unavailable")

	idRE   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	itemRE = regexp.MustCompile(`^[a-z_]{1,32}$`)
	// autoIDRE is the form chestID writes.
	autoIDRE = regexp.MustCompile(`^chest-(m?[0-9]+)-(m?[0-9]+)-(m?[0-9]+)$`)
)

type Pos struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}

// Box is inclusive on both corners.
type Box struct {
	Min Pos `json:"min"`
	Max Pos `json:"max"`
}

type Place struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Box    Box      `json:"box"`
	Items  []string `json:"items,omitempty"`
	Seeded bool     `json:"-"`
}

// Stock is the count a unit last saw, not a live inventory.
type Stock struct {
	Item   string
	Count  int
	SeenAt time.Time
	Unit   string
}

type Ledger interface {
	Places(ctx context.Context) ([]Place, error)
	Place(ctx context.Context, id string) (Place, bool, error)
	// PutPlace creates or replaces an unseeded place; ErrSeeded for a seeded id.
	// An id in the form Observe gives the chests it finds is refused unless it
	// names such a chest and keeps its box and kind: only the name can change.
	PutPlace(ctx context.Context, p Place) error
	// DeletePlace removes an unseeded place and its stock.
	DeletePlace(ctx context.Context, id string) error
	// Seed upserts ps as seeded and removes seeded places not in ps; an id in
	// Observe's form is refused. It never removes unseeded places, but an
	// unseeded chest at a seeded chest's position is folded into it: its
	// stock moves over if newer, then it is deleted, so one position never
	// holds two chests.
	Seed(ctx context.Context, ps []Place) error
	// Observe replaces the stock of the chest at the position with items,
	// which is the chest's whole contents, creating an unseeded place if no
	// chest is known there. A partial observation is only some of the
	// contents: it updates the items it carries and leaves the rest as they
	// were. An observation older than the stock already held is ignored:
	// events can arrive out of order. A time in the future is taken as now,
	// so one bad clock cannot outrank every later report.
	//
	// The guard reads the stored rows' times, so a chest observed empty
	// (which has no rows to carry a time) cannot reject an older report that
	// arrives later. The schema has no place-level last-seen to hold it.
	Observe(ctx context.Context, unit string, at time.Time, chest Pos, items map[string]int, partial bool) error
	Stock(ctx context.Context, placeID string) ([]Stock, error)
	ChestAt(ctx context.Context, p Pos) (Place, bool, error)
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

func validPos(p Pos) bool {
	return p.X >= minX && p.X <= maxX && p.Y >= minY && p.Y <= maxY && p.Z >= minZ && p.Z <= maxZ
}

// Validate applies the world's bounds and naming rules, which the database
// repeats only for the id and kind.
func Validate(p Place) error {
	if !idRE.MatchString(p.ID) {
		return invalid("id must match %s", idRE)
	}
	if n := len([]rune(strings.TrimSpace(p.Name))); n == 0 || n > maxName {
		return invalid("name must be 1 to %d characters", maxName)
	}
	if !validPos(p.Box.Min) || !validPos(p.Box.Max) {
		return invalid("box is outside the world")
	}
	mn, mx := p.Box.Min, p.Box.Max
	if mn.X > mx.X || mn.Y > mx.Y || mn.Z > mx.Z {
		return invalid("box min is above max")
	}
	switch p.Kind {
	case KindChest:
		if mn != mx {
			return invalid("a chest is one block")
		}
	case KindArea:
		if mx.X-mn.X >= maxAreaSpan || mx.Y-mn.Y >= maxAreaSpan || mx.Z-mn.Z >= maxAreaSpan {
			return invalid("an area spans at most %d blocks on each axis", maxAreaSpan)
		}
	default:
		return invalid("kind must be chest or area")
	}
	if len(p.Items) > maxItems {
		return invalid("at most %d items", maxItems)
	}
	for _, it := range p.Items {
		if !itemRE.MatchString(it) {
			return invalid("item %q is not an item name", it)
		}
	}
	return nil
}

// chestID names a chest found at a position nobody described. A minus sign
// cannot appear in the id, where it would read as a separator, so a negative
// coordinate is written m<abs>.
func chestID(p Pos) string {
	c := func(v int) string {
		if v < 0 {
			return "m" + strconv.Itoa(-v)
		}
		return strconv.Itoa(v)
	}
	return "chest-" + c(p.X) + "-" + c(p.Y) + "-" + c(p.Z)
}

// Observe files stock under the id its position encodes, so that id must
// stay on that position and stay a chest.
func checkAutoID(p, old Place, exists bool) error {
	if autoIDRE.MatchString(p.ID) && (!exists || old.Kind != p.Kind || old.Box != p.Box) {
		return invalid("ids like chest-x-y-z are kept for chests units find")
	}
	return nil
}

func validSeed(p Place) error {
	if err := Validate(p); err != nil {
		return err
	}
	return checkAutoID(p, Place{}, false)
}

func autoChest(p Pos) Place {
	return Place{ID: chestID(p), Name: "Unnamed chest", Kind: KindChest, Box: Box{p, p}}
}

// cleanObservation keeps what the ledger can store. The report is already
// filtered upstream, but a row the table would reject must not discard the
// rest of a chest's contents.
func cleanObservation(items map[string]int) map[string]int {
	out := make(map[string]int, len(items))
	for k, v := range items {
		if itemRE.MatchString(k) && v >= 0 {
			out[k] = v
		}
	}
	return out
}

// positionsFree rejects two chests at one position within a single list.
func positionsFree(ps []Place) error {
	at := map[Pos]string{}
	for _, p := range ps {
		if p.Kind != KindChest {
			continue
		}
		if other, dup := at[p.Box.Min]; dup {
			return fmt.Errorf("%w: %s and %s", ErrConflict, other, p.ID)
		}
		at[p.Box.Min] = p.ID
	}
	return nil
}
