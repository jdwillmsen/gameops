package camera

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/png"
	"slices"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// A skin on the wire is an RGBA image in the 64-wide player layout, at any
// whole multiple of it. Persona pieces arrive as separate assets the server
// does not composite for us, and custom geometry has its own UVs: neither is
// drawn, and a skin that does not fit the standard layout is left to the
// default model.
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

// Why a skin was or was not drawn, as counted by crew_camera_skins_total.
const (
	skinAccepted        = "accepted"
	skinAcceptedCustom  = "accepted_custom_geometry"
	skinAcceptedPersona = "accepted_persona"
	skinNoData          = "rejected_no_data"
	skinBadSize         = "rejected_bad_size"
	skinBadLength       = "rejected_bad_length"
	skinTransparent     = "rejected_transparent"
	skinEncodeFailed    = "rejected_encode"
	skinCapEvicted      = "cap_evicted"
	skinCapDropped      = "cap_dropped"
	skinNoOverride      = "update_without_override"
)

var metricSkins = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "crew_camera_skins_total",
	Help: "Skins the camera received from the server, by whether and why they were drawn.",
}, []string{"result"})

// skinOf converts a skin as the server sends it. ok is false for one that is
// missing, the wrong size or not in the standard layout, which draws with the
// model's default texture.
func skinOf(s protocol.Skin) (skinInfo, bool) {
	info, _ := decodeSkin(s)
	return info, info.png != nil
}

// decodeSkin is skinOf with the reason: an accepted* result, or a rejected_*.
// Accepted skins with custom geometry or persona flags are still drawn on the
// standard model, since most keep the standard UV layout in their base image.
func decodeSkin(s protocol.Skin) (skinInfo, string) {
	info, reason := convertSkin(int(s.SkinImageWidth), int(s.SkinImageHeight), s.SkinData, skinIsSlim(s))
	if info.png == nil {
		return skinInfo{}, reason
	}
	switch {
	case s.PersonaSkin:
		reason = skinAcceptedPersona
	case !standardGeometry(s.SkinResourcePatch):
		reason = skinAcceptedCustom
	}
	return info, reason
}

// geometryName is the default geometry the resource patch names, or "".
func geometryName(patch []byte) string {
	var p struct {
		Geometry struct {
			Default string `json:"default"`
		} `json:"geometry"`
	}
	if json.Unmarshal(patch, &p) != nil {
		return ""
	}
	return p.Geometry.Default
}

// standardGeometry is true when the patch names no geometry or one of the
// stock humanoid models, whose UV layout the camera model matches.
func standardGeometry(patch []byte) bool {
	n := geometryName(patch)
	return n == "" || n == "geometry.humanoid" || strings.HasPrefix(n, "geometry.humanoid.custom")
}

// skinIsSlim prefers the geometry the patch names, which is what the client
// draws. Only a patch that names nothing falls back to the arm size field,
// whose zero value is slim and so cannot overrule a named wide model.
func skinIsSlim(s protocol.Skin) bool {
	switch n := geometryName(s.SkinResourcePatch); {
	case strings.Contains(n, "customSlim"):
		return true
	case n != "":
		return false
	case bytes.Contains(s.SkinResourcePatch, []byte("customSlim")):
		return true
	}
	return s.ArmSize == protocol.ArmSizeSlim && len(s.SkinResourcePatch) > 0
}

func skinFromRGBA(w, h int, rgba []byte, slim bool) (skinInfo, bool) {
	info, _ := convertSkin(w, h, rgba, slim)
	return info, info.png != nil
}

func convertSkin(w, h int, rgba []byte, slim bool) (skinInfo, string) {
	switch {
	case len(rgba) == 0:
		return skinInfo{}, skinNoData
	case w < skinBase || w > maxSkinSide || w%skinBase != 0 || (h != w && h != w/2):
		return skinInfo{}, skinBadSize
	case len(rgba) != w*h*4:
		return skinInfo{}, skinBadLength
	case fullyTransparent(rgba):
		// A persona's own image is a blank placeholder; drawing it would make
		// the player invisible instead of default-skinned.
		return skinInfo{}, skinTransparent
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, w))
	copy(img.Pix, rgba)
	if h != w {
		legacyLimbs(img)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return skinInfo{}, skinEncodeFailed
	}
	return skinInfo{png: b.Bytes(), slim: slim}, skinAccepted
}

func fullyTransparent(rgba []byte) bool {
	for i := 3; i < len(rgba); i += 4 {
		if rgba[i] != 0 {
			return false
		}
	}
	return true
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
