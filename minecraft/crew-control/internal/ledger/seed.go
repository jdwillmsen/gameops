package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ParseSeed reads the CREW_PLACES value. Unset or blank returns nil, which
// means "do not seed"; an explicit empty list returns a non-nil empty slice,
// which seeds the empty set and so clears earlier seeded places.
func ParseSeed(raw string) ([]Place, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	ps := []Place{}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ps); err != nil {
		return nil, fmt.Errorf("CREW_PLACES: %w", err)
	}
	if dec.More() {
		return nil, errors.New("CREW_PLACES: trailing data after the list")
	}
	return checkSeed(ps)
}

func checkSeed(ps []Place) ([]Place, error) {
	seen := map[string]bool{}
	for i := range ps {
		ps[i].Seeded = true
		if err := Validate(ps[i]); err != nil {
			return nil, fmt.Errorf("CREW_PLACES[%d]: %w", i, err)
		}
		if seen[ps[i].ID] {
			return nil, fmt.Errorf("CREW_PLACES: duplicate id %q", ps[i].ID)
		}
		seen[ps[i].ID] = true
	}
	if err := positionsFree(ps); err != nil {
		return nil, fmt.Errorf("CREW_PLACES: %w", err)
	}
	return ps, nil
}
