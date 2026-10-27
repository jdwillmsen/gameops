package act

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// The conformance table pits one tick of the unit's collision against a
// reference written a different way: per-box offsets over the raw shape data,
// in float64, with no region queries or skin shortcuts. A unit whose
// prediction differs from it either clips into a block the server holds it out
// of or stops short of one it could enter, and the server corrects both.

// refSettle is how deep into a box the reference still holds a body.
const refSettle = 0.01

type box64 struct{ lo, hi [3]float64 }

func (b box64) overlapsOn(o box64, axis int) bool {
	for a := 0; a < 3; a++ {
		if a != axis && (b.lo[a] >= o.hi[a]-1e-3 || b.hi[a] <= o.lo[a]+1e-3) {
			return false
		}
	}
	return true
}

func bodyAt(f [3]float64) box64 {
	return box64{
		lo: [3]float64{f[0] - halfWidth, f[1], f[2] - halfWidth},
		hi: [3]float64{f[0] + halfWidth, f[1] + bodyHeight, f[2] + halfWidth},
	}
}

// boxesAround lists the world-space boxes within a few blocks of the feet.
func boxesAround(w Shaper, f [3]float64) []box64 {
	var out []box64
	cx, cy, cz := int32(math.Floor(f[0])), int32(math.Floor(f[1])), int32(math.Floor(f[2]))
	for x := cx - 3; x <= cx+3; x++ {
		for y := cy - 3; y <= cy+4; y++ {
			for z := cz - 3; z <= cz+3; z++ {
				for _, b := range w.Shape(protocol.BlockPos{x, y, z}) {
					out = append(out, box64{
						lo: [3]float64{float64(x) + float64(b.Min[0]), float64(y) + float64(b.Min[1]), float64(z) + float64(b.Min[2])},
						hi: [3]float64{float64(x) + float64(b.Max[0]), float64(y) + float64(b.Max[1]), float64(z) + float64(b.Max[2])},
					})
				}
			}
		}
	}
	return out
}

// refOffset is how far the body may move along axis by d before a box stops it.
func refOffset(boxes []box64, f [3]float64, axis int, d float64) (float64, bool) {
	body := bodyAt(f)
	stopped := false
	for _, o := range boxes {
		if !body.overlapsOn(o, axis) {
			continue
		}
		// A body a hair inside a face is held where it is, not pushed out.
		if d > 0 && body.hi[axis] <= o.lo[axis]+refSettle && o.lo[axis]-body.hi[axis] < d {
			d, stopped = math.Max(o.lo[axis]-body.hi[axis], 0), true
		}
		if d < 0 && body.lo[axis] >= o.hi[axis]-refSettle && o.hi[axis]-body.lo[axis] > d {
			d, stopped = math.Min(o.hi[axis]-body.lo[axis], 0), true
		}
	}
	return d, stopped
}

func refMove(boxes []box64, f [3]float64, dx, dy, dz float64) (out [3]float64, hitX, hitZ, floor bool) {
	var s bool
	dy, s = refOffset(boxes, f, 1, dy)
	floor = s && dy <= 0
	f[1] += dy
	dx, hitX = refOffset(boxes, f, 0, dx)
	f[0] += dx
	dz, hitZ = refOffset(boxes, f, 2, dz)
	f[2] += dz
	return f, hitX, hitZ, floor
}

