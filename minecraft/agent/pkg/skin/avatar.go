package skin

import (
	"crypto/sha256"
	"image"
	"image/color"
)

// The picture Avatar draws: a grid of cells, each a square of pixels, with
// a frame one cell wide around a pattern that is the same left and right.
const (
	avatarCells = 8
	avatarCell  = 2
	// AvatarSide is the side of an avatar in pixels, a size the map takes
	// a head in.
	AvatarSide = avatarCells * avatarCell

	// minContrast is how far apart in brightness, out of 255, the two
	// colours of an avatar must be for its pattern to show.
	minContrast = 64
)

// Tints are the colours a character-creator skin says its wearer has. They
// are numbers a player's client chose, used for nothing but the two
// colours of that player's own avatar. A colour with no alpha is one the
// skin did not give.
type Tints struct {
	Skin, Hair color.RGBA
}

// usable reports whether the tints are ones to draw with: a skin colour
// that is opaque and reads as a skin tone. That is every tone the
// character creator offers and almost no arbitrary colour, so it also
// catches the channels arriving in another order than expected, which
// would otherwise put every player in the wrong colours. Hair may be any
// colour and has no such test; it is taken only beside a skin that passed.
func (t Tints) usable() bool {
	s := t.Skin
	return s.A == 255 && s.R >= s.G && s.G >= s.B && s.R > s.B
}

func brightness(c color.NRGBA) int {
	return (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
}

func opaque(c rgba) color.NRGBA { return color.NRGBA{c.r, c.g, c.b, 255} }

// Avatar is the picture drawn for a player whose skin gave no head: a
// pattern worked out from identity alone, so the same player has the same
// one in every session and two players almost never share one. It is a
// badge and looks like one; nothing in it is taken from the skin's image.
//
// With usable tints the pattern is drawn in the player's skin colour on
// their hair colour, which is as near to theirs as a picture made without
// their skin can be. tinted reports whether it was.
func Avatar(identity string, tints Tints) (img *image.NRGBA, tinted bool) {
	sum := sha256.Sum256([]byte(identity))
	// A deep ground and a pale mark from the far side of the hue wheel:
	// apart in brightness whatever the hue, so the pattern always shows.
	hue := float64(uint16(sum[0])<<8|uint16(sum[1])) / 65536 * 360
	ground, mark := opaque(hsv(hue, 0.75, 0.55)), opaque(hsv(mod(hue+180, 360), 0.25, 0.97))
	if tints.usable() {
		tinted = true
		mark = color.NRGBA{tints.Skin.R, tints.Skin.G, tints.Skin.B, 255}
		if tints.Hair.A == 255 {
			ground = color.NRGBA{tints.Hair.R, tints.Hair.G, tints.Hair.B, 255}
		}
		// Fair hair on fair skin would hide the pattern, so the ground
		// gives way: it is the hair darkened, or lightened if dark already.
		if d := brightness(mark) - brightness(ground); d > -minContrast && d < minContrast {
			if brightness(mark) >= 128 {
				ground = color.NRGBA{ground.R / 3, ground.G / 3, ground.B / 3, 255}
			} else {
				ground = color.NRGBA{255 - (255-ground.R)/3, 255 - (255-ground.G)/3, 255 - (255-ground.B)/3, 255}
			}
		}
	}

	// Half the inner columns are chosen by the identity and mirrored. One
	// cell is always set and one always clear, so no identity gives a
	// blank tile or a full one.
	const inner, half = avatarCells - 2, (avatarCells - 2) / 2
	var on [inner][half]bool
	for i := range inner * half {
		on[i/half][i%half] = sum[2+i/8]>>(i%8)&1 == 1
	}
	on[inner/2][half-1], on[0][0] = true, false

	img = image.NewNRGBA(image.Rect(0, 0, AvatarSide, AvatarSide))
	for y := range AvatarSide {
		for x := range AvatarSide {
			c := ground
			if cx, cy := x/avatarCell-1, y/avatarCell-1; cx >= 0 && cx < inner && cy >= 0 && cy < inner {
				if on[cy][min(cx, inner-1-cx)] {
					c = mark
				}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img, tinted
}
