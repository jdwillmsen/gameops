package camera

import (
	"encoding/binary"
	"slices"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/world/subchunk"
)

const timeStopped byte = 1

func encodeTime(ticks int32, stopped bool) []byte {
	f := binary.LittleEndian.AppendUint32([]byte{frameTime}, uint32(ticks))
	if stopped {
		return append(f, timeStopped)
	}
	return append(f, 0)
}

// biomeTint is a biome's colour for each tinted block kind.
type biomeTint struct {
	ID                    uint16
	Grass, Foliage, Water [3]uint8
	WaterAlpha            uint8
}

func encodeBiomeTable(tints []biomeTint) []byte {
	f := binary.LittleEndian.AppendUint16([]byte{frameBiomeTable}, uint16(len(tints)))
	for _, t := range tints {
		f = binary.LittleEndian.AppendUint16(f, t.ID)
		f = append(f, t.Grass[:]...)
		f = append(f, t.Foliage[:]...)
		f = append(f, t.Water[:]...)
		f = append(f, t.WaterAlpha)
	}
	return f
}

func encodeBiomes(cx, cz int32, b *subchunk.ColumnBiomes) []byte {
	var palette []uint16
	for _, id := range b {
		if !slices.Contains(palette, id) {
			palette = append(palette, id)
		}
	}
	f := binary.LittleEndian.AppendUint32([]byte{frameBiomes}, uint32(cx))
	f = binary.LittleEndian.AppendUint32(f, uint32(cz))
	f = binary.LittleEndian.AppendUint16(f, uint16(len(palette)))
	for _, id := range palette {
		f = binary.LittleEndian.AppendUint16(f, id)
	}
	if len(palette) > 1 {
		for _, id := range b {
			f = append(f, byte(slices.Index(palette, id)))
		}
	}
	return f
}
