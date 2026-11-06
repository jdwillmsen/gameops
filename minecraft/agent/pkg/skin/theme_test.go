package skin

import (
	"crypto/sha256"
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

func TestVariantsAppearAcrossNames(t *testing.T) {
	for _, th := range []string{ThemeDotablaze, ThemeNeon} {
		chests, builds := map[int]bool{}, map[int]bool{}
		for i := range 60 {
			sum := sha256.Sum256([]byte(fmt.Sprintf("bot-%d|theme|%s", i, th)))
			chests[int(sum[11])%themeVariants] = true
			builds[int(sum[9])%themeVariants] = true
		}
		if len(chests) != themeVariants || len(builds) != themeVariants {
			t.Errorf("%s: variants never all drawn (%d chest, %d head)", th, len(chests), len(builds))
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
