package icons

import (
	"errors"
	"fmt"
	"image"
	"image/color"
)

// Some of the samples' textures are TGA files, which the standard library
// does not read: those whose fourth channel is not how see-through a pixel
// is but something else the game's materials use it for, such as which
// parts glow. The format has no signature, so a file is taken for one only
// if every field of its header is one of the few values the samples use.
const tgaHeader = 18

var errNotTGA = errors.New("not a TGA of a kind this reads")

// decodeTGA reads a true-colour TGA, plain or run-length encoded, of 24 or
// 32 bits a pixel. The size is read from the header and held to maxSide
// before a pixel is.
func decodeTGA(raw []byte, maxSide int) (*image.NRGBA, error) {
	if len(raw) < tgaHeader {
		return nil, errNotTGA
	}
	idLength, mapType, kind := int(raw[0]), raw[1], raw[2]
	w, h := int(raw[12])|int(raw[13])<<8, int(raw[14])|int(raw[15])<<8
	depth, descriptor := raw[16], raw[17]
	if mapType != 0 || (kind != 2 && kind != 10) || (depth != 24 && depth != 32) || descriptor&0xc0 != 0 {
		return nil, errNotTGA
	}
	if w < 1 || h < 1 || w > maxSide || h > maxSide {
		return nil, fmt.Errorf("texture is %dx%d, outside 1 to %d a side", w, h, maxSide)
	}
	data := raw[min(tgaHeader+idLength, len(raw)):]
	size := int(depth / 8)
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	at := 0
	// The pixels are stored blue first, and from the bottom row up unless
	// the header says from the top; either way from one side to the other.
	put := func(n int, p []byte) {
		x, y := n%w, n/w
		if descriptor&0x20 == 0 {
			y = h - 1 - y
		}
		if descriptor&0x10 != 0 {
			x = w - 1 - x
		}
		c := color.NRGBA{p[2], p[1], p[0], 255}
		if size == 4 {
			c.A = p[3]
		}
		out.SetNRGBA(x, y, c)
	}
	for n := 0; n < w*h; {
		if kind == 2 {
			if at+size > len(data) {
				return nil, errNotTGA
			}
			put(n, data[at:at+size])
			at += size
			n++
			continue
		}
		if at >= len(data) {
			return nil, errNotTGA
		}
		packet := data[at]
		count := int(packet&0x7f) + 1
		at++
		if n+count > w*h {
			return nil, errNotTGA
		}
		if packet&0x80 != 0 {
			if at+size > len(data) {
				return nil, errNotTGA
			}
			for range count {
				put(n, data[at:at+size])
				n++
			}
			at += size
			continue
		}
		if at+count*size > len(data) {
			return nil, errNotTGA
		}
		for range count {
			put(n, data[at:at+size])
			at += size
			n++
		}
	}
	return out, nil
}
