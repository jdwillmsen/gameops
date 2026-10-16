package atlas

import (
	"errors"
	"fmt"
	"image"
)

// decodeTGA reads the uncompressed and run-length truecolour variants, which
// are the only ones the vanilla pack uses. The standard library has no TGA
// decoder and the format is small enough not to warrant a dependency.
func decodeTGA(data []byte) (*image.NRGBA, error) {
	if len(data) < 18 {
		return nil, errors.New("tga: truncated header")
	}
	idLen, cmapType, typ := int(data[0]), data[1], data[2]
	w := int(data[12]) | int(data[13])<<8
	h := int(data[14]) | int(data[15])<<8
	bpp, desc := int(data[16]), data[17]
	if cmapType != 0 || (typ != 2 && typ != 10) {
		return nil, fmt.Errorf("tga: unsupported type %d (colour map %d)", typ, cmapType)
	}
	if bpp != 24 && bpp != 32 {
		return nil, fmt.Errorf("tga: unsupported depth %d", bpp)
	}
	if w == 0 || h == 0 {
		return nil, errors.New("tga: empty image")
	}
	bytesPer := bpp / 8
	pix := data[min(18+idLen, len(data)):]

	raw := make([]byte, 0, w*h*bytesPer)
	if typ == 2 {
		if len(pix) < w*h*bytesPer {
			return nil, errors.New("tga: short pixel data")
		}
		raw = pix[:w*h*bytesPer]
	} else {
		for len(raw) < w*h*bytesPer {
			if len(pix) == 0 {
				return nil, errors.New("tga: short pixel data")
			}
			head := pix[0]
			pix = pix[1:]
			count := int(head&0x7f) + 1
			if head&0x80 != 0 {
				if len(pix) < bytesPer {
					return nil, errors.New("tga: short pixel data")
				}
				for i := 0; i < count; i++ {
					raw = append(raw, pix[:bytesPer]...)
				}
				pix = pix[bytesPer:]
			} else {
				if len(pix) < count*bytesPer {
					return nil, errors.New("tga: short pixel data")
				}
				raw = append(raw, pix[:count*bytesPer]...)
				pix = pix[count*bytesPer:]
			}
		}
		raw = raw[:w*h*bytesPer]
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	topFirst := desc&0x20 != 0
	rightFirst := desc&0x10 != 0
	for i := 0; i < w*h; i++ {
		x, y := i%w, i/w
		if !topFirst {
			y = h - 1 - y
		}
		if rightFirst {
			x = w - 1 - x
		}
		p := raw[i*bytesPer:]
		a := byte(255)
		if bytesPer == 4 {
			a = p[3]
		}
		o := img.PixOffset(x, y)
		img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = p[2], p[1], p[0], a
	}
	return img, nil
}
