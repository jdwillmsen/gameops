package life

import (
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
)

func TestDecideTable(t *testing.T) {
	c := DefaultCombatBaseline()
	cases := []struct {
		name string
		in   situation
		want stance
	}{
		// Ordinary mobs.
		{"melee healthy by day", situation{Dist: 4}, stanceFight},
		{"melee while fleeing", situation{Dist: 4, Fleeing: true}, stanceRetreat},
		{"melee while fleeing in reach", situation{Dist: 1, InReach: true, Fleeing: true}, stanceRetreat},
		{"melee fleeing but cornered", situation{Dist: 1, InReach: true, Fleeing: true, Cornered: true}, stanceFight},
		{"night under-geared, mob not yet in reach", situation{Dist: 4, Night: true, Under: true}, stanceRetreat},
		{"night under-geared, mob in reach", situation{Dist: 1, InReach: true, Night: true, Under: true}, stanceFight},
		{"night under-geared but cornered", situation{Dist: 4, Night: true, Under: true, Cornered: true}, stanceFight},
		{"night well geared", situation{Dist: 4, Night: true}, stanceFight},
		{"day under-geared", situation{Dist: 4, Under: true}, stanceFight},

		// Creepers: never struck once lit or charged, struck only when cornered.
		{"unlit creeper backs off", situation{Threat: threatCreeper, Dist: 4}, stanceRetreat},
		{"lit creeper backs off", situation{Threat: threatCreeper, Dist: 2, Lit: true, InReach: true}, stanceRetreat},
		{"charged creeper backs off", situation{Threat: threatCreeper, Dist: 6, Charged: true}, stanceRetreat},
		{"lit creeper, cornered, in reach: hold, never strike", situation{Threat: threatCreeper, Dist: 2, Lit: true, InReach: true, Cornered: true}, stanceHold},
		{"charged creeper, cornered, in reach: hold", situation{Threat: threatCreeper, Dist: 2, Charged: true, InReach: true, Cornered: true}, stanceHold},
		{"unlit creeper, cornered, in reach: one blow", situation{Threat: threatCreeper, Dist: 2, InReach: true, Cornered: true}, stanceFight},
		{"unlit creeper, cornered, out of reach: hold", situation{Threat: threatCreeper, Dist: 4, Cornered: true}, stanceHold},
		{"unlit creeper, cornered, fleeing: hold", situation{Threat: threatCreeper, Dist: 2, InReach: true, Cornered: true, Fleeing: true}, stanceHold},

		// Archers.
		{"archer in reach is meleed", situation{Threat: threatArcher, Dist: 2, InReach: true, Shield: true, Aimed: true, GuardTurn: true}, stanceFight},
		{"archer, nothing special: weave", situation{Threat: threatArcher, Dist: 10}, stanceWeave},
		{"archer, arrow closing, shield: guard", situation{Threat: threatArcher, Dist: 10, Shield: true, ArrowIn: true}, stanceGuard},
		{"archer, arrow closing, no shield: weave", situation{Threat: threatArcher, Dist: 10, ArrowIn: true}, stanceWeave},
		{"archer aimed, guarded part of the cycle: guard", situation{Threat: threatArcher, Dist: 10, Shield: true, Aimed: true, GuardTurn: true}, stanceGuard},
		{"archer aimed, advancing part of the cycle: weave", situation{Threat: threatArcher, Dist: 10, Shield: true, Aimed: true}, stanceWeave},
		{"archer aimed point blank: weave straight in", situation{Threat: threatArcher, Dist: 2.9, Shield: true, Aimed: true, GuardTurn: true}, stanceWeave},
		{"archer, arrow closing point blank: guard", situation{Threat: threatArcher, Dist: 2.9, Shield: true, ArrowIn: true}, stanceGuard},
		{"archer, weave stalled: pathing layer", situation{Threat: threatArcher, Dist: 10, Stalled: true}, stanceFight},
		{"archer while fleeing: break the line", situation{Threat: threatArcher, Dist: 10, Fleeing: true, Shield: true, ArrowIn: true}, stanceCover},
		{"archer at night under-geared: break the line", situation{Threat: threatArcher, Dist: 10, Night: true, Under: true}, stanceCover},
		{"archer fleeing but cornered: duel on", situation{Threat: threatArcher, Dist: 10, Fleeing: true, Cornered: true}, stanceWeave},
		{"archer fleeing, shield, cornered: guard", situation{Threat: threatArcher, Dist: 10, Fleeing: true, Cornered: true, Shield: true, ArrowIn: true}, stanceGuard},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.decide(tc.in); got != tc.want {
				t.Errorf("decide = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDecideNightAvoidanceCanBeSwitchedOff(t *testing.T) {
	c := DefaultCombatBaseline()
	c.AvoidNight = false
	in := situation{Dist: 4, Night: true, Under: true}
	if got := c.decide(in); got != stanceFight {
		t.Errorf("decide = %v with night avoidance off, want fight", got)
	}
	in.Threat = threatArcher
	if got := c.decide(in); got != stanceWeave {
		t.Errorf("archer decide = %v with night avoidance off, want weave", got)
	}
}

func TestLatchTripsAtTheFleeBarAndHoldsUntilResume(t *testing.T) {
	c := DefaultCombatBaseline()
	cases := []struct {
		name         string
		prev         bool
		health       float32
		known        bool
		night, under bool
		want         bool
	}{
		{"healthy", false, 20, true, false, false, false},
		{"just above the bar", false, 7, true, false, false, false},
		{"at the bar", false, 6, true, false, false, true},
		{"under the bar", false, 2, true, false, false, true},
		{"recovering, below resume: still fleeing", true, 10, true, false, false, true},
		{"just under resume: still fleeing", true, 11.5, true, false, false, true},
		{"back at resume", true, 12, true, false, false, false},
		{"mid health never trips on its own", false, 10, true, false, false, false},
		{"night bar needs night", false, 8, true, false, true, false},
		{"night bar needs under-geared", false, 8, true, true, false, false},
		{"night and under-geared trips higher", false, 8, true, true, true, true},
		{"unknown health trips nothing", true, 1, false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.latch(tc.prev, tc.health, tc.known, tc.night, tc.under); got != tc.want {
				t.Errorf("latch = %v, want %v", got, tc.want)
			}
		})
	}
	c.AvoidNight = false
	if c.latch(false, 8, true, true, true) {
		t.Error("the night bar applied with AvoidNight off")
	}
}

// The bars are tied to the reflexes they sit beside: the flee bar is the
// reactive defence's own, and the resume bar is a health a unit fed by the
// eat reflex can reach.
func TestDefaultBarsAgreeWithTheHealthReflex(t *testing.T) {
	c := DefaultCombatBaseline()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.FleeHealth != lowHealth {
		t.Errorf("flee bar %v, the reflex's low health is %d", c.FleeHealth, lowHealth)
	}
	if !(c.FleeHealth <= c.NightFleeHealth && c.NightFleeHealth < c.ResumeHealth && c.ResumeHealth <= fullHealth) {
		t.Errorf("bars out of order: %+v", c)
	}
	// Regeneration needs food of 18; the reflex eats a hurt unit from 17, so
	// a unit sent to heal has what it needs to climb to the resume bar.
	if eatBelowHurt < 17 {
		t.Errorf("eatBelowHurt %d no longer lets a hurt unit reach regeneration", eatBelowHurt)
	}
}

func TestValidate(t *testing.T) {
	good := DefaultCombatBaseline()
	cases := []struct {
		name string
		mod  func(*CombatBaseline)
		ok   bool
	}{
		{"defaults", func(*CombatBaseline) {}, true},
		{"flee zero", func(c *CombatBaseline) { c.FleeHealth = 0 }, false},
		{"night bar below day bar", func(c *CombatBaseline) { c.NightFleeHealth = c.FleeHealth - 1 }, false},
		{"night bar equals day bar", func(c *CombatBaseline) { c.NightFleeHealth = c.FleeHealth }, true},
		{"resume at night bar", func(c *CombatBaseline) { c.ResumeHealth = c.NightFleeHealth }, false},
		{"resume above full", func(c *CombatBaseline) { c.ResumeHealth = fullHealth + 1 }, false},
		{"resume at full", func(c *CombatBaseline) { c.ResumeHealth = fullHealth }, true},
		{"negative armour bar", func(c *CombatBaseline) { c.MinArmour = -1 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := good
			tc.mod(&c)
			if err := c.Validate(); (err == nil) != tc.ok {
				t.Errorf("Validate = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func pct(n int) *int { return &n }

func TestArmourRating(t *testing.T) {
	piece := func(slot, name string, dur *int) belief.ArmourPiece {
		return belief.ArmourPiece{Slot: slot, Name: name, DurabilityPct: dur}
	}
	cases := []struct {
		name  string
		worn  []belief.ArmourPiece
		score int
	}{
		{"nothing", nil, 0},
		{"full iron", []belief.ArmourPiece{piece("helmet", "iron_helmet", nil), piece("chest", "iron_chestplate", nil), piece("legs", "iron_leggings", nil), piece("boots", "iron_boots", nil)}, 15},
		{"full leather", []belief.ArmourPiece{piece("helmet", "leather_helmet", nil), piece("chest", "leather_chestplate", nil), piece("legs", "leather_leggings", nil), piece("boots", "leather_boots", nil)}, 7},
		{"full diamond", []belief.ArmourPiece{piece("helmet", "diamond_helmet", nil), piece("chest", "diamond_chestplate", nil), piece("legs", "diamond_leggings", nil), piece("boots", "diamond_boots", nil)}, 20},
		{"namespaced", []belief.ArmourPiece{piece("chest", "minecraft:iron_chestplate", nil)}, 6},
		{"worn out piece does not count", []belief.ArmourPiece{piece("chest", "iron_chestplate", pct(10))}, 0},
		{"piece at the line counts", []belief.ArmourPiece{piece("chest", "iron_chestplate", pct(wornOutPct))}, 6},
		{"elytra is no armour", []belief.ArmourPiece{piece("chest", "elytra", nil)}, 0},
		{"unknown material", []belief.ArmourPiece{piece("helmet", "mystery_helmet", nil)}, 0},
		{"turtle shell", []belief.ArmourPiece{piece("helmet", "turtle_helmet", nil)}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := armourRating(belief.Snapshot{Armour: tc.worn}); got != tc.score {
				t.Errorf("armourRating = %d, want %d", got, tc.score)
			}
		})
	}
}

func TestUnderGeared(t *testing.T) {
	c := DefaultCombatBaseline()
	iron := []belief.ArmourPiece{
		{Slot: "helmet", Name: "iron_helmet"}, {Slot: "chest", Name: "iron_chestplate"},
		{Slot: "legs", Name: "iron_leggings"}, {Slot: "boots", Name: "iron_boots"},
	}
	sword := belief.Item{Slot: 0, Name: "stone_sword", Count: 1}
	axe := belief.Item{Slot: 0, Name: "wooden_axe", Count: 1}
	pick := belief.Item{Slot: 0, Name: "iron_pickaxe", Count: 1}
	cases := []struct {
		name string
		snap belief.Snapshot
		want bool
	}{
		{"sword and iron", belief.Snapshot{Inventory: []belief.Item{sword}, Armour: iron}, false},
		{"axe and iron", belief.Snapshot{Inventory: []belief.Item{axe}, Armour: iron}, false},
		{"only a pickaxe", belief.Snapshot{Inventory: []belief.Item{pick}, Armour: iron}, true},
		{"bare hands", belief.Snapshot{Armour: iron}, true},
		{"sword, no armour", belief.Snapshot{Inventory: []belief.Item{sword}}, true},
		{"sword, helmet and boots only", belief.Snapshot{Inventory: []belief.Item{sword}, Armour: []belief.ArmourPiece{iron[0], iron[3]}}, true},
		{"sword, chest and legs only", belief.Snapshot{Inventory: []belief.Item{sword}, Armour: iron[1:3]}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.underGeared(tc.snap); got != tc.want {
				t.Errorf("underGeared = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsNight(t *testing.T) {
	for _, tc := range []struct {
		phase string
		dim   int32
		want  bool
	}{
		{belief.PhaseNight, 0, true},
		{belief.PhaseDusk, 0, true},
		{belief.PhaseDay, 0, false},
		{belief.PhaseDawn, 0, false},
		{"", 0, false},
		{belief.PhaseNight, 1, false},
	} {
		s := belief.Snapshot{Dimension: tc.dim, Env: belief.Env{DayPhase: tc.phase}}
		if got := isNight(s); got != tc.want {
			t.Errorf("isNight(%q, dim %d) = %v, want %v", tc.phase, tc.dim, got, tc.want)
		}
	}
}

func TestCreeperFarClearsTheBlast(t *testing.T) {
	cases := []struct {
		name string
		in   situation
		want float64
	}{
		{"plain, close", situation{Dist: 2}, 8},
		{"plain, never under the ordinary retreat", situation{Dist: 5}, 8},
		{"charged, close", situation{Dist: 2, Charged: true}, 12.5 - 2 + 1.5},
		{"charged, far", situation{Dist: 9, Charged: true}, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := creeperFar(tc.in); got != tc.want {
				t.Errorf("creeperFar = %v, want %v", got, tc.want)
			}
		})
	}
}
