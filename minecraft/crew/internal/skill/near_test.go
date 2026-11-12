package skill

import (
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Near brings the eye close to a solid block, which Navigate cannot path to.
func TestNearWalksBesideASolidBlock(t *testing.T) {
	w := flatWorld(14)
	table := protocol.BlockPos{10, 1, 10}
	w[table] = "crafting_table"
	r := newRig(t, w, feetAt(1, 1, 2))
	st, err := r.run(NewNear(w, r.self, table, 2.5), 600)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if d := centre(table).Sub(eyeOf(r.Actuator.Pose().Feet)).Len(); d > 3.0 {
		t.Errorf("eye is %.2f from the table, want within 3.0", d)
	}
}

func TestNearIsDoneWhenAlreadyClose(t *testing.T) {
	w := flatWorld(14)
	table := protocol.BlockPos{3, 1, 3}
	w[table] = "crafting_table"
	r := newRig(t, w, feetAt(2, 1, 3))
	st, err := r.run(NewNear(w, r.self, table, 2.5), 5)
	if st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
}
