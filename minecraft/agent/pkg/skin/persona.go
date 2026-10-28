package skin

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

// Persona is who a unit is, as far as its appearance goes. The role picks the
// outfit; the squad role refines a warrior's.
type Persona struct {
	Role      string
	SquadRole string
}

// Known reports whether the persona has an outfit of its own. An unknown role
// falls back to the plain name-coloured skin rather than failing a login.
func (p Persona) Known() bool {
	_, ok := outfits[outfitKey(p)]
	return ok
}

// ForPersona builds a deterministic outfit for a named unit.
//
// The outfit comes from the role and the individual from the name, so every
// miner wears the same gear but no two look alike: skin tone, hair and the
// outfit's accent shift with the name hash. Same inputs, same skin, on every
// restart.
func ForPersona(name string, p Persona) Skin {
	key := outfitKey(p)
	paint, ok := outfits[key]
	if !ok {
		return For(name)
	}
	sum := sha256.Sum256([]byte(name + "|" + key))
	look := lookFrom(sum)

	px := make([]byte, Width*Height*4)
	body(px, look)
	paintFaceMark(px, look)
	paint(px, look)
	paintHandMarks(px, look, key)

	return Skin{
		ID:     fmt.Sprintf("%x-%s", sum[:8], key),
		Data:   base64.StdEncoding.EncodeToString(px),
		Width:  Width,
		Height: Height,
	}
}

func outfitKey(p Persona) string {
	role := strings.ToLower(strings.TrimSpace(p.Role))
	if role == "warrior" {
		if sq := strings.ToLower(strings.TrimSpace(p.SquadRole)); sq != "" {
			return "warrior-" + sq
		}
	}
	return role
}

// look is the block of a unit's appearance that belongs to the individual.
type look struct {
	skin, hair, accent rgba
	// mark picks the face mark, and glove whether the hands are gloved;
	// both come from the name, and both stay visible under armour, which
	// covers the outfit but not the face or the hands.
	mark  int
	glove bool
}

var skinTones = []rgba{
	{255, 219, 172, 255}, {241, 194, 145, 255}, {224, 172, 105, 255},
	{198, 134, 66, 255}, {141, 85, 36, 255}, {96, 60, 30, 255},
}

var hairTones = []rgba{
	{45, 30, 20, 255}, {20, 20, 24, 255}, {110, 70, 35, 255},
	{190, 140, 60, 255}, {150, 60, 30, 255}, {170, 170, 170, 255},
}

func lookFrom(sum [32]byte) look {
	return look{
		skin: skinTones[int(sum[2])%len(skinTones)],
		hair: hairTones[int(sum[3])%len(hairTones)],
		// A small hue spread keeps units of one role recognisably a team
		// without making them clones.
		accent: hsv(float64(sum[4])/255*360, 0.55, 0.8),
		mark:   int(sum[5]) % faceMarks,
		glove:  sum[6]&1 == 1,
	}
}

// Rectangles of the standard 64x64 layout. Each limb's block is
// 2*(w+d) wide and d+h tall with its top face first; the front face sits at
// (x+d, y+d).
type block struct{ x, y, w, h, d int }

var (
	head      = block{0, 0, 8, 8, 8}
	hat       = block{32, 0, 8, 8, 8}
	torso     = block{16, 16, 8, 12, 4}
	jacket    = block{16, 32, 8, 12, 4}
	rightArm  = block{40, 16, 4, 12, 4}
	rightSlv  = block{40, 32, 4, 12, 4}
	leftArm   = block{32, 48, 4, 12, 4}
	leftSlv   = block{48, 48, 4, 12, 4}
	rightLeg  = block{0, 16, 4, 12, 4}
	rightPant = block{0, 32, 4, 12, 4}
	leftLeg   = block{16, 48, 4, 12, 4}
	leftPant  = block{0, 48, 4, 12, 4}
)

func (p block) front() (x, y, w, h int) { return p.x + p.d, p.y + p.d, p.w, p.h }
func (p block) back() (x, y, w, h int)  { return p.x + 2*p.d + p.w, p.y + p.d, p.w, p.h }

