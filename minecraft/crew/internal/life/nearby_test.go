package life

import (
	"encoding/json"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func eyeAt(feet mgl32.Vec3) [3]float32 { return [3]float32{feet[0], feet[1] + eyeToFeet, feet[2]} }

func TestNearbyCountsHostilesAndPlayersInRange(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	r.spawn(10, "minecraft:zombie", start.Add(mgl32.Vec3{3, 0, 0}))
	r.spawn(11, "minecraft:skeleton", start.Add(mgl32.Vec3{0, 0, 15}))
	r.spawn(12, "minecraft:creeper", start.Add(mgl32.Vec3{-8, 0, 0}))
	r.spawn(13, "minecraft:zombie", start.Add(mgl32.Vec3{40, 0, 0}))
	r.spawn(14, "minecraft:cow", start.Add(mgl32.Vec3{1, 0, 1}))
	r.spawn(15, "minecraft:enderman", start.Add(mgl32.Vec3{1, 0, 2}))
	r.player(20, start.Add(mgl32.Vec3{5, 0, 5}))
	r.player(21, start.Add(mgl32.Vec3{100, 0, 0}))

	got := r.l.Nearby(eyeAt(start))
	if got != (Nearby{Hostiles: 3, Players: 1}) {
		t.Fatalf("nearby = %+v, want 3 hostiles (zombie, skeleton, creeper) and 1 player", got)
	}
}

func TestNearbyIsCappedAtNine(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	for i := range 20 {
		r.spawn(uint64(100+i), "minecraft:zombie", start.Add(mgl32.Vec3{float32(i % 5), 0, 2}))
		r.player(uint64(200+i), start.Add(mgl32.Vec3{2, 0, float32(i % 5)}))
	}
	if got := r.l.Nearby(eyeAt(start)); got != (Nearby{Hostiles: 9, Players: 9}) {
		t.Fatalf("nearby = %+v, want both capped at 9", got)
	}
}

func TestNearbyForgetsWhatTheSessionEndedWith(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	r.spawn(10, "minecraft:zombie", start.Add(mgl32.Vec3{3, 0, 0}))
	r.l.End()
	if got := r.l.Nearby(eyeAt(start)); got != (Nearby{}) {
		t.Fatalf("nearby = %+v after the session ended", got)
	}
}

func TestNearbyAnswersWithCountsOnlyNeverAPlayersName(t *testing.T) {
	r := newWarrior(t, "vanguard", start)
	p := addPlayer(30, start.Add(mgl32.Vec3{1, 0, 1}))
	p.Username = "SomeRealPlayer"
	r.l.Apply(p)
	r.spawn(31, "minecraft:zombie", start.Add(mgl32.Vec3{2, 0, 2}))

	raw, err := json.Marshal(r.l.Nearby(eyeAt(start)))
	if err != nil || string(raw) != `{"hostiles":1,"players":1}` {
		t.Fatalf("nearby = %s (%v)", raw, err)
	}
}
