// Package camera is the rendered-camera spike: it turns the world packets a
// crew unit already receives into a compact stream a browser can draw. It is
// spike code, not production: there is no authentication and no back-pressure
// beyond dropping a viewer that cannot keep up.
package camera

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/df-mc/dragonfly/server/block" // registers block states for chunk decoding
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	// Overworld range. The spike only supports the overworld.
	minY, maxY = -64, 319
)

// Binary frame types. Text frames are JSON.
const frameSubChunk byte = 1

type PacketWriter interface{ WritePacket(packet.Packet) error }

type entity struct {
	ID    uint64  `json:"id"`
	Type  string  `json:"type"`
	X     float32 `json:"x"`
	Y     float32 `json:"y"`
	Z     float32 `json:"z"`
	Yaw   float32 `json:"yaw"`
	Pitch float32 `json:"pitch"`
}

type self struct {
	T     string  `json:"t"`
	X     float32 `json:"x"`
	Y     float32 `json:"y"`
	Z     float32 `json:"z"`
	Yaw   float32 `json:"yaw"`
	Pitch float32 `json:"pitch"`
}

type Hub struct {
	mats      *materialTable
	rangeOver cube.Range

	mu       sync.Mutex
	runtime  uint64
	dim      int32
	me       self
	subs     map[[3]int32][]byte // encoded binary frames, replayed to new viewers
	ents     map[uint64]*entity
	uniqueID map[int64]uint64
	clients  map[*client]struct{}

	// Counters for the spike's measurements.
	RawBytes                                                           atomic.Uint64 // payload bytes handed to viewers
	SubChunks                                                          atomic.Uint64
	DecodeNs                                                           atomic.Uint64
	DecodeFail                                                         atomic.Uint64
	Unmapped                                                           atomic.Uint64
	LastErr                                                            atomic.Value
	deflated                                                           atomic.Uint64
	LevelChunks, RequestedSubs, SubPackets, SubOK, SubAllAir, SubOther atomic.Uint64
}

func NewHub() *Hub {
	world.DefaultBlockRegistry.Finalize()
	return &Hub{
		mats:      newMaterialTable(),
		rangeOver: cube.Range{minY, maxY},
		subs:      map[[3]int32][]byte{},
		ents:      map[uint64]*entity{},
		uniqueID:  map[int64]uint64{},
		clients:   map[*client]struct{}{},
		me:        self{T: "self"},
	}
}

// Spawn resets the hub for a new session.
func (h *Hub) Spawn(runtimeID uint64, pos mgl32.Vec3, pitch, yaw float32, dim int32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runtime, h.dim = runtimeID, dim
	h.subs = map[[3]int32][]byte{}
	h.ents = map[uint64]*entity{}
	h.uniqueID = map[int64]uint64{}
	h.me = self{T: "self", X: pos[0], Y: pos[1], Z: pos[2], Yaw: yaw, Pitch: pitch}
	h.broadcastJSONLocked(map[string]any{"t": "reset"})
	h.broadcastJSONLocked(h.me)
}

func (h *Hub) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs = map[[3]int32][]byte{}
	h.ents = map[uint64]*entity{}
	h.broadcastJSONLocked(map[string]any{"t": "reset"})
}

