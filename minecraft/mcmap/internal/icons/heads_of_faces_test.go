package icons

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

// blank is a synthetic PNG of nothing, a side pixels square.
func blank(t *testing.T, side int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, side, side))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// faced is a face w by h units whose head is the main box, made of one
// layer of a made-up texture.
func faced(w, h int, main [4]int) Recipe {
	return Recipe{W: w, H: h, Main: main, Layers: []Layer{{Texture: "textures/entity/made_up", Units: [2]int{64, 64}, Src: [4]int{0, 0, w, h}, Dst: [4]int{0, 0, w, h}, Skin: true}}}
}

func TestHead_IsWhereRenderPutsTheHeadInTheSquare(t *testing.T) {
	for _, c := range []struct {
		name   string
		recipe Recipe
		side   int
		want   [4]int
		ok     bool
	}{
		// Eight wide in a square of eleven: one column to the left and two to the right.
		{"a head over a row of nose", faced(8, 11, [4]int{0, 0, 8, 10}), 11, [4]int{1, 0, 8, 10}, true},
		{"the same at twice the texture's size", faced(8, 11, [4]int{0, 0, 8, 10}), 22, [4]int{3, 0, 16, 20}, true},
		{"a head under a hat and inside its brim", faced(10, 14, [4]int{1, 4, 8, 10}), 14, [4]int{3, 4, 8, 10}, true},
		{"a head with ears either side", faced(14, 7, [4]int{3, 1, 8, 5}), 14, [4]int{3, 4, 8, 5}, true},
		{"a head that is the whole of it", faced(8, 8, [4]int{0, 0, 8, 8}), 8, [4]int{0, 0, 8, 8}, true},
		{"a picture of some other size", faced(8, 11, [4]int{0, 0, 8, 10}), 16, [4]int{}, false},
		{"an item, which has no head", Recipe{Flat: "textures/items/made_up"}, 16, [4]int{}, false},
		{"an item drawn small in its square", func() Recipe { r := faced(16, 16, [4]int{0, 0, 16, 16}); r.Sparse = true; return r }(), 16, [4]int{}, false},
	} {
		got, ok := c.recipe.head(c.side)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: head(%d) = %v, %v; want %v, %v", c.name, c.side, got, ok, c.want, c.ok)
		}
	}
}

// What render makes is what head describes: the two are worked out apart,
// and a head that render put elsewhere would be centred on nothing.
func TestHead_AgreesWithThePictureRenderMakes(t *testing.T) {
	recipe := faced(8, 11, [4]int{0, 0, 8, 10})
	skin := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := range 11 {
		for x := range 8 {
			skin.Pix[skin.PixOffset(x, y)+3] = 255
			skin.Pix[skin.PixOffset(x, y)] = uint8(20 * x)
			skin.Pix[skin.PixOffset(x, y)+1] = uint8(20 * y)
		}
	}
	img, err := recipe.render(map[string]*image.NRGBA{"textures/entity/made_up": skin})
	if err != nil {
		t.Fatal(err)
	}
	box, ok := recipe.head(img.Rect.Dx())
	if !ok {
		t.Fatalf("no head in a face %d a side", img.Rect.Dx())
	}
	// Every pixel of the head's box is drawn, and the column either side of it is not.
	for y := box[1]; y < box[1]+box[3]; y++ {
		for x := box[0]; x < box[0]+box[2]; x++ {
			if img.NRGBAAt(x, y).A == 0 {
				t.Fatalf("the head's box %v has nothing at %d,%d", box, x, y)
			}
		}
	}
	if img.NRGBAAt(box[0]-1, 0).A != 0 || img.NRGBAAt(box[0]+box[2], 0).A != 0 {
		t.Errorf("the head's box %v is not where the picture has its head", box)
	}
}

func TestHeads_AreOfThePicturesThatAreFaces(t *testing.T) {
	face := faced(8, 11, [4]int{0, 0, 8, 10})
	stood := face
	stood.Else = "textures/items/made_up"
	m := &Mobs{Ref: "v9.9.9"}
	m.set(Set{
		Pictures: map[string][]byte{
			"face/" + villagerKind: blank(t, 11),
			// The village drawn as its mob's face, and the outpost as the item standing in for one.
			"structure/village": blank(t, 11),
			"structure/outpost": blank(t, 16),
			"face/pillager":     blank(t, 11),
			"container/chest":   blank(t, 16),
			"face/damaged":      []byte("not a picture"),
		},
		Recipes: map[string]Recipe{
			"face/" + villagerKind: face,
			"structure/village":    stood,
			"structure/outpost":    stood,
			"face/pillager":        face,
			"face/damaged":         face,
			"face/unmade":          face,
		},
	})
	got := m.Heads()
	want := [4]int{1, 0, 8, 10}
	for _, key := range []string{"face/" + villagerKind, "structure/village", "face/pillager"} {
		if got[key] != want {
			t.Errorf("%s: head %v, want %v", key, got[key], want)
		}
	}
	if len(got) != 3 {
		t.Errorf("heads of %v; an item, a picture standing in, one unmade and one unreadable have none", got)
	}
}
