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
	// ThemeSheriff is a fixed likeness rather than a per-name look: tall-and-
	// skinny, fair, blond, blue-eyed, in a county sheriff's Class A uniform.
	ThemeSheriff = "sheriff"
)

var themes = map[string]func(px []byte, l look){
	ThemeDotablaze: paintDotablaze,
	ThemeNeon:      paintNeon,
	ThemeSheriff:   paintSheriff,
}

// KnownTheme reports whether a theme name has a look of its own.
// themeVariants is how many head and chest variants each theme draws.
const themeVariants = 3

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
	switch theme {
	case ThemeDotablaze:
		l.hair, l.accent = flameFrom(sum)
	case ThemeSheriff:
		l.skin, l.hair = rgba{250, 222, 192, 255}, rgba{226, 192, 96, 255}
		l.accent, l.mark, l.glove = rgba{240, 200, 60, 255}, 5, false
	default:
		l.accent = hsv(float64(sum[4])/255*360, 0.85, 1)
	}
	l.build, l.chest = int(sum[9])%themeVariants, int(sum[11])%themeVariants
	// The sheriff is slim by design and paints its own arms; every other bot
	// is wide or slim at random, by name.
	slim := theme == ThemeSheriff || sum[10]&1 == 1

	px := make([]byte, Width*Height*4)
	body(px, l)
	paintFaceMark(px, l)
	paint(px, l)
	if theme != ThemeSheriff {
		paintHandMarks(px, l, theme)
		if slim {
			slimArms(px)
		}
	}

	return Skin{
		ID:     fmt.Sprintf("%x-%s", sum[:8], theme),
		Data:   base64.StdEncoding.EncodeToString(px),
		Width:  Width,
		Height: Height,
		Slim:   slim,
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

	chestEmblem(px, l, glow)
	torso.paintBack(px, ember, 2, 2, 4, 1)
	torso.paintBack(px, darken(ember, 0.7), 3, 3, 2, 3)

	dotablazeHead(px, l, glow)
}

// chestEmblem draws one of three Dotablaze chest marks: a flame, three ember
// bars or a glowing ring.
func chestEmblem(px []byte, l look, glow rgba) {
	ember := l.accent
	switch l.chest {
	case 0:
		for y, row := range []string{"..o.", ".oo.", ".ooo", "ooyo", "oyyo", ".oo."} {
			for x, c := range row {
				switch c {
				case 'o':
					torso.paintFront(px, ember, 2+x, 2+y, 1, 1)
				case 'y':
					torso.paintFront(px, glow, 2+x, 2+y, 1, 1)
				}
			}
		}
	case 1:
		for i := range 3 {
			torso.paintFront(px, ember, 1, 2+i*2, 6, 1)
		}
		torso.paintFront(px, glow, 3, 3, 2, 1)
	default:
		torso.paintFront(px, ember, 2, 2, 4, 4)
		torso.paintFront(px, darken(ember, 0.35), 3, 3, 2, 2)
		torso.paintFront(px, glow, 3, 3, 1, 1)
		torso.paintFront(px, glow, 4, 4, 1, 1)
	}
}