// Apply is called for every packet the session reads. w is used to request
// sub-chunks when the server runs the sub-chunk request system.
func (h *Hub) Apply(pk packet.Packet, w PacketWriter) {
	switch pk.(type) {
	case *packet.LevelChunk, *packet.SubChunk:
		start := time.Now()
		defer func() { h.DecodeNs.Add(uint64(time.Since(start))) }()
	}
	switch p := pk.(type) {
	case *packet.LevelChunk:
		h.levelChunk(p, w)
	case *packet.SubChunk:
		h.subChunk(p)
	case *packet.UpdateBlock:
		h.updateBlock(p.Position, p.NewBlockRuntimeID, p.Layer)
	case *packet.UpdateSubChunkBlocks:
		for _, b := range p.Blocks {
			h.updateBlock(b.BlockPos, b.BlockRuntimeID, 0)
		}
	case *packet.Respawn:
		// The spawn position in GameData is a placeholder (Y is offset by
		// 32768); the real one arrives here.
		if p.State == packet.RespawnStateReadyToSpawn {
			h.mu.Lock()
			h.me.X, h.me.Y, h.me.Z = p.Position[0], p.Position[1], p.Position[2]
			h.broadcastJSONLocked(h.me)
			h.mu.Unlock()
		}
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
		h.mu.Lock()
		if p.EntityRuntimeID == h.runtime {
			h.me.X, h.me.Y, h.me.Z, h.me.Yaw, h.me.Pitch = p.Position[0], p.Position[1], p.Position[2], p.Yaw, p.Pitch
			h.broadcastJSONLocked(h.me)
		} else if e, ok := h.ents[p.EntityRuntimeID]; ok {
			e.X, e.Y, e.Z, e.Yaw, e.Pitch = p.Position[0], p.Position[1], p.Position[2], p.Yaw, p.Pitch
			h.broadcastEntityLocked(e)
		}
		h.mu.Unlock()
	case *packet.MoveActorAbsolute:
		h.mu.Lock()
		if e, ok := h.ents[p.EntityRuntimeID]; ok {
			e.X, e.Y, e.Z, e.Pitch, e.Yaw = p.Position[0], p.Position[1], p.Position[2], p.Rotation[0], p.Rotation[1]
			h.broadcastEntityLocked(e)
		}
		h.mu.Unlock()
	case *packet.MoveActorDelta:
		h.mu.Lock()
		if e, ok := h.ents[p.EntityRuntimeID]; ok {
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
			h.broadcastEntityLocked(e)
		}
		h.mu.Unlock()
	case *packet.RemoveActor:
		h.mu.Lock()
		if rid, ok := h.uniqueID[p.EntityUniqueID]; ok {
			delete(h.ents, rid)
			delete(h.uniqueID, p.EntityUniqueID)
			h.broadcastJSONLocked(map[string]any{"t": "rm", "id": rid})
		}
		h.mu.Unlock()
	}
}

func (h *Hub) setEntityLocked(e *entity) {
	if e.ID == h.runtime {
		return
	}
	h.ents[e.ID] = e
	h.broadcastEntityLocked(e)
}

func (h *Hub) broadcastEntityLocked(e *entity) {
	h.broadcastJSONLocked(struct {
		T string `json:"t"`
		*entity
	}{"ent", e})
}

func (h *Hub) levelChunk(p *packet.LevelChunk, w PacketWriter) {
	h.LevelChunks.Add(1)
	if p.Dimension != 0 {
		return
	}
	limit, requestMode := p.SubChunkLimit.Value()
	switch {
	case requestMode:
		// This protocol revision signals the sub-chunk request system with a
		// zero count plus a limit, instead of sentinel counts.
		top := int8(maxY >> 4)
		if limit >= 0 && int8(limit)+int8(minY>>4)-1 < top {
			top = int8(limit) + int8(minY>>4) - 1
		}
		// Offsets are relative to a base of sub-chunk Y 0, so -4 is the
		// bedrock layer.
		var offs []protocol.SubChunkOffset
		for y := int8(minY >> 4); y <= top; y++ {
			offs = append(offs, protocol.SubChunkOffset{0, y, 0})
		}
		h.RequestedSubs.Add(uint64(len(offs)))
		_ = w.WritePacket(&packet.SubChunkRequest{Dimension: p.Dimension, Position: protocol.SubChunkPos{p.Position[0], 0, p.Position[1]}, Offsets: offs})
	default:
		if p.CacheEnabled {
			return // not negotiated by crew
		}
		c, err := h.decodeFull(p.RawPayload, int(p.SubChunkCount))
		if err != nil {
			h.DecodeFail.Add(1)
			return
		}
		for i, sub := range c.Sub() {
			if sub == nil || sub.Empty() {
				continue
			}
			h.emit(p.Position[0], int32(c.SubY(int16(i))), p.Position[1], c, sub)
		}
	}
}

func (h *Hub) decodeFull(data []byte, count int) (*chunk.Chunk, error) {
	return chunk.NetworkDecode(world.DefaultBlockRegistry, data, count, h.rangeOver)
}

