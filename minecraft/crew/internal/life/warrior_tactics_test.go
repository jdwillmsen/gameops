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
	name   string
	family tactics.Family
	seen   []Fight
}

func (t *passTactic) Name() string           { return t.name }
func (t *passTactic) Family() tactics.Family { return t.family }

func (t *passTactic) Engage(_ context.Context, f *Fight) (bool, error) {
	t.seen = append(t.seen, *f)
	return false, nil
}

// standTactic takes the tick and stands still facing nothing.
type standTactic struct {
	name   string
	family tactics.Family
	ticks  int
}

func (t *standTactic) Name() string           { return t.name }
func (t *standTactic) Family() tactics.Family { return t.family }

func (t *standTactic) Engage(_ context.Context, f *Fight) (bool, error) {
	t.ticks++
	return true, f.Actuator.Tick(act.Input{Yaw: f.Pose.Yaw, Pitch: f.Pose.Pitch})
}

// failTactic fails the tick without handling it.
type failTactic struct {
	name   string
	family tactics.Family
}

var errTactic = errors.New("tactic failed")

func (t failTactic) Name() string                                 { return t.name }
func (t failTactic) Family() tactics.Family                       { return t.family }
func (t failTactic) Engage(context.Context, *Fight) (bool, error) { return false, errTactic }

// withTactics replaces the process's tactics for one test.
func withTactics(t *testing.T, ts ...FamilyTactic) {
	t.Helper()
	tacticRegistry.Lock()
	saved := maps.Clone(tacticRegistry.by)
	tacticRegistry.by = map[string]FamilyTactic{}
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

// usingTactic is a profile that fights the zombie family with the named
// tactic.
func usingTactic(name string) tactics.Profile {
	pr := tactics.NewProfile("tactic " + name)
	pr.Tactics = map[tactics.Family][]string{tactics.FamilyZombie: {name}}
	return pr
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
	pr.Tactics = map[tactics.Family][]string{tactics.FamilyZombie: {"zombie/pass"}}
	setPr := func(l *Life) {
		if err := l.SetProfile(pr); err != nil {
			t.Fatal(err)
		}
	}

	withTactics(t)
	base := replay(t, "striker", strikerOnAZombie, setPr)

	pass := &passTactic{name: "zombie/pass", family: tactics.FamilyZombie}
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
	stand := &standTactic{name: "zombie/stand", family: tactics.FamilyZombie}
	withTactics(t, stand)
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(usingTactic("zombie/stand")); err != nil {
		t.Fatal(err)
	}
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
	pass := &passTactic{name: "skeleton/pass", family: tactics.FamilySkeleton}
	withTactics(t, pass)
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(usingTactic("skeleton/pass")); err != nil {
		t.Fatal(err)
	}
	strikerOnAZombie(r)
	if len(pass.seen) != 0 {
		t.Errorf("the skeleton tactic was offered %d zombie ticks", len(pass.seen))
	}
	if len(r.out.attacks()) == 0 {
		t.Error("the striker never swung at the zombie")
	}
}

func TestATacticErrorIsTheTicksError(t *testing.T) {
	withTactics(t, failTactic{name: "zombie/fail", family: tactics.FamilyZombie})
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(usingTactic("zombie/fail")); err != nil {
		t.Fatal(err)
	}
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

func TestATacticErrorIsLoggedOncePerMinute(t *testing.T) {
	withTactics(t, failTactic{name: "zombie/fail", family: tactics.FamilyZombie})
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(usingTactic("zombie/fail")); err != nil {
		t.Fatal(err)
	}
	r.give(0, 303, 1)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})
	snap := r.tracker.Snapshot()
	if !r.l.Engaged(r.a.Pose(), snap) {
		t.Fatal("not engaged with a zombie in reach")
	}
	count := func() int {
		n := 0
		for _, ln := range r.log.lines {
			if ln.event == "tactic_error" {
				n++
			}
		}
		return n
	}
	for range 5 {
		r.l.tick++
		if err := r.l.Fight(context.Background(), r.a, snap); !errors.Is(err, errTactic) {
			t.Fatalf("Fight = %v, want the tactic's error unchanged", err)
		}
	}
	if n := count(); n != 1 {
		t.Fatalf("logged %d times in one minute, want once", n)
	}
	r.l.tick += tacticErrorEvery
	r.l.Fight(context.Background(), r.a, snap)
	if n := count(); n != 2 {
		t.Errorf("logged %d times after the minute, want a second line", n)
	}
}

func TestSetProfileOwnsItsTacticTable(t *testing.T) {
	r := newWarrior(t, "striker", start)
	pr := tactics.NewProfile("p")
	pr.Tactics = map[tactics.Family]string{tactics.FamilyZombie: "zombie/a"}
	if err := r.l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
	pr.Tactics[tactics.FamilyZombie] = "zombie/b"
	if got := r.l.Profile().TacticFor(tactics.FamilyZombie); got != "zombie/a" {
		t.Errorf("tactic = %q after the caller edited its map, want zombie/a", got)
	}
}

