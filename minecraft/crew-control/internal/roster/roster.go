// Package roster is the fixed list of Crew units the console fronts.
package roster

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"unicode/utf8"
)

const (
	maxUnits   = 26
	maxNameLen = 16
)

var (
	idPattern = regexp.MustCompile(`^crew-[0-9]{2,}$`)
	// playerPattern is the id the game's save and scripts name a player
	// by: the player's UniqueID, written as a signed decimal. Both readings
	// of the 64 bits are accepted, as the map's own lookup does.
	playerPattern = regexp.MustCompile(`^-?[0-9]{1,20}$`)
)

// Unit is one worker the console can show.
type Unit struct {
	ID, Name string
	URL      *url.URL
	// Player is the unit's player id in the world save, set by hand; empty
	// when the unit's own report of it is to be used.
	Player string
}

// Roster keeps units in the order they were configured.
type Roster struct {
	units []Unit
	byID  map[string]int
}

func (r Roster) Units() []Unit {
	return append([]Unit(nil), r.units...)
}

// Has reports whether id names a unit on the roster, as the squad routes
// ask, which only need membership and not the unit itself.
func (r Roster) Has(id string) bool {
	_, ok := r.byID[id]
	return ok
}

func (r Roster) Get(id string) (Unit, bool) {
	i, ok := r.byID[id]
	if !ok {
		return Unit{}, false
	}
	return r.units[i], true
}

// Resolve finds a unit by its id or, failing that, by its in-game name. An id
// always wins, and a name two units share names neither, so a name can never
// reach a different unit than the operator meant.
func (r Roster) Resolve(key string) (Unit, bool) {
	if u, ok := r.Get(key); ok {
		return u, true
	}
	var found Unit
	n := 0
	for _, u := range r.units {
		if u.Name == key {
			found = u
			n++
		}
	}
	if n != 1 {
		return Unit{}, false
	}
	return found, true
}

type rawUnit struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	// Player is the unit's id in the world save, and is optional: without
	// it the id the unit reports for itself is used.
	Player string `json:"player"`
}

// Parse reads the CREW_UNITS JSON. The roster is operator-supplied, but the
// proxy dials whatever URL it holds, so it is checked rather than trusted.
func Parse(raw string) (Roster, error) {
	var in []rawUnit
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return Roster{}, fmt.Errorf("roster is not valid JSON: %w", err)
	}
	if len(in) == 0 {
		return Roster{}, errors.New("roster is empty")
	}
	if len(in) > maxUnits {
		return Roster{}, fmt.Errorf("roster has %d units, at most %d", len(in), maxUnits)
	}
	r := Roster{byID: make(map[string]int, len(in))}
	for _, ru := range in {
		if !idPattern.MatchString(ru.ID) {
			return Roster{}, fmt.Errorf("unit id %q must match %s", ru.ID, idPattern)
		}
		if _, dup := r.byID[ru.ID]; dup {
			return Roster{}, fmt.Errorf("duplicate unit id %q", ru.ID)
		}
		if n := utf8.RuneCountInString(ru.Name); n == 0 || n > maxNameLen {
			return Roster{}, fmt.Errorf("unit %s name must be 1 to %d characters", ru.ID, maxNameLen)
		}
		u, err := parseURL(ru.URL)
		if err != nil {
			return Roster{}, fmt.Errorf("unit %s: %w", ru.ID, err)
		}
		if ru.Player != "" && !playerPattern.MatchString(ru.Player) {
			return Roster{}, fmt.Errorf("unit %s player %q must match %s", ru.ID, ru.Player, playerPattern)
		}
		r.byID[ru.ID] = len(r.units)
		r.units = append(r.units, Unit{ID: ru.ID, Name: ru.Name, URL: u, Player: ru.Player})
	}
	return r, nil
}

func parseURL(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("bad url: %w", err)
	}
	switch {
	case u.Scheme != "http":
		return nil, errors.New("url must be http")
	case u.Host == "" || u.Hostname() == "":
		return nil, errors.New("url has no host")
	case u.User != nil:
		return nil, errors.New("url must not carry userinfo")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return nil, errors.New("url must not carry a query or fragment")
	case u.Path != "" && u.Path != "/":
		return nil, errors.New("url must not carry a path")
	}
	return u, nil
}
