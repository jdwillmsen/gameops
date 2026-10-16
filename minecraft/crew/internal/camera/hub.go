// Package camera turns the world packets a crew unit already receives into a
// stream a browser can draw from the unit's point of view.
//
// The stream is a position channel: it carries the unit's coordinates, look
// and surroundings. It has no authentication of its own and is mounted only
// when CAMERA_ENABLED is set, so it must stay in-cluster until crew-control
// puts authentication in front of it. Nothing here ever logs a position.
package camera

import (
	"cmp"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	// Enough for a few seconds of block edits and entity churn while a viewer
	// is busy replaying the held world, small enough that a stalled one is cut
	// loose quickly.
	defaultQueueLimit = 1024

	// Moved things reach a viewer in one batch per tick, matching the
	// server's own rate, instead of one message per movement packet.
	defaultTick = 50 * time.Millisecond
)

// PacketWriter is the part of the connection the hub needs: sub-chunk requests.
type PacketWriter interface{ WritePacket(packet.Packet) error }

// SelfSource is where the unit's own position and look come from. The hub
// reads it after each relevant packet, so it must be updated first.
type SelfSource interface{ Snapshot() belief.Snapshot }

type Options struct {
	Decoder    Decoder
	Self       SelfSource
	MaxViewers int
	// QueueLimit bounds each viewer's ordered backlog. Zero means the default.
	QueueLimit int
	// Tick is the longest a moved entity waits before it is sent. Zero means
	// the default.
	Tick time.Duration
}

type entity struct {
	ID      uint64
	Type    string
	TypeID  uint16
	X, Y, Z float32
	Yaw     float32
	Pitch   float32
}

type Hub struct {
	dec        Decoder
	self       SelfSource
	maxViewers int
	queueLimit int
	tick       time.Duration
	mats       *materialTable

	mu       sync.Mutex
	runtime  uint64
	subs     map[[3]int32][]byte // wire frames, replayed to a new viewer
	ents     map[uint64]*entity
	uniqueID map[int64]uint64
	viewers  map[*Viewer]struct{}
	types    []string // entity type names; the index is the wire type id
	typeIDs  map[string]uint16

	decodeFailures atomic.Uint64
	unmapped       atomic.Uint64
	bytesSent      atomic.Uint64
	subChunksSent  atomic.Uint64
	viewersCut     atomic.Uint64
	viewersTurned  atomic.Uint64
}

// New builds a hub. Nothing is registered globally: the caller mounts it.
func New(o Options) *Hub {
	if o.QueueLimit <= 0 {
		o.QueueLimit = defaultQueueLimit
	}
	if o.Tick <= 0 {
		o.Tick = defaultTick
	}
	return &Hub{
		dec:        o.Decoder,
		self:       o.Self,
		maxViewers: o.MaxViewers,
		queueLimit: o.QueueLimit,
		tick:       o.Tick,
		mats:       newMaterialTable(),
		subs:       map[[3]int32][]byte{},
		ents:       map[uint64]*entity{},
		uniqueID:   map[int64]uint64{},
		viewers:    map[*Viewer]struct{}{},
		typeIDs:    map[string]uint16{},
	}
}

// Stats is safe to serve: it holds counts only.
type Stats struct {
	Viewers        int    `json:"viewers"`
	SubChunksHeld  int    `json:"sub_chunks_held"`
	Entities       int    `json:"entities"`
	DecodeFailures uint64 `json:"decode_failures"`
	UnmappedBlocks uint64 `json:"unmapped_blocks"`
	// BytesSent counts application bytes handed to viewers, before the
	// websocket's own compression.
	BytesSent     uint64 `json:"bytes_sent"`
	SubChunksSent uint64 `json:"sub_chunks_sent"`
	// ViewersDropped counts viewers cut for falling behind; ViewersRefused
	// counts connections turned away at the cap.
	ViewersDropped uint64 `json:"viewers_dropped"`
	ViewersRefused uint64 `json:"viewers_refused"`
}

func (h *Hub) Stats() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	return Stats{
		Viewers: len(h.viewers), SubChunksHeld: len(h.subs), Entities: len(h.ents),
		DecodeFailures: h.decodeFailures.Load(), UnmappedBlocks: h.unmapped.Load(),
		BytesSent: h.bytesSent.Load(), SubChunksSent: h.subChunksSent.Load(),
		ViewersDropped: h.viewersCut.Load(), ViewersRefused: h.viewersTurned.Load(),
	}
}

