package arena

import (
	"fmt"
	"slices"
)

// Duel sides. A side is a spawn position, not a team: with --rounds the teams
// swap sides, so a scorecard's Side says where a team stood that round.
const (
	SideRed  = "red"
	SideBlue = "blue"
)

// Tags the console side puts on duelists. They give selectors and observers a
// handle on the duel; the warriors' combat engine cannot read them, which is
// why the engine side goes through a TargetPolicy.
const (
	duelTagRed  = "duel_red"
	duelTagBlue = "duel_blue"
	duelTagGo   = "duel_go"
	duelTagOut  = "duel_out"
)

// Roster says who fights whom in one round, by spawn side.
type Roster struct {
	Red  []string
	Blue []string
}

// SideOf reports which side a name fights on.
func (r Roster) SideOf(name string) (string, bool) {
	switch {
	case slices.Contains(r.Red, name):
		return SideRed, true
	case slices.Contains(r.Blue, name):
		return SideBlue, true
	}
	return "", false
}

// Enemy is the one rule the engine needs: other is a valid target for self
// only when both are duelists on opposite sides. A player outside the roster,
// a crew unit that is not duelling and a teammate all answer false, so the
// engine's rule of never fighting players stands for everyone but the other
// side.
func (r Roster) Enemy(self, other string) bool {
	a, ok := r.SideOf(self)
	if !ok {
		return false
	}
	b, ok := r.SideOf(other)
	return ok && a != b
}

// TargetPolicy is how a duel's state reaches the warriors' combat engine, which
// skips every player unless told otherwise (life.PvPPolicy):
//
//   - After Arm and before Go a unit holds fire.
//   - After Go a unit may target a player p exactly when Roster.Enemy(self, p)
//     holds and p has not been reported through Eliminated.
//   - After Disarm every unit is back to mob-only targeting.
//   - Beat runs on every poll of a running round and keeps the start signal
//     alive: a signal nobody renews lapses on its own, so a director that
//     died leaves the units hunting only briefly.
//
// All methods must be idempotent. Disarm runs on every exit path, including
// an interrupt, so a failed duel never leaves units hunting each other.
//
// SquadPolicy carries the state to the units through their lab squads;
// ConsolePolicy below only tags the duelists in the world, which the engine
// cannot read.
type TargetPolicy interface {
	Arm(Roster) error
	Go() error
	Eliminated(name string) error
	Beat() error
	Disarm() error
}

// ConsolePolicy marks the duel in the world with tags: duel_red or duel_blue
// from Arm, duel_go from Go, duel_out for an eliminated duelist. Disarm
// removes them all.
type ConsolePolicy struct {
	Console Console

	armed Roster
}

func (p *ConsolePolicy) tagAll(names []string, verb, tag string) error {
	for _, n := range names {
		if err := validateWarriorName(n); err != nil {
			return err
		}
		if _, err := p.Console.Exec(fmt.Sprintf("tag %q %s %s", n, verb, tag)); err != nil {
			return err
		}
	}
	return nil
}

func (p *ConsolePolicy) all() []string {
	return append(slices.Clone(p.armed.Red), p.armed.Blue...)
}

// Arm clears any duel tag a crashed run left on the duelists first: a team
// that swapped sides must not keep the other side's tag.
func (p *ConsolePolicy) Arm(r Roster) error {
	for _, n := range append(slices.Clone(r.Red), r.Blue...) {
		if err := validateWarriorName(n); err != nil {
			return err
		}
	}
	p.armed = Roster{Red: slices.Clone(r.Red), Blue: slices.Clone(r.Blue)}
	if err := p.Disarm(); err != nil {
		return err
	}
	p.armed = Roster{Red: slices.Clone(r.Red), Blue: slices.Clone(r.Blue)}
	if err := p.tagAll(r.Red, "add", duelTagRed); err != nil {
		return err
	}
	return p.tagAll(r.Blue, "add", duelTagBlue)
}

func (p *ConsolePolicy) Go() error { return p.tagAll(p.all(), "add", duelTagGo) }

func (p *ConsolePolicy) Eliminated(name string) error {
	return p.tagAll([]string{name}, "add", duelTagOut)
}

// Beat has nothing to renew: tags stay until Disarm removes them.
func (p *ConsolePolicy) Beat() error { return nil }

// Disarm keeps going past a failed command so one bad tag cannot leave the
// rest marked, and reports the first failure.
func (p *ConsolePolicy) Disarm() error {
	var first error
	for _, n := range p.all() {
		for _, tag := range []string{duelTagRed, duelTagBlue, duelTagGo, duelTagOut} {
			if _, err := p.Console.Exec(fmt.Sprintf("tag %q remove %s", n, tag)); err != nil && first == nil {
				first = err
			}
		}
	}
	p.armed = Roster{}
	return first
}
