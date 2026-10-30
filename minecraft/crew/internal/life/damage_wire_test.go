package life

import (
	"encoding/json"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func (r *rig) spawnWithHealth(id uint64, typ string, at mgl32.Vec3, hp float32) {
	r.l.Apply(&packet.AddActor{
		EntityUniqueID: int64(id), EntityRuntimeID: id, EntityType: typ, Position: at,
		Attributes: []protocol.AttributeValue{{Name: healthName, Value: hp, Max: hp}},
	})
}

func (r *rig) mobHealth(id uint64, hp float32) {
	r.l.Apply(&packet.UpdateAttributes{
		EntityRuntimeID: id,
		Attributes:      []protocol.Attribute{{AttributeValue: protocol.AttributeValue{Name: healthName, Value: hp}}},
	})
}

// dealtRig is a Vanguard with a 20 HP zombie in reach and one swing sent.
func dealtRig(t *testing.T) *rig {
	t.Helper()
	r := newWarrior(t, "vanguard", start)
	r.give(0, 303, 1)
	r.spawnWithHealth(50, "minecraft:zombie", zombieSide, 20)
	r.step()
	if n := len(r.out.attacks()); n != 1 {
		t.Fatalf("%d swings on the first tick, want 1", n)
	}
	return r
}

func TestDamageDealtIsTheHealthTheTargetLostToTheUnitsSwing(t *testing.T) {
	r := dealtRig(t)
	r.mobHealth(50, 15)
	r.step()
	s, _ := r.l.Stats()
	if s.Total.DamageDealt != 5 || s.Current.DamageDealt != 5 {
		t.Fatalf("stats = %+v, want 5 dealt in total and in the fight", s)
	}
	if s.Total.DamageTaken != 0 {
		t.Errorf("damage taken = %v, a mob's health drop is not the unit's", s.Total.DamageTaken)
	}
}

func TestDamageNoSwingAccountsForIsNotDealt(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	r.spawnWithHealth(50, "minecraft:zombie", mgl32.Vec3{40, 1, 40}, 20)
	r.step()
	r.mobHealth(50, 10)
	r.step()
	if s, _ := r.l.Stats(); s.Total.DamageDealt != 0 {
		t.Fatalf("dealt = %v for a drop with no swing", s.Total.DamageDealt)
	}
}

func TestDamageDealtKeepsTheTotalAcrossFightsAndResetsTheFight(t *testing.T) {
	r := dealtRig(t)
	r.mobHealth(50, 14)
	r.step()
	r.l.Apply(&packet.RemoveActor{EntityUniqueID: 50})
	r.run(graceTicks + 5)

	r.spawnWithHealth(52, "minecraft:zombie", zombieSide, 20)
	r.step()
	s, _ := r.l.Stats()
	if s.Total.DamageDealt != 6 || s.Current.DamageDealt != 0 {
		t.Fatalf("stats = %+v, want 6 in total and a fresh fight", s)
	}
	r.mobHealth(52, 18)
	r.step()
	if s, _ := r.l.Stats(); s.Total.DamageDealt != 8 || s.Current.DamageDealt != 2 {
		t.Fatalf("stats = %+v, want 8 in total and 2 in the fight", s)
	}
}

func TestADeathDoesNotLetTheOldSwingClaimTheNextLifesDrop(t *testing.T) {
	r := dealtRig(t)
	r.l.Reset()
	r.mobHealth(50, 10)
	r.step()
	if s, _ := r.l.Stats(); s.Total.DamageDealt != 0 {
		t.Fatalf("dealt = %v, want the swing of the ended life forgotten", s.Total.DamageDealt)
	}
}

func TestHealthSeenIsLoggedOncePerHostileType(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	far := mgl32.Vec3{60, 1, 60}
	r.spawnWithHealth(50, "minecraft:zombie", far, 20)
	r.spawnWithHealth(51, "minecraft:zombie", far, 20)
	r.spawnWithHealth(52, "minecraft:cow", far, 10)
	r.mobHealth(50, 18)
	r.mobHealth(selfID, 20)
	r.l.Apply(&packet.AddActor{EntityUniqueID: 53, EntityRuntimeID: 53, EntityType: "minecraft:skeleton", Position: far})
	r.step()
	r.mobHealth(53, 12)
	r.step()
	r.step()

	got := r.events("health_seen")
	if len(got) != 2 {
		t.Fatalf("health_seen = %v, want one for the zombie and one for the skeleton", got)
	}
	if got[0]["entity"] != "zombie" || got[0]["health"] != 20.0 || got[1]["entity"] != "skeleton" || got[1]["health"] != 12.0 {
		t.Errorf("health_seen = %v", got)
	}
}

func TestHealthSeenIsLoggedAgainInANewSession(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	far := mgl32.Vec3{60, 1, 60}
	r.spawnWithHealth(50, "minecraft:zombie", far, 20)
	r.step()
	r.l.Spawn(selfID)
	r.spawnWithHealth(50, "minecraft:zombie", far, 20)
	r.step()
	if n := len(r.events("health_seen")); n != 2 {
		t.Fatalf("%d health_seen lines over two sessions, want 2", n)
	}
}

func TestHealthSeenIsCappedPerSession(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	far := mgl32.Vec3{60, 1, 60}
	id := uint64(100)
	for typ := range kinds {
		id++
		r.l.Apply(&packet.AddActor{EntityUniqueID: int64(id), EntityRuntimeID: id, EntityType: "minecraft:" + typ, Position: far,
			Attributes: []protocol.AttributeValue{{Name: healthName, Value: 5}}})
	}
	if len(kinds) <= maxHealthTypes {
		t.Fatalf("only %d mob kinds, too few to reach the cap of %d", len(kinds), maxHealthTypes)
	}
	r.step()
	if n := len(r.events("health_seen")); n != maxHealthTypes {
		t.Fatalf("%d health_seen lines, want the cap of %d", n, maxHealthTypes)
	}
}

func TestStateBodyCarriesDamageDealtNextToDamageTaken(t *testing.T) {
	r := dealtRig(t)
	r.mobHealth(50, 17)
	r.step()
	s, _ := r.l.Stats()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Total   map[string]float64 `json:"total"`
		Current map[string]float64 `json:"current"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Total["damage_dealt"] != 3 || got.Current["damage_dealt"] != 3 {
		t.Errorf("state body %s, want damage_dealt 3 in total and current", raw)
	}
	if _, ok := got.Total["damage_taken"]; !ok {
		t.Errorf("state body %s lost damage_taken", raw)
	}
}
