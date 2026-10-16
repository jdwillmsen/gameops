package atlas

import (
	"image/color"
	"testing"
)

func tgaHeader(typ byte, w, h int, bpp, desc byte) []byte {
	return []byte{0, 0, typ, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(w), byte(w >> 8), byte(h), byte(h >> 8), bpp, desc}
}

func TestDecodeTGAUncompressedBottomLeftOrigin(t *testing.T) {
	// Pixels are stored BGRA, bottom row first when bit 5 of the descriptor is clear.
	data := append(tgaHeader(2, 2, 2, 32, 8),
		0, 0, 255, 255, 0, 255, 0, 255, // bottom row: red, green
		255, 0, 0, 255, 255, 255, 255, 128, // top row: blue, half-transparent white
	)
	img, err := decodeTGA(data)
	if err != nil {
		t.Fatal(err)
	}
	want := map[[2]int]color.NRGBA{
		{0, 1}: {255, 0, 0, 255},
		{1, 1}: {0, 255, 0, 255},
		{0, 0}: {0, 0, 255, 255},
		{1, 0}: {255, 255, 255, 128},
	}
	for p, c := range want {
		if got := img.NRGBAAt(p[0], p[1]); got != c {
			t.Errorf("pixel %v = %v, want %v", p, got, c)
		}
	}
}

func TestDecodeTGATopLeftOrigin24Bit(t *testing.T) {
	data := append(tgaHeader(2, 2, 1, 24, 0x20), 0, 0, 255, 255, 0, 0)
	img, err := decodeTGA(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := img.NRGBAAt(0, 0); got != (color.NRGBA{255, 0, 0, 255}) {
		t.Errorf("pixel 0 = %v", got)
	}
	if got := img.NRGBAAt(1, 0); got != (color.NRGBA{0, 0, 255, 255}) {
		t.Errorf("pixel 1 = %v", got)
	}
}

func TestDecodeTGARunLength(t *testing.T) {
	// One run packet repeating green three times, then a raw packet of one blue pixel.
	data := append(tgaHeader(10, 4, 1, 24, 0x20),
		0x82, 0, 255, 0,
		0x00, 255, 0, 0,
	)
	img, err := decodeTGA(data)
	if err != nil {
		t.Fatal(err)
	}
	for x := 0; x < 3; x++ {
		if got := img.NRGBAAt(x, 0); got != (color.NRGBA{0, 255, 0, 255}) {
			t.Errorf("pixel %d = %v", x, got)
		}
	}
	if got := img.NRGBAAt(3, 0); got != (color.NRGBA{0, 0, 255, 255}) {
		t.Errorf("pixel 3 = %v", got)
	}
}

func TestDecodeTGARejectsUnsupported(t *testing.T) {
	for name, data := range map[string][]byte{
		"truncated header": {0, 0, 2},
		"colour mapped":    tgaHeader(1, 1, 1, 8, 0),
		"short pixel data": append(tgaHeader(2, 2, 2, 32, 8), 1, 2, 3),
	} {
		if _, err := decodeTGA(data); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestStripJSONComments(t *testing.T) {
	in := `// header
{"a": "x // not a comment", /* block
comment */ "b": [1, 2,], "c": "/* nor this */",}`
	got := string(stripJSONComments([]byte(in)))
	var v struct {
		A string `json:"a"`
		B []int  `json:"b"`
		C string `json:"c"`
	}
	if err := unmarshalLoose([]byte(in), &v); err != nil {
		t.Fatalf("unmarshalLoose: %v (stripped: %q)", err, got)
	}
	if v.A != "x // not a comment" || len(v.B) != 2 || v.C != "/* nor this */" {
		t.Fatalf("decoded %+v", v)
	}
}
