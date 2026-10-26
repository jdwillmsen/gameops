package life

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// A shooter that holds its distance is closed on and hit again and again:
// the walk to it is replanned as the unit circles or the mob shuffles, and
// those replans must not add up to a give-up that leaves the mob alone for
// the rest of the fight.
func TestWarriorNeverGivesUpAReachableShooterItCanStillSee(t *testing.T) {
	for _, tc := range []struct {
		name string
		step float32
	}{
		{"standing", 0},
		{"backing off", 0.1},
	} {
		for _, role := range []string{"vanguard", "striker", "ranger"} {
			t.Run(tc.name+"/"+role, func(t *testing.T) {
				r := newWarrior(t, role, mgl32.Vec3{20.5, 1, 20.5})
				r.setHealth(20)
				sq := &fakeSquad{leaderID: "lead", leader: r.feet(), leaderOK: true}
				r.setSquad(t, sq)
				pos := mgl32.Vec3{28.5, 1, 20.5}
				r.spawn(60, "minecraft:pillager", pos)
				for i := 0; i < 800; i++ {
					sq.leader = r.feet()
					r.step()
					if r.l.ignore[60] > 0 {
						t.Fatalf("gave up the pillager at tick %d (%s)", i, r.l.endReason)
					}
					if tc.step > 0 && dist(r.feet(), pos) < 5 && pos.X() < 36 {
						pos = pos.Add(mgl32.Vec3{tc.step, 0, 0})
						r.moveTo(60, pos)
					}
				}
				if got := len(r.out.attacks()); got < 20 {
					t.Fatalf("landed %d swings in 800 ticks, want a steady fight", got)
				}
			})
		}
	}
}
