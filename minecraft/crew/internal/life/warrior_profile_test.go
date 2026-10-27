package life

import (
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func TestFamilyOfClassifiesEveryFamily(t *testing.T) {
	for _, tc := range []struct {
		typ    string
		player bool
		want   tactics.Family
	}{
		{typ: "minecraft:zombie", want: tactics.FamilyZombie},
		{typ: "minecraft:husk", want: tactics.FamilyZombie},
		{typ: "minecraft:drowned", want: tactics.FamilyZombie},
		{typ: "minecraft:zombie_villager_v2", want: tactics.FamilyZombie},
		{typ: "minecraft:zombie_pigman", want: tactics.FamilyZombie},
		{typ: "minecraft:skeleton", want: tactics.FamilySkeleton},
		{typ: "minecraft:stray", want: tactics.FamilySkeleton},
		{typ: "minecraft:bogged", want: tactics.FamilySkeleton},
		{typ: "minecraft:wither_skeleton", want: tactics.FamilySkeleton},
		{typ: "minecraft:creeper", want: tactics.FamilyCreeper},
		{typ: "minecraft:spider", want: tactics.FamilySpider},
		{typ: "minecraft:cave_spider", want: tactics.FamilySpider},
		{typ: "minecraft:blaze", want: tactics.FamilyBlaze},
		{typ: "minecraft:breeze", want: tactics.FamilyBlaze},
		{typ: "minecraft:enderman", want: tactics.FamilyEnderman},
		{typ: "minecraft:witch", want: tactics.FamilyWitch},
		{typ: "minecraft:pillager", want: tactics.FamilyIllager},
		{typ: "minecraft:vindicator", want: tactics.FamilyIllager},
		{typ: "minecraft:evocation_illager", want: tactics.FamilyIllager},
		{typ: "minecraft:vex", want: tactics.FamilyIllager},
		{typ: "minecraft:piglin_brute", want: tactics.FamilyBrute},
		{typ: "minecraft:ravager", want: tactics.FamilyBrute},
		{typ: "minecraft:hoglin", want: tactics.FamilyBrute},
		{typ: "minecraft:warden", want: tactics.FamilyBoss},
		{typ: "minecraft:wither", want: tactics.FamilyBoss},
		{typ: "minecraft:elder_guardian", want: tactics.FamilyBoss},
		{typ: "player", player: true, want: tactics.FamilyPlayer},
		{typ: "minecraft:slime", want: tactics.FamilyDefault},
		{typ: "minecraft:cow", want: tactics.FamilyDefault},
		{typ: "", want: tactics.FamilyDefault},
	} {
		if got := familyOf(belief.Entity{ID: 9, Type: tc.typ, Player: tc.player}); got != tc.want {
			t.Errorf("familyOf(%q, player=%v) = %q, want %q", tc.typ, tc.player, got, tc.want)
		}
	}
}

// The engine's constants and the contract's defaults are two copies of the
// same numbers; a unit with no profile must fight exactly as it shipped.
func TestTacticsDefaultsAreTheShippedConstants(t *testing.T) {
	want := tactics.Params{
		FocusRange: focusRange, HealBelow: healBelow, HealSafe: healSafe,
		PressRange: pressRange, RegroupWithin: regroupWithin, KiteBackoff: kiteBackoff,
		ShotEvery: shotEvery, ArrowNear: arrowNear, LineWidth: lineWidth,
		OrbitMin: orbitMin, CritWindow: critWindow, MeleeShieldTick: meleeShieldTicks,
	}
	if got := tactics.Defaults(); got != want {
		t.Fatalf("tactics.Defaults() = %+v, want the engine constants %+v", got, want)
	}
	if got := tactics.Defaults().Clamp(); got != want {
		t.Fatalf("the shipped constants fall outside tactics.Bounds: clamped to %+v", got)
	}
}

func TestParamsForResolvesTheTargetsFamily(t *testing.T) {
	l := New(Config{Role: "ranger"}, nil, nil, nil, nil)
	zombie := belief.Entity{ID: 50, Type: "minecraft:zombie"}
	skeleton := belief.Entity{ID: 51, Type: "minecraft:skeleton"}

	if got := l.paramsFor(zombie); got != tactics.Defaults() {
		t.Fatalf("no profile set: params %+v, want the defaults", got)
	}

	pr := tactics.NewProfile("lab")
	kite := tactics.Defaults()
	kite.KiteBackoff = 8
	pr.Overrides = map[tactics.Family]tactics.Params{tactics.FamilyZombie: kite}
	if err := l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
	if got := l.paramsFor(zombie).KiteBackoff; got != 8 {
		t.Errorf("zombie kiteBackoff = %v, want the override's 8", got)
	}
	if got := l.paramsFor(skeleton).KiteBackoff; got != kiteBackoff {
		t.Errorf("skeleton kiteBackoff = %v, want the default %v", got, float64(kiteBackoff))
	}
	if got := l.paramsFor(belief.Entity{}); got != pr.Default {
		t.Errorf("no target: params %+v, want the profile default", got)
	}
}

func TestSetProfileClampsCopiesAndRefusesAnUnreadableProfile(t *testing.T) {
	l := New(Config{Role: "striker"}, nil, nil, nil, nil)
	zombie := belief.Entity{ID: 50, Type: "minecraft:zombie"}

	wild := tactics.Defaults()
	wild.ShotEvery = 0
	overrides := map[tactics.Family]tactics.Params{tactics.FamilyZombie: wild}
	pr := tactics.NewProfile("wild")
	pr.Overrides = overrides
	if err := l.SetProfile(pr); err != nil {
		t.Fatal(err)
	}
	if got := l.paramsFor(zombie).ShotEvery; got != tactics.Bounds()["shotEvery"].Min {
		t.Errorf("shotEvery 0 loaded as %v, want it clamped to the bound", got)
	}
	if overrides[tactics.FamilyZombie].ShotEvery != 0 {
		t.Error("SetProfile wrote to the caller's overrides")
	}
	overrides[tactics.FamilyZombie] = tactics.Defaults()
	if got := l.paramsFor(zombie).ShotEvery; got == shotEvery {
		t.Error("a later write to the caller's map reached the profile in force")
	}

	stale := tactics.NewProfile("stale")
	stale.Version = tactics.Version + 1
	if err := l.SetProfile(stale); err == nil {
		t.Fatal("a profile of another version was accepted")
	}
	if got := l.Profile().Name; got != "wild" {
		t.Errorf("profile in force = %q after a refused swap, want the old one kept", got)
	}
}
