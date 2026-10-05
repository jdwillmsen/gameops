package skin

import (
	"encoding/base64"
	"errors"
	"image/color"
	"testing"
)

// canvas is a synthetic skin: every pixel one colour, to be painted on.
func canvas(w, h int, c rgba) []byte {
	px := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		copy(px[i*4:], c.bytes())
	}
	return px
}

func paint(px []byte, width, x, y, w, h int, c rgba) {
	for dy := range h {
		for dx := range w {
			copy(px[((y+dy)*width+x+dx)*4:], c.bytes())
		}
	}
}

var (
	elsewhere = rgba{250, 0, 250, 255}
	skinTone  = rgba{200, 150, 100, 255}
	hair      = rgba{40, 20, 0, 255}
	clear     = rgba{0, 0, 0, 0}
)

func at(t *testing.T, w, h int, px []byte, x, y int) color.NRGBA {
	t.Helper()
	head, err := Head(w, h, px)
	if err != nil {
		t.Fatal(err)
	}
	return head.NRGBAAt(x, y)
}

func TestHeadIsTheFaceWithTheHatOverIt(t *testing.T) {
	for _, size := range [][2]int{{64, 32}, {64, 64}, {128, 128}, {256, 256}} {
		w, h := size[0], size[1]
		scale := w / 64
		px := canvas(w, h, elsewhere)
		paint(px, w, 8*scale, 8*scale, 8*scale, 8*scale, skinTone)
		// A hat that covers only the top row of the face.
		paint(px, w, 40*scale, 8*scale, 8*scale, 8*scale, clear)
		paint(px, w, 40*scale, 8*scale, 8*scale, 1*scale, hair)

		head, err := Head(w, h, px)
		if err != nil {
			t.Fatalf("%dx%d: %v", w, h, err)
		}
		if got := head.Bounds().Dx(); got != 8*scale || head.Bounds().Dy() != got {
			t.Errorf("%dx%d: head is %dx%d, want %d a side", w, h, got, head.Bounds().Dy(), 8*scale)
		}
		last := 8*scale - 1
		for _, p := range [][2]int{{0, 0}, {last, scale - 1}} {
			if got := head.NRGBAAt(p[0], p[1]); got != (color.NRGBA{hair.r, hair.g, hair.b, 255}) {
				t.Errorf("%dx%d: pixel %v is %v, want the hat", w, h, p, got)
			}
		}
		for _, p := range [][2]int{{0, scale}, {last, last}} {
			if got := head.NRGBAAt(p[0], p[1]); got != (color.NRGBA{skinTone.r, skinTone.g, skinTone.b, 255}) {
				t.Errorf("%dx%d: pixel %v is %v, want the face", w, h, p, got)
			}
		}
	}
}

func TestHeadBlendsAHalfClearHatAndIsAlwaysSolid(t *testing.T) {
	px := canvas(64, 64, clear)
	paint(px, 64, 8, 8, 8, 8, rgba{100, 100, 100, 0})
	paint(px, 64, 40, 8, 8, 8, rgba{200, 200, 200, 128})
	got := at(t, 64, 64, px, 3, 3)
	if got.A != 255 {
		t.Errorf("head pixel has alpha %d; a face with holes in it shows the map through", got.A)
	}
	if got.R < 148 || got.R > 152 {
		t.Errorf("a half-clear hat over the face gives %d, want about 150", got.R)
	}
}

// What arrives is whatever another player's client chose to send.
func TestHeadRefusesWhatIsNotAClassicSkin(t *testing.T) {
	for name, c := range map[string]struct {
		w, h, n int
	}{
		"an unknown size":                        {100, 100, 100 * 100 * 4},
		"a size with no face in it":              {4, 4, 4 * 4 * 4},
		"zero":                                   {0, 0, 0},
		"negative":                               {-64, -64, 64 * 64 * 4},
		"too large":                              {512, 512, 512 * 512 * 4},
		"an enormous claim":                      {1 << 15, 1 << 15, 16},
		"fewer bytes than declared":              {64, 64, 64*64*4 - 1},
		"more bytes than declared":               {64, 64, 64*64*4 + 4},
		"a legacy size with a full skin's bytes": {64, 32, 64 * 64 * 4},
		"nothing at all":                         {64, 64, 0},
	} {
		head, err := Head(c.w, c.h, make([]byte, c.n))
		if !errors.Is(err, ErrNotASkin) || head != nil {
			t.Errorf("%s (%dx%d, %d bytes): head %v, err %v", name, c.w, c.h, c.n, head != nil, err)
		}
	}
}

func TestHeadOfTheAgentsOwnSkin(t *testing.T) {
	s := For("fwb-server-agent")
	raw, err := base64.StdEncoding.DecodeString(s.Data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Head(s.Width, s.Height, raw); err != nil {
		t.Errorf("the agent's own skin has no head: %v", err)
	}
}
