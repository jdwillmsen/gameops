package skin

import (
	"errors"
	"fmt"
	"image"
)

// ErrNotASkin is a buffer that is not one of the images Bedrock sends as a
// classic skin.
var ErrNotASkin = errors.New("skin: not a classic skin image")

// The face and the hat layer over it, in the 64-pixel-wide layout every
// classic skin uses. A larger skin is the same layout at a whole multiple.
const (
	faceX, faceY = 8, 8
	hatX, hatY   = 40, 8
	faceSide     = 8
	layoutWidth  = 64
)

// classicSizes are the images Bedrock sends for a classic skin: the legacy
// half-height one, the standard one, and the standard layout at two and
// four times the resolution. Anything else is refused rather than guessed
// at, since the crop below is only right for this layout.
var classicSizes = map[[2]int]bool{{64, 32}: true, {64, 64}: true, {128, 128}: true, {256, 256}: true}

// Head crops the face from a skin and lays the hat over it, as the game
// draws a head seen from the front. rgba is row-major RGBA, the form skins
// arrive in. The result is 8 pixels a side for a standard skin and larger
// by the skin's own multiple.
//
// The buffer comes from another player's client by way of the server, so
// nothing about it is taken on trust: the dimensions must be a known skin
// size and the length must be exactly what they imply.
func Head(width, height int, rgba []byte) (*image.NRGBA, error) {
	if !classicSizes[[2]int{width, height}] {
		return nil, fmt.Errorf("%w: %dx%d", ErrNotASkin, width, height)
	}
	if len(rgba) != width*height*4 {
		return nil, fmt.Errorf("%w: %d bytes for %dx%d", ErrNotASkin, len(rgba), width, height)
	}
	scale := width / layoutWidth
	side := faceSide * scale
	out := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			face := rgba[((faceY*scale+y)*width+faceX*scale+x)*4:]
			hat := rgba[((hatY*scale+y)*width+hatX*scale+x)*4:]
			// The base layer is drawn solid whatever its alpha says; only
			// the hat is laid over with its own.
			a := uint32(hat[3])
			px := out.Pix[(y*side+x)*4:]
			for c := range 3 {
				px[c] = byte((uint32(hat[c])*a + uint32(face[c])*(255-a) + 127) / 255)
			}
			px[3] = 255
		}
	}
	return out, nil
}