// refTick is the reference for one tick: the order and step-up rules of the
// server, over a wanted horizontal move (wx, wz) and vertical step wy.
func refTick(w Shaper, feet mgl32.Vec3, onGround bool, wx, wy, wz float64) (mgl32.Vec3, bool) {
	f := [3]float64{float64(feet[0]), float64(feet[1]), float64(feet[2])}
	boxes := boxesAround(w, f)
	if onGround {
		wy = 0
	}
	// The vertical step comes first, and a step up starts from where it left
	// the unit.
	dy, s := refOffset(boxes, f, 1, wy)
	floor := s && wy < 0
	f[1] += dy
	got, hitX, hitZ, _ := refMove(boxes, f, wx, 0, wz)
	if (onGround || floor) && (hitX || hitZ) {
		lift, _ := refOffset(boxes, f, 1, stepHeight)
		// The server stretches the body towards the move before it measures
		// how far it can rise.
		stretched := f
		lo := bodyAt(f)
		lo.hi[0] += math.Max(wx, 0)
		lo.lo[0] += math.Min(wx, 0)
		lo.hi[2] += math.Max(wz, 0)
		lo.lo[2] += math.Min(wz, 0)
		for _, o := range boxes {
			if o.lo[0] < lo.hi[0]-1e-6 && o.hi[0] > lo.lo[0]+1e-6 && o.lo[2] < lo.hi[2]-1e-6 && o.hi[2] > lo.lo[2]+1e-6 &&
				o.lo[1] >= lo.hi[1]-1e-6 && o.lo[1]-lo.hi[1] < lift {
				lift = o.lo[1] - lo.hi[1]
			}
		}
		if lift > 1e-3 {
			stretched[1] += lift
			up, _, _, _ := refMove(boxes, stretched, wx, 0, wz)
			if sq64(up[0]-f[0])+sq64(up[2]-f[2]) > sq64(got[0]-f[0])+sq64(got[2]-f[2]) {
				down, _ := refOffset(boxes, up, 1, -lift)
				up[1] += down
				got = up
			}
		}
	}
	ground := floor
	if onGround && !floor {
		ground = false
		body := bodyAt(got)
		for _, o := range boxes {
			if supportOverlap(body, o) && o.hi[1] > got[1]-supportReach && o.hi[1] <= got[1]+1e-3 {
				ground = true
			}
		}
	}
	return mgl32.Vec3{float32(got[0]), float32(got[1]), float32(got[2])}, ground
}

// supportOverlap needs more than a hair of overlap, as the unit's own check
// does, so a body resting on a box's edge by a micron is not counted as held.
func supportOverlap(b, o box64) bool {
	return b.lo[0] < o.hi[0]-1e-3 && b.hi[0] > o.lo[0]+1e-3 && b.lo[2] < o.hi[2]-1e-3 && b.hi[2] > o.lo[2]+1e-3
}

func sq64(x float64) float64 { return x * x }

// penetrates says the body overlaps a box by more than the skin, which no
// position the server accepts does.
func penetrates(w Shaper, feet mgl32.Vec3) (bool, string) {
	f := [3]float64{float64(feet[0]), float64(feet[1]), float64(feet[2])}
	body := bodyAt(f)
	for _, o := range boxesAround(w, f) {
		deep := true
		for a := 0; a < 3; a++ {
			if body.lo[a] >= o.hi[a]-2e-3 || body.hi[a] <= o.lo[a]+2e-3 {
				deep = false
			}
		}
		if deep {
			return true, fmt.Sprintf("body %v..%v inside box %v..%v", body.lo, body.hi, o.lo, o.hi)
		}
	}
	return false, ""
}

type blocks = map[protocol.BlockPos]cell

func at(x, y, z int32) protocol.BlockPos { return protocol.BlockPos{x, y, z} }

func lay(w blocks, name string, props map[string]any, ps ...protocol.BlockPos) {
	for _, p := range ps {
		w[p] = cell{name, props}
	}
}

var (
	stairsSouthRising = map[string]any{"weirdo_direction": int32(2), "upside_down_bit": false, "minecraft:corner": "none"}
	stairsNorthRising = map[string]any{"weirdo_direction": int32(3), "upside_down_bit": false, "minecraft:corner": "none"}
	bottomHalf        = map[string]any{"minecraft:vertical_half": "bottom"}
	topHalf           = map[string]any{"minecraft:vertical_half": "top"}
)

type scenario struct {
	name  string
	world blocks
	feet  mgl32.Vec3
	// ground starts the unit standing; otherwise it falls with vy.
	ground bool
	vy     float32
	// move is the horizontal step the unit wants this tick.
	dx, dz float32
	// conservative marks a case where the unit knowingly keeps out of a block
	// the server lets it into; the unit may stop short but must never clip.
	conservative string
	// embedded starts the body a few millimetres inside a face, as a pose
	// the server accepted can leave it; the move must not take it deeper.
	embedded bool
}

var (
	south = mgl32.Vec3{0.5, 0, 0.5}
)