// Spawn starts a session: whatever the last one held is gone.
func (h *Hub) Spawn(runtimeID uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runtime = runtimeID
	h.resetLocked()
}

// Reset is called when a session ends, so a viewer never mistakes the last
// session's world for a live one.
func (h *Hub) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runtime = 0
	h.resetLocked()
}

func (h *Hub) resetLocked() {
	h.subs = map[[3]int32][]byte{}
	h.ents = map[uint64]*entity{}
	h.uniqueID = map[int64]uint64{}
	for v := range h.viewers {
		v.restart()
	}
}

// Attach registers a viewer and queues the held world for it. The replay is
// held by the viewer, not pushed through its bounded queue, so a large world
// never counts as a slow consumer.
func (h *Hub) Attach() (*Viewer, error) {
	h.mu.Lock()
	if len(h.viewers) >= h.maxViewers {
		h.mu.Unlock()
		h.viewersTurned.Add(1)
		return nil, ErrFull
	}
	v := newViewer(h, h.queueLimit, h.tick)
	h.viewers[v] = struct{}{}
	type held struct {
		key   [3]int32
		frame []byte
	}
	frames := make([]held, 0, len(h.subs))
	for k, f := range h.subs {
		frames = append(frames, held{k, f})
	}
	for _, e := range h.ents {
		v.ents[e.ID] = e.record()
	}
	head := [][]byte{helloFrame(), encodeMaterials(h.mats.all()), encodeEntityTypes(h.types)}
	// Read under the hub lock so a later publishSelf cannot be overwritten by
	// this older pose.
	var me belief.Snapshot
	if h.self != nil {
		me = h.self.Snapshot()
	}
	if me.PositionKnown {
		p := poseOf(me)
		v.self = &p
	}
	h.mu.Unlock()

	if me.PositionKnown {
		slices.SortFunc(frames, func(a, b held) int {
			return cmp.Compare(distSq(a.key, me.Position), distSq(b.key, me.Position))
		})
	}
	for _, f := range frames {
		head = append(head, f.frame)
	}
	v.mu.Lock()
	v.head = head
	v.mu.Unlock()
	return v, nil
}

func (h *Hub) detach(v *Viewer) {
	h.mu.Lock()
	delete(h.viewers, v)
	h.mu.Unlock()
}

func distSq(sub [3]int32, p [3]float32) float32 {
	var d float32
	for i, c := range sub {
		off := float32(c)*16 + 8 - p[i]
		d += off * off
	}
	return d
}

// Apply is called for every packet the session reads, after belief has seen
// it. It never waits on a viewer. w is used to request sub-chunks.
func (h *Hub) Apply(pk packet.Packet, w PacketWriter) {
	switch p := pk.(type) {
	case *packet.LevelChunk:
		h.levelChunk(p, w)
	case *packet.SubChunk:
		h.subChunk(p)
	case *packet.UpdateBlock:
		if p.Layer == 0 {
			h.updateBlock(p.Position, p.NewBlockRuntimeID)
		}
	case *packet.UpdateSubChunkBlocks:
		for _, b := range p.Blocks {
			h.updateBlock(b.BlockPos, b.BlockRuntimeID)
		}
	case *packet.Respawn:
		if p.State == packet.RespawnStateReadyToSpawn {
			h.publishSelf()
		}
	case *packet.ChangeDimension:
		// Only the overworld is drawn, so the old world is simply gone.
		h.mu.Lock()
		h.resetLocked()
		h.mu.Unlock()
		h.publishSelf()
	case *packet.AddActor:
		h.mu.Lock()
		h.uniqueID[p.EntityUniqueID] = p.EntityRuntimeID
		h.setEntityLocked(&entity{ID: p.EntityRuntimeID, Type: p.EntityType, X: p.Position[0], Y: p.Position[1], Z: p.Position[2], Yaw: p.Yaw, Pitch: p.Pitch})
		h.mu.Unlock()
	case *packet.AddPlayer:
		h.mu.Lock()
		h.setEntityLocked(&entity{ID: p.EntityRuntimeID, Type: "player", X: p.Position[0], Y: p.Position[1], Z: p.Position[2], Yaw: p.Yaw, Pitch: p.Pitch})
		h.mu.Unlock()
	case *packet.MovePlayer:
		h.movePlayer(p)
	case *packet.MoveActorAbsolute:
		h.moveEntity(p.EntityRuntimeID, func(e *entity) {
			e.X, e.Y, e.Z, e.Pitch, e.Yaw = p.Position[0], p.Position[1], p.Position[2], p.Rotation[0], p.Rotation[1]
		})
	case *packet.MoveActorDelta:
		h.moveEntity(p.EntityRuntimeID, func(e *entity) {
			if v, ok := p.PositionX.Value(); ok {
				e.X = v
			}
			if v, ok := p.PositionY.Value(); ok {
				e.Y = v
			}
			if v, ok := p.PositionZ.Value(); ok {
				e.Z = v
			}
			if v, ok := p.RotationY.Value(); ok {
				e.Yaw = v
			}
			if v, ok := p.RotationX.Value(); ok {
				e.Pitch = v
			}
		})
	case *packet.RemoveActor:
		h.mu.Lock()
		if rid, ok := h.uniqueID[p.EntityUniqueID]; ok {
			delete(h.ents, rid)
			delete(h.uniqueID, p.EntityUniqueID)
			rm := encodeRemove([]uint64{rid})
			h.broadcastLocked(func(v *Viewer) bool {
				v.dropEntity(rid)
				return v.push(rm)
			})
		}
		h.mu.Unlock()
	}
}

