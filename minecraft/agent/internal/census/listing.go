package census

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

// ListingHeader is the first line of a listing. It carries what every later
// line depends on: how old the world is, where it came from, and how many
// lines follow, so a reader can tell a complete listing from a cut one.
type ListingHeader struct {
	WorldTakenAt string   `json:"world_taken_at"`
	Source       string   `json:"source"`
	Types        []string `json:"types,omitempty"`
	Entities     int      `json:"entities"`
	Orphaned     int      `json:"orphaned"`
	// Unlocatable is never omitted: a reader that finds it absent is reading
	// a listing from before the field existed, not a world with none.
	Unlocatable int `json:"unlocatable"`
}

// ListedEntity is one entity in a listing. Coordinates are the saved ones,
// unrounded: a consumer aiming a command at a mob needs the block it is in,
// and rounding moves an entity standing at -0.4 into the next block.
type ListedEntity struct {
	Identifier string  `json:"identifier"`
	Dimension  string  `json:"dimension"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Z          float64 `json:"z"`
	Persistent bool    `json:"persistent"`
	Name       string  `json:"name,omitempty"`
}

// RenderListing writes one JSON object per line: a header, then every entity
// whose identifier is in types, or every entity when types is empty.
//
// The order is total, down to the unique id, so the same world bytes give the
// same listing and two listings can be compared with diff.
//
// An entity saved at a position that is not a finite number is counted in the
// header and not listed. JSON cannot carry such a coordinate, the order above
// is not an order once NaN is compared, and a line without a position is no
// use to a reader that came for one; failing instead would let one corrupt
// mob withhold every other line.
func RenderListing(entities []Entity, stats ScanStats, takenAt time.Time, sourceKind string, types []string) (string, error) {
	wanted := map[string]bool{}
	for _, t := range types {
		wanted[t] = true
	}
	var kept []Entity
	unlocatable := 0
	for _, e := range entities {
		if len(wanted) != 0 && !wanted[e.Identifier] {
			continue
		}
		if !finite(e.X) || !finite(e.Y) || !finite(e.Z) {
			unlocatable++
			continue
		}
		kept = append(kept, e)
	}
	sort.Slice(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		switch {
		case a.Dimension != b.Dimension:
			return a.Dimension < b.Dimension
		case a.Identifier != b.Identifier:
			return a.Identifier < b.Identifier
		case a.X != b.X:
			return a.X < b.X
		case a.Z != b.Z:
			return a.Z < b.Z
		case a.Y != b.Y:
			return a.Y < b.Y
		case a.UniqueID != b.UniqueID:
			return a.UniqueID < b.UniqueID
		// Two records can agree on all of that, a duplicated actor among
		// them, and still print different lines. Every printed field is
		// compared so that only identical lines are left unordered.
		case a.Persistent != b.Persistent:
			return !a.Persistent
		default:
			return a.CustomName < b.CustomName
		}
	})

	taken := ""
	if !takenAt.IsZero() {
		taken = takenAt.UTC().Format(time.RFC3339)
	}
	var sortedTypes []string
	for t := range wanted {
		sortedTypes = append(sortedTypes, t)
	}
	sort.Strings(sortedTypes)

	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	if err := enc.Encode(ListingHeader{
		WorldTakenAt: taken,
		Source:       sourceKind,
		Types:        sortedTypes,
		Entities:     len(kept),
		Orphaned:     stats.Orphaned,
		Unlocatable:  unlocatable,
	}); err != nil {
		return "", fmt.Errorf("encode listing header: %w", err)
	}
	for _, e := range kept {
		if err := enc.Encode(ListedEntity{
			Identifier: e.Identifier,
			Dimension:  e.Dimension.String(),
			X:          e.X, Y: e.Y, Z: e.Z,
			Persistent: e.Persistent,
			Name:       e.CustomName,
		}); err != nil {
			return "", fmt.Errorf("encode %s: %w", e.Identifier, err)
		}
	}
	return b.String(), nil
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