func (h *Hub) subChunk(p *packet.SubChunk) {
	if p.Dimension != 0 {
		return
	}
	h.SubPackets.Add(1)
	for _, e := range p.SubChunkEntries {
		switch e.Result {
		case protocol.SubChunkResultSuccess:
			h.SubOK.Add(1)
		case protocol.SubChunkResultSuccessAllAir:
			h.SubAllAir.Add(1)
		default:
			h.SubOther.Add(1)
		}
		raw, ok := e.RawPayload.Value()
		if e.Result != protocol.SubChunkResultSuccess || !ok || len(raw) == 0 {
			continue
		}
		// dragonfly decodes sub-chunks only inside a whole chunk, so wrap the
		// payload with a minimal biome section: one palette entry, then 23
		// "same as previous" markers.
		buf := bytes.NewBuffer(append([]byte(nil), raw...))
		buf.Write([]byte{0x01, 0x00})
		buf.Write(bytes.Repeat([]byte{0xff}, 23))
		c, err := chunk.NetworkDecodeBuffer(world.DefaultBlockRegistry, buf, 1, h.rangeOver)
		if err != nil {
			h.DecodeFail.Add(1)
			h.LastErr.Store(err.Error())
			continue
		}
		cy := p.Position[1] + int32(e.Offset[1])
		idx := int(cy) - minY>>4
		if idx < 0 || idx >= len(c.Sub()) || c.Sub()[idx] == nil {
			continue
		}
		h.emit(p.Position[0]+int32(e.Offset[0]), cy, p.Position[2]+int32(e.Offset[2]), c, c.Sub()[idx])
	}
}

func (h *Hub) updateBlock(pos protocol.BlockPos, netID, layer uint32) {
	// Single-block updates are re-sent as a 1-block patch.
	name := h.blockName(netID)
	if layer != 0 {
		return
	}
	id, fresh := h.mats.id(name)
	h.mu.Lock()
	defer h.mu.Unlock()
	if fresh {
		h.broadcastMaterialsLocked()
	}
	b := make([]byte, 0, 14)
	b = append(b, frameBlock)
	b = binary.LittleEndian.AppendUint32(b, uint32(pos.X()))
	b = binary.LittleEndian.AppendUint32(b, uint32(pos.Y()))
	b = binary.LittleEndian.AppendUint32(b, uint32(pos.Z()))
	b = append(b, id)
	h.broadcastBinaryLocked(b)
}

const frameBlock byte = 2

// blockName resolves a network block ID. The server sends hashes of the block
// state, so they go through dragonfly's table first; a state newer than that
// table is drawn as a placeholder instead of vanishing.
func (h *Hub) blockName(netID uint32) string {
	rid, ok := world.DefaultBlockRegistry.HashToRuntimeID(netID)
	if !ok {
		h.Unmapped.Add(1)
		return "unmapped"
	}
	name, _, ok := world.DefaultBlockRegistry.RuntimeIDToState(rid)
	if !ok {
		return "unmapped"
	}
	return name
}

// emit converts one decoded sub-chunk to the wire format: a 4096-byte array of
// material IDs indexed (y*16+z)*16+x.
func (h *Hub) emit(cx, cy, cz int32, c *chunk.Chunk, sub *chunk.SubChunk) {
	frame := make([]byte, 13+4096)
	frame[0] = frameSubChunk
	binary.LittleEndian.PutUint32(frame[1:], uint32(cx))
	binary.LittleEndian.PutUint32(frame[5:], uint32(cy))
	binary.LittleEndian.PutUint32(frame[9:], uint32(cz))
	body := frame[13:]
	cache := map[uint32]uint8{}
	anyBlock, newMat := false, false
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				rid := sub.Block(uint8(x), uint8(y), uint8(z), 0)
				m, seen := cache[rid]
				if !seen {
					var fresh bool
					m, fresh = h.mats.id(h.blockName(rid))
					newMat = newMat || fresh
					cache[rid] = m
				}
				if m != 0 {
					anyBlock = true
				}
				body[(y*16+z)*16+x] = m
			}
		}
	}
	if !anyBlock {
		return
	}
	h.SubChunks.Add(1)
	h.mu.Lock()
	defer h.mu.Unlock()
	if newMat {
		h.broadcastMaterialsLocked()
	}
	h.subs[[3]int32{cx, cy, cz}] = frame
	h.broadcastBinaryLocked(frame)
}

func (h *Hub) broadcastMaterialsLocked() {
	h.broadcastJSONLocked(map[string]any{"t": "materials", "list": h.mats.all()})
}

func (h *Hub) broadcastJSONLocked(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	for c := range h.clients {
		c.send(msg{text: true, data: b})
	}
}

func (h *Hub) broadcastBinaryLocked(b []byte) {
	for c := range h.clients {
		c.send(msg{data: b})
	}
}
