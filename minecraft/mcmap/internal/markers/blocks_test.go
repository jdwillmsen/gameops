package markers

import (
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/internal/chunks"
)

type placed struct {
	x, y, z int
	name    string
}

// slice encodes one sixteen-block-tall slice of a chunk the way the server
// writes it: air everywhere but at the blocks given, at the narrowest
// width that holds the palette.
func slice(t testing.TB, version byte, blocks ...placed) []byte {
	t.Helper()
	palette := []string{"minecraft:air"}
	indexes := make([]int, blocksPerSubChunk)
	for _, b := range blocks {
		at := b.x<<8 | b.z<<4 | b.y
		found := -1
		for i, name := range palette {
			if name == b.name {
				found = i
			}
		}
		if found < 0 {
			found = len(palette)
			palette = append(palette, b.name)
		}
		indexes[at] = found
	}
	bits := 1
	for _, width := range []int{1, 2, 3, 4, 5, 6, 8, 16} {
		if bits = width; 1<<width >= len(palette) {
			break
		}
	}
	out := []byte{version, 1}
	if version == 9 {
		out = append(out, 0)
	}
	out = append(out, byte(bits<<1))
	perWord := 32 / bits
	for start := 0; start < blocksPerSubChunk; start += perWord {
		var word uint32
		for i := 0; i < perWord && start+i < blocksPerSubChunk; i++ {
			word |= uint32(indexes[start+i]) << (i * bits)
		}
		out = binary.LittleEndian.AppendUint32(out, word)
	}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(palette)))
	for _, name := range palette {
		out = append(out, record(t, map[string]any{"name": name, "states": map[string]any{"facing_direction": int32(2)}, "version": int32(18168865)})...)
	}
	return out
}

// subChunk is the record holding the slice at height index y of a chunk.
func subChunk(dim, cx, cz int32, y int8, body []byte) entry {
	k := binary.LittleEndian.AppendUint32(nil, uint32(cx))
	k = binary.LittleEndian.AppendUint32(k, uint32(cz))
	if dim != 0 {
		k = binary.LittleEndian.AppendUint32(k, uint32(dim))
	}
	return entry{append(k, tagSubChunk, byte(y)), body}
}

func TestBlockIn_ReadsTheNameAtAPosition(t *testing.T) {
	// Enough kinds of block to need each width a palette is written at.
	for _, kinds := range []int{1, 3, 7, 12, 20, 40, 100, 300} {
		var blocks []placed
		for i := range kinds {
			blocks = append(blocks, placed{i % 16, i / 16 % 16, i / 256, "minecraft:filler_" + string(rune('a'+i%26)) + string(rune('a'+i/26))})
		}
		blocks = append(blocks, placed{15, 15, 15, "minecraft:purple_shulker_box"}, placed{0, 1, 2, "minecraft:trapped_chest"})
		for _, version := range []byte{8, 9} {
			sub := slice(t, version, blocks...)
			for _, b := range blocks {
				got, ok := blockIn(sub, b.x, b.y, b.z)
				if !ok || "minecraft:"+got != b.name {
					t.Fatalf("version %d, %d kinds: block at %d,%d,%d = %q, %v; want %q", version, kinds, b.x, b.y, b.z, got, ok, b.name)
				}
			}
			if got, ok := blockIn(sub, 9, 9, 9); !ok || got != "air" {
				t.Errorf("version %d, %d kinds: an empty position = %q, %v", version, kinds, got, ok)
			}
		}
	}
}

func TestBlockIn_ReadsASliceOfOneBlock(t *testing.T) {
	// No width, no indexes and no count: only the one palette entry.
	sub := append([]byte{9, 1, 4, 0}, record(t, map[string]any{"name": "minecraft:stone"})...)
	if got, ok := blockIn(sub, 3, 4, 5); !ok || got != "stone" {
		t.Errorf("block = %q, %v", got, ok)
	}
}

// A slice is as much the world's data as a block entity is. None of it may
// read past the record, whatever it claims.
func TestBlockIn_RefusesWhatDoesNotAddUp(t *testing.T) {
	whole := slice(t, 9, placed{1, 2, 3, "minecraft:red_shulker_box"})
	for cut := range whole {
		if got, ok := blockIn(whole[:cut], 1, 2, 3); ok {
			t.Fatalf("a slice cut to %d of %d bytes named a block: %q", cut, len(whole), got)
		}
	}
	for name, sub := range map[string][]byte{
		"an unknown version":      append([]byte{7}, whole[1:]...),
		"no layers":               {9, 0, 0},
		"indexes for the network": append([]byte{9, 1, 0, whole[3] | 1}, whole[4:]...),
		"a width no word holds":   append([]byte{9, 1, 0, 33 << 1}, whole[4:]...),
		"an index past the palette": func() []byte {
			out := append([]byte{}, whole...)
			// The palette's count, after the header and 128 words at
			// one bit a block.
			binary.LittleEndian.PutUint32(out[4+128*4:], 1)
			return out
		}(),
	} {
		if got, ok := blockIn(sub, 1, 2, 3); ok {
			t.Errorf("%s named a block: %q", name, got)
		}
	}
}

