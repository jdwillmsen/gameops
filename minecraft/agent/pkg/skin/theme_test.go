package skin

import (
	"encoding/base64"
	"fmt"
	"testing"
)

var allThemes = []string{ThemeDotablaze, ThemeNeon, ThemeSheriff}

func TestThemeSkinsAreValidAndOpaque(t *testing.T) {
	for _, th := range allThemes {
		s, ok := ForTheme("Agate", th)
		if !ok {
			t.Fatalf("%s: unknown", th)
		}
		raw, err := base64.StdEncoding.DecodeString(s.Data)
		if err != nil || len(raw) != Width*Height*4 {
			t.Fatalf("%s: bad buffer (%d bytes, %v)", th, len(raw), err)
		}
		for _, b := range []block{head, torso, rightArm, leftArm, rightLeg, leftLeg} {
			for y := b.y + b.d; y < b.y+b.d+b.h; y++ {
				for x := b.x; x < b.x+2*(b.w+b.d); x++ {
					if a := raw[(y*Width+x)*4+3]; a != 255 {
						t.Fatalf("%s: base pixel (%d,%d) has alpha %d", th, x, y, a)
					}
				}
			}
		}
	}
}

func TestThemeIsDeterministicAndDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, th := range allThemes {
		for _, name := range []string{"Agate", "Basalt", "Cobble", "Dotablaze"} {
			a, _ := ForTheme(name, th)
			if b, _ := ForTheme(name, th); a != b {
				t.Errorf("%s/%s: two skins for one bot", th, name)
			}
			key := th + "/" + name
			// The sheriff is one fixed likeness, so only its ID varies by name.
			if other, dup := seen[a.Data]; dup && th != ThemeSheriff {
				t.Errorf("%s and %s share a skin", key, other)
			}
			seen[a.Data] = key
			if other, dup := seen[a.ID]; dup {
				t.Errorf("%s and %s share a skin ID", key, other)
			}
			seen[a.ID] = key
		}
	}
}

func TestUnknownThemeIsRefused(t *testing.T) {
	if _, ok := ForTheme("Agate", "plaid"); ok {
		t.Error("unknown theme produced a skin")
	}
	if !KnownTheme(" Dotablaze ") {
		t.Error("theme names should ignore case and padding")
	}
}

func TestSheriffIsSlim(t *testing.T) {
	sh, _ := ForTheme("Dotablaze7321", ThemeSheriff)
	if !sh.Slim || sh.ArmSize() != "slim" || sh.ResourcePatch() == "" {
		t.Errorf("sheriff should use the slim model: %+v", sh)
	}
}

// Every other theme picks wide or slim per name, so a fleet shows both builds,
// and each pick agrees with the arm size sent at login.
func TestBuildIsRandomPerNameAndConsistent(t *testing.T) {
	for _, th := range []string{ThemeDotablaze, ThemeNeon} {
		slim, wide := 0, 0
		for i := range 40 {
			s, _ := ForTheme(fmt.Sprintf("bot-%d", i), th)
			if s.Slim {
				slim++
			} else {
				wide++
			}
			if (s.ArmSize() == "slim") != s.Slim || (s.ResourcePatch() != "") != s.Slim {
				t.Fatalf("%s: arm size and resource patch disagree with Slim", th)
			}
		}
		if slim == 0 || wide == 0 {
			t.Errorf("%s: 40 bots gave %d slim, %d wide", th, slim, wide)
		}
	}
}

// Each variant must actually draw something different, or the per-name pick
// changes nothing a player can see. The same look is painted with every
// variant and the chest and head regions compared.
func TestVariantsRenderDifferently(t *testing.T) {
	l := look{skin: rgba{250, 222, 192, 255}, hair: rgba{40, 30, 20, 255}, accent: rgba{255, 120, 20, 255}}
	region := func(draw func(px []byte, v int), v int, x, y, w, h int) string {
		px := make([]byte, Width*Height*4)
		draw(px, v)
		var out []byte
		for r := 0; r < h; r++ {
			out = append(out, px[((y+r)*Width+x)*4:((y+r)*Width+x+w)*4]...)
		}
		return string(out)
	}
	cases := map[string]struct {
		draw       func(px []byte, v int)
		x, y, w, h int
	}{
		"dotablaze chest": {func(px []byte, v int) { l.chest = v; chestEmblem(px, l, rgba{255, 214, 80, 255}) }, 20, 20, 8, 12},
		"dotablaze head":  {func(px []byte, v int) { l.build = v; dotablazeHead(px, l, rgba{255, 214, 80, 255}) }, 32, 0, 32, 16},
		"neon chest":      {func(px []byte, v int) { neonChest(px, l.accent, v) }, 20, 20, 8, 12},
		"neon head":       {func(px []byte, v int) { l.build = v; neonHead(px, l, rgba{26, 28, 42, 255}) }, 32, 0, 32, 16},
	}
	for name, c := range cases {
		seen := map[string]int{}
		for v := range themeVariants {
			r := region(c.draw, v, c.x, c.y, c.w, c.h)
			if prev, dup := seen[r]; dup {
				t.Errorf("%s: variants %d and %d render the same", name, prev, v)
			}
			seen[r] = v
		}
	}
}

// Painting an arm wide and projecting it to the slim layout must keep the
// front face's first three columns, and the left side where slim expects it.
func TestSlimArmsMovesFacesToTheSlimLayout(t *testing.T) {
	px := make([]byte, Width*Height*4)
	set := func(x, y int, v byte) { px[(y*Width+x)*4] = v }
	get := func(x, y int) byte { return px[(y*Width+x)*4] }
	set(44, 20, 1) // front, first column
	set(46, 20, 2) // front, third column
	set(48, 20, 3) // left side, first column
	set(52, 20, 4) // back, first column
	slimArms(px)
	if get(44, 20) != 1 || get(46, 20) != 2 {
		t.Error("front columns moved")
	}
	if get(47, 20) != 3 {
		t.Errorf("left side should start at x+7, got %d", get(47, 20))
	}
	if get(51, 20) != 4 {
		t.Errorf("back should start at x+11, got %d", get(51, 20))
	}
}