func (p block) paint(px []byte, c rgba) { fill(px, p.x, p.y, 2*(p.w+p.d), p.d+p.h, c) }

func (p block) paintFront(px []byte, c rgba, dx, dy, w, h int) {
	x, y, _, _ := p.front()
	fill(px, x+dx, y+dy, w, h, c)
}

func (p block) paintBack(px []byte, c rgba, dx, dy, w, h int) {
	x, y, _, _ := p.back()
	fill(px, x+dx, y+dy, w, h, c)
}

// body draws the unclothed figure: skin everywhere, hair, face. Outfits are
// painted over it. The overlay layers are painted solid rather than left
// transparent, so a skin never has holes where an outfit does not cover.
func body(px []byte, l look) {
	// The UV sheet has unused gaps; fill them so no pixel is transparent.
	fill(px, 0, 0, Width, Height, l.skin)
	for _, p := range []block{head, hat, torso, jacket, rightArm, rightSlv, leftArm, leftSlv, rightLeg, rightPant, leftLeg, leftPant} {
		p.paint(px, l.skin)
	}
	// Hair: the crown and a fringe on the front, the whole back of the head.
	fill(px, 8, 0, 8, 8, l.hair)
	head.paintFront(px, l.hair, 0, 0, 8, 2)
	head.paintBack(px, l.hair, 0, 0, 8, 8)
	fill(px, 0, 8, 8, 3, l.hair)
	fill(px, 16, 8, 8, 3, l.hair)
	// Eyes, as in the plain skin: near-black, never pure black.
	eye := rgba{20, 20, 28, 255}
	head.paintFront(px, rgba{255, 255, 255, 255}, 1, 4, 2, 1)
	head.paintFront(px, rgba{255, 255, 255, 255}, 5, 4, 2, 1)
	head.paintFront(px, eye, 2, 4, 1, 1)
	head.paintFront(px, eye, 5, 4, 1, 1)
	head.paintFront(px, darken(l.skin, 0.8), 3, 6, 2, 1)
}

func outfit(px []byte, shirt, trousers, shoes rgba) {
	for _, p := range []block{torso, jacket, rightArm, rightSlv, leftArm, leftSlv} {
		p.paint(px, shirt)
	}
	for _, p := range []block{rightLeg, rightPant, leftLeg, leftPant} {
		p.paint(px, trousers)
	}
	for _, p := range []block{rightLeg, rightPant, leftLeg, leftPant} {
		fill(px, p.x, p.y+p.d+p.h-2, 2*(p.w+p.d), 2, shoes)
	}
}

// hands repaints the bottom of each arm in the given colour once an outfit has
// covered the sleeves, so the hands read as bare skin (or gloves).
func hands(px []byte, c rgba) {
	for _, p := range []block{rightArm, rightSlv, leftArm, leftSlv} {
		fill(px, p.x, p.y+p.d+p.h-2, 2*(p.w+p.d), 2, c)
	}
}

// helmet covers the crown and brow on both head layers.
func helmet(px []byte, c, band rgba) {
	fill(px, 8, 0, 8, 8, c)
	fill(px, 40, 0, 8, 8, c)
	for _, x := range []int{0, 8, 16, 24} {
		fill(px, x, 8, 8, 3, c)
		fill(px, 32+x, 8, 8, 3, c)
	}
	fill(px, 8, 10, 8, 1, band)
}

var outfits = map[string]func(px []byte, l look){
	"miner":   paintMiner,
	"chopper": paintChopper,
	"hauler":  paintHauler,
	"shaft":   paintShaft,

	"warrior-vanguard": func(px []byte, l look) { paintWarrior(px, l, rgba{200, 50, 40, 255}, false) },
	"warrior-striker":  func(px []byte, l look) { paintWarrior(px, l, rgba{130, 60, 190, 255}, false) },
	"warrior-ranger":   func(px []byte, l look) { paintWarrior(px, l, rgba{60, 150, 70, 255}, true) },
	"warrior":          func(px []byte, l look) { paintWarrior(px, l, l.accent, false) },
}

