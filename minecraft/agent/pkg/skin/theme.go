package skin

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

// Themes a deployment can pick for its AFK bots. Dotablaze is the FWB look;
// every other server wears neon.
const (
	ThemeDotablaze = "dotablaze"
	ThemeNeon      = "neon"
)

var themes = map[string]func(px []byte, l look){
	ThemeDotablaze: paintDotablaze,
	ThemeNeon:      paintNeon,
}

// KnownTheme reports whether a theme name has a look of its own.
func KnownTheme(theme string) bool {
	_, ok := themes[normaliseTheme(theme)]
	return ok
}

func normaliseTheme(theme string) string { return strings.ToLower(strings.TrimSpace(theme)) }

// ForTheme builds a deterministic themed skin for a named bot. The theme picks
// the outfit and the name picks the individual (skin tone, hair, accent, face
// mark), so bots on one server read as a team without being clones. ok is
// false for an unknown theme, and the caller picks a fallback rather than
// failing a login.
func ForTheme(name, theme string) (s Skin, ok bool) {
	theme = normaliseTheme(theme)
	paint, ok := themes[theme]
	if !ok {
		return Skin{}, false
	}
	sum := sha256.Sum256([]byte(name + "|theme|" + theme))
	l := lookFrom(sum)
	if theme == ThemeDotablaze {
		l.hair, l.accent = flameFrom(sum)
	} else {
		l.accent = hsv(float64(sum[4])/255*360, 0.85, 1)
	}

	px := make([]byte, Width*Height*4)
	body(px, l)
	paintFaceMark(px, l)
	paint(px, l)
	paintHandMarks(px, l, theme)

	return Skin{
		ID:     fmt.Sprintf("%x-%s", sum[:8], theme),
		Data:   base64.StdEncoding.EncodeToString(px),
		Width:  Width,
		Height: Height,
	}, true
}

// flameFrom picks the hair and trim colour from the fire range, so every
// Dotablaze bot burns a slightly different shade.
func flameFrom(sum [32]byte) (hair, trim rgba) {
	flames := []rgba{
		{255, 120, 20, 255}, {235, 70, 25, 255}, {255, 165, 30, 255}, {215, 45, 35, 255},
	}
	return flames[int(sum[7])%len(flames)], flames[int(sum[8])%len(flames)]
}

// Dotablaze: charcoal armour with ember trim, a flame emblem on the chest, a
// burning headband over glowing eyes and hair in a fire colour.
func paintDotablaze(px []byte, l look) {
	char := rgba{40, 38, 46, 255}
	outfit(px, char, darken(char, 0.7), rgba{25, 24, 30, 255})
	hands(px, l.skin)

	ember := l.accent
	glow := rgba{255, 214, 80, 255}
	for _, p := range []block{rightArm, leftArm} {
		fill(px, p.x, p.y+p.d, 2*(p.w+p.d), 1, ember)
	}
	for _, p := range []block{rightLeg, leftLeg} {
		fill(px, p.x, p.y+p.d, 2*(p.w+p.d), 1, ember)
		fill(px, p.x, p.y+p.d+p.h-3, 2*(p.w+p.d), 1, darken(ember, 0.7))
	}
	torso.paintFront(px, ember, 0, 0, 8, 1)
	torso.paintFront(px, darken(char, 1.5), 0, 10, 8, 2)

	flame := []string{
		"..o.",
		".oo.",
		".ooo",
		"ooyo",
		"oyyo",
		".oo.",
	}
	for y, row := range flame {
		for x, c := range row {
			switch c {
			case 'o':
				torso.paintFront(px, ember, 2+x, 2+y, 1, 1)
			case 'y':
				torso.paintFront(px, glow, 2+x, 2+y, 1, 1)
			}
		}
	}
	torso.paintBack(px, ember, 2, 2, 4, 1)
	torso.paintBack(px, darken(ember, 0.7), 3, 3, 2, 3)

	// The headband goes on the hat layer too, or the game draws the hair over it.
	for _, x := range []int{0, 8, 16, 24} {
		fill(px, 32+x, 10, 8, 1, ember)
	}
	head.paintFront(px, glow, 2, 4, 1, 1)
	head.paintFront(px, glow, 5, 4, 1, 1)
}

// Neon: a dark hooded jacket with a glowing visor, light strips down the arms
// and legs and a chevron on the chest, all in a colour taken from the name.
func paintNeon(px []byte, l look) {
	dark := rgba{26, 28, 42, 255}
	neon := l.accent
	outfit(px, dark, darken(dark, 0.75), rgba{16, 16, 26, 255})
	hands(px, l.skin)

	for _, p := range []block{rightArm, leftArm, rightLeg, leftLeg} {
		x, y, _, _ := p.front()
		fill(px, x, y, 1, p.h, neon)
		bx, by, _, _ := p.back()
		fill(px, bx+p.w-1, by, 1, p.h, neon)
	}
	for i := range 3 {
		torso.paintFront(px, neon, 1+i, 2+i, 1, 1)
		torso.paintFront(px, neon, 6-i, 2+i, 1, 1)
	}
	torso.paintFront(px, neon, 1, 8, 6, 1)
	torso.paintBack(px, neon, 1, 3, 6, 1)
	torso.paintBack(px, neon, 3, 4, 2, 4)

	helmet(px, dark, neon)
	// The hood closes over the back of the head on the hat layer; otherwise
	// the hair shows through behind the visor.
	hat.paintBack(px, dark, 0, 0, 8, 8)
	head.paintFront(px, dark, 0, 3, 8, 5)
	head.paintFront(px, l.skin, 1, 5, 6, 3)
	head.paintFront(px, darken(neon, 0.35), 1, 3, 6, 2)
	head.paintFront(px, neon, 1, 4, 6, 1)
	// The hood redraws the lower face, so its mark goes on afterwards.
	paintFaceMark(px, l)
}
