package markers

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

// record encodes one compound the way the server writes it.
func record(t testing.TB, m map[string]any) []byte {
	t.Helper()
	b, err := nbt.MarshalEncoding(m, nbt.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFields_HandsOutTheTopLevelTagsOfEachRecord(t *testing.T) {
	chest := map[string]any{
		"id": "Chest", "x": int32(-12), "y": int32(64), "z": int32(300), "pairlead": uint8(1),
		"Items": []any{
			map[string]any{"Name": "minecraft:shulker_box", "Count": uint8(1), "tag": map[string]any{
				"Items": []any{map[string]any{"Name": "minecraft:dirt", "Count": uint8(64)}},
			}},
			map[string]any{"Name": "minecraft:stone", "Count": uint8(3)},
		},
		"Longs": []int64{1, 2, 3}, "Ints": []int32{4, 5}, "Bytes": []byte{6}, "D": 1.5, "S": int16(-2), "L": int64(9),
	}
	actor := map[string]any{"identifier": "minecraft:cat", "Pos": []any{float32(1.5), float32(-2.25), float32(3)}, "Empty": []any{}}
	b := append(record(t, chest), record(t, actor)...)

	got := map[string]any{}
	visit := func(name []byte, tag byte, payload []byte) {
		if v, ok := intOf(tag, payload); ok {
			got[string(name)] = v
		} else if s, ok := stringOf(tag, payload); ok {
			got[string(name)] = s
		} else if x, y, z, ok := floatsOf(tag, payload); ok {
			got[string(name)] = [3]float64{x, y, z}
		} else if tag == tagList {
			got[string(name)] = listLen(tag, payload)
		}
	}
	rest, err := fields(b, visit)
	if err != nil {
		t.Fatal(err)
	}
	if got["id"] != "Chest" || got["x"] != int32(-12) || got["z"] != int32(300) || got["pairlead"] != int32(1) || got["Items"] != 2 || got["S"] != int32(-2) {
		t.Errorf("chest = %v", got)
	}
	clear(got)
	rest, err = fields(rest, visit)
	if err != nil || len(rest) != 0 {
		t.Fatalf("second record: rest %d bytes, %v", len(rest), err)
	}
	if got["identifier"] != "minecraft:cat" || got["Pos"] != [3]float64{1.5, -2.25, 3} || got["Empty"] != 0 {
		t.Errorf("actor = %v", got)
	}
}

// What a damaged table or a hostile save could hold. None of it may read
// past the record, loop for longer than the record is, or recurse without
// end.
func TestFields_RefusesWhatDoesNotAddUp(t *testing.T) {
	whole := record(t, map[string]any{"id": "Bed", "x": int32(1), "Items": []any{map[string]any{"a": "b"}}})
	for cut := range whole {
		if _, err := fields(whole[:cut], func([]byte, byte, []byte) {}); err == nil {
			t.Errorf("a record cut to %d of %d bytes was accepted", cut, len(whole))
		}
	}

	list := func(elem byte, n int32, body ...byte) []byte {
		b := []byte{tagCompound, 0, 0, tagList, 1, 0, 'l', elem}
		b = binary.LittleEndian.AppendUint32(b, uint32(n))
		return append(append(b, body...), tagEnd)
	}
	deep := []byte{tagCompound, 0, 0}
	for range 100 {
		deep = append(deep, tagCompound, 1, 0, 'c')
	}
	deep = append(deep, bytes.Repeat([]byte{tagEnd}, 101)...)
	for name, b := range map[string][]byte{
		"empty":                            nil,
		"not a compound":                   {tagString, 0, 0, 1, 0, 'x'},
		"a list of two billion ints":       list(tagInt, 1<<31-1),
		"a list of two billion end tags":   list(tagEnd, 1<<31-1),
		"a list with a negative length":    list(tagByte, -1),
		"a list of two billion compounds":  list(tagCompound, 1<<31-1, tagEnd),
		"an array longer than the record":  {tagCompound, 0, 0, tagLongArray, 1, 0, 'a', 0xff, 0xff, 0xff, 0x7f, tagEnd},
		"a string longer than the record":  {tagCompound, 0, 0, tagString, 1, 0, 's', 0xff, 0xff, 'x', tagEnd},
		"a name longer than the record":    {tagCompound, 0, 0, tagByte, 0xff, 0xff, 'n', 1, tagEnd},
		"an unknown tag type":              {tagCompound, 0, 0, 13, 1, 0, 'u', 1, tagEnd},
		"nested deeper than anything real": deep,
	} {
		if _, err := fields(b, func([]byte, byte, []byte) {}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