// publishSelf sends the unit's pose from belief. Until belief trusts the
// position there is nothing true to send, and a placeholder would put the
// camera underground.
func (h *Hub) publishSelf() {
	if h.self == nil {
		return
	}
	snap := h.self.Snapshot()
	if !snap.PositionKnown {
		return
	}
	p := poseOf(snap)
	h.mu.Lock()
	for v := range h.viewers {
		v.setSelf(p)
	}
	h.mu.Unlock()
}

func (h *Hub) movePlayer(p *packet.MovePlayer) {
	h.mu.Lock()
	own := h.runtime != 0 && p.EntityRuntimeID == h.runtime
	h.mu.Unlock()
	if own {
		h.publishSelf()
		return
	}
	h.moveEntity(p.EntityRuntimeID, func(e *entity) {
		e.X, e.Y, e.Z, e.Yaw, e.Pitch = p.Position[0], p.Position[1], p.Position[2], p.Yaw, p.Pitch
	})
}

func (h *Hub) moveEntity(id uint64, update func(*entity)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e, ok := h.ents[id]; ok {
		update(e)
		h.pushEntityLocked(e)
	}
}

func (h *Hub) setEntityLocked(e *entity) {
	if h.runtime != 0 && e.ID == h.runtime {
		return
	}
	e.TypeID = h.typeIDLocked(e.Type)
	h.ents[e.ID] = e
	h.pushEntityLocked(e)
}

func (h *Hub) pushEntityLocked(e *entity) {
	rec := e.record()
	for v := range h.viewers {
		v.setEntity(e.ID, rec)
	}
}

func (e *entity) record() []byte {
	return entityRecord(e.ID, e.TypeID, e.X, e.Y, e.Z, e.Yaw, e.Pitch)
}

// typeIDLocked numbers entity type names. A new name is announced to every
// viewer in order, ahead of any record that uses it.
func (h *Hub) typeIDLocked(name string) uint16 {
	if id, ok := h.typeIDs[name]; ok {
		return id
	}
	// 65535 distinct entity types never happens; the last id absorbs the
	// overflow instead of wrapping.
	if len(h.types) >= 0xffff {
		return 0xffff
	}
	id := uint16(len(h.types))
	h.types = append(h.types, name)
	h.typeIDs[name] = id
	m := encodeEntityTypes(h.types)
	h.broadcastLocked(func(v *Viewer) bool { return v.push(m) })
	return id
}

// broadcastLocked runs send for every viewer and forgets those it cut loose.
func (h *Hub) broadcastLocked(send func(*Viewer) (cut bool)) {
	for v := range h.viewers {
		if send(v) {
			delete(h.viewers, v)
			h.viewersCut.Add(1)
		}
	}
}

func poseOf(s belief.Snapshot) selfPose {
	return selfPose{s.Position[0], s.Position[1], s.Position[2], s.Yaw, s.Pitch}
}

