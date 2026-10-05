package census

import (
	"bytes"
	"encoding/json"
	"fmt"
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
func RenderListing(entities []Entity, stats ScanStats, takenAt time.Time, sourceKind string, types []string) (string, error) {
	wanted := map[string]bool{}
	for _, t := range types {
		wanted[t] = true
	}
	var kept []Entity
	for _, e := range entities {
		if len(wanted) == 0 || wanted[e.Identifier] {
			kept = append(kept, e)
		}
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
		default:
			return a.UniqueID < b.UniqueID
		}
	})

	taken := ""
	if !takenAt.IsZero() {
		taken = takenAt.UTC().Format(time.RFC3339)
	}
	sortedTypes := append([]string(nil), types...)
	sort.Strings(sortedTypes)

	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	if err := enc.Encode(ListingHeader{
		WorldTakenAt: taken,
		Source:       sourceKind,
		Types:        sortedTypes,
		Entities:     len(kept),
		Orphaned:     stats.Orphaned,
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
