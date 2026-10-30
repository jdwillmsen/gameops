package life

import (
	"slices"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

const (
	elytraID = 574
	rocketID = 575
	swordID  = 303
)

// airRig is a striker in a world with sky to fly in, a sword on slot 0, rockets
// on slot 1 and the elytra on its chest.
func airRig(t *testing.T, rockets int) *rig {
	t.Helper()
	r := newWarrior(t, "striker", start)
	r.w.size, r.w.sky = 400, 200
	table := append(slices.Clone(itemTable),
		protocol.ItemEntry{Name: "minecraft:elytra", RuntimeID: elytraID},
		protocol.ItemEntry{Name: "minecraft:firework_rocket", RuntimeID: rocketID})
	r.tracker.ItemNames(table)
	r.a.ItemNames(table)
	r.give(0, swordID, 1)
	if rockets > 0 {
		r.give(1, rocketID, rockets)
	}
	r.tracker.Apply(&packet.InventorySlot{WindowID: protocol.WindowIDArmour, Slot: 1, NewItem: protocol.ItemInstance{
		StackNetworkID: 9, Stack: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: elytraID}, Count: 1},
	}})
	mustProfile(t, r, usingAir())
	return r
}

func usingAir() tactics.Profile {
	pr := tactics.NewProfile("air")
	pr.Tactics = map[tactics.Family][]string{
		tactics.FamilyZombie: {"air/glide-zombie"},
		tactics.FamilyAir:    {"air/glide"},
		tactics.FamilyPlayer: {"air/glide-player"},
	}
	return pr
}

var phantomAt = mgl32.Vec3{28.5, 24, 20.5}

func TestTheAirTacticsAreRegisteredForTheirFamilies(t *testing.T) {
	for fam, want := range map[tactics.Family]string{
		tactics.FamilyAir: "air/glide", tactics.FamilyZombie: "air/glide-zombie", tactics.FamilyPlayer: "air/glide-player",
	} {
		if !slices.Contains(TacticNames(fam), want) {
			t.Errorf("no tactic %q registered for %s", want, fam)
		}
	}
}

func hasGlideStart(r *rig) bool {
	for _, in := range r.out.inputs() {
		if in.InputData.Load(packet.InputFlagStartGliding) {
			return true
		}
	}
	return false
}

func TestAnAirborneTargetSendsAnArmedUnitIntoTheAir(t *testing.T) {
	r := airRig(t, 32)
	r.spawn(50, "minecraft:phantom", phantomAt)
	for i := 0; i < 40 && !r.a.Pose().Gliding(); i++ {
		r.step()
	}
	if !r.a.Pose().Gliding() || !hasGlideStart(r) {
		t.Fatalf("never took off: gliding %v, feet %v", r.a.Pose().Gliding(), r.feet())
	}
}

func TestWithoutElytraTheTacticDeclinesAndTheUnitStaysOnTheGround(t *testing.T) {
	r := airRig(t, 32)
	r.tracker.Apply(&packet.InventorySlot{WindowID: protocol.WindowIDArmour, Slot: 1})
	r.spawn(50, "minecraft:phantom", phantomAt)
	r.run(80)
	if hasGlideStart(r) || r.a.Pose().Gliding() {
		t.Error("glided without an elytra")
	}
}

func TestWithoutRocketsTheTacticDeclinesOnTheGround(t *testing.T) {
	r := airRig(t, 0)
	r.spawn(50, "minecraft:phantom", phantomAt)
	r.run(80)
	if hasGlideStart(r) {
		t.Error("took off with no rockets to climb with")
	}
}

func TestATargetOnTheGroundIsLeftToTheGroundFight(t *testing.T) {
	r := airRig(t, 32)
	r.spawn(50, "minecraft:phantom", mgl32.Vec3{26.5, 1, 20.5})
	r.run(80)
	if hasGlideStart(r) {
		t.Error("took off against a target standing on the ground")
	}
}

func TestTheAirFightClimbsDivesStrikesAndRecovers(t *testing.T) {
	r := airRig(t, 64)
	r.spawn(50, "minecraft:phantom", phantomAt)
	seen := map[airPhase]bool{}
	var order []airPhase
	for i := 0; i < 900; i++ {
		r.step()
		st := airStates.of(r.l)
		if !seen[st.phase] {
			seen[st.phase] = true
			order = append(order, st.phase)
		}
		if len(r.out.attacks()) >= 3 && seen[airRecover] {
			break
		}
	}
	for _, ph := range []airPhase{airClimb, airDive, airStrike, airRecover} {
		if !seen[ph] {
			t.Errorf("phase %v never ran; order %v", ph, order)
		}
	}
	if len(r.out.attacks()) == 0 {
		t.Fatalf("no swing in the whole fight; feet %v", r.feet())
	}
	for _, a := range r.out.attacks() {
		if a.TargetEntityRuntimeID != 50 {
			t.Errorf("swung at %d, want the phantom", a.TargetEntityRuntimeID)
		}
	}
}

