package arena

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func TestRecorderFillsDamageDealtFromTheGrowthOfEachWarriorsTotal(t *testing.T) {
	f := &FakeDamage{}
	f.SetDealt("Rusty", 100)
	f.SetDealt("Slate", 3)
	f.Set("Rusty", 40)
	r := &Recorder{Meta: recMeta, Damage: f}
	r.FightStart(time.Now())
	f.AddDealt("Rusty", 14)
	f.AddDealt("Slate", 6)
	f.Add("Rusty", 2)
	got := r.Finish(tactics.OutcomeWin, 10)
	if got.DamageDealt != 20 || got.DamageTaken != 2 {
		t.Errorf("dealt %v taken %v, want 20 and 2", got.DamageDealt, got.DamageTaken)
	}
}

func TestRecorderIgnoresADealtTotalThatFell(t *testing.T) {
	f := &FakeDamage{}
	f.SetDealt("Rusty", 50)
	f.SetDealt("Slate", 1)
	r := &Recorder{Meta: recMeta, Damage: f}
	r.FightStart(time.Now())
	f.SetDealt("Rusty", 2)
	f.AddDealt("Slate", 4)
	if got := r.Finish(tactics.OutcomeWin, 1).DamageDealt; got != 4 {
		t.Errorf("DamageDealt = %v, want only the unit that did not restart", got)
	}
}

func TestRecorderKeepsDealtZeroWhenTheReadingFails(t *testing.T) {
	f := &FakeDamage{}
	f.SetDealt("Rusty", 1)
	r := &Recorder{Meta: recMeta, Damage: f}
	r.FightStart(time.Now())
	f.AddDealt("Rusty", 9)
	f.Fail(errors.New("down"))
	if got := r.Finish(tactics.OutcomeLoss, 1).DamageDealt; got != 0 {
		t.Errorf("DamageDealt = %v, want 0 after a failed reading", got)
	}

	r.FightStart(time.Now())
	f.Fail(nil)
	f.AddDealt("Rusty", 5)
	if got := r.Finish(tactics.OutcomeLoss, 1).DamageDealt; got != 0 {
		t.Errorf("DamageDealt = %v without a start reading, want 0", got)
	}
}

// takenOnly is a DamageSource that has no idea of damage dealt.
type takenOnly struct{ v map[string]float64 }

func (s *takenOnly) Taken(context.Context, []string) (map[string]float64, error) { return s.v, nil }

func TestRecorderWithATakenOnlySourceLeavesDealtZero(t *testing.T) {
	s := &takenOnly{v: map[string]float64{"Rusty": 1}}
	r := &Recorder{Meta: recMeta, Damage: s}
	r.FightStart(time.Now())
	s.v = map[string]float64{"Rusty": 4}
	got := r.Finish(tactics.OutcomeWin, 1)
	if got.DamageTaken != 3 || got.DamageDealt != 0 {
		t.Errorf("taken %v dealt %v, want 3 and 0", got.DamageTaken, got.DamageDealt)
	}
}

func TestHTTPDamageReadsDamageDealtAndTreatsAnOlderUnitAsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/units/Rusty/v1/state":
			w.Write([]byte(`{"tasks":{"fight":{"total":{"damage_taken":12.5,"damage_dealt":31},"current":{"damage_dealt":4}}}}`))
		case "/units/Old/v1/state":
			w.Write([]byte(`{"tasks":{"fight":{"total":{"damage_taken":2}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	src, err := NewHTTPDamage(srv.URL + "/units/{name}/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	got, err := src.Totals(context.Background(), []string{"Rusty", "Old", "Gone"})
	if err == nil {
		t.Error("a missing unit raised no error")
	}
	if got["Rusty"] != (UnitDamage{Taken: 12.5, Dealt: 31}) || got["Old"] != (UnitDamage{Taken: 2}) || len(got) != 2 {
		t.Errorf("totals = %v, want the total counts of Rusty and Old only", got)
	}
}
