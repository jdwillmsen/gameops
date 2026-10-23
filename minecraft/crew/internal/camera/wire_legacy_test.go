package camera

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// decodeSubChunkV3 is the version 3 reader a page loaded before the upgrade
// still runs: palette entries are bytes.
func decodeSubChunkV3(t *testing.T, f []byte) (palette []uint8, cells [subChunkCells]uint8) {
	t.Helper()
	if f[0] != frameSubChunk {
		t.Fatalf("frame type %d", f[0])
	}
	n := int(binary.LittleEndian.Uint16(f[13:]))
	w := indexBits(n)
	if len(f) != subHeaderSize+2+n+subChunkCells*w/8 {
		t.Fatalf("v3 frame is %d bytes for a palette of %d", len(f), n)
	}
	palette, body := f[subHeaderSize+2:subHeaderSize+2+n], f[subHeaderSize+2+n:]
	for i := range cells {
		var v int
		for b := 0; b < w; b++ {
			at := i*w + b
			v |= int(body[at>>3]>>(at&7)&1) << b
		}
		cells[i] = palette[v]
	}
	return palette, cells
}

type v3Material struct {
	id   uint8
	name string
}

func parseMaterialsV3(t *testing.T, f []byte) (out []v3Material) {
	t.Helper()
	if f[0] != frameMaterials {
		t.Fatalf("frame type %d", f[0])
	}
	n, p := int(binary.LittleEndian.Uint16(f[1:])), 3
	for i := 0; i < n; i++ {
		id, l := f[p], int(f[p+6])
		out = append(out, v3Material{id, string(f[p+7 : p+7+l])})
		p += 7 + l
	}
	if p != len(f) {
		t.Fatalf("materials frame has %d trailing bytes", len(f)-p)
	}
	return out
}

func materialsFrame(first, count int, prefix string) []byte {
	var list []material
	for i := first; i < first+count; i++ {
		list = append(list, material{ID: uint16(i), Name: prefix + string(rune('a'+i%26)), Color: "#102030", Alpha: 1})
	}
	return encodeMaterials(list)
}

func TestDowngradeAnswersHelloWithVersion3(t *testing.T) {
	got, err := (&v3Downgrader{}).convert(helloFrame())
	if err != nil || !bytes.Equal(got, []byte{frameHello, 3}) {
		t.Errorf("hello = % x, %v", got, err)
	}
}

func TestDowngradeSubChunkClampsWideIdsAndRepacksThePalette(t *testing.T) {
	ids := []uint16{0, 1, 200, 255, 256, 3000, 65535}
	cells := cellsFrom(func(i int) uint16 { return ids[i%len(ids)] })
	got, err := (&v3Downgrader{}).convert(encodeSubChunk(4, -1, 9, cells))
	if err != nil {
		t.Fatal(err)
	}
	palette, narrow := decodeSubChunkV3(t, got)
	if len(palette) != 4 {
		t.Errorf("palette = %v, want 0 1 200 255 once each", palette)
	}
	want := []uint8{0, 1, 200, 255, 255, 255, 255}
	for i, c := range narrow {
		if c != want[i%len(want)] {
			t.Fatalf("cell %d = %d, want %d", i, c, want[i%len(want)])
		}
	}
	if x, y, z := int32(binary.LittleEndian.Uint32(got[1:])), int32(binary.LittleEndian.Uint32(got[5:])), int32(binary.LittleEndian.Uint32(got[9:])); x != 4 || y != -1 || z != 9 {
		t.Errorf("position = %d %d %d", x, y, z)
	}
}

func TestDowngradeBlockEditClampsTheMaterial(t *testing.T) {
	got, err := (&v3Downgrader{}).convert(encodeBlock(-1, 64, 5, 700))
	want := []byte{frameBlock, 255, 255, 255, 255, 64, 0, 0, 0, 5, 0, 0, 0, 255}
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("block = % x, %v; want % x", got, err, want)
	}
	got, _ = (&v3Downgrader{}).convert(encodeBlock(0, 0, 0, 12))
	if got[13] != 12 || len(got) != 14 {
		t.Errorf("small id changed: % x", got)
	}
}

// A version 3 materials frame replaces the page's table, so each additive
// frame has to come out as the whole table so far.
func TestDowngradeMaterialsAccumulateAndDropIdsAPageCannotHold(t *testing.T) {
	d := &v3Downgrader{}
	first, err := d.convert(materialsFrame(1, 2, "m"))
	if err != nil {
		t.Fatal(err)
	}
	if got := parseMaterialsV3(t, first); len(got) != 2 || got[0] != (v3Material{1, "mb"}) || got[1] != (v3Material{2, "mc"}) {
		t.Errorf("first = %v", got)
	}
	second, err := d.convert(materialsFrame(254, 5, "w"))
	if err != nil {
		t.Fatal(err)
	}
	got := parseMaterialsV3(t, second)
	if len(got) != 4 || got[0].id != 1 || got[1].id != 2 || got[2].id != 254 || got[3].id != 255 {
		t.Errorf("second = %v, want ids 1 2 254 255 and nothing wider", got)
	}
	// The same id sent again replaces it rather than listing it twice.
	third, _ := d.convert(materialsFrame(2, 1, "z"))
	if got := parseMaterialsV3(t, third); len(got) != 4 || got[1] != (v3Material{2, "zc"}) {
		t.Errorf("third = %v", got)
	}
}

func TestDowngradeLeavesOtherFramesAlone(t *testing.T) {
	tick := encodeTick(nil, nil)
	got, err := (&v3Downgrader{}).convert(tick)
	if err != nil || !bytes.Equal(got, tick) {
		t.Errorf("tick = % x, %v", got, err)
	}
}

func TestDowngradeRejectsFramesItCannotRead(t *testing.T) {
	good := encodeSubChunk(0, 0, 0, cellsFrom(func(i int) uint16 { return uint16(i % 3) }))
	mats := materialsFrame(1, 2, "m")
	for name, f := range map[string][]byte{
		"truncated sub-chunk": good[:len(good)-1],
		"short block":         encodeBlock(0, 0, 0, 1)[:14],
		"short materials":     mats[:2],
		"cut materials":       mats[:len(mats)-1],
	} {
		if _, err := (&v3Downgrader{}).convert(f); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
