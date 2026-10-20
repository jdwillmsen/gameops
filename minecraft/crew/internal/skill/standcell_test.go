package skill

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/act"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/shape/blocktable"
)

// withBlockTable gives the test the real shapes of chests and paths, as the
// binary has them.
func withBlockTable(t *testing.T) {
	t.Helper()
	old := shape.Fallback
	shape.Fallback = blocktable.Boxes
	t.Cleanup(func() { shape.Fallback = old })
}

// A chest is a little lower than a block, so a unit can stand on it, and the
// stand cell nearest a unit that already is on it is the chest's own cell:
// the kit visit then tried to open a chest it was standing on and bobbed there.
func TestStandCellIsBesideTheChestNotOnIt(t *testing.T) {
	withBlockTable(t)
	w := chestWorld()
	chest := chestPos
	if !path.Standable(w, chest) {
		t.Fatal("a chest 0.95 high should be a surface a unit can stand on, or this test checks nothing")
	}
	onTop := act.Pose{Feet: mgl32.Vec3{float32(chest.X()) + 0.5, float32(chest.Y()) + 0.95, float32(chest.Z()) + 0.5}, OnGround: true}
	got, ok := standCell(w, chest, onTop)
	if !ok {
		t.Fatal("no stand cell")
	}
	if got == chest {
		t.Errorf("stand cell = the chest's own cell %v", got)
	}
	if dx, dz := got.X()-chest.X(), got.Z()-chest.Z(); dx == 0 && dz == 0 {
		t.Errorf("stand cell %v is in the chest's column", got)
	}
	if !path.Standable(w, got) {
		t.Errorf("stand cell %v is not standable", got)
	}
}
