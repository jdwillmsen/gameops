package shape

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	_ "github.com/df-mc/dragonfly/server/block"
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/block/model"
	"github.com/df-mc/dragonfly/server/world"
)

func key(bs []Box) string {
	var out []string
	for _, b := range bs {
		out = append(out, fmt.Sprintf("%.4f,%.4f,%.4f-%.4f,%.4f,%.4f", b.Min[0], b.Min[1], b.Min[2], b.Max[0], b.Max[1], b.Max[2]))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func fromCube(bs []cube.BBox) []Box {
	var out []Box
	for _, b := range bs {
		mn, mx := b.Min(), b.Max()
		out = append(out, Box{
			Min: [3]float32{float32(mn[0]), float32(mn[1]), float32(mn[2])},
			Max: [3]float32{float32(mx[0]), float32(mx[1]), float32(mx[2])},
		})
	}
	return out
}

// thinner turns the block table's three-sixteenths door and trapdoor panels
// into the 0.1825 the server collides with.
func thinner(bs []Box) []Box {
	out := make([]Box, len(bs))
	for i, b := range bs {
		for a := 0; a < 3; a++ {
			for _, v := range []*float32{&b.Min[a], &b.Max[a]} {
				switch *v {
				case 0.1875:
					*v = 0.1825
				case 0.8125:
					*v = 0.8175
				}
			}
		}
		out[i] = b
	}
	return out
}

// Every state of the block kinds whose model needs no neighbours must give the
// boxes dragonfly's table gives, which is where the facing and hinge
// conventions come from.
func TestBoxesMatchTheBlockTableForEveryState(t *testing.T) {
	world.DefaultBlockRegistry.Finalize()
	checked := map[string]int{}
	for _, b := range world.DefaultBlockRegistry.Blocks() {
		name, props := b.EncodeBlock()
		name = strings.TrimPrefix(name, "minecraft:")
		if strings.Contains(name, "shulker") {
			continue // its model reads state a bare block lacks
		}
		var kind string
		switch b.Model().(type) {
		case model.Slab:
			kind = "slab"
		case model.Stair:
			kind = "stairs"
		case model.Door:
			kind = "door"
		case model.Trapdoor:
			kind = "trapdoor"
		case model.Ladder:
			kind = "ladder"
		case model.FenceGate:
			kind = "gate"
		case model.Carpet:
			kind = "carpet"
		default:
			continue
		}
		if kind == "ladder" && num(props, "facing_direction") < 2 {
			continue // placeholder state of a ladder with no side
		}
		want := fromCube(b.Model().BBox(cube.Pos{}, nil))
		if kind == "door" || kind == "trapdoor" {
			want = thinner(want)
		}
		if got := key(Boxes(name, props)); got != key(want) {
			t.Fatalf("%s %v: got %s, want %s", name, props, got, key(want))
		}
		checked[kind]++
	}
	for _, kind := range []string{"slab", "stairs", "door", "trapdoor", "ladder", "gate", "carpet"} {
		if checked[kind] == 0 {
			t.Errorf("no %s state was compared", kind)
		}
	}
}

func TestFencesAndWallsAreOneAndAHalfHighAndFollowTheirConnections(t *testing.T) {
	post := key([]Box{{Min: [3]float32{0.375, 0, 0.375}, Max: [3]float32{0.625, 1.5, 0.625}}})
	if got := key(Boxes("oak_fence", nil)); got != post {
		t.Errorf("lone fence = %s, want %s", got, post)
	}
	east := map[string]any{"minecraft:connection_east": true}
	if got, want := key(Boxes("oak_fence", east)), key([]Box{{Min: [3]float32{0.375, 0, 0.375}, Max: [3]float32{1, 1.5, 0.625}}}); got != want {
		t.Errorf("fence joined east = %s, want %s", got, want)
	}
	for _, b := range Boxes("cobblestone_wall", map[string]any{"wall_connection_type_north": "short"}) {
		if b.Max[1] != 1.5 {
			t.Errorf("wall box height = %v, want 1.5", b.Max[1])
		}
	}
	if n := len(Boxes("cobblestone_wall", map[string]any{"wall_connection_type_north": "tall", "wall_connection_type_south": "tall"})); n != 3 {
		t.Errorf("wall with two arms has %d boxes, want post plus 2", n)
	}
}

func TestGateIsThinAcrossItsFacingAndEmptyWhenOpen(t *testing.T) {
	got := Boxes("fence_gate", map[string]any{"minecraft:cardinal_direction": "north"})
	if len(got) != 1 || got[0].Min[2] != 0.375 || got[0].Max[2] != 0.625 || got[0].Max[1] != 1.5 || got[0].Max[0] != 1 {
		t.Errorf("closed gate = %v", got)
	}
	if got := Boxes("fence_gate", map[string]any{"open_bit": true}); got != nil {
		t.Errorf("open gate = %v, want none", got)
	}
}

func TestSnowLayerCollisionIsOneLayerBelowWhatIsDrawn(t *testing.T) {
	if got := Boxes("snow_layer", map[string]any{"height": int32(0)}); got != nil {
		t.Errorf("a single layer collides: %v", got)
	}
	for h := 1; h < 8; h++ {
		got := Boxes("snow_layer", map[string]any{"height": int32(h)})
		if want := float32(h) / 8; got[0].Max[1] != want {
			t.Errorf("height %d top = %v, want %v", h, got[0].Max[1], want)
		}
	}
}

func TestUnknownBlocksAreFullCubesAndPlantsAreEmpty(t *testing.T) {
	if got := Boxes("stone", nil); len(got) != 1 || got[0] != Full() {
		t.Errorf("stone = %v", got)
	}
	if got := Boxes("water", nil); len(got) != 1 {
		t.Errorf("water must stay solid, got %v", got)
	}
	for _, n := range []string{"air", "short_grass", "oak_sapling", "stone_pressure_plate", "rail"} {
		if Boxes(n, nil) != nil {
			t.Errorf("%s should have no collision", n)
		}
	}
}