func scenarios() []scenario {
	var out []scenario
	add := func(s scenario) { out = append(out, s) }
	wall := func(name string, props map[string]any, y int32) blocks {
		w := blocks{}
		lay(w, name, props, at(0, y, 1))
		return w
	}
	walk := func(name string, w blocks, from mgl32.Vec3, dx, dz float32) scenario {
		return scenario{name: name, world: w, feet: from, ground: true, dx: dx, dz: dz}
	}

	add(walk("full block wall", wall("stone", nil, 0), south, 0, 0.45))
	add(walk("bottom slab steps up", wall("stone_slab", bottomHalf, 0), south, 0, 0.45))
	add(walk("top slab is a wall", wall("stone_slab", topHalf, 0), south, 0, 0.45))
	add(walk("double slab is a wall", wall("stone_double_slab", nil, 0), south, 0, 0.45))
	add(walk("stairs rising away step up", wall("oak_stairs", stairsSouthRising, 0), south, 0, 0.45))
	add(walk("stairs rising towards are a wall at the high side", wall("oak_stairs", stairsNorthRising, 0), south, 0, 0.45))
	add(walk("upside-down stairs are a wall", wall("oak_stairs", map[string]any{"weirdo_direction": int32(2), "upside_down_bit": true}, 0), south, 0, 0.45))
	add(walk("inner corner stairs", wall("oak_stairs", map[string]any{"weirdo_direction": int32(2), "minecraft:corner": "inner_left"}, 0), south, 0, 0.45))
	add(walk("outer corner stairs", wall("oak_stairs", map[string]any{"weirdo_direction": int32(2), "minecraft:corner": "outer_right"}, 0), south, 0, 0.45))
	add(walk("fence post is 1.5 high", wall("oak_fence", nil, 0), south, 0, 0.45))
	add(walk("fence arm is 1.5 high", wall("oak_fence", map[string]any{"minecraft:connection_north": true}, 0), south, 0, 0.45))
	add(walk("wall post is 1.5 high", wall("cobblestone_wall", nil, 0), south, 0, 0.45))
	add(walk("wall arm is 1.5 high", wall("cobblestone_wall", map[string]any{"wall_connection_type_north": "short"}, 0), south, 0, 0.45))
	add(walk("closed fence gate", wall("fence_gate", map[string]any{"minecraft:cardinal_direction": "north"}, 0), south, 0, 0.45))
	add(walk("open fence gate", wall("fence_gate", map[string]any{"minecraft:cardinal_direction": "north", "open_bit": true}, 0), south, 0, 0.45))
	add(walk("closed door", wall("wooden_door", map[string]any{"minecraft:cardinal_direction": "east"}, 0), south, 0, 0.45))
	add(walk("open door", wall("wooden_door", map[string]any{"minecraft:cardinal_direction": "east", "open_bit": true}, 0), south, 0, 0.45))
	add(walk("closed trapdoor is a step", wall("trapdoor", map[string]any{"direction": int32(2)}, 0), south, 0, 0.45))
	add(walk("open trapdoor is a wall", wall("trapdoor", map[string]any{"direction": int32(2), "open_bit": true}, 0), south, 0, 0.45))
	add(walk("upside-down trapdoor is above the head of a walker", wall("trapdoor", map[string]any{"direction": int32(2), "upside_down_bit": true}, 1), south, 0, 0.45))
	add(walk("chest", wall("chest", nil, 0), south, 0, 0.45))
	add(walk("trapped chest", wall("trapped_chest", nil, 0), south, 0, 0.45))
	add(walk("ender chest", wall("ender_chest", nil, 0), south, 0, 0.45))
	add(walk("bed", wall("bed", nil, 0), south, 0, 0.45))
	add(walk("carpet", wall("white_carpet", nil, 0), south, 0, 0.45))
	for h := int32(0); h <= 7; h++ {
		add(walk(fmt.Sprintf("snow layer height %d", h), wall("snow_layer", map[string]any{"height": h}, 0), south, 0, 0.45))
	}
	add(walk("soul sand", wall("soul_sand", nil, 0), south, 0, 0.45))
	add(walk("honey block", wall("honey_block", nil, 0), south, 0, 0.45))
	add(walk("mud", wall("mud", nil, 0), south, 0, 0.45))
	add(walk("ladder panel", wall("ladder", map[string]any{"facing_direction": int32(2)}, 0), south, 0, 0.45))
	add(walk("ladder panel on the near side", wall("ladder", map[string]any{"facing_direction": int32(3)}, 0), south, 0, 0.45))
	add(walk("scaffolding", wall("scaffolding", nil, 0), south, 0, 0.45))
	add(walk("flower pot", wall("flower_pot", nil, 0), south, 0, 0.45))
	add(walk("hopper", wall("hopper", nil, 0), south, 0, 0.45))
	add(walk("cauldron", wall("cauldron", nil, 0), south, 0, 0.45))
	add(walk("end rod", wall("end_rod", nil, 0), south, 0, 0.45))
	add(walk("lantern", wall("lantern", nil, 0), south, 0, 0.45))
	add(walk("torch", wall("torch", nil, 0), south, 0, 0.45))
	add(walk("enchanting table", wall("enchanting_table", nil, 0), south, 0, 0.45))
	add(walk("campfire", wall("campfire", nil, 0), south, 0, 0.45))
	add(walk("anvil", wall("anvil", nil, 0), south, 0, 0.45))
	add(walk("dragon egg", wall("dragon_egg", nil, 0), south, 0, 0.45))
	add(walk("pointed dripstone", wall("pointed_dripstone", nil, 0), south, 0, 0.45))
	add(walk("sea pickle", wall("sea_pickle", nil, 0), south, 0, 0.45))
	add(walk("cake", wall("cake", nil, 0), south, 0, 0.45))
	add(walk("lectern", wall("lectern", nil, 0), south, 0, 0.45))
	add(walk("stonecutter", wall("stonecutter_block", nil, 0), south, 0, 0.45))
	add(walk("candle", wall("candle", nil, 0), south, 0, 0.45))
	add(walk("amethyst cluster", wall("amethyst_cluster", nil, 0), south, 0, 0.45))
	add(walk("skull", wall("skull", nil, 0), south, 0, 0.45))
	add(walk("lightning rod", wall("lightning_rod", nil, 0), south, 0, 0.45))
	add(walk("chain", wall("iron_chain", nil, 0), south, 0, 0.45))
	add(walk("glass pane", wall("glass_pane", nil, 0), south, 0, 0.45))
	add(walk("iron bars", wall("iron_bars", nil, 0), south, 0, 0.45))
	add(walk("end portal frame", wall("end_portal_frame", nil, 0), south, 0, 0.45))
	add(walk("farmland", wall("farmland", nil, 0), south, 0, 0.45))
	add(walk("grass path", wall("grass_path", nil, -1), south, 0, 0.45))
	add(walk("sweet berry bush", wall("sweet_berry_bush", nil, 0), south, 0, 0.45))
	add(walk("cobweb", wall("web", nil, 0), south, 0, 0.45))
	add(walk("short grass is not an obstacle", wall("short_grass", nil, 0), south, 0, 0.45))
	add(walk("pressure plate is not an obstacle", wall("stone_pressure_plate", nil, 0), south, 0, 0.45))

	// An entity is not a block: nothing in the block store stands where a
	// player or mob does, so the unit's step is whole.
	add(walk("entities do not block", blocks{}, south, 0, 0.45))

	// Corners: the move is blocked on one axis and slides on the other.
	{
		w := blocks{}
		lay(w, "stone", nil, at(1, 0, 0), at(0, 0, 1))
		add(walk("inside corner stops both axes", w, south, 0.45, 0.45))
	}
	{
		w := blocks{}
		lay(w, "stone", nil, at(1, 0, 1))
		add(walk("outside corner is slid past on the open axis", w, mgl32.Vec3{0.5, 0, 0.5}, 0.45, 0.45))
		add(walk("outside corner entered from the other side", w, mgl32.Vec3{0.5, 0, 0.5}, 0.45, 0.2))
	}
	{
		w := blocks{}
		lay(w, "stone", nil, at(1, 0, 0), at(0, 0, 1))
		add(walk("diagonal between two blocks", w, mgl32.Vec3{0.5, 0, 0.5}, 0.4, 0.4))
	}
	{
		w := blocks{}
		lay(w, "stone", nil, at(1, 0, 1))
		add(walk("diagonal approach to a single block", w, mgl32.Vec3{0.65, 0, 0.65}, 0.3, 0.3))
	}
	{
		// Two blocks that only touch at a corner leave a diagonal seam.
		w := blocks{}
		lay(w, "stone", nil, at(0, 0, 1), at(1, 0, 0))
		add(walk("seam between diagonal blocks from the corner cell", w, mgl32.Vec3{0.7, 0, 0.7}, 0.3, 0.3))
	}
	{
		w := blocks{}
		lay(w, "stone", nil, at(1, 0, 0), at(1, 1, 0))
		add(walk("walking along a wall", w, mgl32.Vec3{0.6, 0, 0.5}, 0.4, 0.4))
	}

	// Step-up edges.
	{
		w := blocks{}
		lay(w, "stone", nil, at(0, 0, 1))
		add(walk("a full block is not a step", w, south, 0, 0.45))
	}
	{
		w := blocks{}
		lay(w, "stone_slab", bottomHalf, at(0, 0, 1))
		lay(w, "stone", nil, at(0, 1, 1))
		add(walk("step refused under a lintel", w, south, 0, 0.45))
	}
	{
		w := blocks{}
		lay(w, "stone_slab", bottomHalf, at(0, 0, 1))
		lay(w, "stone", nil, at(0, 2, 1))
		add(walk("step allowed under a high lintel", w, south, 0, 0.45))
	}
	{
		w := blocks{}
		lay(w, "stone_slab", bottomHalf, at(0, 0, 1))
		lay(w, "stone_slab", topHalf, at(0, 1, 1))
		add(walk("step refused under a top slab", w, south, 0, 0.45))
	}
	{
		w := blocks{}
		lay(w, "snow_layer", map[string]any{"height": int32(4)}, at(0, 0, 1)) // 0.5
		lay(w, "stone", nil, at(0, 1, 2))
		add(walk("step onto snow then a wall", w, mgl32.Vec3{0.5, 0, 0.7}, 0, 0.3))
	}

	// Falling.
	{
		w := blocks{}
		lay(w, "stone", nil, at(0, 0, 0))
		add(scenario{name: "fall onto a full block", world: w, feet: mgl32.Vec3{0.5, 2, 0.5}, vy: -0.5})
	}
	{
		w := blocks{}
		lay(w, "stone_slab", bottomHalf, at(0, 0, 0))
		add(scenario{name: "fall onto a bottom slab", world: w, feet: mgl32.Vec3{0.5, 1, 0.5}, vy: -0.6})
	}
	{
		w := blocks{}
		lay(w, "oak_fence", nil, at(0, 0, 0))
		add(scenario{name: "fall onto a fence post", world: w, feet: mgl32.Vec3{0.5, 2, 0.5}, vy: -0.6})
	}
	{
		w := blocks{}
		lay(w, "chest", nil, at(0, 0, 0))
		add(scenario{name: "fall onto a chest", world: w, feet: mgl32.Vec3{0.5, 1.2, 0.5}, vy: -0.6})
	}
	{
		w := blocks{}
		lay(w, "stone", nil, at(0, 0, 0))
		// The body overhangs the block by 0.1 on x.
		add(scenario{name: "fall onto the edge of a block", world: w, feet: mgl32.Vec3{1.39, 2, 0.5}, vy: -0.5})
		add(scenario{name: "fall past the edge of a block", world: w, feet: mgl32.Vec3{1.41, 2, 0.5}, vy: -0.5})
	}
	{
		w := blocks{}
		lay(w, "stone", nil, at(0, 2, 0))
		add(scenario{name: "jump into a ceiling", world: w, feet: mgl32.Vec3{0.5, 0, 0.5}, vy: 0.42})
		add(scenario{name: "jump into a ceiling edge", world: w, feet: mgl32.Vec3{-0.25, 0, 0.5}, vy: 0.42})
	}
	{
		w := blocks{}
		lay(w, "stone", nil, at(1, 0, 0))
		add(scenario{name: "falling beside a wall slides down it", world: w, feet: mgl32.Vec3{0.65, 2, 0.5}, vy: -0.3, dx: 0.2})
	}
	{
		add(scenario{name: "airborne move into a wall", world: blocks{at(0, 0, 1): {"stone", nil}, at(0, 1, 1): {"stone", nil}}, feet: mgl32.Vec3{0.5, 0.2, 0.5}, vy: 0.1, dz: 0.4})
	}

	// Poses a few millimetres inside a face.
	{
		w := blocks{}
		lay(w, "stone", nil, at(1, 0, 0), at(-1, 0, 0), at(0, 0, 1), at(0, 0, -1), at(0, 2, 0))
		add(scenario{name: "embedded in a face to the east", world: w, feet: mgl32.Vec3{0.705, 0, 0.5}, ground: true, dx: 0.1, embedded: true})
		add(scenario{name: "embedded in a face to the west", world: w, feet: mgl32.Vec3{0.295, 0, 0.5}, ground: true, dx: -0.1, embedded: true})
		add(scenario{name: "embedded in a face to the south", world: w, feet: mgl32.Vec3{0.5, 0, 0.705}, ground: true, dz: 0.1, embedded: true})
		add(scenario{name: "embedded in a face to the north", world: w, feet: mgl32.Vec3{0.5, 0, 0.295}, ground: true, dz: -0.1, embedded: true})
		add(scenario{name: "embedded in a ceiling", world: w, feet: mgl32.Vec3{0.5, 0.205, 0.5}, vy: 0.4, embedded: true})
		add(scenario{name: "embedded in a floor", world: w, feet: mgl32.Vec3{0.5, -0.005, 0.5}, vy: -0.3, embedded: true})
		add(scenario{name: "embedded in a face and airborne", world: w, feet: mgl32.Vec3{0.705, 1, 0.5}, vy: -0.1, dx: 0.1, embedded: true})
	}

	// Edges of the supporting block.
	{
		w := blocks{}
		lay(w, "stone", nil, at(0, 0, 0))
		add(scenario{name: "walking off the edge of a block", world: w, feet: mgl32.Vec3{0.7, 1, 0.5}, ground: true, dx: 0.5})
		add(scenario{name: "body overhanging an edge is supported", world: w, feet: mgl32.Vec3{1.25, 1, 0.5}, ground: true, dx: 0})
	}
	return out
}

