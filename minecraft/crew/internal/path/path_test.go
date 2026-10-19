package path

import (
	"errors"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

type world map[protocol.BlockPos]string

func (w world) Block(p protocol.BlockPos) (string, bool) {
	n, ok := w[p]
	return n, ok
}

// flat fills x,z in [0,size) with stone at y=0 and air from y=1 to y=6, so
// feet at y=1 stand on it.
func flat(size int32) world {
	w := world{}
	for x := int32(0); x < size; x++ {
		for z := int32(0); z < size; z++ {
			w[protocol.BlockPos{x, 0, z}] = "stone"
			for y := int32(1); y <= 6; y++ {
				w[protocol.BlockPos{x, y, z}] = "air"
			}
		}
	}
	return w
}

// raise makes the column's ground top at height h (feet stand at h+1).
func (w world) raise(x, z, h int32) {
	for y := int32(0); y <= 6; y++ {
		switch {
		case y <= h:
			w[protocol.BlockPos{x, y, z}] = "stone"
		default:
			w[protocol.BlockPos{x, y, z}] = "air"
		}
	}
}

func TestFindsAStraightPathOnFlatGround(t *testing.T) {
	w := flat(8)
	got, err := Find(w, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{5, 1, 0}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || got[0] != (protocol.BlockPos{1, 1, 0}) || got[4] != (protocol.BlockPos{5, 1, 0}) {
		t.Fatalf("path = %v", got)
	}
}

func TestStepsUpOneBlockButNotTwo(t *testing.T) {
	w := flat(6)
	w.raise(2, 0, 1)
	got, err := Find(w, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{2, 2, 0}, 0)
	if err != nil || got[len(got)-1] != (protocol.BlockPos{2, 2, 0}) {
		t.Fatalf("one-block step: %v, %v", got, err)
	}

	w = flat(6)
	for z := int32(0); z < 6; z++ {
		w.raise(2, z, 2)
	}
	if _, err := Find(w, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{2, 3, 0}, 0); !errors.Is(err, ErrNoPath) {
		t.Fatalf("two-block wall: err = %v", err)
	}
}

func TestDropsUpToThreeBlocks(t *testing.T) {
	for _, tc := range []struct {
		drop int32
		ok   bool
	}{{3, true}, {4, false}} {
		w := flat(6)
		for x := int32(0); x < 6; x++ {
			for z := int32(0); z < 6; z++ {
				w.raise(x, z, tc.drop)
			}
		}
		w.raise(3, 0, 0)
		for z := int32(1); z < 6; z++ {
			w.raise(3, z, tc.drop) // keep the ledge sealed so the drop is the only way
		}
		from := protocol.BlockPos{0, tc.drop + 1, 0}
		to := protocol.BlockPos{3, 1, 0}
		got, err := Find(w, from, to, 0)
		if tc.ok && (err != nil || got[len(got)-1] != to) {
			t.Fatalf("drop %d: %v, %v", tc.drop, got, err)
		}
		if !tc.ok && !errors.Is(err, ErrNoPath) {
			t.Fatalf("drop %d: err = %v", tc.drop, err)
		}
	}
}

func TestDiagonalsDoNotCutCorners(t *testing.T) {
	w := flat(4)
	w.raise(1, 0, 3) // wall beside the diagonal from (0,0) to (1,1)
	got, err := Find(w, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{1, 1, 1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (protocol.BlockPos{0, 1, 1}) {
		t.Fatalf("path cut the corner: %v", got)
	}
}

func TestUnknownBlocksAreNotWalkable(t *testing.T) {
	w := flat(6)
	delete(w, protocol.BlockPos{3, 0, 0}) // no floor knowledge
	if Standable(w, protocol.BlockPos{3, 1, 0}) {
		t.Fatal("unknown floor is standable")
	}
	delete(w, protocol.BlockPos{2, 2, 2}) // unknown head block
	if Standable(w, protocol.BlockPos{2, 1, 2}) {
		t.Fatal("unknown head block is standable")
	}
	for z := int32(0); z < 6; z++ {
		delete(w, protocol.BlockPos{3, 0, z})
	}
	if _, err := Find(w, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{5, 1, 0}, 0); !errors.Is(err, ErrNoPath) {
		t.Fatalf("err = %v", err)
	}
	s := NewSolid(w)
	if !s.Solid(protocol.BlockPos{3, 0, 0}) || s.Solid(protocol.BlockPos{1, 1, 1}) {
		t.Fatal("solid adapter disagrees with the store")
	}
}

func TestReturnsErrNoPathPastMaxNodes(t *testing.T) {
	w := flat(40)
	from, to := protocol.BlockPos{0, 1, 0}, protocol.BlockPos{39, 1, 39}
	if _, err := Find(w, from, to, 10); !errors.Is(err, ErrNoPath) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Find(w, from, to, 0); err != nil {
		t.Fatalf("default cap: %v", err)
	}
}

func TestWaterIsNotPassable(t *testing.T) {
	if Passable("water") || Passable("lava") || Passable("stone") {
		t.Fatal("water, lava and stone must block")
	}
	for _, n := range []string{"air", "short_grass", "oak_sapling", "vine"} {
		if !Passable(n) {
			t.Fatalf("%s should be passable", n)
		}
	}
	w := flat(4)
	w[protocol.BlockPos{1, 1, 0}] = "water"
	if Standable(w, protocol.BlockPos{1, 1, 0}) {
		t.Fatal("water cell is standable")
	}
}
