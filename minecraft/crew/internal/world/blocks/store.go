// Package blocks keeps the blocks a crew unit has been told about. It is the
// only place that asks the server for sub-chunks, so skills see the world
// whether or not the camera is running.
package blocks

import (
	"strings"
	"sync"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/subchunk"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Overworld range: the only dimension the store keeps.
const minY, maxY = -64, 319

// airHash is the network hash of air, which materialises an all-air section
// when a block is placed in it. It is a hash of the block's name and states,
// so it does not move between server versions.
const airHash uint32 = 3690217760

// Names resolves a network block hash. ok is false for a hash the table does
// not know, which the store reports as an unknown block.
type Names interface {
	BlockName(hash uint32) (name string, ok bool)
}

// PacketWriter is the part of the connection the store needs.
type PacketWriter interface{ WritePacket(packet.Packet) error }

// section is one sub-chunk's layer 0. A nil pointer is a section of air, which
// the server often sends without a payload.
type section struct{ hashes *[subchunk.Size]uint32 }

type Store struct {
	names Names
	limit int

	mu       sync.RWMutex
	subs     map[[3]int32]section
	anchor   [2]int32 // chunk of the latest LevelChunk
	anchored bool
}

func New(names Names, maxSubChunks int) *Store {
	return &Store{names: names, limit: maxSubChunks, subs: map[[3]int32]section{}}
}

// Apply feeds the store a packet the session read. Only LevelChunk writes.
func (s *Store) Apply(pk packet.Packet, w PacketWriter) {
	switch p := pk.(type) {
	case *packet.LevelChunk:
		if p.Dimension != 0 {
			return
		}
		s.mu.Lock()
		s.anchor, s.anchored = p.Position, true
		s.mu.Unlock()
		if req := SubChunkRequest(p); req != nil {
			_ = w.WritePacket(req)
		}
	case *packet.SubChunk:
		s.subChunk(p)
	case *packet.UpdateBlock:
		if p.Layer == 0 {
			s.set(p.Position, p.NewBlockRuntimeID)
		}
	case *packet.UpdateSubChunkBlocks:
		for _, b := range p.Blocks {
			s.set(b.BlockPos, b.BlockRuntimeID)
		}
	case *packet.ChangeDimension:
		s.Reset()
	}
}

// Reset forgets every block, for the end of a session or a dimension change.
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = map[[3]int32]section{}
	s.anchored = false
}

// Block returns the block's name without its namespace. known is false for a
// block in a sub-chunk the unit has not received and for a hash the name table
// lacks, so callers can treat both as solid.
func (s *Store) Block(pos protocol.BlockPos) (name string, known bool) {
	s.mu.RLock()
	sec, held := s.subs[[3]int32{pos.X() >> 4, pos.Y() >> 4, pos.Z() >> 4}]
	var hash uint32
	if held && sec.hashes != nil {
		hash = sec.hashes[index(pos.X()&15, pos.Y()&15, pos.Z()&15)]
	}
	s.mu.RUnlock()
	if !held {
		return "", false
	}
	if sec.hashes == nil {
		return "air", true
	}
	n, ok := s.names.BlockName(hash)
	if !ok {
		return "", false
	}
	return strings.TrimPrefix(n, "minecraft:"), true
}

// index matches subchunk.Layer.Block, which Bedrock orders x, then z, then y.
func index(x, y, z int32) int { return int(x<<8 | z<<4 | y) }

func (s *Store) subChunk(p *packet.SubChunk) {
	if p.Dimension != 0 {
		return
	}
	for _, e := range p.SubChunkEntries {
		key := [3]int32{p.Position[0] + int32(e.Offset[0]), p.Position[1] + int32(e.Offset[1]), p.Position[2] + int32(e.Offset[2])}
		switch e.Result {
		case protocol.SubChunkResultSuccessAllAir:
			s.put(key, section{})
		case protocol.SubChunkResultSuccess:
			raw, ok := e.RawPayload.Value()
			if !ok || len(raw) == 0 {
				continue
			}
			sc, err := subchunk.Decode(raw)
			if err != nil {
				continue
			}
			if len(sc.Layers) == 0 {
				s.put(key, section{})
				continue
			}
			l := &sc.Layers[0]
			var h [subchunk.Size]uint32
			for x := 0; x < 16; x++ {
				for z := 0; z < 16; z++ {
					for y := 0; y < 16; y++ {
						h[index(int32(x), int32(y), int32(z))] = l.Block(x, y, z)
					}
				}
			}
			s.put(key, section{&h})
		}
	}
}

func (s *Store) put(key [3]int32, sec section) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs[key] = sec
	for len(s.subs) > s.limit {
		s.evictFarthestLocked()
	}
}

// evictFarthestLocked measures from the latest LevelChunk, the area the unit
// is being told about, so the unit's surroundings outlive distant leftovers.
func (s *Store) evictFarthestLocked() {
	var worst [3]int32
	var worstD int64 = -1
	for k := range s.subs {
		var d int64
		if s.anchored {
			dx, dz := int64(k[0]-s.anchor[0]), int64(k[2]-s.anchor[1])
			d = dx*dx + dz*dz
		}
		if d > worstD {
			worst, worstD = k, d
		}
	}
	delete(s.subs, worst)
}

func (s *Store) set(pos protocol.BlockPos, hash uint32) {
	key := [3]int32{pos.X() >> 4, pos.Y() >> 4, pos.Z() >> 4}
	s.mu.Lock()
	defer s.mu.Unlock()
	sec, held := s.subs[key]
	if !held {
		return
	}
	if sec.hashes == nil {
		sec.hashes = new([subchunk.Size]uint32)
		for i := range sec.hashes {
			sec.hashes[i] = airHash
		}
		s.subs[key] = sec
	}
	sec.hashes[index(pos.X()&15, pos.Y()&15, pos.Z()&15)] = hash
}

// SubChunkRequest builds the request for a chunk the server announced in
// request mode: the whole column from the world floor to the server's limit.
func SubChunkRequest(p *packet.LevelChunk) *packet.SubChunkRequest {
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
