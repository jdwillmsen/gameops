package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

// duelSquads is where a duel's start signal goes. The units read it from
// their lab squads, so without it the run only tags and scores them.
type duelSquads struct {
	base      *url.URL
	red, blue string
	// token is the shared unit token, read from the environment so it
	// appears in no argument list, log or message.
	token string
}

// parseDuelSquads reads the three flags as one: all or none. The squads must
// be lab squads and distinct, and the token must be set, before the run
// touches a server.
func parseDuelSquads(squadURL, red, blue string) (*duelSquads, error) {
	if squadURL == "" && red == "" && blue == "" {
		return nil, nil
	}
	if squadURL == "" || red == "" || blue == "" {
		return nil, errors.New("--squad-url, --red-squad and --blue-squad go together")
	}
	base, err := url.Parse(squadURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("--squad-url: want an http or https URL with a host")
	}
	for flag, id := range map[string]string{"--red-squad": red, "--blue-squad": blue} {
		if err := arena.ValidateLabSquad(id); err != nil {
			return nil, fmt.Errorf("%s: %w", flag, err)
		}
	}
	if red == blue {
		return nil, errors.New("--red-squad must differ from --blue-squad: a squad has one duel state")
	}
	token := strings.TrimSpace(os.Getenv("CONTROL_TOKEN"))
	if token == "" {
		return nil, errors.New("CONTROL_TOKEN is required with --squad-url")
	}
	return &duelSquads{base: base, red: red, blue: blue, token: token}, nil
}

// policy is the target policy the run hands the duel: the squad delivery with
// the console tags kept as bookkeeping, or the tags alone when no squads were
// given.
func (c *duelConfig) policy(console arena.Console) (arena.TargetPolicy, error) {
	tags := &arena.ConsolePolicy{Console: console}
	if c.squads == nil {
		return tags, nil
	}
	return arena.NewSquadPolicy(c.squads.base, c.squads.red, c.squads.blue, c.squads.token,
		c.duel.Red, c.duel.Blue, tags)
}
