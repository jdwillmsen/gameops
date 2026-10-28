package skin

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

var allPersonas = []Persona{
	{Role: "miner"}, {Role: "chopper"}, {Role: "hauler"}, {Role: "shaft"},
	{Role: "mender"}, {Role: "builder"}, {Role: "warrior"}, {Role: "warrior", SquadRole: "vanguard"},
	{Role: "warrior", SquadRole: "striker"}, {Role: "warrior", SquadRole: "ranger"},
}

func TestPersonaSkinsAreValidAndOpaque(t *testing.T) {
	for _, p := range allPersonas {
		s := ForPersona("Agate", p)
		raw, err := base64.StdEncoding.DecodeString(s.Data)
		if err != nil || len(raw) != Width*Height*4 {
			t.Fatalf("%+v: bad buffer (%d bytes, %v)", p, len(raw), err)
		}
		// Only the base layers must be solid; the overlays are transparent
		// unless an outfit puts something on them.
		for _, b := range []block{head, torso, rightArm, leftArm, rightLeg, leftLeg} {
			for y := b.y + b.d; y < b.y+b.d+b.h; y++ {
				for x := b.x; x < b.x+2*(b.w+b.d); x++ {
					if a := raw[(y*Width+x)*4+3]; a != 255 {
						t.Fatalf("%+v: base pixel (%d,%d) has alpha %d", p, x, y, a)
					}
				}
			}
		}
	}
}

func TestPersonaIsDeterministic(t *testing.T) {
	for _, p := range allPersonas {
		if a, b := ForPersona("Agate", p), ForPersona("Agate", p); a != b {
			t.Errorf("%+v: same unit produced two skins", p)
		}
	}
}

// Roles must look different from each other, and units of one role from each
// other, or the persona adds nothing over a name colour.
func TestPersonasAndUnitsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, p := range allPersonas {
		for _, name := range []string{"Agate", "Basalt", "Cobble"} {
			s := ForPersona(name, p)
			key := name + "/" + outfitKey(p)
			if other, dup := seen[s.Data]; dup {
				t.Errorf("%s and %s share a skin", key, other)
			}
			seen[s.Data] = key
			if other, dup := seen[s.ID]; dup {
				t.Errorf("%s and %s share a skin ID", key, other)
			}
			seen[s.ID] = key
		}
	}
}

func TestUnknownRoleFallsBackToNameSkin(t *testing.T) {
	if (Persona{Role: "wizard"}).Known() {
		t.Fatal("wizard should not be a known persona")
	}
	if got, want := ForPersona("Zed", Persona{Role: "wizard"}), For("Zed"); got != want {
		t.Error("unknown role did not fall back to the plain skin")
	}
}

func TestOverrideRoundTripAndMissing(t *testing.T) {
	dir := t.TempDir()
	if _, found, err := Override(dir, "Agate"); found || err != nil {
		t.Fatalf("missing file: found=%v err=%v", found, err)
	}
	gen := ForPersona("Agate", Persona{Role: "miner"})
	b, err := gen.PNG()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Agate.png"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, found, err := Override(dir, "Agate")
	if !found || err != nil {
		t.Fatalf("override: found=%v err=%v", found, err)
	}
	if got.Data != gen.Data {
		t.Error("PNG round trip changed the pixels")
	}
	if got.ID == gen.ID {
		t.Error("an override must carry its own ID so clients drop the cached look")
	}
}

func TestOverrideRefusesBadInput(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../x", "a/b", `a\b`, ""} {
		if _, _, err := Override(dir, name); err == nil {
			t.Errorf("name %q was accepted", name)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "Bad.png"), []byte("not a png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Override(dir, "Bad"); err == nil {
		t.Error("garbage file was accepted")
	}
}

func TestFromPNGRefusesWrongSizeBeforeDecoding(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if _, err := FromPNG(buf.Bytes()); err == nil {
		t.Error("a 32x32 PNG was accepted")
	}
}

// Units of one role must differ where armour cannot hide it: the face and the
// hands.
func TestUnitsOfARoleDifferOnFaceAndHands(t *testing.T) {
	sig := func(s Skin) string {
		raw, _ := base64.StdEncoding.DecodeString(s.Data)
		var b []byte
		for y := 8; y < 16; y++ { // face front
			b = append(b, raw[(y*Width+8)*4:(y*Width+16)*4]...)
		}
		for _, p := range []block{rightArm, leftArm} { // forearms and hands
			for y := p.y + p.d + p.h - 3; y < p.y+p.d+p.h; y++ {
				b = append(b, raw[(y*Width+p.x)*4:(y*Width+p.x+2*(p.w+p.d))*4]...)
			}
		}
		return string(b)
	}
	names := []string{"Agate", "Dusty", "Hazel", "Loam", "Pebble", "Basalt", "Ember", "Ingot"}
	for _, p := range allPersonas {
		seen := map[string]bool{}
		for _, n := range names {
			seen[sig(ForPersona(n, p))] = true
		}
		if len(seen) != len(names) {
			t.Errorf("%+v: only %d distinct face/hand looks across %d units", p, len(seen), len(names))
		}
	}
}

// The game draws the overlay layers over the base ones, so a solid hat layer
// would hide the eyes: the head's front must not be covered by the hat layer
// unless the outfit has a helm there.
func TestFaceIsNotHiddenByTheHatLayer(t *testing.T) {
	for _, p := range allPersonas {
		raw, _ := base64.StdEncoding.DecodeString(ForPersona("Agate", p).Data)
		// Hat-layer front, eye row.
		for x := 41; x < 47; x++ {
			if raw[(12*Width+x)*4+3] == 255 {
				t.Errorf("%+v: hat layer is opaque over the eyes at x=%d", p, x)
				break
			}
		}
	}
}
