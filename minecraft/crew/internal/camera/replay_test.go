package camera

import (
	"context"
	"errors"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// placeUnit moves the unit the way the server does, so belief trusts the pose
// and the hub publishes it.
func placeUnit(h *Hub, tr *belief.Tracker, x, y, z float32) {
	mv := &packet.MovePlayer{EntityRuntimeID: self, Position: mgl32.Vec3{x, y, z}}
	tr.Apply(mv)
	h.Apply(mv, &sink{})
}

// newLocatedHub is a hub whose unit stands in the middle of sub-chunk 0,0,0.
func newLocatedHub(t *testing.T, max int) (*Hub, *belief.Tracker) {
	t.Helper()
	h, tr := newTestHub(t, max)
	placeUnit(h, tr, 8, 8, 8)
	return h, tr
}

func subKeys(t *testing.T, ms [][]byte) [][3]int32 {
	t.Helper()
	var out [][3]int32
	for _, m := range ofType(ms, frameSubChunk) {
		x, y, z, _, err := decodeSubChunk(m)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, [3]int32{x, y, z})
	}
	return out
}

func hold(h *Hub, keys ...[3]int32) {
	for _, k := range keys {
		h.Apply(subChunkPacket(k[0], k[1], k[2]), &sink{})
	}
}

func equalKeys(a, b [][3]int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestReplayStartsAtFourChunksNearestFirst(t *testing.T) {
	h, _ := newLocatedHub(t, 1)
	hold(h, [3]int32{4, 0, 0}, [3]int32{0, 0, 0}, [3]int32{5, 0, 0}, [3]int32{-2, 0, 0}, [3]int32{0, 5, 0}, [3]int32{1, 0, 0}, [3]int32{0, 0, -4})
	got := subKeys(t, first(drain(t, attach(t, h))))
	// (1,0,0) and (-2,0,0) are 1 and 2 chunks away; (4,0,0) and (0,0,-4) sit on the edge.
	if want := [][3]int32{{0, 0, 0}, {1, 0, 0}, {-2, 0, 0}}; !equalKeys(got[:3], want) || len(got) != 5 {
		t.Errorf("replayed %v, want %v first and the two edge chunks last, nothing at 5", got, want)
	}
}

func first(ms [][]byte, _ error) [][]byte { return ms }

func TestWideningTheRadiusReplaysTheRestOnceNearestFirst(t *testing.T) {
	h, _ := newLocatedHub(t, 1)
	hold(h, [3]int32{3, 0, 0}, [3]int32{9, 0, 0}, [3]int32{6, 0, 0}, [3]int32{20, 0, 0})
	v := attach(t, h)
	if got := subKeys(t, first(drain(t, v))); len(got) != 1 {
		t.Fatalf("initial replay = %v, want only the chunk inside the starting radius", got)
	}
	v.SetRadius(10)
	got := subKeys(t, first(drain(t, v)))
	if want := [][3]int32{{6, 0, 0}, {9, 0, 0}}; !equalKeys(got, want) {
		t.Errorf("after widening = %v, want %v", got, want)
	}
	v.SetRadius(10)
	if got := subKeys(t, first(drain(t, v))); len(got) != 0 {
		t.Errorf("resent %v", got)
	}
}

func TestRadiusIsClamped(t *testing.T) {
	h := New(Options{Decoder: fakeDecoder{section: stoneOnFloor()}, MaxViewers: 1, MaxRadius: 6})
	v := attach(t, h)
	radius := func() int {
		v.mu.Lock()
		defer v.mu.Unlock()
		return v.radius
	}
	if radius() != 4 {
		t.Errorf("starting radius = %d, want 4", radius())
	}
	v.SetRadius(0)
	if radius() != 1 {
		t.Errorf("radius = %d, want the floor of 1", radius())
	}
	v.SetRadius(200)
	if radius() != 6 {
		t.Errorf("radius = %d, want the ceiling of 6", radius())
	}
}

func TestMovingBringsNewChunksIntoRange(t *testing.T) {
	h, tr := newLocatedHub(t, 1)
	hold(h, [3]int32{0, 0, 0}, [3]int32{7, 0, 0})
	v := attach(t, h)
	if got := subKeys(t, first(drain(t, v))); len(got) != 1 {
		t.Fatalf("initial = %v", got)
	}
	placeUnit(h, tr, 7*16+8, 8, 8)
	got := subKeys(t, first(drain(t, v)))
	if len(got) != 1 || got[0] != ([3]int32{7, 0, 0}) {
		t.Errorf("after moving = %v, want the chunk the unit walked up to", got)
	}
}

func TestNothingIsReplayedUntilThePoseIsTrusted(t *testing.T) {
	h, tr := newTestHub(t, 1)
	hold(h, [3]int32{0, 0, 0})
	v := attach(t, h)
	if got := subKeys(t, first(drain(t, v))); len(got) != 0 {
		t.Fatalf("replayed %v with no known position", got)
	}
	placeUnit(h, tr, 8, 8, 8)
	if got := subKeys(t, first(drain(t, v))); len(got) != 1 {
		t.Errorf("replayed %v once the pose arrived, want 1", got)
	}
}

func TestLiveChunksInRangeArriveAndFarOnesWait(t *testing.T) {
	h, _ := newLocatedHub(t, 1)
	v := attach(t, h)
	drain(t, v)
	hold(h, [3]int32{2, 0, 0}, [3]int32{12, 0, 0})
	got := subKeys(t, first(drain(t, v)))
	if len(got) != 1 || got[0] != ([3]int32{2, 0, 0}) {
		t.Errorf("live = %v, want only the chunk in range", got)
	}
}

func TestBlockEditsReachHeldChunksAndOnlyViewersThatHaveThem(t *testing.T) {
	h, _ := newLocatedHub(t, 2)
	hold(h, [3]int32{0, 0, 0}, [3]int32{9, 0, 0})
	near := attach(t, h)
	drain(t, near)
	h.Apply(&packet.UpdateBlock{Position: protocol.BlockPos{3, 1, 3}, NewBlockRuntimeID: 1}, &sink{})
	h.Apply(&packet.UpdateBlock{Position: protocol.BlockPos{9*16 + 3, 1, 3}, NewBlockRuntimeID: 1}, &sink{})
	got, _ := drain(t, near)
	if n := len(ofType(got, frameBlock)); n != 1 {
		t.Errorf("%d edits sent, want only the one in the chunk the viewer holds", n)
	}

	// A viewer that connects later sees the edit inside the replayed chunk.
	late := attach(t, h)
	frames := ofType(first(drain(t, late)), frameSubChunk)
	_, _, _, cells, err := decodeSubChunk(frames[0])
	if err != nil || cells[(1*16+3)*16+3] == 0 {
		t.Errorf("held chunk lost the edit: %v", err)
	}
	if c := cells[(1*16+3)*16+3]; c == 0 {
		t.Error("edit missing")
	}
}

func TestEditInAnEmptyChunkCreatesIt(t *testing.T) {
	h, _ := newLocatedHub(t, 1)
	v := attach(t, h)
	drain(t, v)
	h.Apply(&packet.UpdateBlock{Position: protocol.BlockPos{1, 40, 1}, NewBlockRuntimeID: 1}, &sink{})
	if got := subKeys(t, first(drain(t, v))); len(got) != 1 || got[0] != ([3]int32{0, 2, 0}) {
		t.Errorf("sent %v, want the new chunk", got)
	}
	if h.Stats().SubChunksHeld != 1 {
		t.Error("new chunk not held")
	}
}

func TestBulkEditsToOneChunkSendTheChunkNotEveryBlock(t *testing.T) {
	h, _ := newLocatedHub(t, 1)
	hold(h, [3]int32{0, 0, 0})
	v := attach(t, h)
	drain(t, v)
	var edits []protocol.BlockChangeEntry
	for i := 0; i < 2000; i++ {
		edits = append(edits, protocol.BlockChangeEntry{BlockPos: protocol.BlockPos{int32(i % 16), int32(i / 256 % 16), int32(i / 16 % 16)}, BlockRuntimeID: 1})
	}
	h.Apply(&packet.UpdateSubChunkBlocks{Blocks: edits}, &sink{})
	got, err := drain(t, v)
	if err != nil {
		t.Fatalf("a bulk edit cut the viewer: %v", err)
	}
	if len(ofType(got, frameBlock)) != 0 || len(ofType(got, frameSubChunk)) != 1 {
		t.Errorf("sent %d edits and %d chunks, want the chunk once", len(ofType(got, frameBlock)), len(ofType(got, frameSubChunk)))
	}
}

// The replay is pulled by the viewer's own reads, so a big world can never
// look like a slow consumer however small the queue is.
func TestReplayIsPacedByTheReaderNotQueued(t *testing.T) {
	h, _ := newLocatedHub(t, 1)
	for x := int32(-4); x <= 4; x++ {
		for z := int32(-4); z <= 4; z++ {
			hold(h, [3]int32{x, 0, z})
		}
	}
	v := attach(t, h)
	v.mu.Lock()
	queued := len(v.fifo) + len(v.head)
	v.mu.Unlock()
	if queued > 8 {
		t.Errorf("%d messages queued up front for 81 chunks", queued)
	}
	if n := len(subKeys(t, first(drain(t, v)))); n != 81 {
		t.Errorf("replayed %d chunks, want 81", n)
	}
}

func TestStalledViewerIsNotFedLiveChunksOutOfRange(t *testing.T) {
	h, _ := newLocatedHub(t, 1)
	v := attach(t, h) // never reads
	for i := int32(0); i < 500; i++ {
		hold(h, [3]int32{i + 20, 0, 0})
	}
	if _, err := v.Next(canceled()); errors.Is(err, ErrTooSlow) {
		t.Error("chunks nobody asked for filled the queue")
	}
}

func canceled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