// Miner: yellow hard hat with a lamp, grey work shirt, brown belt over dark
// trousers, rock dust on the knees.
func paintMiner(px []byte, l look) {
	outfit(px, rgba{110, 112, 120, 255}, rgba{60, 55, 70, 255}, rgba{40, 35, 35, 255})
	hands(px, l.skin)
	helmet(px, rgba{245, 195, 40, 255}, darken(rgba{245, 195, 40, 255}, 0.7))
	head.paintFront(px, rgba{255, 250, 200, 255}, 3, 0, 2, 1)
	fill(px, 20, 27, 8, 1, rgba{110, 70, 35, 255})
	torso.paintFront(px, rgba{110, 70, 35, 255}, 0, 9, 8, 1)
	torso.paintFront(px, l.accent, 2, 3, 2, 2)
	fill(px, 4, 24, 4, 2, rgba{85, 85, 90, 255})
}

// Chopper: a plaid flannel in the unit's accent colour, a beard, brown
// trousers, boots. The beard is what makes it read as a woodsman at range.
func paintChopper(px []byte, l look) {
	flannel := darken(l.accent, 0.9)
	dark := darken(l.accent, 0.5)
	outfit(px, flannel, rgba{90, 65, 40, 255}, rgba{50, 35, 25, 255})
	for _, p := range []block{torso, jacket, rightArm, rightSlv, leftArm, leftSlv} {
		for dy := 0; dy < p.d+p.h; dy++ {
			for dx := 0; dx < 2*(p.w+p.d); dx++ {
				if (dx/2+dy/2)%2 == 0 {
					fill(px, p.x+dx, p.y+dy, 1, 1, dark)
				}
			}
		}
	}
	hands(px, l.skin)
	beard := darken(l.hair, 0.9)
	head.paintFront(px, beard, 1, 5, 6, 3)
	head.paintFront(px, l.skin, 3, 5, 2, 1)
	fill(px, 0, 8, 8, 1, l.hair)
	fill(px, 8, 0, 8, 8, l.hair)
	fill(px, 28, 24, 2, 4, rgba{150, 150, 160, 255})
}

// Hauler: a flat cap, a hi-contrast tan work vest over a blue shirt, and the
// leather straps of a pack across the chest.
func paintHauler(px []byte, l look) {
	outfit(px, rgba{50, 90, 150, 255}, rgba{80, 70, 60, 255}, rgba{60, 45, 30, 255})
	hands(px, l.skin)
	vest := rgba{190, 150, 95, 255}
	torso.paintFront(px, vest, 0, 0, 8, 12)
	torso.paintBack(px, rgba{120, 85, 50, 255}, 0, 0, 8, 12)
	torso.paintFront(px, rgba{50, 90, 150, 255}, 3, 0, 2, 12)
	torso.paintFront(px, rgba{110, 75, 40, 255}, 1, 0, 1, 12)
	torso.paintFront(px, rgba{110, 75, 40, 255}, 6, 0, 1, 12)
	torso.paintBack(px, l.accent, 1, 2, 6, 6)
	cap := darken(l.accent, 0.7)
	fill(px, 8, 0, 8, 8, cap)
	fill(px, 40, 0, 8, 8, cap)
	for _, x := range []int{0, 8, 16, 24} {
		fill(px, x, 8, 8, 2, cap)
		fill(px, 32+x, 8, 8, 2, cap)
	}
	head.paintFront(px, darken(cap, 0.7), 0, 2, 8, 1)
}

// Shaft diver: white helmet with a lamp, orange hi-vis jacket with two
// reflective bands, so someone down a hole is spotted from the top.
func paintShaft(px []byte, l look) {
	orange := rgba{240, 110, 30, 255}
	outfit(px, orange, rgba{55, 60, 75, 255}, rgba{35, 35, 40, 255})
	hands(px, l.skin)
	helmet(px, rgba{235, 235, 235, 255}, rgba{190, 190, 190, 255})
	head.paintFront(px, rgba{255, 240, 120, 255}, 3, 0, 2, 2)
	silver := rgba{215, 220, 225, 255}
	for _, p := range []block{torso, jacket} {
		fill(px, p.x, p.y+p.d+3, 2*(p.w+p.d), 1, silver)
		fill(px, p.x, p.y+p.d+8, 2*(p.w+p.d), 1, silver)
	}
	for _, p := range []block{rightArm, rightSlv, leftArm, leftSlv} {
		fill(px, p.x, p.y+p.d+8, 2*(p.w+p.d), 1, silver)
	}
	torso.paintFront(px, l.accent, 5, 1, 2, 2)
}

