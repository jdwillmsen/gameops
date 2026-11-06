package skin

import (
	"encoding/base64"
	"testing"
)

var allThemes = []string{ThemeDotablaze, ThemeNeon}

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
			if other, dup := seen[a.Data]; dup {
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
