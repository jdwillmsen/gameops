package main

import (
	"errors"
	"fmt"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
)

// duelPolicy builds the policy for one duel round from this sink's squads:
// each side's units keep the squad they were handed a profile in, so the
// start signal reaches the squad that holds the profile under test. The
// console tags stay as bookkeeping.
func (s SquadSink) duelPolicy(console arena.Console, red, blue []string) (arena.TargetPolicy, error) {
	redSquad, err := s.sideSquad(red)
	if err != nil {
		return nil, err
	}
	blueSquad, err := s.sideSquad(blue)
	if err != nil {
		return nil, err
	}
	return arena.NewSquadPolicy(s.Base, redSquad, blueSquad, s.Token, red, blue, &arena.ConsolePolicy{Console: console})
}

// sideSquad is the one lab squad a side's units share; a side split across
// squads could not be told apart from its enemy by the squads' own rosters.
func (s SquadSink) sideSquad(units []string) (string, error) {
	if len(units) == 0 {
		return "", errors.New("trainbridge: a duel side has no units")
	}
	squad := s.squadOf(units[0])
	for _, u := range units[1:] {
		if got := s.squadOf(u); got != squad {
			return "", fmt.Errorf("trainbridge: units %q and %q of one duel side are in squads %q and %q", units[0], u, squad, got)
		}
	}
	return squad, nil
}
