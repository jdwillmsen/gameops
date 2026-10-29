// Package place is the placement table: for each block a unit can place, the
// click that produces each state, learned from the server by cmd/placecal.
// A planner asks it which click gives the wanted state instead of guessing
// from the block's name.
package place

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// FormatVersion is bumped when the file layout changes.
const FormatVersion = 1

// Recipe is one click that places a block in a state. The support block is
// the one clicked; the block lands in the cell the clicked face points at.
type Recipe struct {
	// Support is where the clicked block lies relative to the placed cell:
	// bottom, top, north, south, east or west.
	Support string `json:"support"`
	// Face is the face of the support that is clicked, named like Support
	// seen from the support: top, bottom, north, south, east or west.
	Face string `json:"face"`
	// Hit is the point clicked on that face, as a fraction of the support
	// block along each axis (x east, y up, z south).
	Hit [3]float32 `json:"hit"`
	// Yaw is the unit's look direction in degrees (0 south, 90 west, 180
	// north, 270 east), Pitch its look up or down in degrees (negative up).
	Yaw   int `json:"yaw"`
	Pitch int `json:"pitch"`
	// Sneak says the unit is sneaking for the click.
	Sneak bool `json:"sneak,omitempty"`
	// Soil is the block the support must be when it is not the default
	// (stone or dirt): sand for a cactus, farmland for a carrot.
	Soil string `json:"soil,omitempty"`
	// Stand says where the unit must be for the click, when the facing
	// follows its position, not its look: "above" is an eye at least three
	// cells over the placed cell and within one cell of it horizontally,
	// "below" is a placed cell higher than the eye, within one cell
	// horizontally. Empty means anywhere in reach.
	Stand string `json:"stand,omitempty"`
}

// Cost orders recipes by how much a unit has to do: the support it must
// find, an off-centre hit, a sneak, a tilted look. A turn costs almost
// nothing, so it only breaks ties.
func (r Recipe) Cost() float64 {
	var c float64
	switch r.Face {
	case "top":
	case "bottom":
		c += 2
	default:
		c++
	}
	if r.Hit != [3]float32{0.5, 0.5, 0.5} && !centred(r) {
		c++
	}
	if r.Sneak {
		c += 2
	}
	if r.Stand != "" {
		c += 2
	}
	if r.Pitch != 0 {
		c++
	}
	if r.Yaw != 0 {
		c += 0.1
	}
	return c
}

// centred says the hit is the middle of the clicked face, whatever the axis
// the face fixes.
func centred(r Recipe) bool {
	h := r.Hit
	switch r.Face {
	case "top", "bottom":
		return h[0] == 0.5 && h[2] == 0.5
	case "north", "south":
		return h[0] == 0.5 && h[1] == 0.5
	default:
		return h[1] == 0.5 && h[2] == 0.5
	}
}

// Entry is one state a block was seen in and the cheapest click that gave it.
// The state omits the block's derived keys.
type Entry struct {
	State  map[string]any `json:"state"`
	Recipe Recipe         `json:"recipe"`
	// Item is the item to hold when it differs from the block's Item.
	Item string `json:"item,omitempty"`
}

// Block is everything calibration learned about one block.
type Block struct {
	// Item is the item name the unit holds to place it.
	Item string `json:"item"`
	// States are the states some click gave, ordered by State.
	States []Entry `json:"states"`
	// Unreachable lists the states the server's vocabulary has for the block
	// that no click in the matrix gave. Derived keys are left out.
	Unreachable []map[string]any `json:"unreachable,omitempty"`
	// NotSeen lists the states the calibration did not get to try: the
	// block's sweep was cut at the click cap, so they are unknown, not
	// proven unreachable.
	NotSeen []map[string]any `json:"not_seen,omitempty"`
	// Truncated says the sweep was cut at the click cap; see NotSeen.
	Truncated bool `json:"truncated,omitempty"`
	// Derived names the state keys that follow the neighbours or the other
	// half of the block, not the click.
	Derived []string `json:"derived,omitempty"`
	// Mixed names state keys that also follow neighbours but took more than
	// one value across clicks on the same face, so they are not derived: a
	// matcher must compare them.
	Mixed []string `json:"mixed,omitempty"`
	// Inferred says the sweep was taken from a sibling block with the same
	// responses and confirmed on a sample, not run in full.
	Inferred bool `json:"inferred,omitempty"`
}

// Matrix is the click matrix a table was built from, so a reader knows what
// "unreachable" means.
type Matrix struct {
	Faces   []string `json:"faces"`
	Yaws    []int    `json:"yaws"`
	Pitches []int    `json:"pitches"`
	Sneak   []bool   `json:"sneak"`
	Hits    []string `json:"hits"`
	Notes   []string `json:"notes,omitempty"`
}

