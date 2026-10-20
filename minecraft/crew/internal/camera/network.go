package camera

import (
	"fmt"
	"sync"

	// Imported for the block states it registers; the hash table is empty
	// without them.
	_ "github.com/df-mc/dragonfly/server/block"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/subchunk"
)

const (
	// Overworld range: the only dimension the camera draws.
	minY, maxY = -64, 319
	// The bottom as a sub-chunk index: the legacy chunk path numbers its
	// sections from here.
	minSubY = minY >> 4
)

var (
	finalizeRegistry sync.Once

	metricUnmapped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "crew_camera_unmapped_block_hashes_total",
		Help: "Block hashes the server sent that crew's block table does not know, by server version. A rise after a server update means the table has drifted and the camera is drawing placeholders.",
	}, []string{"server_version"})
)

// NetworkDecoder decodes world packets with crew's own sub-chunk decoder and
// resolves the block hashes through dragonfly's table. The table tracks a
// server version, so a newer server shows unknown blocks as placeholders;
// this type counts them so that drift is noticed.
type NetworkDecoder struct {
	warn func(serverVersion string)

	mu      sync.Mutex
	version string
	warned  bool

	// states remembers what a hash resolved to: the registry builds a fresh
	// property map on every call, and the planner reads a hash per cell.
	statesMu sync.RWMutex
	states   map[uint32]blockState
}

type blockState struct {
	name  string
	props map[string]any
}

// NewNetworkDecoder returns a decoder. warn, if set, is called at most once
// per session, on the first unmapped hash; it must not be given positions.
func NewNetworkDecoder(warn func(serverVersion string)) *NetworkDecoder {
	finalizeRegistry.Do(world.DefaultBlockRegistry.Finalize)
	return &NetworkDecoder{warn: warn, version: "unknown", states: map[uint32]blockState{}}
}

// BeginSession labels the unmapped-hash counter with the server version and
// re-arms the once-per-session warning.
func (d *NetworkDecoder) BeginSession(serverVersion string) {
	if serverVersion == "" {
		serverVersion = "unknown"
	}
	d.mu.Lock()
	d.version, d.warned = serverVersion, false
	d.mu.Unlock()
	// Creates the series at zero so the metric exists before any drift.
	metricUnmapped.WithLabelValues(serverVersion)
}

func (d *NetworkDecoder) SubChunk(payload []byte) (Section, error) {
	sc, err := subchunk.Decode(payload)
	if err != nil {
		return Section{}, err
	}
	return d.section(sc), nil
}

// Chunk reads the legacy LevelChunk layout: count sub-chunks back to back,
// then biomes, which the camera ignores.
func (d *NetworkDecoder) Chunk(payload []byte, count int) ([]PlacedSection, error) {
	var out []PlacedSection
	rest := payload
	for i := 0; i < count; i++ {
		sc, err := subchunk.Decode(rest)
		if err != nil {
			return nil, fmt.Errorf("section %d: %w", i, err)
		}
		// Each decode reports what followed it as its trailer; that is the
		// next section.
		rest = sc.Trailer
		if len(sc.Layers) == 0 {
			continue
		}
		out = append(out, PlacedSection{Y: int32(minSubY + i), Section: d.section(sc)})
	}
	return out, nil
}

func (d *NetworkDecoder) BlockName(networkID uint32) (string, bool) {
	name, _, ok := d.BlockState(networkID)
	return name, ok
}

// BlockState is BlockName with the block's states. The returned map is shared
// between calls and must not be changed.
func (d *NetworkDecoder) BlockState(networkID uint32) (string, map[string]any, bool) {
	d.statesMu.RLock()
	st, hit := d.states[networkID]
	d.statesMu.RUnlock()
	if hit {
		return st.name, st.props, true
	}
	reg := world.DefaultBlockRegistry
	if rid, ok := reg.HashToRuntimeID(networkID); ok {
		if name, props, ok := reg.RuntimeIDToState(rid); ok {
			d.statesMu.Lock()
			d.states[networkID] = blockState{name, props}
			d.statesMu.Unlock()
			return name, props, true
		}
	}
	d.noteUnmapped()
	return "", nil, false
}

// section reads the first layer; layer 1 only holds waterlogging.
func (d *NetworkDecoder) section(sc subchunk.SubChunk) Section {
	if len(sc.Layers) == 0 {
		return Section{Palette: []string{"air"}}
	}
	l := &sc.Layers[0]
	s := Section{Palette: make([]string, len(l.Palette))}
	for i, hash := range l.Palette {
		name, ok := d.BlockName(hash)
		if !ok {
			name = unmappedBlock
		}
		s.Palette[i] = name
	}
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				s.Cells[(y*16+z)*16+x] = l.Indices[x<<8|z<<4|y]
			}
		}
	}
	return s
}

func (d *NetworkDecoder) noteUnmapped() {
	d.mu.Lock()
	version, first := d.version, !d.warned
	d.warned = true
	d.mu.Unlock()
	metricUnmapped.WithLabelValues(version).Inc()
	if first && d.warn != nil {
		d.warn(version)
	}
}