func TestRocketsAreSpacedAndAimedAtTheClimb(t *testing.T) {
	r := airRig(t, 64)
	r.spawn(50, "minecraft:phantom", phantomAt)
	var at []int
	last := 0
	for i := 0; i < 300; i++ {
		r.step()
		if n := r.out.uses(1); n > last {
			at = append(at, i)
			last = n
		}
	}
	if len(at) < 2 {
		t.Fatalf("rockets lit at ticks %v, want several over a climb", at)
	}
	for i := 1; i < len(at); i++ {
		if at[i]-at[i-1] < rocketEvery {
			t.Errorf("rockets at ticks %d and %d, closer than %d", at[i-1], at[i], rocketEvery)
		}
	}
}

func TestTheUnitSwingsOnlyWithinReachAndOnTheCadence(t *testing.T) {
	r := airRig(t, 64)
	r.spawn(50, "minecraft:phantom", phantomAt)
	var swings []int
	prev := 0
	for i := 0; i < 900; i++ {
		r.step()
		if n := len(r.out.attacks()); n > prev {
			prev = n
			swings = append(swings, i)
			eye := r.feet().Add(mgl32.Vec3{0, 1.62, 0})
			if d := eye.Sub(phantomAt.Add(mgl32.Vec3{0, bodyCentre, 0})).Len(); d > airReach+1.5 {
				t.Errorf("swung from %.1f blocks, reach is %.1f", d, airReach)
			}
		}
	}
	for i := 1; i < len(swings); i++ {
		if swings[i]-swings[i-1] < airAttackEvery {
			t.Errorf("swings at %d and %d, closer than %d ticks", swings[i-1], swings[i], airAttackEvery)
		}
	}
}

func TestTheSwordIsHeldForTheStrikeAndTheRocketsForTheClimb(t *testing.T) {
	r := airRig(t, 64)
	r.spawn(50, "minecraft:phantom", phantomAt)
	for i := 0; i < 900 && len(r.out.attacks()) == 0; i++ {
		r.step()
	}
	atks := r.out.attacks()
	if len(atks) == 0 {
		t.Fatal("never struck")
	}
	if atks[0].HotBarSlot != 0 || atks[0].HeldItem.Stack.NetworkID != swordID {
		t.Errorf("first strike with slot %d item %d, want the sword on slot 0", atks[0].HotBarSlot, atks[0].HeldItem.Stack.NetworkID)
	}
}

func TestWhenTheTargetFallsTheUnitGlidesDownAndLetsGoAtTheGround(t *testing.T) {
	r := airRig(t, 32)
	r.spawn(50, "minecraft:phantom", phantomAt)
	for i := 0; i < 400 && !r.a.Pose().Gliding(); i++ {
		r.step()
	}
	r.run(60)
	if !r.a.Pose().Gliding() {
		t.Fatal("not gliding to begin with")
	}
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 50})
	for i := 0; i < 1500 && !r.a.Pose().OnGround; i++ {
		r.step()
	}
	p := r.a.Pose()
	if !p.OnGround || p.Gliding() {
		t.Fatalf("still in the air after the target went: feet %v gliding %v", p.Feet, p.Gliding())
	}
	stops := 0
	for _, in := range r.out.inputs() {
		if in.InputData.Load(packet.InputFlagStopGliding) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("%d stop-gliding flags, want 1", stops)
	}
}

func TestAGlideThatLosesItsRocketsIsLandedNotAbandoned(t *testing.T) {
	r := airRig(t, 2)
	r.spawn(50, "minecraft:phantom", phantomAt)
	left, used := 2, 0
	for i := 0; i < 4000; i++ {
		r.step()
		// The server counts the rockets down; the rig has no server.
		if n := r.out.uses(1); n > used {
			left -= n - used
			used = n
			r.give(1, rocketID, max(left, 0))
			if left <= 0 {
				r.tracker.Apply(&packet.InventorySlot{WindowID: 0, Slot: 1})
			}
		}
		if left <= 0 && r.a.Pose().OnGround {
			break
		}
	}
	if used != 2 {
		t.Fatalf("lit %d rockets, want both", used)
	}
	if r.a.Pose().Gliding() || !r.a.Pose().OnGround {
		t.Fatalf("not landed with the rockets spent: feet %v gliding %v", r.feet(), r.a.Pose().Gliding())
	}
}

func TestAirborneMeansNoGroundWithinAFewBlocksBelow(t *testing.T) {
	r := airRig(t, 8)
	for _, tc := range []struct {
		at   mgl32.Vec3
		want bool
	}{
		{mgl32.Vec3{20.5, 24, 20.5}, true},
		{mgl32.Vec3{20.5, 1, 20.5}, false},
		{mgl32.Vec3{20.5, 3, 20.5}, false},
		{mgl32.Vec3{20.5, 9, 20.5}, true},
	} {
		if got := r.l.airborne(belief.Entity{Pos: tc.at}); got != tc.want {
			t.Errorf("airborne at %v = %v, want %v", tc.at, got, tc.want)
		}
	}
}

func TestAnAirUnitFocusesAThreatHigherThanAGroundUnitSees(t *testing.T) {
	r := airRig(t, 8)
	r.spawn(50, "minecraft:phantom", phantomAt)
	if !r.l.Engaged(r.a.Pose(), r.tracker.Snapshot()) {
		t.Error("an armed unit did not engage a phantom 23 blocks up")
	}
	g := newWarrior(t, "striker", start)
	g.spawn(50, "minecraft:phantom", phantomAt)
	if g.l.Engaged(g.a.Pose(), g.tracker.Snapshot()) {
		t.Error("a ground unit engaged a phantom 23 blocks up")
	}
}
