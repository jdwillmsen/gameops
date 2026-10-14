package server

import (
	"strconv"
	"sync"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/heights"
)

// surfaceCacheMax bounds the answers kept with surfaces on them: a
// dimension's markers and a few days', each written out once per reading of
// the world.
const surfaceCacheMax = 24

// surfaceCache holds answers that were written out again with the surface
// under each marker, by what they were written from.
type surfaceCache struct {
	mu   sync.Mutex
	held map[string][]byte
}

func (c *surfaceCache) get(key string, make func() ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	body, ok := c.held[key]
	c.mu.Unlock()
	if ok {
		return body, nil
	}
	body, err := make()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.held == nil || len(c.held) >= surfaceCacheMax {
		c.held = map[string][]byte{}
	}
	c.held[key] = body
	return body, nil
}

// surfaceReading is the surface heights of the last reading of the world
// and a tag that changes with it, or nil and a tag that says there are
// none. The tag is what a kept answer is told apart from a newer one by.
func (s *Server) surfaceReading() (*heights.Map, string) {
	if s.Biomes == nil {
		return nil, ""
	}
	h := s.Biomes.World().Heights()
	if h == nil {
		return nil, "0"
	}
	return h, strconv.FormatInt(h.SnapshotAt.UnixNano(), 36)
}

// surfaceOf is the surface under a column of the dimension, for the
// markers and structures that are served with it.
func surfaceOf(h *heights.Map, d chunks.Dimension) func(x, z int32) (int32, bool) {
	return func(x, z int32) (int32, bool) { return h.Surface(d, x, z) }
}
