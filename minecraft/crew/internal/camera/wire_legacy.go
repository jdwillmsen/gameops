package camera

import (
	"encoding/binary"
	"errors"
)

// legacyMaxMaterial is the largest id a version 3 page can hold.
const legacyMaxMaterial = 255

// v3Downgrader rewrites the hub's version 4 frames as version 3 for a page
// that was loaded before the upgrade and so did not ask for 4. It keeps what a
// version 3 page needs and a version 4 stream no longer repeats: the whole
// material table, because a version 3 materials frame replaces the page's.
// Every block with an id past 255 is drawn as material 255, as it was before
// ids were widened. One downgrader serves one connection.
type v3Downgrader struct {
	// entries[id-1] is a material's bytes from alpha on, nil until seen.
	entries [legacyMaxMaterial][]byte
}

// convert returns the version 3 form of f. A frame whose layout did not change
// is returned as is. A nil result means there is nothing to send.
func (d *v3Downgrader) convert(f []byte) ([]byte, error) {
	if len(f) == 0 {
		return f, nil
	}
	switch f[0] {
	case frameHello:
		return []byte{frameHello, legacyWireVersion}, nil
	case frameMaterials:
		return d.materials(f)
	case frameSubChunk:
		cx, cy, cz, cells, err := decodeSubChunk(f)
		if err != nil {
			return nil, err
		}
		var narrow [subChunkCells]uint8
		for i, c := range cells {
			narrow[i] = clampLegacy(c)
		}
		return encodeSubChunkV3(cx, cy, cz, &narrow), nil
	case frameBlock:
		if len(f) != 15 {
			return nil, errors.New("camera: bad block frame")
		}
		g := make([]byte, 14)
		copy(g, f[:13])
		g[13] = clampLegacy(binary.LittleEndian.Uint16(f[13:]))
		return g, nil
	}
	return f, nil
}

func clampLegacy(id uint16) uint8 { return uint8(min(id, legacyMaxMaterial)) }

func (d *v3Downgrader) materials(f []byte) ([]byte, error) {
	if len(f) < 3 {
		return nil, errors.New("camera: bad materials frame")
	}
	n, p := int(binary.LittleEndian.Uint16(f[1:])), 3
	for i := 0; i < n; i++ {
		if len(f) < p+8 {
			return nil, errors.New("camera: truncated materials frame")
		}
		id, end := int(binary.LittleEndian.Uint16(f[p:])), p+2+6+int(f[p+7])
		if len(f) < end {
			return nil, errors.New("camera: truncated materials frame")
		}
		if id >= 1 && id <= legacyMaxMaterial {
			d.entries[id-1] = f[p+2 : end]
		}
		p = end
	}
	var body []byte
	count := 0
	for i, e := range d.entries {
		if e == nil {
			continue
		}
		body = append(body, byte(i+1))
		body = append(body, e...)
		count++
	}
	return append(binary.LittleEndian.AppendUint16([]byte{frameMaterials}, uint16(count)), body...), nil
}

// encodeSubChunkV3 is the version 3 sub-chunk layout: one byte per palette
// entry, so at most 256 materials.
func encodeSubChunkV3(cx, cy, cz int32, c *[subChunkCells]uint8) []byte {
	var slot [legacyMaxMaterial + 1]int16
	for i := range slot {
		slot[i] = -1
	}
	palette := make([]uint8, 0, 8)
	for _, m := range c {
		if slot[m] < 0 {
			slot[m] = int16(len(palette))
			palette = append(palette, m)
		}
	}
	w := indexBits(len(palette))
	f := make([]byte, subHeaderSize+2, subHeaderSize+2+len(palette)+subChunkCells*w/8)
	f[0] = frameSubChunk
	binary.LittleEndian.PutUint32(f[1:], uint32(cx))
	binary.LittleEndian.PutUint32(f[5:], uint32(cy))
	binary.LittleEndian.PutUint32(f[9:], uint32(cz))
	binary.LittleEndian.PutUint16(f[13:], uint16(len(palette)))
	f = append(f, palette...)
	if w == 0 {
		return f
	}
	f = append(f, make([]byte, subChunkCells*w/8)...)
	body := f[len(f)-subChunkCells*w/8:]
	for i, m := range c {
		v, at := uint(slot[m]), i*w
		for b := 0; b < w; b++ {
			if v>>b&1 != 0 {
				body[(at+b)>>3] |= 1 << ((at + b) & 7)
			}
		}
	}
	return f
}