// Warrior: iron plate over the whole body, a helm with a visor slit, and a
// tabard in the squad-role colour so the line reads at a glance. A ranger
// wears a hood instead of the full helm.
func paintWarrior(px []byte, l look, tabard rgba, hood bool) {
	iron := rgba{170, 172, 180, 255}
	outfit(px, iron, darken(iron, 0.7), rgba{60, 60, 66, 255})
	hands(px, rgba{70, 70, 78, 255})
	for _, p := range []block{rightArm, rightSlv, leftArm, leftSlv} {
		fill(px, p.x, p.y+p.d, 2*(p.w+p.d), 3, darken(iron, 0.85))
		fill(px, p.x, p.y+p.d+p.h-2, 2*(p.w+p.d), 2, rgba{70, 70, 78, 255})
	}
	torso.paintFront(px, tabard, 2, 1, 4, 11)
	torso.paintFront(px, darken(tabard, 0.6), 3, 3, 2, 2)
	torso.paintBack(px, tabard, 2, 1, 4, 11)
	if hood {
		helmet(px, darken(tabard, 0.7), darken(tabard, 0.5))
		head.paintFront(px, l.skin, 1, 3, 6, 5)
		head.paintFront(px, rgba{255, 255, 255, 255}, 1, 4, 2, 1)
		head.paintFront(px, rgba{255, 255, 255, 255}, 5, 4, 2, 1)
		head.paintFront(px, rgba{20, 20, 28, 255}, 2, 4, 1, 1)
		head.paintFront(px, rgba{20, 20, 28, 255}, 5, 4, 1, 1)
		return
	}
	helmet(px, iron, darken(iron, 0.7))
	head.paintFront(px, iron, 0, 3, 8, 5)
	head.paintFront(px, rgba{18, 18, 22, 255}, 1, 4, 6, 1)
	head.paintFront(px, tabard, 3, 0, 2, 3)
}

const faceMarks = 6

// paintFaceMark gives each unit something on its face that no one else of its
// role has. It is painted before the outfit so a beard or visor covers it
// rather than the other way round.
func paintFaceMark(px []byte, l look) {
	shade := darken(l.skin, 0.8)
	switch l.mark {
	case 0: // freckles
		for _, d := range [][2]int{{1, 5}, {2, 6}, {5, 5}, {6, 6}} {
			head.paintFront(px, shade, d[0], d[1], 1, 1)
		}
	case 1: // scar across the left cheek
		for i := range 3 {
			head.paintFront(px, rgba{190, 90, 90, 255}, 1, 3+i, 1, 1)
		}
	case 2: // war paint under the eyes, in the unit's accent colour
		head.paintFront(px, l.accent, 1, 5, 2, 1)
		head.paintFront(px, l.accent, 5, 5, 2, 1)
	case 3: // heavy brows
		head.paintFront(px, darken(l.hair, 0.8), 1, 3, 2, 1)
		head.paintFront(px, darken(l.hair, 0.8), 5, 3, 2, 1)
	case 4: // beauty mark and cheek blush
		head.paintFront(px, shade, 6, 6, 1, 1)
		head.paintFront(px, rgba{225, 140, 130, 255}, 1, 5, 1, 1)
	}
	// 5: a clean face.
}

// paintHandMarks runs after the outfit. A unit wears a wristband in its accent
// colour, and half of them gloves in a darker shade of it. Warriors keep their
// iron gauntlets and take only the band.
func paintHandMarks(px []byte, l look, key string) {
	arms := []block{rightArm, rightSlv, leftArm, leftSlv}
	for _, p := range arms {
		fill(px, p.x, p.y+p.d+p.h-3, 2*(p.w+p.d), 1, l.accent)
		if l.glove && !strings.HasPrefix(key, "warrior") {
			fill(px, p.x, p.y+p.d+p.h-2, 2*(p.w+p.d), 2, darken(l.accent, 0.55))
		}
	}
}
