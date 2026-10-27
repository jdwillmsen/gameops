package life

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// passTactic is the smallest FamilyTactic: it is offered every tick against
// its family, notes what it saw and hands the tick back. A new family's
// tactic starts from this shape and registers itself from init.
type passTactic struct {
	family tactics.Family
	seen   []Fight
}

func (t *passTactic) Family() tactics.Family { return t.family }

func (t *passTactic) Engage(_ context.Context, f *Fight) (bool, error) {
	t.seen = append(t.seen, *f)
	return false, nil
}

// standTactic takes the tick and stands still facing nothing.
type standTactic struct {
	family tactics.Family
	ticks  int
}

func (t *standTactic) Family() tactics.Family { return t.family }

func (t *standTactic) Engage(_ context.Context, f *Fight) (bool, error) {
	t.ticks++
	return true, f.Actuator.Tick(act.Input{Yaw: f.Pose.Yaw, Pitch: f.Pose.Pitch})
}

// failTactic fails the tick without handling it.
type failTactic struct{ family tactics.Family }

var errTactic = errors.New("tactic failed")

func (t failTactic) Family() tactics.Family                       { return t.family }
func (t failTactic) Engage(context.Context, *Fight) (bool, error) { return false, errTactic }

// withTactics replaces the process's tactics for one test.
func withTactics(t *testing.T, ts ...FamilyTactic) {
	t.Helper()
	tacticRegistry.Lock()
	saved := maps.Clone(tacticRegistry.by)
	tacticRegistry.by = map[tactics.Family]FamilyTactic{}
	tacticRegistry.Unlock()
	t.Cleanup(func() {
		tacticRegistry.Lock()
		tacticRegistry.by = saved
		tacticRegistry.Unlock()
	})
	for _, tc := range ts {
		RegisterTactic(tc)
	}
}

// strikerOnAZombie is a striker with a sword and a zombie two blocks off.
func strikerOnAZombie(r *rig) {
	r.give(0, 303, 1)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})
	r.run(40)
}

func TestANoOpTacticIsOfferedTheTickAndTheRoleFightRuns(t *testing.T) {
	pr := tactics.NewProfile("zombie tuned")
	tuned := tactics.Defaults()
	tuned.CritWindow = 8
	pr.Overrides = map[tactics.Family]tactics.Params{tactics.FamilyZombie: tuned}
	setPr := func(l *Life) {
		if err := l.SetProfile(pr); err != nil {
			t.Fatal(err)
		}
	}

	withTactics(t)
	base := replay(t, "striker", strikerOnAZombie, setPr)

	pass := &passTactic{family: tactics.FamilyZombie}
	withTactics(t, pass)
	r := newWarrior(t, "striker", start)
	setPr(r.l)
	strikerOnAZombie(r)

	if len(pass.seen) == 0 {
		t.Fatal("the zombie tactic was never offered the tick")
	}
	got := make([]any, len(r.out.got))
	for i, pk := range r.out.got {
		got[i] = pk
	}
	if !reflect.DeepEqual(got, base) {
		t.Errorf("a tactic that handled nothing changed the fight: %d packets, want %d", len(got), len(base))
	}
	f := pass.seen[len(pass.seen)-1]
	if f.Life != r.l || f.Actuator == nil {
		t.Error("the tactic was not handed the unit and its actuator")
	}
	if f.Target.ID != 50 || f.Family != tactics.FamilyZombie {
		t.Errorf("tactic saw target %d of family %q, want 50, zombie", f.Target.ID, f.Family)
	}
	if f.Params != tuned {
		t.Errorf("tactic saw params %+v, want the zombie override %+v", f.Params, tuned)
	}
	if len(f.Snapshot.Inventory) == 0 {
		t.Error("tactic saw an empty snapshot")
	}
	if f.Pose.Feet == (mgl32.Vec3{}) {
		t.Error("tactic saw no pose")
	}
}

func TestATacticThatHandlesTheTickReplacesTheRoleFight(t *testing.T) {
	stand := &standTactic{family: tactics.FamilyZombie}
	withTactics(t, stand)
	r := newWarrior(t, "striker", start)
	strikerOnAZombie(r)
	if stand.ticks == 0 {
		t.Fatal("the zombie tactic never took a tick")
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d attacks while the tactic held the fight", n)
	}
	if d := dist(r.feet(), start); d > 0.01 {
		t.Errorf("the unit moved %.2f blocks while the tactic stood it still", d)
	}
}

func TestATacticForAnotherFamilyIsNotOffered(t *testing.T) {
	pass := &passTactic{family: tactics.FamilySkeleton}
	withTactics(t, pass)
	r := newWarrior(t, "striker", start)
	strikerOnAZombie(r)
	if len(pass.seen) != 0 {
		t.Errorf("the skeleton tactic was offered %d zombie ticks", len(pass.seen))
	}
	if len(r.out.attacks()) == 0 {
		t.Error("the striker never swung at the zombie")
	}
}

func TestATacticErrorIsTheTicksError(t *testing.T) {
	withTactics(t, failTactic{family: tactics.FamilyZombie})
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})
	snap := r.tracker.Snapshot()
	if !r.l.Engaged(r.a.Pose(), snap) {
		t.Fatal("not engaged with a zombie in reach")
	}
	if err := r.l.Fight(context.Background(), r.a, snap); !errors.Is(err, errTactic) {
		t.Fatalf("Fight = %v, want the tactic's error", err)
	}
}

func TestRegisterTacticReplacesTheFamilysTactic(t *testing.T) {
	first, second := &passTactic{family: tactics.FamilyZombie}, &passTactic{family: tactics.FamilyZombie}
	withTactics(t, first, second)
	if got, ok := tacticFor(tactics.FamilyZombie); !ok || got != second {
		t.Fatalf("tactic in force = %v, want the later registration", got)
	}
	if _, ok := tacticFor(tactics.FamilyCreeper); ok {
		t.Error("a family with nothing registered has a tactic")
	}
}
