// Package camera turns the world packets a crew unit already receives into a
// stream a browser can draw from the unit's point of view.
//
// The stream is a position channel: it carries the unit's coordinates, look
// and surroundings. It has no authentication of its own and is mounted only
// when CAMERA_ENABLED is set, so it must stay in-cluster until crew-control
// puts authentication in front of it. Nothing here ever logs a position.
package camera

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/belief"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	// Enough for a few seconds of block edits and entity churn while a viewer
	// is busy replaying the held world, small enough that a stalled one is cut
	// loose quickly.
	defaultQueueLimit = 1024

	// A fill touches thousands of blocks; past this many in one chunk, the
	// chunk is cheaper to send whole than edit by edit.
	maxEditFrames = 64

	// Moved things reach a viewer in one batch per tick, matching the
	// server's own rate, instead of one message per movement packet.
	defaultTick = 50 * time.Millisecond

	// A camera wall tile needs the unit's surroundings, not its whole world.
	defaultStartRadius = 4
	defaultMaxRadius   = 32
)

// PacketWriter is the connection Apply is handed.
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
	// StartRadius is how many chunks around the unit a new viewer is replayed;
	// MaxRadius bounds what a viewer may ask for. Zero means the defaults.
	StartRadius, MaxRadius int
}

type entity struct {
	ID      uint64
	Type    string
	TypeID  uint16
	X, Y, Z float32
	Yaw     float32
	Pitch   float32
	HeadYaw float32
	Name    string
	Look    entityLook
	// UUID ties a player to the skin the server listed for it.
	UUID  uuid.UUID
	Skin  *skinInfo
	Armor armourSlots
}

// playerEyeHeight is how far above the feet a MovePlayer position sits. The
// other packets that place an entity (AddPlayer, AddActor, the actor moves)
// report the feet; observed against a live server for AddPlayer and MovePlayer,
// assumed for the rest.
const playerEyeHeight = 1.62

type Hub struct {
	dec        Decoder
	self       SelfSource
	maxViewers int
	queueLimit int
	tick       time.Duration
	startR     int
	maxRadius  int
	mats       *materialTable

	mu       sync.Mutex
	runtime  uint64
	subs     map[[3]int32][]byte // wire frames, replayed to a new viewer
	ents     map[uint64]*entity
	uniqueID map[int64]uint64
	viewers  map[*Viewer]struct{}
	atlasPNG []byte
	atlasDoc []byte
	itemsPNG []byte
	itemsDoc []byte
	entPNG   []byte
	entDoc   []byte
	itemName map[int32]string // item network id -> name without namespace
	skins    map[uuid.UUID]*skinInfo
	ownSkin  *skinInfo
	ownArmor armourSlots
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
	if o.MaxRadius <= 0 {
		o.MaxRadius = defaultMaxRadius
	}
	if o.StartRadius <= 0 {
		o.StartRadius = defaultStartRadius
	}
	o.StartRadius = min(o.StartRadius, o.MaxRadius)
	return &Hub{
		dec:        o.Decoder,
		self:       o.Self,
		maxViewers: o.MaxViewers,
		queueLimit: o.QueueLimit,
		tick:       o.Tick,
		startR:     o.StartRadius,
		maxRadius:  o.MaxRadius,
		mats:       newMaterialTable(),
		subs:       map[[3]int32][]byte{},
		ents:       map[uint64]*entity{},
		uniqueID:   map[int64]uint64{},
		skins:      map[uuid.UUID]*skinInfo{},
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
	h.forgetPlayersLocked()
	h.resetLocked()
}

// Reset is called when a session ends, so a viewer never mistakes the last
// session's world for a live one.
func (h *Hub) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runtime = 0
	h.forgetPlayersLocked()
	h.resetLocked()
}

// forgetPlayersLocked drops what the server sends once per session, not again
// after a dimension change.
func (h *Hub) forgetPlayersLocked() {
	h.skins = map[uuid.UUID]*skinInfo{}
	h.ownArmor = armourSlots{}
}

func (h *Hub) resetLocked() {
	h.subs = map[[3]int32][]byte{}
	h.ents = map[uint64]*entity{}
	h.uniqueID = map[int64]uint64{}
	for v := range h.viewers {
		v.restart()
	}
	if h.ownSkin != nil {
		h.pushOrderedLocked(encodeSkin(0, *h.ownSkin))
	}
	if !h.ownArmor.zero() {
		h.pushOrderedLocked(encodeArmour(map[uint64]armourSlots{0: h.ownArmor}))
	}
}

