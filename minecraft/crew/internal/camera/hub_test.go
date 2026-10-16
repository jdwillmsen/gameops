package camera

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
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
	h := New(Options{Decoder: fakeDecoder{section: stoneOnFloor()}, Self: tr, MaxViewers: max, QueueLimit: 8})
	h.Spawn(self)
	return h, tr
}

// drain reads whatever is ready and stops at the first wait.
func drain(t *testing.T, v *Viewer) ([]Message, error) {
	t.Helper()
	var out []Message
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
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

func texts(ms []Message) []map[string]any {
	var out []map[string]any
	for _, m := range ms {
		if !m.Text {
			continue
		}
		var d map[string]any
		_ = json.Unmarshal(m.Data, &d)
		out = append(out, d)
	}
	return out
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

func TestSubChunkReachesViewerAsMaterialsThenFrame(t *testing.T) {
	h, _ := newTestHub(t, 1)
	v := attach(t, h)
	drain(t, v) // materials only
	h.Apply(subChunkPacket(1, 2, 3), &sink{})
	got, err := drain(t, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Text || got[0].Data[0] != '{' || got[1].Text {
		t.Fatalf("messages = %+v, want materials then a binary frame", got)
	}
	f := got[1].Data
	if len(f) != 13+4096 || f[0] != frameSubChunk ||
		int32(binary.LittleEndian.Uint32(f[1:])) != 1 || int32(binary.LittleEndian.Uint32(f[5:])) != 2 || int32(binary.LittleEndian.Uint32(f[9:])) != 3 {
		t.Fatalf("bad frame header")
	}
	if f[13] == 0 || f[13+256] != 0 {
		t.Errorf("stone layer = %d, air above = %d", f[13], f[13+256])
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
	var frames int
	for i, m := range got {
		if i == 0 && !m.Text {
			t.Fatal("first message is not the material table")
		}
		if !m.Text {
			frames++
		}
	}
	if frames != 2 {
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
	var blocks int
	for _, m := range got {
		if !m.Text && m.Data[0] == frameBlock {
			blocks++
		}
	}
	if blocks != 2 || h.Stats().UnmappedBlocks != 1 {
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

func TestEntityMovesCoalesceInsteadOfFillingTheQueue(t *testing.T) {
	h, _ := newTestHub(t, 1)
	v := attach(t, h)
	h.Apply(&packet.AddActor{EntityRuntimeID: 50, EntityType: "minecraft:cow", Position: mgl32.Vec3{1, 2, 3}}, &sink{})
	for i := 0; i < 5000; i++ {
		h.Apply(&packet.MoveActorAbsolute{EntityRuntimeID: 50, Position: mgl32.Vec3{float32(i), 2, 3}}, &sink{})
	}
	got, err := drain(t, v)
	if err != nil {
		t.Fatalf("viewer was dropped by entity traffic: %v", err)
	}
	var ents []map[string]any
	for _, d := range texts(got) {
		if d["t"] == "ent" {
			ents = append(ents, d)
		}
	}
	if len(ents) != 1 || ents[0]["x"] != float64(4999) {
		t.Errorf("entity updates = %v, want the single latest", ents)
	}
}

func TestRemovedEntityIsAnnounced(t *testing.T) {
	h, _ := newTestHub(t, 1)
	v := attach(t, h)
	drain(t, v)
	h.Apply(&packet.AddActor{EntityUniqueID: 9, EntityRuntimeID: 50, EntityType: "minecraft:cow"}, &sink{})
	h.Apply(&packet.RemoveActor{EntityUniqueID: 9}, &sink{})
	got, _ := drain(t, v)
	for _, d := range texts(got) {
		if d["t"] == "ent" {
			t.Errorf("a removed entity was still sent: %v", d)
		}
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
	if got, _ := drain(t, v); len(texts(got)) != 0 {
		t.Fatalf("an untrusted position was published: %v", texts(got))
	}

	own.Position, own.Yaw, own.Pitch = mgl32.Vec3{4, 70, 5}, 90, -10
	tr.Apply(own)
	h.Apply(own, &sink{})
	got, _ := drain(t, v)
	ts := texts(got)
	if len(ts) != 1 || ts[0]["t"] != "self" || ts[0]["yaw"] != float64(90) || ts[0]["pitch"] != float64(-10) || ts[0]["y"] != float64(70) {
		t.Errorf("self = %v", ts)
	}
}

func TestOtherPlayersMoveAsEntitiesNotAsSelf(t *testing.T) {
	h, _ := newTestHub(t, 1)
	v := attach(t, h)
	h.Apply(&packet.AddPlayer{EntityRuntimeID: 8}, &sink{})
	h.Apply(&packet.MovePlayer{EntityRuntimeID: 8, Position: mgl32.Vec3{1, 2, 3}}, &sink{})
	for _, d := range texts(first(drain(t, v))) {
		if d["t"] == "self" {
			t.Errorf("another player's move was published as self: %v", d)
		}
	}
}

func first(ms []Message, _ error) []Message { return ms }

func TestResetEmptiesTheWorldForEveryone(t *testing.T) {
	h, _ := newTestHub(t, 1)
	v := attach(t, h)
	h.Apply(subChunkPacket(0, 0, 0), &sink{})
	h.Reset()
	got, _ := drain(t, v)
	if len(got) != 1 || texts(got)[0]["t"] != "reset" {
		t.Errorf("after reset = %+v, want only the reset marker", got)
	}
	if h.Stats().SubChunksHeld != 0 {
		t.Error("world still held")
	}
}