func labWorldFor(s scenario) shaped {
	w := shaped{}
	for p, c := range s.world {
		w[p] = c
	}
	return w
}

// TestCollisionMatchesTheSweptReference runs every scenario for one tick and
// compares the unit's feet and ground flag with the reference's.
func TestCollisionMatchesTheSweptReference(t *testing.T) {
	for _, s := range scenarios() {
		t.Run(s.name, func(t *testing.T) {
			w := labWorldFor(s)
			start := Pose{Feet: s.feet, OnGround: s.ground, vy: s.vy}
			wantV := mgl32.Vec2{s.dx, s.dz}
			if s.ground {
				start.Carry = wantV
			} else {
				start.Carry = wantV.Mul(1 / airDrag)
			}
			if bad, why := penetrates(w, s.feet); bad && !s.embedded {
				t.Fatalf("scenario starts inside a block: %s", why)
			}
			got, _ := start.advance(0, false, false, w)
			dy := float64(s.vy)
			wantFeet, wantGround := refTick(w, s.feet, s.ground, float64(s.dx), dy, float64(s.dz))
			if s.conservative != "" {
				if bad, why := penetrates(w, got.Feet); bad {
					t.Fatalf("clipped: %s", why)
				}
				return
			}
			const tol = 2e-3
			for a := 0; a < 3; a++ {
				if math.Abs(float64(got.Feet[a]-wantFeet[a])) > tol {
					t.Errorf("feet = %.4f, want %.4f (reference)", got.Feet[:], wantFeet[:])
					break
				}
			}
			if bad, why := penetrates(w, got.Feet); bad && !s.embedded {
				t.Errorf("clipped into a block: %s", why)
			}
			if got.OnGround != wantGround {
				t.Errorf("on ground = %v, want %v", got.OnGround, wantGround)
			}
		})
	}
}

