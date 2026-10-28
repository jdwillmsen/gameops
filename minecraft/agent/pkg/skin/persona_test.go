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
	{Role: "warrior"}, {Role: "warrior", SquadRole: "vanguard"},
	{Role: "warrior", SquadRole: "striker"}, {Role: "warrior", SquadRole: "ranger"},
}

func TestPersonaSkinsAreValidAndOpaque(t *testing.T) {
	for _, p := range allPersonas {
		s := ForPersona("Agate", p)
		raw, err := base64.StdEncoding.DecodeString(s.Data)
		if err != nil || len(raw) != Width*Height*4 {
			t.Fatalf("%+v: bad buffer (%d bytes, %v)", p, len(raw), err)
		}
		for i := 3; i < len(raw); i += 4 {
			if raw[i] != 255 {
				t.Fatalf("%+v: pixel %d has alpha %d", p, i/4, raw[i])
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