// dotablazeHead draws one of three head pieces on the hat layer, which the
// game paints over the hair: a burning headband, a flame crest or goggles.
func dotablazeHead(px []byte, l look, glow rgba) {
	ember := l.accent
	switch l.build {
	case 0:
		for _, x := range []int{0, 8, 16, 24} {
			fill(px, 32+x, 10, 8, 1, ember)
		}
	case 1:
		fill(px, 40, 0, 8, 8, ember)
		for _, x := range []int{0, 8, 16, 24} {
			for dx := 0; dx < 8; dx += 2 {
				fill(px, 32+x+dx, 8, 1, 1, ember)
				fill(px, 32+x+dx+1, 8, 1, 1, glow)
			}
		}
	default:
		dark := rgba{30, 28, 34, 255}
		for _, x := range []int{0, 8, 16, 24} {
			fill(px, 32+x, 12, 8, 1, dark)
		}
		fill(px, 41, 11, 2, 3, darken(ember, 0.8))
		fill(px, 45, 11, 2, 3, darken(ember, 0.8))
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
	neonChest(px, neon, l.chest)
	torso.paintBack(px, neon, 1, 3, 6, 1)
	torso.paintBack(px, neon, 3, 4, 2, 4)

	neonHead(px, l, dark)
}

// neonChest draws one of three chest marks: a chevron, a pair of bars or a
// ring.
func neonChest(px []byte, neon rgba, variant int) {
	switch variant {
	case 0:
		for i := range 3 {
			torso.paintFront(px, neon, 1+i, 2+i, 1, 1)
			torso.paintFront(px, neon, 6-i, 2+i, 1, 1)
		}
		torso.paintFront(px, neon, 1, 8, 6, 1)
	case 1:
		torso.paintFront(px, neon, 2, 1, 1, 8)
		torso.paintFront(px, neon, 5, 1, 1, 8)
		torso.paintFront(px, neon, 3, 4, 2, 1)
	default:
		torso.paintFront(px, neon, 2, 2, 4, 1)
		torso.paintFront(px, neon, 2, 5, 4, 1)
		torso.paintFront(px, neon, 2, 3, 1, 2)
		torso.paintFront(px, neon, 5, 3, 1, 2)
		torso.paintFront(px, neon, 0, 9, 8, 1)
	}
}

// neonHead draws one of three head pieces: a hood with a glowing visor, a cap
// with a neon brim over the bot's own hair, or headphones.
func neonHead(px []byte, l look, dark rgba) {
	neon := l.accent
	switch l.build {
	case 0:
		helmet(px, dark, neon)
		// The hood closes over the back of the head on the hat layer;
		// otherwise the hair shows through behind the visor.
		hat.paintBack(px, dark, 0, 0, 8, 8)
		head.paintFront(px, dark, 0, 3, 8, 5)
		head.paintFront(px, l.skin, 1, 5, 6, 3)
		head.paintFront(px, darken(neon, 0.35), 1, 3, 6, 2)
		head.paintFront(px, neon, 1, 4, 6, 1)
		// The hood redraws the lower face, so its mark goes on afterwards.
		paintFaceMark(px, l)
	case 1:
		helmet(px, dark, neon)
		head.paintFront(px, neon, 2, 4, 1, 1)
		head.paintFront(px, neon, 5, 4, 1, 1)
	default:
		fill(px, 40, 0, 8, 1, neon)
		fill(px, 40, 7, 8, 1, neon)
		for _, x := range []int{32, 48} {
			fill(px, x, 11, 8, 3, darken(neon, 0.6))
			fill(px, x+2, 12, 4, 1, neon)
		}
		head.paintFront(px, neon, 2, 4, 1, 1)
		head.paintFront(px, neon, 5, 4, 1, 1)
	}
}

var (
	slimRightArm = block{40, 16, 3, 12, 4}
	slimLeftArm  = block{32, 48, 3, 12, 4}
)

// Sheriff: a county Class A uniform. Tan long-sleeved shirt with a dark tie,
// a gold six-point star on the left breast, forest-green trousers with a black
// stripe, a black duty belt with a brass buckle and a tan campaign hat over
// blond hair, with blue eyes and slim arms.
func paintSheriff(px []byte, l look) {
	tan := rgba{196, 168, 118, 255}
	green := rgba{52, 78, 54, 255}
	black := rgba{24, 24, 28, 255}
	gold := l.accent

	for _, p := range []block{torso, slimRightArm, slimLeftArm} {
		p.paint(px, tan)
	}
	for _, p := range []block{rightLeg, leftLeg} {
		p.paint(px, green)
		fill(px, p.x, p.y+p.d+p.h-2, 2*(p.w+p.d), 2, black)
		// The stripe runs down the outer seam, which is the first column of
		// the front face on one leg and the last on the other.
		x, y, _, _ := p.front()
		fill(px, x, y, 1, p.h-2, black)
		bx, by, _, _ := p.back()
		fill(px, bx+p.w-1, by, 1, p.h-2, black)
	}
	for _, p := range []block{slimRightArm, slimLeftArm} {
		fill(px, p.x, p.y+p.d+p.h-2, 2*(p.w+p.d), 2, l.skin)
		x, y, _, _ := p.front()
		fill(px, x, y+1, p.w, 3, green)
		fill(px, x+1, y+2, 1, 1, gold)
	}

	torso.paintFront(px, darken(tan, 0.8), 2, 0, 4, 1)
	torso.paintFront(px, black, 3, 1, 2, 1)
	torso.paintFront(px, black, 3, 2, 2, 5)
	torso.paintFront(px, darken(tan, 0.85), 1, 4, 2, 1)
	for y, row := range []string{".X.", "XXX", ".X."} {
		for x, c := range row {
			if c == 'X' {
				torso.paintFront(px, gold, 5+x-1, 2+y, 1, 1)
			}
		}
	}
	torso.paintFront(px, black, 0, 9, 8, 2)
	torso.paintFront(px, gold, 3, 9, 2, 2)
	torso.paintBack(px, black, 0, 9, 8, 2)
	torso.paintBack(px, darken(tan, 0.85), 2, 1, 4, 1)

	head.paintFront(px, rgba{60, 110, 200, 255}, 2, 4, 1, 1)
	head.paintFront(px, rgba{60, 110, 200, 255}, 5, 4, 1, 1)
	head.paintFront(px, darken(l.hair, 0.85), 1, 3, 2, 1)
	head.paintFront(px, darken(l.hair, 0.85), 5, 3, 2, 1)

	hatColour := rgba{170, 138, 88, 255}
	helmet(px, hatColour, darken(hatColour, 0.6))
	hat.paintFront(px, gold, 3, 2, 2, 1)
}

// slimArms rebuilds each arm's texture for the 3-pixel model. Every arm is
// painted wide first, so one painting routine serves both builds; this keeps
// the right side, the first three columns of the front and back, and the left
// side, and moves them to where the slim layout expects them.
func slimArms(px []byte) {
	for _, o := range [][2]int{{40, 16}, {32, 48}, {40, 32}, {48, 48}} {
		x, y := o[0], o[1]
		snap := make([]byte, len(px))
		copy(snap, px)
		move := func(sx, sy, dx, dy, w, h int) {
			for r := 0; r < h; r++ {
				copy(px[((dy+r)*Width+dx)*4:], snap[((sy+r)*Width+sx)*4:((sy+r)*Width+sx+w)*4])
			}
		}
		move(x+4, y, x+4, y, 3, 4)
		move(x+8, y, x+7, y, 3, 4)
		move(x, y+4, x, y+4, 4, 12)
		move(x+4, y+4, x+4, y+4, 3, 12)
		move(x+8, y+4, x+7, y+4, 4, 12)
		move(x+12, y+4, x+11, y+4, 3, 12)
	}
}