// Header says what the table was built against and how far it got.
type Header struct {
	Format        int      `json:"format"`
	ServerVersion string   `json:"server_version"`
	Image         string   `json:"image,omitempty"`
	Protocol      int32    `json:"protocol"`
	Vocabulary    string   `json:"vocabulary"`
	Matrix        Matrix   `json:"matrix"`
	Partial       bool     `json:"partial,omitempty"`
	NotCovered    []string `json:"not_covered,omitempty"`
	// Unplaceable lists items no click in the matrix placed as a block.
	Unplaceable []string `json:"unplaceable,omitempty"`
	// FamilyGaps lists blocks whose reachable states still differ from the
	// majority of their family after the gap check re-ran them in full, each
	// with the difference. Such a block is "unreachable" with evidence, not
	// by a dropped click.
	FamilyGaps []string `json:"family_gaps,omitempty"`
	// UnmappedItems names the items whose placement produced a block hash the
	// vocabulary has no name for.
	UnmappedItems []string `json:"unmapped_items,omitempty"`
	// Unmapped counts block hashes the server answered that the vocabulary
	// has no name for.
	Unmapped int `json:"unmapped,omitempty"`
}

// Table is the whole calibration result.
type Table struct {
	Header Header            `json:"header"`
	Blocks map[string]*Block `json:"blocks"`
}

// Read decodes a table from gzip JSON.
func Read(r io.Reader) (*Table, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("place table: %w", err)
	}
	defer zr.Close()
	var t Table
	if err := json.NewDecoder(zr).Decode(&t); err != nil {
		return nil, fmt.Errorf("place table: %w", err)
	}
	// The decoder stops at the end of the JSON value, before the gzip
	// trailer; reading on to EOF is what checks the CRC and length.
	if _, err := io.Copy(io.Discard, zr); err != nil {
		return nil, fmt.Errorf("place table: %w", err)
	}
	if t.Header.Format != FormatVersion {
		return nil, fmt.Errorf("place table: format %d, want %d", t.Header.Format, FormatVersion)
	}
	return &t, nil
}

// Write encodes the table as gzip JSON. Map keys are sorted by the encoder,
// and the gzip header carries no time, so equal tables give equal bytes.
func (t *Table) Write(w io.Writer) error {
	zw, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(zw)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(t); err != nil {
		return err
	}
	return zw.Close()
}

// Lookup returns the cheapest recipe that gives the block the wanted states.
// want may name only some of the block's states; the rest are free. Derived
// keys in want are ignored, because no click decides them.
func (t *Table) Lookup(block string, want map[string]any) (Recipe, bool) {
	b := t.Blocks[strings.TrimPrefix(block, "minecraft:")]
	if b == nil {
		return Recipe{}, false
	}
	skip := map[string]bool{}
	for _, k := range b.Derived {
		skip[k] = true
	}
	var best Recipe
	found := false
	for _, e := range b.States {
		if !covers(e.State, want, skip) {
			continue
		}
		if !found || e.Recipe.Cost() < best.Cost() {
			best, found = e.Recipe, true
		}
	}
	return best, found
}

// Derived names the state keys of the block that follow its neighbours.
func (t *Table) Derived(block string) []string {
	if b := t.Blocks[strings.TrimPrefix(block, "minecraft:")]; b != nil {
		return append([]string(nil), b.Derived...)
	}
	return nil
}

// Unreachable says whether the table knows the block and has seen no click
// give the wanted states, as opposed to not knowing the block at all.
func (t *Table) Unreachable(block string, want map[string]any) bool {
	b := t.Blocks[strings.TrimPrefix(block, "minecraft:")]
	if b == nil {
		return false
	}
	_, ok := t.Lookup(block, want)
	return !ok
}

func covers(have, want map[string]any, skip map[string]bool) bool {
	for k, w := range want {
		if skip[k] {
			continue
		}
		h, ok := have[k]
		if !ok || !SameValue(h, w) {
			return false
		}
	}
	return true
}

// SameValue compares a state value read from the wire, a table file or a
// blueprint, whose integers and flags may not share a Go type.
func SameValue(a, b any) bool {
	na, nb := norm(a), norm(b)
	return na == nb
}

func norm(v any) string {
	switch x := v.(type) {
	case bool:
		// Bedrock stores a flag as a byte; the registry types the same flag
		// as a bool on one block and an integer on its sibling.
		if x {
			return "i:1"
		}
		return "i:0"
	case string:
		return "s:" + x
	case float32:
		return normFloat(float64(x))
	case float64:
		return normFloat(x)
	case int:
		return "i:" + strconv.FormatInt(int64(x), 10)
	case int8:
		return "i:" + strconv.FormatInt(int64(x), 10)
	case int16:
		return "i:" + strconv.FormatInt(int64(x), 10)
	case int32:
		return "i:" + strconv.FormatInt(int64(x), 10)
	case int64:
		return "i:" + strconv.FormatInt(x, 10)
	case uint8:
		return "i:" + strconv.FormatUint(uint64(x), 10)
	case uint16:
		return "i:" + strconv.FormatUint(uint64(x), 10)
	case uint32:
		return "i:" + strconv.FormatUint(uint64(x), 10)
	case uint64:
		return "i:" + strconv.FormatUint(x, 10)
	}
	return fmt.Sprintf("?:%v", v)
}

func normFloat(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1<<53 {
		return "i:" + strconv.FormatInt(int64(f), 10)
	}
	return "f:" + strconv.FormatFloat(f, 'g', -1, 64)
}

// Key is a canonical text for a state, equal for equal states whatever the Go
// types of their values, with the skipped keys left out.
func Key(state map[string]any, skip map[string]bool) string {
	keys := make([]string, 0, len(state))
	for k := range state {
		if !skip[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(norm(state[k]))
		sb.WriteByte(';')
	}
	return sb.String()
}
