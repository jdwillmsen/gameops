package camera

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const self uint64 = 7

type fakeDecoder struct {
	section Section
	err     error
}

func (f fakeDecoder) SubChunk([]byte) (Section, error) { return f.section, f.err }
func (f fakeDecoder) Chunk([]byte, int) ([]PlacedSection, error) {
	return []PlacedSection{{Y: 2, Section: f.section}}, f.err
}
func (fakeDecoder) BlockName(id uint32) (string, bool) {
	if id == 1 {
		return "minecraft:stone", true
	}
	return "", false
}

// stoneOnFloor is a section whose y=0 layer is stone and the rest air.
func stoneOnFloor() Section {
	s := Section{Palette: []string{"minecraft:air", "minecraft:stone"}}
	for i := 0; i < 256; i++ {
		s.Cells[i] = 1
	}
	return s
}

type sink struct{ sent []packet.Packet }

func (s *sink) WritePacket(p packet.Packet) error { s.sent = append(s.sent, p); return nil }

func newTestHub(t *testing.T, max int) (*Hub, *belief.Tracker) {
	t.Helper()
	tr := belief.New(time.Now)
	tr.Spawn(self, mgl32.Vec3{0, 1000, 0}, 0, 0, 0)
	h := New(Options{Decoder: fakeDecoder{section: stoneOnFloor()}, Self: tr, MaxViewers: max, QueueLimit: 8, Tick: time.Millisecond})
	h.Spawn(self)
	return h, tr
}

// drain reads whatever is ready and stops at the first wait.
func drain(t *testing.T, v *Viewer) ([][]byte, error) {
	t.Helper()
	var out [][]byte
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		m, err := v.Next(ctx)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, m)
	}
}

func ofType(ms [][]byte, typ byte) [][]byte {
	var out [][]byte
	for _, m := range ms {
		if m[0] == typ {
			out = append(out, m)
		}
	}
	return out
}

// tickEntities lists the entity ids and x positions of tick frames, and the
// last self pose seen.
type tickView struct {
	ids  []uint64
	x    map[uint64]float32
	self *selfPose
}

func parseTicks(t *testing.T, ms [][]byte) tickView {
	t.Helper()
	tv := tickView{x: map[uint64]float32{}}
	for _, f := range ofType(ms, frameTick) {
		p := 2
		if f[1]&1 != 0 {
			var s selfPose
			fl := func(i int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(f[2+4*i:])) }
			s = selfPose{fl(0), fl(1), fl(2), fl(3), fl(4)}
			tv.self, p = &s, 22
		}
		n, k := binary.Uvarint(f[p:])
		p += k
		for ; n > 0; n-- {
			id, k := binary.Uvarint(f[p:])
			p += k + 2
			tv.ids = append(tv.ids, id)
			tv.x[id] = math.Float32frombits(binary.LittleEndian.Uint32(f[p:]))
			p += 14
		}
	}
	return tv
}

func attach(t *testing.T, h *Hub) *Viewer {
	t.Helper()
	v, err := h.Attach()
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(v.Close)
	return v
}

func subChunkPacket(x, y, z int32) *packet.SubChunk {
	return &packet.SubChunk{
		Position: protocol.SubChunkPos{x, y, z},
		SubChunkEntries: []protocol.SubChunkEntry{{
			Result:     protocol.SubChunkResultSuccess,
			RawPayload: protocol.Option([]byte{1}),
		}},
	}
}

func TestLevelChunkInRequestModeAsksForTheWholeColumn(t *testing.T) {
	h, _ := newTestHub(t, 1)
	w := &sink{}
	h.Apply(&packet.LevelChunk{Position: protocol.ChunkPos{3, -5}, SubChunkLimit: protocol.Option(int32(9))}, w)
	if len(w.sent) != 1 {
		t.Fatalf("sent %d packets, want 1", len(w.sent))
	}
	req := w.sent[0].(*packet.SubChunkRequest)
	if req.Position != (protocol.SubChunkPos{3, 0, -5}) || len(req.Offsets) != 9 ||
		req.Offsets[0] != (protocol.SubChunkOffset{0, -4, 0}) || req.Offsets[8] != (protocol.SubChunkOffset{0, 4, 0}) {
		t.Errorf("request = %+v", req)
	}
}

func TestOtherDimensionsAreNotDrawn(t *testing.T) {
	h, _ := newTestHub(t, 1)
	w := &sink{}
	h.Apply(&packet.LevelChunk{Dimension: 1, SubChunkLimit: protocol.Option(int32(9))}, w)
	h.Apply(&packet.SubChunk{Dimension: 1}, w)
	if len(w.sent) != 0 || h.Stats().SubChunksHeld != 0 {
		t.Errorf("another dimension reached the hub: sent=%d held=%d", len(w.sent), h.Stats().SubChunksHeld)
	}
}