func TestTacticNamesListsAFamilysVariantsSorted(t *testing.T) {
	withTactics(t,
		&passTactic{name: "zombie/b", family: tactics.FamilyZombie},
		&passTactic{name: "zombie/a", family: tactics.FamilyZombie},
		&passTactic{name: "creeper/x", family: tactics.FamilyCreeper},
	)
	if got, want := TacticNames(tactics.FamilyZombie), []string{"zombie/a", "zombie/b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("TacticNames(zombie) = %v, want %v", got, want)
	}
	if got := TacticNames(tactics.FamilySpider); len(got) != 0 {
		t.Errorf("TacticNames(spider) = %v, want none", got)
	}
}

func TestRegisteringANameTwiceIsAnInitTimeBug(t *testing.T) {
	withTactics(t, &passTactic{name: "zombie/pass", family: tactics.FamilyZombie})
	defer func() {
		if recover() == nil {
			t.Error("registering a second tactic under the same name did not panic")
		}
	}()
	RegisterTactic(&passTactic{name: "zombie/pass", family: tactics.FamilyZombie})
}

func TestTheDefaultProfileNeverRunsARegisteredTactic(t *testing.T) {
	withTactics(t)
	base := replay(t, "striker", strikerOnAZombie, func(*Life) {})

	stand := &standTactic{name: "zombie/stand", family: tactics.FamilyZombie}
	withTactics(t, stand)
	r := newWarrior(t, "striker", start)
	strikerOnAZombie(r)
	if stand.ticks != 0 {
		t.Errorf("the tactic took %d ticks under the default profile", stand.ticks)
	}
	got := make([]any, len(r.out.got))
	for i, pk := range r.out.got {
		got[i] = pk
	}
	if !reflect.DeepEqual(got, base) {
		t.Errorf("registering a tactic changed the default fight: %d packets, want %d", len(got), len(base))
	}
}

func TestAProfileNamingAnUnknownOrForeignTacticFallsBackToTheRoleFight(t *testing.T) {
	for name, named := range map[string]string{
		"unknown": "zombie/nope",
		"foreign": "skeleton/stand",
	} {
		t.Run(name, func(t *testing.T) {
			zombie := &standTactic{name: "zombie/stand", family: tactics.FamilyZombie}
			skeleton := &standTactic{name: "skeleton/stand", family: tactics.FamilySkeleton}
			withTactics(t, zombie, skeleton)
			r := newWarrior(t, "striker", start)
			if err := r.l.SetProfile(usingTactic(named)); err != nil {
				t.Fatal(err)
			}
			strikerOnAZombie(r)
			if zombie.ticks+skeleton.ticks != 0 {
				t.Errorf("a tactic ran for the profile naming %q", named)
			}
			if len(r.out.attacks()) == 0 {
				t.Error("the striker never swung at the zombie")
			}
		})
	}
}

func TestSwappingTheProfileSwitchesTheTacticMidFight(t *testing.T) {
	stand := &standTactic{name: "zombie/stand", family: tactics.FamilyZombie}
	withTactics(t, stand)
	r := newWarrior(t, "striker", start)
	r.give(0, 303, 1)
	r.spawn(50, "minecraft:zombie", mgl32.Vec3{22.5, 1, 20.5})
	if err := r.l.SetProfile(usingTactic("zombie/stand")); err != nil {
		t.Fatal(err)
	}
	r.run(40)
	if stand.ticks == 0 {
		t.Fatal("the named tactic never took a tick")
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Fatalf("%d attacks while the tactic held the fight", n)
	}

	if err := r.l.SetProfile(tactics.NewProfile("back to default")); err != nil {
		t.Fatal(err)
	}
	held := stand.ticks
	r.run(40)
	if stand.ticks != held {
		t.Errorf("the tactic took %d more ticks after the profile stopped naming it", stand.ticks-held)
	}
	if len(r.out.attacks()) == 0 {
		t.Error("the role fight never resumed after the swap")
	}
}

func zombieList(names ...string) tactics.Profile {
	pr := tactics.NewProfile("zombie list")
	pr.Tactics = map[tactics.Family][]string{tactics.FamilyZombie: names}
	return pr
}

func TestTheFirstTacticToTakeTheTickWinsInListedOrder(t *testing.T) {
	first := &standTactic{name: "zombie/first", family: tactics.FamilyZombie}
	second := &standTactic{name: "zombie/second", family: tactics.FamilyZombie}
	withTactics(t, first, second)
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(zombieList("zombie/second", "zombie/first")); err != nil {
		t.Fatal(err)
	}
	strikerOnAZombie(r)
	if second.ticks == 0 {
		t.Fatal("the tactic listed first never took a tick")
	}
	if first.ticks != 0 {
		t.Errorf("the tactic listed second was offered %d ticks the first had taken", first.ticks)
	}
}