func TestScan_ReadsWhatOnlyTheBlockSays(t *testing.T) {
	w, _ := scanOf(t,
		blockEntities(t, 0, 0, 0,
			chestAt(1, 64, 1, nil), chestAt(2, 64, 1, nil),
			map[string]any{"id": "ShulkerBox", "x": int32(3), "y": int32(-40), "z": int32(1), "Items": stack, "facing": uint8(1)},
			map[string]any{"id": "ShulkerBox", "x": int32(4), "y": int32(64), "z": int32(1), "Items": stack},
			map[string]any{"id": "Barrel", "x": int32(5), "y": int32(64), "z": int32(1), "Items": stack},
			// In a slice the world does not hold.
			map[string]any{"id": "ShulkerBox", "x": int32(6), "y": int32(200), "z": int32(1), "Items": stack},
			bedAt(8, 64, 8, 9), bedAt(8, 64, 9, 9),
			// A colour the game has no dye for.
			bedAt(10, 64, 8, 77),
			map[string]any{"id": "Bed", "x": int32(12), "y": int32(64), "z": int32(8)},
		),
		subChunk(0, 0, 0, 4, slice(t, 9,
			placed{1, 0, 1, "minecraft:chest"}, placed{2, 0, 1, "minecraft:trapped_chest"},
			placed{4, 0, 1, "minecraft:undyed_shulker_box"}, placed{5, 0, 1, "minecraft:barrel"})),
		// Below zero: the height index is signed.
		subChunk(0, 0, 0, -3, slice(t, 9, placed{3, 8, 1, "minecraft:light_gray_shulker_box"})),
		// The same position in another dimension is another block.
		subChunk(1, 0, 0, 4, slice(t, 9, placed{2, 0, 1, "minecraft:chest"}, placed{4, 0, 1, "minecraft:red_shulker_box"})),
	)
	ow := w[chunks.Overworld]
	if want := []Marker{
		{X: 1, Y: 64, Z: 1, Kind: "chest"},
		{X: 2, Y: 64, Z: 1, Kind: "chest", Trapped: true},
		{X: 3, Y: -40, Z: 1, Kind: "shulker", Colour: "light_gray"},
		{X: 4, Y: 64, Z: 1, Kind: "shulker", Colour: Undyed},
		{X: 5, Y: 64, Z: 1, Kind: "barrel"},
		{X: 6, Y: 200, Z: 1, Kind: "shulker"},
	}; !reflect.DeepEqual(ow.Containers, want) {
		t.Errorf("containers = %+v\nwant         %+v", ow.Containers, want)
	}
	if want := []Marker{{X: 8, Y: 64, Z: 8, Colour: "cyan"}, {X: 10, Y: 64, Z: 8}, {X: 12, Y: 64, Z: 8}}; !reflect.DeepEqual(ow.Beds, want) {
		t.Errorf("beds = %+v, want %+v", ow.Beds, want)
	}
}

func TestScan_SaysWhichNamedMobsAreBabies(t *testing.T) {
	at := []any{float32(1), float32(64), float32(1)}
	w, _ := scanOf(t,
		actor(t, 1, map[string]any{"identifier": "minecraft:sheep", "CustomName": "jeb_", "Pos": at, "IsBaby": uint8(1), "Age": int32(-24000)}),
		actor(t, 2, map[string]any{"identifier": "minecraft:cat", "CustomName": "OJ", "Pos": at, "IsBaby": uint8(0)}),
		// Not every actor's record says either way.
		actor(t, 3, map[string]any{"identifier": "minecraft:armor_stand", "CustomName": "Stan", "Pos": at}),
		digp(0, 0, 0, 1, 2, 3),
	)
	babies := map[string]bool{}
	for _, m := range w[chunks.Overworld].Mobs {
		babies[m.Name] = m.Baby
	}
	if want := map[string]bool{"jeb_": true, "OJ": false, "Stan": false}; !reflect.DeepEqual(babies, want) {
		t.Errorf("babies = %v, want %v", babies, want)
	}
}