// Full reports whether Attach would refuse a viewer right now. It attaches
// nothing and does not count a refusal.
func (h *Hub) Full() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.viewers) >= h.maxViewers
}

// Attach registers a viewer. Its connection head is queued; the held world is
// not, because the viewer pulls it nearest-first as it reads.
func (h *Hub) Attach() (*Viewer, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.viewers) >= h.maxViewers {
		h.viewersTurned.Add(1)
		return nil, ErrFull
	}
	v := newViewer(h, h.queueLimit, h.tick, h.startR)
	h.viewers[v] = struct{}{}
	for _, e := range h.ents {
		v.ents[e.ID] = e.record()
	}
	v.head = [][]byte{helloFrame(), encodeMaterials(h.mats.all()), encodeEntityTypes(h.types)}
	if h.atlasDoc != nil {
		v.head = append(v.head, atlasFrame())
	}
	if h.entDoc != nil {
		v.head = append(v.head, modelsFrame())
	}
	looks := map[uint64]entityLook{}
	for _, e := range h.ents {
		if !e.Look.zero() {
			looks[e.ID] = e.Look
		}
	}
	if len(looks) > 0 {
		v.head = append(v.head, encodeEntityData(looks))
	}
	names := map[uint64]string{}
	for _, e := range h.ents {
		if e.Name != "" {
			names[e.ID] = e.Name
		}
	}
	if len(names) > 0 {
		v.head = append(v.head, encodeNames(names))
	}
	worn := map[uint64]armourSlots{}
	for _, e := range h.ents {
		if e.Skin != nil {
			v.head = append(v.head, encodeSkin(e.ID, *e.Skin))
		}
		if !e.Armor.zero() {
			worn[e.ID] = e.Armor
		}
	}
	if h.ownSkin != nil {
		v.head = append(v.head, encodeSkin(0, *h.ownSkin))
	}
	if !h.ownArmor.zero() {
		worn[0] = h.ownArmor
	}
	if len(worn) > 0 {
		v.head = append(v.head, encodeArmour(worn))
	}
	// Read under the hub lock so a later publishSelf cannot be overwritten by
	// this older pose.
	if h.self != nil {
		if me := h.self.Snapshot(); me.PositionKnown {
			p := poseOf(me)
			v.self = &p
			v.setPoseLocked(p)
		}
	}
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
// it. It never waits on a viewer. It requests nothing: the world store asks
// for sub-chunks, and the hub only draws the replies. w is kept so callers
// need not change.
func (h *Hub) Apply(pk packet.Packet, _ PacketWriter) {
	switch p := pk.(type) {
	case *packet.LevelChunk:
		h.levelChunk(p)
	case *packet.SubChunk:
		h.subChunk(p)
	case *packet.UpdateBlock:
		if p.Layer == 0 {
			h.updateBlocks(h.blockEdits([]blockPlacement{{p.Position, p.NewBlockRuntimeID}}))
		}
	case *packet.UpdateSubChunkBlocks:
		placed := make([]blockPlacement, len(p.Blocks))
		for i, b := range p.Blocks {
			placed[i] = blockPlacement{b.BlockPos, b.BlockRuntimeID}
		}
		h.updateBlocks(h.blockEdits(placed))
	case *packet.CorrectPlayerMovePrediction:
		h.publishSelf()
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
		h.setEntityLocked(&entity{ID: p.EntityRuntimeID, Type: p.EntityType, X: p.Position[0], Y: p.Position[1], Z: p.Position[2], Yaw: p.Yaw, Pitch: p.Pitch, HeadYaw: p.HeadYaw, Look: lookOf(p.EntityMetadata, entityLook{})})
		h.mu.Unlock()
	case *packet.AddItemActor:
		h.mu.Lock()
		h.uniqueID[p.EntityUniqueID] = p.EntityRuntimeID
		h.setEntityLocked(&entity{ID: p.EntityRuntimeID, Type: "minecraft:item", X: p.Position[0], Y: p.Position[1], Z: p.Position[2],
			Look: entityLook{Item: h.itemName[p.Item.Stack.NetworkID]}})
		h.mu.Unlock()
	case *packet.AddPlayer:
		h.mu.Lock()
		h.uniqueID[p.AbilityData.EntityUniqueID] = p.EntityRuntimeID
		h.setEntityLocked(&entity{ID: p.EntityRuntimeID, Type: "player", Name: p.Username, UUID: p.UUID, Skin: h.skins[p.UUID],
			X: p.Position[0], Y: p.Position[1], Z: p.Position[2], Yaw: p.Yaw, Pitch: p.Pitch, HeadYaw: p.HeadYaw})
		h.mu.Unlock()
	case *packet.PlayerList:
		for _, e := range p.Entries {
			switch e.ActionType {
			case protocol.PlayerListActionAdd:
				h.setSkin(e.UUID, e.Skin)
			case protocol.PlayerListActionRemove:
				h.forgetSkin(e.UUID)
			}
		}
	case *packet.PlayerSkin:
		h.setSkin(p.UUID, p.Skin)
	case *packet.MobArmourEquipment:
		h.setArmour(p.EntityRuntimeID, p.Helmet, p.Chestplate, p.Leggings, p.Boots)
	case *packet.InventoryContent:
		if p.WindowID == protocol.WindowIDArmour {
			h.setOwnArmour(p.Content)
		}
	case *packet.InventorySlot:
		if p.WindowID == protocol.WindowIDArmour {
			h.setOwnArmourSlot(int(p.Slot), p.NewItem)
		}
	case *packet.SetActorData:
		h.setLook(p.EntityRuntimeID, p.EntityMetadata)
	case *packet.MovePlayer:
		h.movePlayer(p)
	case *packet.MoveActorAbsolute:
		h.moveEntity(p.EntityRuntimeID, func(e *entity) {
			e.X, e.Y, e.Z, e.Pitch, e.Yaw, e.HeadYaw = p.Position[0], p.Position[1], p.Position[2], p.Rotation[0], p.Rotation[1], p.Rotation[2]
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
			if v, ok := p.RotationYHead.Value(); ok {
				e.HeadYaw = v
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

// PublishSelf pushes belief's current pose to the viewers; the unit calls it
// after each input it sends, which the server does not answer.
func (h *Hub) PublishSelf() { h.publishSelf() }

func (h *Hub) movePlayer(p *packet.MovePlayer) {
	h.mu.Lock()
	own := h.runtime != 0 && p.EntityRuntimeID == h.runtime
	h.mu.Unlock()
	if own {
		h.publishSelf()
		return
	}
	h.moveEntity(p.EntityRuntimeID, func(e *entity) {
		e.X, e.Y, e.Z, e.Yaw, e.Pitch, e.HeadYaw = p.Position[0], p.Position[1]-playerEyeHeight, p.Position[2], p.Yaw, p.Pitch, p.HeadYaw
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

// SetItems gives the hub the server's item table, which names dropped items.
func (h *Hub) SetItems(entries []protocol.ItemEntry) {
	names := make(map[int32]string, len(entries))
	for _, e := range entries {
		names[int32(e.RuntimeID)] = strings.TrimPrefix(e.Name, "minecraft:")
	}
	h.mu.Lock()
	h.itemName = names
	h.mu.Unlock()
}

// metaInt reads an integer metadata value; the protocol carries them in
// several widths.
func metaInt(m protocol.EntityMetadata, key uint32) (int64, bool) {
	switch v := m[key].(type) {
	case int64:
		return v, true
	case int32:
		return int64(v), true
	case int16:
		return int64(v), true
	case byte:
		return int64(v), true
	}
	return 0, false
}

// metaFlag reads one flag; indices from 64 up live in the second flags word.
func metaFlag(m protocol.EntityMetadata, index int) bool {
	key := uint32(protocol.EntityDataKeyFlags)
	if index >= 64 {
		key, index = protocol.EntityDataKeyFlagsTwo, index-64
	}
	v, _ := metaInt(m, key)
	return v>>uint(index)&1 != 0
}

// lookOf applies what metadata says over prev. A packet that carries no
// flags or colour leaves them as they were, because SetActorData sends only
// what changed.
func lookOf(m protocol.EntityMetadata, prev entityLook) entityLook {
	l := prev
	_, hasFlags := m[protocol.EntityDataKeyFlags]
	if _, ok := m[protocol.EntityDataKeyFlagsTwo]; ok || hasFlags {
		l.Flags = 0
		if metaFlag(m, protocol.EntityDataFlagBaby) {
			l.Flags |= entBaby
		}
		if metaFlag(m, protocol.EntityDataFlagSheared) {
			l.Flags |= entSheared
		}
	}
	if c, ok := metaInt(m, protocol.EntityDataKeyColorIndex); ok {
		l.Color = byte(c) & 15
	}
	return l
}

func (h *Hub) setLook(id uint64, m protocol.EntityMetadata) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.ents[id]
	if !ok {
		return
	}
	if look := lookOf(m, e.Look); look != e.Look {
		e.Look = look
		h.pushOrderedLocked(encodeEntityData(map[uint64]entityLook{id: look}))
	}
}

func (h *Hub) setEntityLocked(e *entity) {
	if h.runtime != 0 && e.ID == h.runtime {
		return
	}
	e.TypeID = h.typeIDLocked(e.Type)
	h.ents[e.ID] = e
	if e.Name != "" {
		h.pushOrderedLocked(encodeNames(map[uint64]string{e.ID: e.Name}))
	}
	if !e.Look.zero() {
		h.pushOrderedLocked(encodeEntityData(map[uint64]entityLook{e.ID: e.Look}))
	}
	if e.Skin != nil {
		h.pushOrderedLocked(encodeSkin(e.ID, *e.Skin))
	}
	h.pushEntityLocked(e)
}

func (h *Hub) pushEntityLocked(e *entity) {
	rec := e.record()
	for v := range h.viewers {
		v.setEntity(e.ID, rec)
	}
}

func (e *entity) record() []byte {
	return entityRecord(e.ID, e.TypeID, e.X, e.Y, e.Z, e.Yaw, e.Pitch, e.HeadYaw)
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

func (h *Hub) levelChunk(p *packet.LevelChunk) {
	if p.Dimension != 0 {
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

type blockPlacement struct {
	pos   protocol.BlockPos
	netID uint32
}

// blockEdits resolves names outside the hub lock; fresh is true if any
// material is new.
func (h *Hub) blockEdits(placed []blockPlacement) (edits []blockEdit, fresh bool) {
	edits = make([]blockEdit, len(placed))
	for i, p := range placed {
		id, f := h.blockMaterial(p.netID)
		edits[i], fresh = blockEdit{p.pos, id}, fresh || f
	}
	return edits, fresh
}

func (h *Hub) blockMaterial(netID uint32) (id uint8, fresh bool) {
	var name string
	var ok bool
	if r, shaped := h.dec.(interface {
		RenderName(uint32) (string, bool)
	}); shaped {
		name, ok = r.RenderName(netID)
	} else {
		name, ok = h.dec.BlockName(netID)
	}
	if !ok {
		h.unmapped.Add(1)
		name = unmappedBlock
	}
	return h.mats.id(name)
}

type blockEdit struct {
	pos protocol.BlockPos
	id  uint8
}

func (h *Hub) updateBlocks(edits []blockEdit, fresh bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pushMaterialsLocked(fresh)

	type group struct {
		cells [subChunkCells]uint8
		held  bool
		n     int
	}
	groups := map[[3]int32]*group{}
	for _, e := range edits {
		key := [3]int32{e.pos.X() >> 4, e.pos.Y() >> 4, e.pos.Z() >> 4}
		g, ok := groups[key]
		if !ok {
			g = &group{}
			if frame, held := h.subs[key]; held {
				_, _, _, g.cells, _ = decodeSubChunk(frame)
				g.held = true
			}
			groups[key] = g
		}
		g.cells[(e.pos.Y()&15<<4|e.pos.Z()&15)<<4|e.pos.X()&15] = e.id
		g.n++
	}
	for key, g := range groups {
		old, held := h.subs[key]
		frame := encodeSubChunk(key[0], key[1], key[2], &g.cells)
		if held && string(old) == string(frame) {
			continue
		}
		if !held && !slices.ContainsFunc(g.cells[:], func(c uint8) bool { return c != 0 }) {
			continue
		}
		h.subs[key] = frame
		if !held || g.n > maxEditFrames {
			h.broadcastLocked(func(v *Viewer) bool { return v.noteSub(key, frame, true) })
			continue
		}
		for _, e := range edits {
			if e.pos.X()>>4 == key[0] && e.pos.Y()>>4 == key[1] && e.pos.Z()>>4 == key[2] {
				m := encodeBlock(e.pos.X(), e.pos.Y(), e.pos.Z(), e.id)
				h.broadcastLocked(func(v *Viewer) bool { return v.noteEdit(key, m) })
			}
		}
	}
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
	h.broadcastLocked(func(v *Viewer) bool { return v.noteSub(key, frame, anyBlock) })
}

func (h *Hub) pushMaterialsLocked(fresh bool) {
	if fresh {
		h.pushOrderedLocked(encodeMaterials(h.mats.all()))
	}
}

func (h *Hub) pushOrderedLocked(m []byte) {
	h.broadcastLocked(func(v *Viewer) bool { return v.push(m) })
}
