package life

import (
	"encoding/json"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

var farAway = mgl32.Vec3{60, 1, 60}

func TestActorFlagsAreLoggedOnceForTheFirstSightingOfAFlagState(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	r.spawn(60, "minecraft:creeper", farAway)
	r.setActorFlags(60)
	r.spawn(61, "minecraft:creeper", farAway)
	r.setActorFlags(61)
	r.spawn(62, "minecraft:cow", farAway)
	r.setActorFlags(62)
	r.step()

	got := r.events("actor_flags")
	if len(got) != 1 {
		t.Fatalf("actor_flags = %v, want one line for the plain creeper pair and none for the cow", got)
	}
	want := map[string]any{"type": "creeper", "ignited": false, "charged": false, "angry": false}
	for k, v := range want {
		if got[0][k] != v {
			t.Errorf("actor_flags[%q] = %v, want %v (line %v)", k, got[0][k], v, got[0])
		}
	}
}

func TestActorFlagsSeparateAChargedCreeperFromAPlainOne(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	r.spawn(60, "minecraft:creeper", farAway)
	r.setActorFlags(60)
	r.spawn(61, "minecraft:creeper", farAway)
	r.setActorFlags(61, protocol.EntityDataFlagPowered)
	r.step()

	got := r.events("actor_flags")
	if len(got) != 2 || got[0]["charged"] != false || got[1]["charged"] != true {
		t.Fatalf("actor_flags = %v, want the plain creeper first and the charged one after it", got)
	}
}

func TestActorFlagsWaitForTheFlagWord(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	r.spawn(60, "minecraft:creeper", farAway)
	r.step()
	if got := r.events("actor_flags"); len(got) != 0 {
		t.Fatalf("actor_flags = %v for a creeper that was never told its flags", got)
	}
	r.setActorFlags(60, protocol.EntityDataFlagIgnited)
	r.step()
	got := r.events("actor_flags")
	if len(got) != 1 || got[0]["ignited"] != true {
		t.Fatalf("actor_flags = %v, want one ignited line once the flag word arrived", got)
	}
}

func TestActorFlagsAreCappedPerSession(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	id := uint64(100)
	for typ := range kinds {
		for _, bit := range []uint8{protocol.EntityDataFlagIgnited, protocol.EntityDataFlagAngry} {
			id++
			r.spawn(id, "minecraft:"+typ, farAway)
			r.setActorFlags(id, bit)
		}
	}
	r.step()
	if n := len(r.events("actor_flags")); n != maxFlagSightings {
		t.Fatalf("%d actor_flags lines, want the cap of %d", n, maxFlagSightings)
	}
}

func TestSightingsMirrorTheLoggedLinesAndRestartWithASession(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	if _, ok := r.l.Sightings(); !ok {
		t.Fatal("a warrior reports no sightings")
	}
	r.spawnWithHealth(50, "minecraft:zombie", farAway, 20)
	r.spawn(60, "minecraft:creeper", farAway)
	r.setActorFlags(60, protocol.EntityDataFlagPowered)
	r.step()

	s, _ := r.l.Sightings()
	if len(s.Health) != 1 || s.Health[0].Entity != "zombie" || s.Health[0].Health != 20 {
		t.Fatalf("health sightings = %+v", s.Health)
	}
	if len(s.Flags) != 1 || s.Flags[0].Type != "creeper" || !s.Flags[0].Charged || s.Flags[0].Ignited {
		t.Fatalf("flag sightings = %+v", s.Flags)
	}
	if s.Health[0].Seq == 0 || s.Flags[0].Seq <= s.Health[0].Seq {
		t.Errorf("seq = health %d, flags %d: want a rising count so a reader can ask for what is new", s.Health[0].Seq, s.Flags[0].Seq)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string][]map[string]any
	if err := json.Unmarshal(b, &back); err != nil || back["health"][0]["entity"] != "zombie" || back["flags"][0]["charged"] != true {
		t.Errorf("sightings JSON = %s (%v)", b, err)
	}

	r.l.Spawn(selfID)
	if s, _ := r.l.Sightings(); len(s.Health) != 0 || len(s.Flags) != 0 {
		t.Errorf("sightings = %+v after a new session", s)
	}
}

func TestSightingsAreAbsentForAUnitThatIsNotAWarrior(t *testing.T) {
	r := newRig(t, defaults, start)
	if _, ok := r.l.Sightings(); ok {
		t.Fatal("a non-warrior reports sightings")
	}
}
