package life

import (
	"math"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/path"
)

// safeFloor says whether a block is fit to stand on. The planner treats any
// block that is not passable as a floor, which includes liquids and blocks
// that hurt or slide the unit; a unit left to itself must not choose them.
func safeFloor(block string) bool {
	switch {
	case strings.Contains(block, "water"), strings.Contains(block, "lava"),
		strings.Contains(block, "magma"), strings.Contains(block, "cactus"),
		strings.Contains(block, "fire"), strings.Contains(block, "powder_snow"),
		strings.Contains(block, "berry"), strings.Contains(block, "wither_rose"),
		strings.Contains(block, "dripstone"), strings.Contains(block, "bubble"),
		strings.Contains(block, "honey"), strings.Contains(block, "soul_sand"),
		strings.Contains(block, "slime"), strings.HasSuffix(block, "ice"):
		return false
	}
	return true
}

// safeStand is path.Standable on a floor that is safe to stand on.
func safeStand(w path.Blocks, feet protocol.BlockPos) bool {
	if !path.Standable(w, feet) {
		return false
	}
	name, _ := w.Block(protocol.BlockPos{feet.X(), feet.Y() - 1, feet.Z()})
	return safeFloor(name)
}

// safePath says whether every step of a plan stays on safe floor and within
// a band of heights, so a walk cannot lead down a slope or off a ledge.
func safePath(w path.Blocks, plan []protocol.BlockPos, minY, maxY int32) bool {
	for _, c := range plan {
		if c.Y() < minY || c.Y() > maxY || !safeStand(w, c) {
			return false
		}
	}
	return true
}

// cellAt finds the standable cell in a column nearest to a height, looking one
// block up and two down.
func cellAt(w path.Blocks, x, z, nearY int32) (protocol.BlockPos, bool) {
	for _, dy := range [...]int32{0, 1, -1, -2} {
		if c := (protocol.BlockPos{x, nearY + dy, z}); safeStand(w, c) {
			return c, true
		}
	}
	return protocol.BlockPos{}, false
}

func cellOf(p mgl32.Vec3) protocol.BlockPos {
	// A unit standing on a floor sits at an exact integer height, which float
	// error can leave a hair below it.
	return protocol.BlockPos{int32(math.Floor(float64(p.X()))), int32(math.Floor(float64(p.Y()) + 0.01)), int32(math.Floor(float64(p.Z())))}
}

func horizontal(a, b mgl32.Vec3) float64 {
	return math.Hypot(float64(a.X()-b.X()), float64(a.Z()-b.Z()))
}

// lookAt gives the yaw and pitch, in degrees, that face a point from the eye.
func lookAt(eye, to mgl32.Vec3) (yaw, pitch float32) {
	d := to.Sub(eye)
	yaw = float32(math.Atan2(float64(-d.X()), float64(d.Z())) * 180 / math.Pi)
	pitch = float32(math.Atan2(float64(-d.Y()), math.Hypot(float64(d.X()), float64(d.Z()))) * 180 / math.Pi)
	return yaw, pitch
}
