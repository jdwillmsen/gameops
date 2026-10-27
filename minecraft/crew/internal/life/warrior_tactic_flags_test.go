package life

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// setActorFlags sends the flag word a server sends when an actor's state
// changes: every flag it names set, every other one clear.
func (r *rig) setActorFlags(id uint64, bits ...uint8) {
	var v int64
	for _, b := range bits {
		v |= 1 << int64(b)
	}
	r.l.Apply(&packet.SetActorData{EntityRuntimeID: id, EntityMetadata: protocol.EntityMetadata{protocol.EntityDataKeyFlags: v}})
}

// setActorFuse sends metadata that has no flag word, which says nothing about
// whether the actor is lit, charged or angry.
func (r *rig) setActorFuse(id uint64) {
	r.l.Apply(&packet.SetActorData{EntityRuntimeID: id, EntityMetadata: protocol.EntityMetadata{protocol.EntityDataKeyFuseTime: int32(30)}})
}

func TestFlaglessMetadataLeavesTheCreeperHeuristicUntouched(t *testing.T) {
	for _, name := range []string{"creeper/backoff", "creeper/ranged"} {
		t.Run(name, func(t *testing.T) {
			run := func(withMeta bool) ([]any, int, float64) {
				r := creeperRig(t, "striker", name, nil)
				c := mgl32.Vec3{23.95, 1, 20.5}
				r.spawn(creeperID, "minecraft:creeper", c)
				if withMeta {
					r.setActorFuse(creeperID)
				}
				r.run(30)
				out := make([]any, len(r.out.got))
				for i, pk := range r.out.got {
					out[i] = pk
				}
				return out, swings(r), dist(r.feet(), c)
			}
			base, baseSwings, baseDist := run(false)
			got, _, _ := run(true)
			if !reflect.DeepEqual(got, base) {
				t.Errorf("metadata without a flag word changed the fight: %d packets, want %d", len(got), len(base))
			}
			if baseSwings != 0 || baseDist < blastReach {
				t.Errorf("the stillness heuristic no longer reads a stopped creeper as lit: %d blows, %.1f blocks off", baseSwings, baseDist)
			}
		})
	}
}

func TestAnIgnitedFlagBacksTheUnitOffACreeperThatStillWalks(t *testing.T) {
	r := creeperRig(t, "striker", "creeper/backoff", nil)
	f := newFuse(r, mgl32.Vec3{24.4, 1, 20.5}, 0.1)
	r.setActorFlags(creeperID, protocol.EntityDataFlagIgnited)
	f.run(30)
	if n := swings(r); n != 0 {
		t.Errorf("%d blows on a creeper whose fuse is burning", n)
	}
	if d := dist(r.feet(), start); d < 2 {
		t.Errorf("the unit moved %.1f blocks: it should back out of the blast", d)
	}
}

func TestAKnownUnlitCreeperThatStandsStillIsNotTakenForALitOne(t *testing.T) {
	still := func(flags bool) int {
		r := creeperRig(t, "striker", "creeper/backoff", nil)
		c := mgl32.Vec3{23.8, 1, 20.5}
		r.spawn(creeperID, "minecraft:creeper", c)
		if flags {
			r.setActorFlags(creeperID)
		}
		r.run(30)
		return swings(r)
	}
	if guessed, told := still(false), still(true); told == 0 || told <= guessed {
		t.Errorf("%d blows on a creeper the server says is unlit, %d when it was guessed lit from standing still", told, guessed)
	}
}

func TestAChargedFlagKeepsTheUnitFarFromACreeperWithoutAHook(t *testing.T) {
	r := creeperRig(t, "striker", "creeper/backoff", nil)
	f := newFuse(r, mgl32.Vec3{31.5, 1, 20.5}, 0.12)
	r.setActorFlags(creeperID, protocol.EntityDataFlagPowered)
	f.run(20)
	if moved := dist(r.feet(), start); moved < 1 {
		t.Errorf("the unit moved %.1f blocks: it should be backing away from a charged creeper", moved)
	}
	f.run(80)
	if f.lit {
		t.Error("the charged creeper got close enough to light its fuse")
	}
	if n := swings(r); n != 0 {
		t.Errorf("%d blows on a charged creeper", n)
	}
}

func TestAnAngryFlagKeepsTheDefaultFightEvenWithTheHeadTurnedAway(t *testing.T) {
	scenario := func(r *rig) {
		r.hitByEnderman(50)
		away := r.feet()
		r.endermanAt(50, mgl32.Vec3{28.5, 1, 20.5}, &away, true)
		r.setActorFlags(50, protocol.EntityDataFlagAngry)
		r.run(60)
	}
	sameAsDefault(t, "striker", ignoring, scenario)
}

func TestACalmFlagLetsIgnoreDropAnEndermanWhoseHeadIsTurnedOnTheUnit(t *testing.T) {
	r := newWarrior(t, "striker", start)
	setEnderTactic(ignoring)(r.l)
	r.hitByEnderman(50)
	toward := r.feet()
	r.endermanAt(50, mgl32.Vec3{28.5, 1, 20.5}, &toward, false)
	r.setActorFlags(50)
	r.run(enderFreshHit)
	held, swings := r.feet(), len(r.out.attacks())

	r.run(40)
	if d := dist(r.feet(), held); d > 0.5 {
		t.Errorf("the unit walked %.1f blocks after an enderman the server says is calm", d)
	}
	if n := len(r.out.attacks()) - swings; n != 0 {
		t.Errorf("%d swings at a calm enderman", n)
	}
}

func TestFlaglessMetadataLeavesTheEndermanHeadInferenceUntouched(t *testing.T) {
	scenario := func(meta bool) func(*rig) {
		return func(r *rig) {
			r.hitByEnderman(50)
			toward := r.feet()
			r.endermanAt(50, mgl32.Vec3{28.5, 1, 20.5}, &toward, false)
			if meta {
				r.setActorFuse(50)
			}
			r.run(60)
		}
	}
	base := replay(t, "striker", scenario(false), setEnderTactic(ignoring))
	got := replay(t, "striker", scenario(true), setEnderTactic(ignoring))
	if len(base) == 0 || !reflect.DeepEqual(got, base) {
		t.Errorf("metadata without a flag word changed the fight: %d packets, want %d", len(got), len(base))
	}
	sameAsDefault(t, "striker", ignoring, scenario(true))
}
