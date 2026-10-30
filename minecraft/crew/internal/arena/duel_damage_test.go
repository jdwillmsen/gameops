package arena

import (
	"context"
	"errors"
	"testing"
	"time"
)

// duelDamage reports, at reading k, rate * (k/2) for each name: the pair of
// readings around a round (2r-1 and 2r) then differ by exactly rate.
type duelDamage struct {
	rate  map[string]float64
	reads int
	err   error
}

func (d *duelDamage) Taken(_ context.Context, units []string) (map[string]float64, error) {
	d.reads++
	if d.err != nil {
		return nil, d.err
	}
	out := map[string]float64{}
	for _, u := range units {
		out[u] = d.rate[u] * float64(d.reads/2)
	}
	return out, nil
}

func newDuelDamage() *duelDamage {
	return &duelDamage{rate: map[string]float64{"Rusty": 3, "Slate": 2, "Tinder": 1, "Agate": 0}}
}

func TestDuelCardsTakeDealtFromTheOpponentsLoss(t *testing.T) {
	r := newDuelRig()
	r.d.Damage = newDuelDamage()
	r.run(t)
	if len(r.sink.Cards) != 4 {
		t.Fatalf("cards = %d, want 4", len(r.sink.Cards))
	}
	// Round 1: Rusty and Slate on red lose 5 together, Tinder and Agate 1.
	red, blue := r.sink.Cards[0], r.sink.Cards[1]
	if red.DamageTaken != 5 || red.DamageDealt != 1 || blue.DamageTaken != 1 || blue.DamageDealt != 5 {
		t.Errorf("round 1 red %v/%v blue %v/%v, want taken/dealt 5/1 and 1/5",
			red.DamageTaken, red.DamageDealt, blue.DamageTaken, blue.DamageDealt)
	}
	// Round 2 swaps the lines, so the red card is Tinder and Agate's.
	red, blue = r.sink.Cards[2], r.sink.Cards[3]
	if red.DamageTaken != 1 || red.DamageDealt != 5 || blue.DamageTaken != 5 || blue.DamageDealt != 1 {
		t.Errorf("round 2 red %v/%v blue %v/%v, want taken/dealt 1/5 and 5/1",
			red.DamageTaken, red.DamageDealt, blue.DamageTaken, blue.DamageDealt)
	}
}

func TestDuelDealtIsTheFoesLossEvenFromATakenOnlySource(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 1
	r.d.Damage = newDuelDamage()
	r.run(t)
	if c := r.sink.Cards[0]; c.DamageDealt != 1 {
		t.Errorf("DamageDealt = %v, want the foe's loss of 1", c.DamageDealt)
	}
}

func TestDuelScoresWithoutDamageWhenNoSourceOrTheReadingFails(t *testing.T) {
	for name, src := range map[string]DamageSource{
		"no source":        nil,
		"a failing source": &duelDamage{err: errors.New("down")},
	} {
		t.Run(name, func(t *testing.T) {
			r := newDuelRig()
			r.d.Rounds = 1
			r.d.Damage = src
			r.run(t)
			for _, c := range r.sink.Cards {
				if c.DamageDealt != 0 || c.DamageTaken != 0 {
					t.Errorf("card = %+v, want zero damage", c)
				}
			}
		})
	}
}

func TestDuelAnAbortedRoundReadsNoDamage(t *testing.T) {
	r := newDuelRig()
	r.d.Rounds = 1
	src := newDuelDamage()
	r.d.Damage = src
	r.s.respawn = false
	r.s.die(1, "Tinder", 10*time.Second)
	if _, err := r.d.Run(context.Background()); !errors.Is(err, ErrDuelistGone) {
		t.Fatalf("err = %v, want ErrDuelistGone", err)
	}
	if src.reads != 1 {
		t.Errorf("%d damage readings, want only the one before the round", src.reads)
	}
	for _, c := range r.sink.Cards {
		if c.DamageDealt != 0 || c.DamageTaken != 0 {
			t.Errorf("aborted card = %+v, want zero damage", c)
		}
	}
}