// subChunkRequest builds the request for a chunk the server announced in
// request mode: the whole column from the world floor to the server's limit.
func subChunkRequest(p *packet.LevelChunk) *packet.SubChunkRequest {
	limit, ok := p.SubChunkLimit.Value()
	if !ok {
		return nil
	}
	const floor, ceiling = minY >> 4, maxY >> 4
	top := ceiling
	if limit >= 0 && floor+int(limit)-1 < top {
		top = floor + int(limit) - 1
	}
	if top < floor {
		return nil
	}
	offsets := make([]protocol.SubChunkOffset, 0, top-floor+1)
	// Offsets are relative to a base of sub-chunk Y 0.
	for y := floor; y <= top; y++ {
		offsets = append(offsets, protocol.SubChunkOffset{0, int8(y), 0})
	}
	return &packet.SubChunkRequest{
		Dimension: p.Dimension,
		Position:  protocol.SubChunkPos{p.Position[0], 0, p.Position[1]},
		Offsets:   offsets,
	}
}

func (h *Hub) levelChunk(p *packet.LevelChunk, w PacketWriter) {
	if p.Dimension != 0 {
		return
	}
	if req := subChunkRequest(p); req != nil {
		_ = w.WritePacket(req)
		return
	}
	if _, requestMode := p.SubChunkLimit.Value(); requestMode || p.CacheEnabled {
		return // a limit of zero, or a blob cache crew does not negotiate
	}
	secs, err := h.dec.Chunk(p.RawPayload, int(p.SubChunkCount))
	if err != nil {
		h.decodeFailures.Add(1)
		return
	}
	for _, s := range secs {
		h.emit(p.Position[0], s.Y, p.Position[1], s.Section)
	}
}

func (h *Hub) subChunk(p *packet.SubChunk) {
	if p.Dimension != 0 {
		return
	}
	for _, e := range p.SubChunkEntries {
		raw, ok := e.RawPayload.Value()
		if e.Result != protocol.SubChunkResultSuccess || !ok || len(raw) == 0 {
			continue
		}
		sec, err := h.dec.SubChunk(raw)
		if err != nil {
			h.decodeFailures.Add(1)
			continue
		}
		h.emit(p.Position[0]+int32(e.Offset[0]), p.Position[1]+int32(e.Offset[1]), p.Position[2]+int32(e.Offset[2]), sec)
	}
}

func (h *Hub) blockMaterial(netID uint32) (id uint8, fresh bool) {
	name, ok := h.dec.BlockName(netID)
	if !ok {
		h.unmapped.Add(1)
		name = unmappedBlock
	}
	return h.mats.id(name)
}

func (h *Hub) updateBlock(pos protocol.BlockPos, netID uint32) {
	id, fresh := h.blockMaterial(netID)
	frame := encodeBlock(pos.X(), pos.Y(), pos.Z(), id)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pushMaterialsLocked(fresh)
	h.pushOrderedLocked(frame)
}

// emit converts a section to the wire format, keeps it for later viewers and
// tells current ones.
func (h *Hub) emit(cx, cy, cz int32, s Section) {
	mat := make([]uint8, len(s.Palette))
	anyFresh, anyBlock := false, false
	for i, name := range s.Palette {
		var fresh bool
		if name == unmappedBlock {
			h.unmapped.Add(1)
		}
		mat[i], fresh = h.mats.id(name)
		anyFresh = anyFresh || fresh
	}
	var cells [subChunkCells]uint8
	for i, c := range s.Cells {
		if int(c) >= len(mat) {
			h.decodeFailures.Add(1)
			return
		}
		cells[i] = mat[c]
		anyBlock = anyBlock || mat[c] != 0
	}
	frame := encodeSubChunk(cx, cy, cz, &cells)
	key := [3]int32{cx, cy, cz}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, held := h.subs[key]
	switch {
	case anyBlock:
		h.subs[key] = frame
	case held:
		// Cleared sections must still reach the viewer, but need not be kept.
		delete(h.subs, key)
	default:
		return
	}
	h.pushMaterialsLocked(anyFresh)
	h.pushOrderedLocked(frame)
}

func (h *Hub) pushMaterialsLocked(fresh bool) {
	if fresh {
		h.pushOrderedLocked(encodeMaterials(h.mats.all()))
	}
}

func (h *Hub) pushOrderedLocked(m []byte) {
	h.broadcastLocked(func(v *Viewer) bool { return v.push(m) })
}
