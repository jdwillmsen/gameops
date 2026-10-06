package skin

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"testing"
)

func colours(img *image.NRGBA) map[color.NRGBA]int {
	seen := map[color.NRGBA]int{}
	for y := range img.Bounds().Dy() {
		for x := range img.Bounds().Dx() {
			seen[img.NRGBAAt(x, y)]++
		}
	}
	return seen
}

// A player whose skin gave no head is drawn as a badge that is theirs: the
// same every time, and not the next player's.
func TestAvatarIsTheSameForOnePlayerAndDifferentForAnother(t *testing.T) {
	first, _ := Avatar("2535400000000001", Tints{})
	again, _ := Avatar("2535400000000001", Tints{})
	if !bytes.Equal(first.Pix, again.Pix) {
		t.Error("one player has two avatars")
	}
	seen := map[string]bool{}
	for i := range 500 {
		img, _ := Avatar(strconv.Itoa(2535400000000000+i), Tints{})
		seen[string(img.Pix)] = true
	}
	if len(seen) != 500 {
		t.Errorf("500 players share %d avatars", len(seen))
	}
}

// It goes to the map as a head does, so it is held to what the map takes:
// square, 8 to 32 pixels a side, solid, and a small PNG.
func TestAvatarIsAHeadTheMapAccepts(t *testing.T) {
	for i := range 200 {
		img, _ := Avatar(strconv.Itoa(i), Tints{Skin: color.RGBA{uint8(i), 100, 50, 255}, Hair: color.RGBA{10, uint8(i * 7), 200, 255}})
		if b := img.Bounds(); b.Dx() != AvatarSide || b.Dy() != AvatarSide || AvatarSide < minHeadSide || AvatarSide > MaxHeadSide {
			t.Fatalf("avatar is %v", b)
		}
		seen := colours(img)
		if len(seen) != 2 {
			t.Fatalf("player %d: %d colours; a badge has a ground and a mark", i, len(seen))
		}
		for c := range seen {
			if c.A != 255 {
				t.Fatalf("player %d: colour %v shows the map through", i, c)
			}
		}
		for y := range AvatarSide {
			for x := range AvatarSide / 2 {
				if img.NRGBAAt(x, y) != img.NRGBAAt(AvatarSide-1-x, y) {
					t.Fatalf("player %d: not the same left and right at %d,%d", i, x, y)
				}
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil || buf.Len() > 8<<10 {
			t.Fatalf("player %d: %d bytes of PNG, err %v", i, buf.Len(), err)
		}
	}
}

// The pattern has to show whatever colours it is drawn in.
func TestAvatarColoursAreAlwaysFarEnoughApartToRead(t *testing.T) {
	check := func(who string, tints Tints) {
		t.Helper()
		img, _ := Avatar(who, tints)
		var pair []color.NRGBA
		for c := range colours(img) {
			pair = append(pair, c)
		}
		d := brightness(pair[0]) - brightness(pair[1])
		if d < 0 {
			d = -d
		}
		if d < 40 {
			t.Errorf("player %s with %v: colours %v are %d apart", who, tints, pair, d)
		}
	}
	for i := range 2000 {
		check(strconv.Itoa(i), Tints{})
	}
	for _, skin := range []color.RGBA{{255, 224, 196, 255}, {141, 85, 36, 255}, {60, 40, 30, 255}} {
		for _, hair := range []color.RGBA{{250, 240, 190, 255}, {30, 20, 10, 255}, {128, 128, 128, 255}, {141, 85, 36, 255}, {}} {
			check("1", Tints{Skin: skin, Hair: hair})
		}
	}
}

func TestAvatarWearsThePlayersOwnColoursWhenTheyCanBeTrusted(t *testing.T) {
	skin, hair := color.RGBA{224, 172, 105, 255}, color.RGBA{40, 20, 90, 255}
	img, tinted := Avatar("1", Tints{Skin: skin, Hair: hair})
	seen := colours(img)
	if !tinted || seen[color.NRGBA{224, 172, 105, 255}] == 0 || seen[color.NRGBA{40, 20, 90, 255}] == 0 {
		t.Errorf("tinted %v, colours %v", tinted, seen)
	}
	plain, _ := Avatar("1", Tints{})

	// A skin colour with no hair is still the player's; the ground is then
	// the one their identity gives.
	img, tinted = Avatar("1", Tints{Skin: skin})
	if seen := colours(img); !tinted || seen[color.NRGBA{224, 172, 105, 255}] == 0 {
		t.Errorf("skin alone: tinted %v, colours %v", tinted, seen)
	}

	for name, bad := range map[string]Tints{
		"no colours":                       {},
		"hair with no skin":                {Hair: hair},
		"a skin that is see-through":       {Skin: color.RGBA{224, 172, 105, 128}, Hair: hair},
		"a skin with its channels swapped": {Skin: color.RGBA{105, 172, 224, 255}, Hair: hair},
		"a skin that is green":             {Skin: color.RGBA{20, 220, 20, 255}, Hair: hair},
		"a skin that is grey":              {Skin: color.RGBA{128, 128, 128, 255}, Hair: hair},
	} {
		img, tinted := Avatar("1", bad)
		if tinted || !bytes.Equal(img.Pix, plain.Pix) {
			t.Errorf("%s: drawn in the skin's colours", name)
		}
	}

	// Hair that is see-through is no colour; the skin beside it still is.
	img, tinted = Avatar("1", Tints{Skin: skin, Hair: color.RGBA{40, 20, 90, 0}})
	if seen := colours(img); !tinted || seen[color.NRGBA{40, 20, 90, 255}] != 0 {
		t.Errorf("see-through hair was drawn: %v", seen)
	}
}