func TestATacticThatDeclinesFallsThroughToTheNext(t *testing.T) {
	pass := &passTactic{name: "zombie/pass", family: tactics.FamilyZombie}
	stand := &standTactic{name: "zombie/stand", family: tactics.FamilyZombie}
	withTactics(t, pass, stand)
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(zombieList("zombie/pass", "zombie/stand")); err != nil {
		t.Fatal(err)
	}
	strikerOnAZombie(r)
	if len(pass.seen) == 0 || stand.ticks == 0 {
		t.Fatalf("offered %d ticks to the decliner and %d taken by the next, want both", len(pass.seen), stand.ticks)
	}
	if len(pass.seen) != stand.ticks {
		t.Errorf("the decliner saw %d ticks and the next took %d, want every tick passed on", len(pass.seen), stand.ticks)
	}
	if n := len(r.out.attacks()); n != 0 {
		t.Errorf("%d attacks while the second tactic held the fight", n)
	}
}

func TestAListedTacticThatDeclinesEverywhereLeavesTheRoleFight(t *testing.T) {
	a := &passTactic{name: "zombie/a", family: tactics.FamilyZombie}
	b := &passTactic{name: "zombie/b", family: tactics.FamilyZombie}
	withTactics(t, a, b)
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(zombieList("zombie/a", "zombie/b")); err != nil {
		t.Fatal(err)
	}
	strikerOnAZombie(r)
	if len(a.seen) == 0 || len(b.seen) == 0 {
		t.Fatal("not every listed tactic was offered the tick")
	}
	if len(r.out.attacks()) == 0 {
		t.Error("the striker never swung after every listed tactic declined")
	}
}

func TestUnknownAndOtherFamilyNamesAreSkippedNotFatal(t *testing.T) {
	skeleton := &standTactic{name: "skeleton/stand", family: tactics.FamilySkeleton}
	stand := &standTactic{name: "zombie/stand", family: tactics.FamilyZombie}
	withTactics(t, skeleton, stand)
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(zombieList("zombie/missing", "skeleton/stand", "zombie/stand")); err != nil {
		t.Fatal(err)
	}
	strikerOnAZombie(r)
	if skeleton.ticks != 0 {
		t.Errorf("a skeleton tactic took %d zombie ticks", skeleton.ticks)
	}
	if stand.ticks == 0 {
		t.Error("the valid tactic after the skipped names never took a tick")
	}
}

func TestAnEmptyTacticListIsTheRoleFight(t *testing.T) {
	pass := &passTactic{name: "zombie/pass", family: tactics.FamilyZombie}
	withTactics(t, pass)
	r := newWarrior(t, "striker", start)
	if err := r.l.SetProfile(zombieList()); err != nil {
		t.Fatal(err)
	}
	strikerOnAZombie(r)
	if len(pass.seen) != 0 {
		t.Errorf("an empty list offered %d ticks to a tactic", len(pass.seen))
	}
	if len(r.out.attacks()) == 0 {
		t.Error("the striker never swung with an empty list")
	}
}

// swapTactic replaces the unit's profile from inside a tick and declines.
type swapTactic struct {
	name   string
	family tactics.Family
	to     tactics.Profile
}

func (t *swapTactic) Name() string           { return t.name }
func (t *swapTactic) Family() tactics.Family { return t.family }

func (t *swapTactic) Engage(_ context.Context, f *Fight) (bool, error) {
	return false, f.Life.SetProfile(t.to)
}

func TestAProfileSwappedMidTickDoesNotSplitThePhase(t *testing.T) {
	later := tactics.NewProfile("later")
	swap := &swapTactic{name: "zombie/swap", family: tactics.FamilyZombie, to: later}
	pass := &passTactic{name: "zombie/pass", family: tactics.FamilyZombie}
	withTactics(t, swap, pass)

	r := newWarrior(t, "striker", start)
	pr := tactics.NewProfile("earlier")
	pr.Tactics = map[tactics.Family][]string{tactics.FamilyZombie: {"zombie/swap", "zombie/pass"}}
	if err := r.l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
	strikerOnAZombie(r)

	if len(pass.seen) == 0 {
		t.Fatal("the tactic behind the swap was never offered the tick")
	}
	if got := pass.seen[0].Profile.Name; got != "earlier" {
		t.Errorf("the phase read profile %q after the swap, want the one it began with", got)
	}
	if got := r.l.Profile().Name; got != "later" {
		t.Errorf("unit profile = %q, want the swap to have landed", got)
	}
}