func TestConnectionStartsWithHelloThenMaterials(t *testing.T) {
	h, _ := newTestHub(t, 1)
	got, _ := drain(t, attach(t, h))
	if len(got) < 2 || got[0][0] != frameHello || got[0][1] != wireVersion || got[1][0] != frameMaterials {
		t.Fatalf("head = %v", got)
	}
}

func TestSubChunkReachesViewerAsMaterialsThenPalettisedFrame(t *testing.T) {
	h, _ := newTestHub(t, 1)
	v := attach(t, h)
	drain(t, v) // hello, materials, entity types
	h.Apply(subChunkPacket(1, 2, 3), &sink{})
	got, err := drain(t, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0][0] != frameMaterials || got[1][0] != frameSubChunk {
		t.Fatalf("messages = %v, want materials then a sub-chunk frame", got)
	}
	x, y, z, cells, err := decodeSubChunk(got[1])
	if err != nil || x != 1 || y != 2 || z != 3 {
		t.Fatalf("frame = %d,%d,%d %v", x, y, z, err)
	}
	if cells[0] == 0 || cells[256] != 0 {
		t.Errorf("stone layer = %d, air above = %d", cells[0], cells[256])
	}
	if len(got[1]) > 13+2+2+512 {
		t.Errorf("frame is %d bytes; a stone-and-air section needs one index bit per cell", len(got[1]))
	}
}

func TestDecodeFailureIsCountedNotFatal(t *testing.T) {
	h := New(Options{Decoder: fakeDecoder{err: errors.New("bad")}, MaxViewers: 1})
	h.Apply(subChunkPacket(0, 0, 0), &sink{})
	if h.Stats().DecodeFailures != 1 {
		t.Errorf("failures = %d, want 1", h.Stats().DecodeFailures)
	}
}

func TestLateViewerGetsMaterialsThenTheHeldWorld(t *testing.T) {
	h, _ := newTestHub(t, 2)
	h.Apply(subChunkPacket(0, 0, 0), &sink{})
	h.Apply(subChunkPacket(5, 0, 5), &sink{})
	got, _ := drain(t, attach(t, h))
	if got[1][0] != frameMaterials {
		t.Fatal("the material table does not precede the world")
	}
	if frames := len(ofType(got, frameSubChunk)); frames != 2 {
		t.Errorf("replayed %d frames, want 2", frames)
	}
}

func TestBlockUpdateIsForwardedAndUnknownBlocksCounted(t *testing.T) {
	h, _ := newTestHub(t, 1)
	v := attach(t, h)
	drain(t, v)
	h.Apply(&packet.UpdateBlock{Position: protocol.BlockPos{-1, 64, 2}, NewBlockRuntimeID: 1}, &sink{})
	h.Apply(&packet.UpdateBlock{Position: protocol.BlockPos{0, 0, 0}, NewBlockRuntimeID: 99}, &sink{})
	got, _ := drain(t, v)
	if blocks := len(ofType(got, frameBlock)); blocks != 2 || h.Stats().UnmappedBlocks != 1 {
		t.Errorf("blocks=%d unmapped=%d, want 2 and 1", blocks, h.Stats().UnmappedBlocks)
	}
}

// A viewer that never reads must cost the packet loop nothing: the loop
// finishes, and the viewer alone is cut.
func TestStalledViewerNeverBlocksTheReadLoop(t *testing.T) {
	h, _ := newTestHub(t, 2)
	stalled := attach(t, h) // never calls Next
	healthy := attach(t, h)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50_000; i++ {
			h.Apply(&packet.UpdateBlock{Position: protocol.BlockPos{int32(i), 64, 0}, NewBlockRuntimeID: 1}, &sink{})
			drain1(healthy)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Apply blocked behind a stalled viewer")
	}
	if n := h.Stats().Viewers; n != 1 {
		t.Errorf("viewers = %d, want only the healthy one", n)
	}
	if _, err := stalled.Next(context.Background()); !errors.Is(err, ErrTooSlow) {
		t.Errorf("stalled viewer err = %v, want ErrTooSlow", err)
	}
}

// drain1 empties a viewer without waiting.
func drain1(v *Viewer) {
	for {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := v.Next(ctx); err != nil {
			return
		}
	}
}

func TestEntityMovesCoalesceIntoOneRecordPerTick(t *testing.T) {
	h, _ := newTestHub(t, 1)
	v := attach(t, h)
	drain(t, v)
	h.Apply(&packet.AddActor{EntityRuntimeID: 50, EntityType: "minecraft:cow", Position: mgl32.Vec3{1, 2, 3}}, &sink{})
	h.Apply(&packet.AddActor{EntityRuntimeID: 51, EntityType: "minecraft:pig"}, &sink{})
	for i := 0; i < 5000; i++ {
		h.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: 50, Position: mgl32.Vec3{float32(i), 2, 3}}, &sink{})
	}
	got, err := drain(t, v)
	if err != nil {
		t.Fatalf("viewer was dropped by entity traffic: %v", err)
	}
	if n := len(ofType(got, frameTick)); n != 1 {
		t.Errorf("%d tick frames, want the two entities batched in one", n)
	}
	tv := parseTicks(t, got)
	if len(tv.ids) != 2 || tv.x[50] != 4999 {
		t.Errorf("tick = %v, want both entities at their latest", tv)
	}
	if types := ofType(got, frameEntityTypes); len(types) == 0 {
		t.Error("entity types were not announced before their records")
	}
}