// TestWalkingNeverEntersABlock walks a unit for a few seconds towards every
// shape at several approach angles, with and without jumping, and checks that
// no tick leaves the body inside a box.
func TestWalkingNeverEntersABlock(t *testing.T) {
	approaches := []float32{0, 20, 45, 90, 135, 180, 225, 270, 315}
	for _, s := range scenarios() {
		if s.embedded || (!s.ground && s.vy != 0) {
			continue
		}
		for _, yaw := range approaches {
			for _, jump := range []bool{false, true} {
				name := fmt.Sprintf("%s/yaw%.0f/jump=%v", s.name, yaw, jump)
				t.Run(name, func(t *testing.T) {
					w := labWorldFor(s)
					p := Pose{Feet: s.feet, OnGround: true, Yaw: yaw}
					for i := 0; i < 50; i++ {
						p, _ = p.advance(1, jump, false, w)
						if bad, why := penetrates(w, p.Feet); bad {
							t.Fatalf("tick %d at %v: %s", i, p.Feet, why)
						}
					}
				})
			}
		}
	}
}

var randomWorldBlocks = []cell{
	{"stone", nil}, {"stone", nil}, {"stone", nil},
	{"stone_slab", bottomHalf}, {"stone_slab", topHalf},
	{"oak_stairs", stairsSouthRising}, {"oak_stairs", stairsNorthRising},
	{"oak_stairs", map[string]any{"weirdo_direction": int32(0), "upside_down_bit": true}},
	{"oak_stairs", map[string]any{"weirdo_direction": int32(1), "minecraft:corner": "inner_left"}},
	{"oak_fence", nil}, {"cobblestone_wall", nil}, {"fence_gate", map[string]any{"minecraft:cardinal_direction": "east"}},
	{"wooden_door", map[string]any{"minecraft:cardinal_direction": "east"}},
	{"trapdoor", map[string]any{"direction": int32(1), "open_bit": true}}, {"trapdoor", nil},
	{"chest", nil}, {"snow_layer", map[string]any{"height": int32(3)}}, {"white_carpet", nil},
	{"soul_sand", nil}, {"bed", nil}, {"hopper", nil}, {"cauldron", nil}, {"flower_pot", nil},
	{"lantern", nil}, {"enchanting_table", nil},
}

