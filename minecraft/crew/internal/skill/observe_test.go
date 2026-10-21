package skill

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// pair is one (run length, palette index) of a cell stream.
type pair struct {
	run int
	idx byte
}

// expand decodes a cell stream the way the controller does, so the tests
// read what the unit actually sent.
func expand(t *testing.T, cells string) []pair {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(cells)
	if err != nil {
		t.Fatalf("cells %q are not base64: %v", cells, err)
	}
	var out []pair
	for len(raw) > 0 {
		run, n := binary.Uvarint(raw)
		if n <= 0 || run == 0 || n >= len(raw) {
			t.Fatalf("cells %q hold a bad or truncated run", cells)
		}
		out = append(out, pair{int(run), raw[n]})
		raw = raw[n+1:]
	}
	return out
}

func TestObserveReportsTheBoxInWireOrder(t *testing.T) {
	w := simWorld{
		protocol.BlockPos{0, 1, 0}: "dirt",
		protocol.BlockPos{1, 1, 0}: "air",
		protocol.BlockPos{2, 1, 0}: "dirt",
		protocol.BlockPos{0, 1, 1}: "air",
		protocol.BlockPos{1, 1, 1}: "air",
		protocol.BlockPos{2, 1, 1}: "air",
	}
	r := newRig(t, w, mgl32.Vec3{10.5, 1, 10.5})
	o := NewObserve(w, r.self, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{2, 1, 1})
	if err := o.Ready(r.self()); err != nil {
		t.Fatal(err)
	}
	if st, err := r.run(o, 50); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	b, ok := o.(BlockReporter).Blocks()
	if !ok {
		t.Fatal("the box was never read")
	}
	if b.Min != (Vec{0, 1, 0}) || b.Size != (Vec{3, 1, 2}) {
		t.Errorf("box = min %+v, size %+v", b.Min, b.Size)
	}
	if len(b.Palette) != 2 || b.Palette[0] != "dirt" || b.Palette[1] != "air" {
		t.Errorf("palette = %v", b.Palette)
	}
	// x fastest, then z, then y: dirt, air, dirt, then the air of the next z
	// row runs three long.
	if got := expand(t, b.Cells); len(got) != 4 ||
		got[0] != (pair{1, 0}) || got[1] != (pair{1, 1}) || got[2] != (pair{1, 0}) || got[3] != (pair{3, 1}) {
		t.Errorf("cells = %v", got)
	}
}

func TestObserveNamesUnreadableCellsUnknown(t *testing.T) {
	w := simWorld{
		protocol.BlockPos{0, 1, 0}: "cobblestone",
		protocol.BlockPos{1, 1, 0}: "cobblestone",
	}
	r := newRig(t, w, mgl32.Vec3{10.5, 1, 10.5})
	o := NewObserve(w, r.self, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{2, 1, 1})
	if st, err := r.run(o, 50); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	b, ok := o.(BlockReporter).Blocks()
	if !ok || len(b.Palette) != 2 || b.Palette[1] != UnknownBlock {
		t.Fatalf("palette = %v, read %v", b.Palette, ok)
	}
	// The two cobblestone cells, then the four the world never answered for.
	if got := expand(t, b.Cells); len(got) != 2 || got[0] != (pair{2, 0}) || got[1] != (pair{4, 1}) {
		t.Errorf("cells = %v", got)
	}
}

func TestObserveWalksUntilTheBoxIsStreamedIn(t *testing.T) {
	w := simWorld{}
	w.platform(0, 131, -1, 1)
	r := newRig(t, w, mgl32.Vec3{130.5, 1, 0.5})
	o := NewObserve(w, r.self, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{0, 1, 0})
	before := r.ticks
	if st, err := r.run(o, 600); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.ticks == before {
		t.Error("the unit read the box without walking within eight chunks of it")
	}
	b, ok := o.(BlockReporter).Blocks()
	if !ok || len(b.Palette) != 1 || b.Palette[0] != "air" {
		t.Fatalf("palette = %v, read %v", b.Palette, ok)
	}
}

func TestObserveRunLengthsOutlastASingleByte(t *testing.T) {
	w := simWorld{}
	w.fill("dirt", 0, 1, 0, 199, 1, 0)
	r := newRig(t, w, mgl32.Vec3{99.5, 1, 60.5})
	o := NewObserve(w, r.self, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{199, 1, 0})
	if st, err := r.run(o, 50); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	b, ok := o.(BlockReporter).Blocks()
	if !ok {
		t.Fatal("the box was never read")
	}
	// 200 needs two bytes of uvarint; a coder that stopped at 127 would send
	// two runs and describe the box wrongly.
	if got := expand(t, b.Cells); len(got) != 1 || got[0] != (pair{200, 0}) {
		t.Errorf("cells = %v", got)
	}
}

func TestObserveWalksToAStandableSpotWhenTheBoxCentreIsSolid(t *testing.T) {
	w := simWorld{}
	w.platform(0, 131, -1, 1)
	// The box is one solid block, so its centre is no cell to stand in.
	w[protocol.BlockPos{0, 1, 0}] = "stone"
	r := newRig(t, w, mgl32.Vec3{130.5, 1, 0.5})
	o := NewObserve(w, r.self, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{0, 1, 0})
	before := r.ticks
	if st, err := r.run(o, 600); st != Done || err != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if r.ticks == before {
		t.Error("the unit read the box without walking within eight chunks of it")
	}
	b, ok := o.(BlockReporter).Blocks()
	if !ok || len(b.Palette) != 1 || b.Palette[0] != "stone" {
		t.Fatalf("palette = %v, read %v", b.Palette, ok)
	}
}

func TestObserveFailsWithAClearErrorWhenNoSpotCanBeStoodIn(t *testing.T) {
	w := simWorld{}
	// Stone all around the box, so no cell near it can be stood in.
	w.fill("stone", 0, 0, 0, 200, 3, 2)
	w.fill("air", 150, 1, 1, 150, 2, 1)
	r := newRig(t, w, mgl32.Vec3{150.5, 1, 1.5})
	o := NewObserve(w, r.self, protocol.BlockPos{0, 1, 0}, protocol.BlockPos{0, 1, 0})
	st, err := r.run(o, 50)
	if st != Failed || err == nil || !strings.Contains(err.Error(), "standing spot") {
		t.Fatalf("status %v, err %v", st, err)
	}
}

func TestObserveFailsWhenTheReportCannotTravel(t *testing.T) {
	// A checkerboard is the worst case: every cell starts a run, and the
	// stream alone runs to many times the event that carries it.
	w := simWorld{}
	for x := int32(0); x < 16; x++ {
		for z := int32(0); z < 16; z++ {
			for y := int32(0); y < 16; y++ {
				name := "air"
				if (x+y+z)%2 == 0 {
					name = "dirt"
				}
				w[protocol.BlockPos{x, y, z}] = name
			}
		}
	}
	r := newRig(t, w, mgl32.Vec3{30.5, 1, 30.5})
	o := NewObserve(w, r.self, protocol.BlockPos{0, 0, 0}, protocol.BlockPos{15, 15, 15})
	st, err := r.run(o, 50)
	if st != Failed || err == nil || err.Error() != "observation too large" {
		t.Fatalf("status %v, err %v", st, err)
	}
	if _, ok := o.(BlockReporter).Blocks(); ok {
		t.Error("an observation too large to send was reported anyway")
	}
}