func TestRemovedEntityIsAnnouncedAndNeverMovedAgain(t *testing.T) {
	h, _ := newTestHub(t, 1)
	v := attach(t, h)
	drain(t, v)
	h.Apply(&packet.AddActor{EntityUniqueID: 9, EntityRuntimeID: 50, EntityType: "minecraft:cow"}, &sink{})
	h.Apply(&packet.RemoveActor{EntityUniqueID: 9}, &sink{})
	got, _ := drain(t, v)
	if ids := parseTicks(t, got).ids; len(ids) != 0 {
		t.Errorf("a removed entity was still sent: %v", ids)
	}
	rm := ofType(got, frameRemove)
	if len(rm) != 1 || !bytes.Equal(rm[0], encodeRemove([]uint64{50})) {
		t.Errorf("remove frames = %v", rm)
	}
	if h.Stats().Entities != 0 {
		t.Error("entity still held")
	}
}

func TestViewerCap(t *testing.T) {
	h, _ := newTestHub(t, 2)
	a, b := attach(t, h), attach(t, h)
	if _, err := h.Attach(); !errors.Is(err, ErrFull) {
		t.Fatalf("third viewer err = %v, want ErrFull", err)
	}
	a.Close()
	a.Close() // idempotent
	if _, err := h.Attach(); err != nil {
		t.Errorf("slot not freed after Close: %v", err)
	}
	_ = b
}

// GameData's position is a placeholder; viewers must not be told it.
func TestSelfIsPublishedOnlyOnceBeliefTrustsIt(t *testing.T) {
	h, tr := newTestHub(t, 1)
	v := attach(t, h)
	drain(t, v)

	own := &packet.MovePlayer{EntityRuntimeID: self}
	h.Apply(own, &sink{}) // belief has not seen it yet, as in main's ordering mistake
	if got, _ := drain(t, v); len(got) != 0 {
		t.Fatalf("an untrusted position was published: %v", got)
	}

	own.Position, own.Yaw, own.Pitch = mgl32.Vec3{4, 70, 5}, 90, -10
	tr.Apply(own)
	h.Apply(own, &sink{})
	got, _ := drain(t, v)
	s := parseTicks(t, got).self
	if s == nil || *s != (selfPose{4, 70, 5, 90, -10}) {
		t.Errorf("self = %+v", s)
	}
}

func TestOtherPlayersMoveAsEntitiesNotAsSelf(t *testing.T) {
	h, _ := newTestHub(t, 1)
	v := attach(t, h)
	drain(t, v)
	h.Apply(&packet.AddPlayer{EntityRuntimeID: 8}, &sink{})
	h.Apply(&packet.MovePlayer{EntityRuntimeID: 8, Position: mgl32.Vec3{1, 2, 3}}, &sink{})
	got, _ := drain(t, v)
	tv := parseTicks(t, got)
	if tv.self != nil || len(tv.ids) != 1 || tv.x[8] != 1 {
		t.Errorf("tick = %+v", tv)
	}
}

func TestResetEmptiesTheWorldForEveryone(t *testing.T) {
	h, _ := newTestHub(t, 1)
	v := attach(t, h)
	h.Apply(subChunkPacket(0, 0, 0), &sink{})
	h.Reset()
	got, _ := drain(t, v)
	if r := ofType(got, frameReset); len(r) != 1 || got[len(got)-1][0] != frameReset {
		t.Errorf("after reset = %v, want the connection head then only the reset marker", got)
	}
	if len(ofType(got, frameSubChunk)) != 0 || h.Stats().SubChunksHeld != 0 {
		t.Error("world still held")
	}
}

func TestMetricsCountBytesDropsAndRefusals(t *testing.T) {
	h, _ := newTestHub(t, 2)
	h.Apply(subChunkPacket(0, 0, 0), &sink{})
	v := attach(t, h)
	got, _ := drain(t, v)
	var want uint64
	for _, m := range got {
		want += uint64(len(m))
	}
	st := h.Stats()
	if st.BytesSent != want || st.SubChunksSent != 1 {
		t.Errorf("bytes %d (want %d), sub-chunks %d", st.BytesSent, want, st.SubChunksSent)
	}
	stalled := attach(t, h)
	if _, err := h.Attach(); !errors.Is(err, ErrFull) {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		h.Apply(&packet.UpdateBlock{Position: protocol.BlockPos{int32(i), 64, 0}, NewBlockRuntimeID: 1}, &sink{})
		drain1(v)
	}
	if st := h.Stats(); st.ViewersDropped != 1 || st.ViewersRefused != 1 {
		t.Errorf("dropped %d refused %d, want 1 and 1", st.ViewersDropped, st.ViewersRefused)
	}
	_ = stalled
}
