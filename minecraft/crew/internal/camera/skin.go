package camera

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"slices"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// A skin on the wire is a PNG in the 64-wide player layout, at any whole
// multiple of it. Persona pieces and custom geometry are not drawn: a skin
// that does not fit the standard layout is left to the default model.
const (
	skinBase    = 64
	maxSkinSide = 512

	skinSlim byte = 1
)

// skinInfo is one player's skin, ready for a viewer.
type skinInfo struct {
	png  []byte
	slim bool
}

// skinOf converts a skin as the server sends it. ok is false for one that is
// missing, the wrong size or not in the standard layout, which draws with the
// model's default texture.
func skinOf(s protocol.Skin) (skinInfo, bool) {
	return skinFromRGBA(int(s.SkinImageWidth), int(s.SkinImageHeight), s.SkinData, strings.Contains(string(s.SkinResourcePatch), "customSlim"))
}

func skinFromRGBA(w, h int, rgba []byte, slim bool) (skinInfo, bool) {
	if w < skinBase || w > maxSkinSide || w%skinBase != 0 || (h != w && h != w/2) || len(rgba) != w*h*4 {
		return skinInfo{}, false
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, w))
	copy(img.Pix, rgba)
	if h != w {
		legacyLimbs(img)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return skinInfo{}, false
	}
	return skinInfo{png: b.Bytes(), slim: slim}, true
}

// legacyLimbs fills the left arm and leg of a skin that only has the right
// ones, as the game does for a 64x32 skin: each face of the right limb is
// copied across, turned left to right.
func legacyLimbs(img *image.NRGBA) {
	k := img.Bounds().Dx() / skinBase
	// srcX, srcY, dstX, dstY, w, h in the 64-wide grid.
	for _, r := range [][6]int{
		{4, 16, 20, 48, 4, 4}, {8, 16, 24, 48, 4, 4}, {0, 20, 24, 52, 4, 12}, {4, 20, 20, 52, 4, 12}, {8, 20, 16, 52, 4, 12}, {12, 20, 28, 52, 4, 12},
		{44, 16, 36, 48, 4, 4}, {48, 16, 40, 48, 4, 4}, {40, 20, 40, 52, 4, 12}, {44, 20, 36, 52, 4, 12}, {48, 20, 32, 52, 4, 12}, {52, 20, 44, 52, 4, 12},
	} {
		sx, sy, dx, dy, w, h := r[0]*k, r[1]*k, r[2]*k, r[3]*k, r[4]*k, r[5]*k
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				s := img.PixOffset(sx+w-1-x, sy+y)
				d := img.PixOffset(dx+x, dy+y)
				copy(img.Pix[d:d+4], img.Pix[s:s+4])
			}
		}
	}
}

// encodeSkin is the skin frame: [id uvarint][flags u8][len uvarint][png]. An
// id of zero is the unit's own body.
func encodeSkin(id uint64, s skinInfo) []byte {
	f := binary.AppendUvarint([]byte{frameSkin}, id)
	var flags byte
	if s.slim {
		flags |= skinSlim
	}
	f = append(f, flags)
	f = binary.AppendUvarint(f, uint64(len(s.png)))
	return append(f, s.png...)
}

// armourSlots is what an entity wears, helmet first.
type armourSlots [4]string

func (a armourSlots) zero() bool { return a == armourSlots{} }

// encodeArmour is the armour frame: [n uvarint]{id uvarint, 4 x (len u8, item
// name)}. An id of zero is the unit's own body.
func encodeArmour(worn map[uint64]armourSlots) []byte {
	f := binary.AppendUvarint([]byte{frameArmour}, uint64(len(worn)))
	ids := make([]uint64, 0, len(worn))
	for id := range worn {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		f = binary.AppendUvarint(f, id)
		for _, n := range worn[id] {
			if len(n) > maxNameLen {
				n = ""
			}
			f = append(f, uint8(len(n)))
			f = append(f, n...)
		}
	}
	return f
}