func randomWorld(r *rand.Rand) shaped {
	w := shaped{}
	for x := int32(-3); x <= 6; x++ {
		for z := int32(-3); z <= 6; z++ {
			for y := int32(0); y <= 3; y++ {
				if r.Float64() < 0.18-0.03*float64(y) {
					w[protocol.BlockPos{x, y, z}] = randomWorldBlocks[r.Intn(len(randomWorldBlocks))]
				}
			}
		}
	}
	return w
}

// TestRandomWorldsMatchTheReference drives a unit through seeded random
// worlds of every shape family, with jumps, turns and server knockback, and
// compares each tick with the reference. The seeds are fixed so a failure
// names the world that reproduces it.
func TestRandomWorldsMatchTheReference(t *testing.T) {
	for seed := int64(0); seed < 400; seed++ {
		r := rand.New(rand.NewSource(seed))
		w := randomWorld(r)
		p := Pose{Feet: mgl32.Vec3{float32(r.Intn(4)) + r.Float32(), float32(r.Intn(4)), float32(r.Intn(4)) + r.Float32()}}
		if bad, _ := penetrates(w, p.Feet); bad {
			continue
		}
		for tick := 0; tick < 80; tick++ {
			if r.Float64() < 0.04 {
				p.knock(mgl32.Vec3{r.Float32()*2 - 1, r.Float32()*1.2 - 0.3, r.Float32()*2 - 1})
			}
			fwd, jump := float32(r.Intn(2)), r.Float64() < 0.2
			p.Yaw = float32(r.Intn(24) * 15)

			var v mgl32.Vec2
			var dy float32
			dir := forwardDir(p.Yaw)
			standing := p.OnGround && !jump
			if p.OnGround {
				v = p.Carry.Add(dir.Mul(walkAccel * fwd))
				if jump {
					dy = jumpVelocity
				}
			} else {
				v = p.Carry.Mul(airDrag).Add(dir.Mul(airAccel * fwd))
				dy = p.vy
			}
			start := p.Feet
			next, _ := p.advance(fwd, jump, false, w)
			want, wantGround := refTick(w, start, standing, float64(v[0]), float64(dy), float64(v[1]))

			if bad, why := penetrates(w, next.Feet); bad {
				t.Fatalf("seed %d tick %d: clipped from %v to %v: %s", seed, tick, start, next.Feet, why)
			}
			for a := 0; a < 3; a++ {
				if math.Abs(float64(next.Feet[a]-want[a])) > 2e-3 {
					t.Fatalf("seed %d tick %d: from %v to %v, reference %v", seed, tick, start, next.Feet, want)
				}
			}
			// A jump leaves the ground whatever the reference says of the
			// landing, so only the other ticks compare the flag.
			if !(jump && p.OnGround) && next.OnGround != wantGround {
				t.Fatalf("seed %d tick %d: on ground = %v at %v, reference %v", seed, tick, next.OnGround, next.Feet, wantGround)
			}
			p = next
		}
	}
}
