package icons

import (
	"errors"
	"fmt"
	"image"
	"image/color"
)

// How bright each of the three sides of a block is drawn, in thousandths:
// the top lit, the left less so and the right least, which is what makes
// three flat pictures read as a solid.
const (
	shadeTop   = 980
	shadeLeft  = 800
	shadeRight = 608

	// maxBlockUnits is the longest side a block may have: a bed is 32.
	maxBlockUnits = 32
)

// Block is a box drawn from above and to one side, the way the game draws
// a block in a slot: its top, and the two sides that face the viewer.
type Block struct {
	// W, H and D are the box in texture pixels: across the left side, up,
	// and across the right side.
	W int `json:"w"`
	H int `json:"h"`
	D int `json:"d"`
	// Top is W by D with the edge it shares with the left side at the
	// bottom, Left is W by H and Right is D by H.
	Top   []Layer `json:"t"`
	Left  []Layer `json:"l"`
	Right []Layer `json:"r"`
}

func (b *Block) check() error {
	if b.W < 1 || b.H < 1 || b.D < 1 || b.W > maxBlockUnits || b.H > maxBlockUnits || b.D > maxBlockUnits || (b.W+b.D)%2 != 0 || b.D%2 != 0 {
		return fmt.Errorf("a block of %dx%dx%d", b.W, b.H, b.D)
	}
	if err := checkLayers(b.Top, b.W, b.D); err != nil {
		return err
	}
	if err := checkLayers(b.Left, b.W, b.H); err != nil {
		return err
	}
	return checkLayers(b.Right, b.D, b.H)
}

func shade(c color.NRGBA, thousandths int) color.NRGBA {
	return color.NRGBA{uint8(int(c.R) * thousandths / 1000), uint8(int(c.G) * thousandths / 1000), uint8(int(c.B) * thousandths / 1000), c.A}
}

// isometric draws a box from its three sides. Two pixels across to one
// down is the slope of every edge, so each pixel of a side is a pixel of
// the picture moved down by half its distance along, and each pixel of the
// top is two side by side: nothing is blended and no pixel is invented.
// The picture is W+D wide and half that, plus H, tall.
func isometric(top, left, right *image.NRGBA, w, h, d int) *image.NRGBA {
	width, rise := w+d, (w+d)/2
	out := image.NewNRGBA(image.Rect(0, 0, width, rise+h))
	for x := range width {
		// Where the side in this column begins: the top's lower edge,
		// which falls from the left corner to the front one and rises
		// again to the right.
		var edge int
		side, column, shadeOf := left, x, shadeLeft
		if x < w {
			edge = d/2 + (x+1)/2
		} else {
			side, column, shadeOf = right, x-w, shadeRight
			edge = w/2 + (width-x)/2
		}
		for y := range h {
			if c := side.NRGBAAt(column, y); c.A != 0 {
				out.SetNRGBA(x, edge+y, shade(c, shadeOf))
			}
		}
		// And where the top in this column begins: its upper edge, which
		// rises from the left corner to the back one and falls again.
		upper := (d - 1 - x) / 2
		if x >= d {
			upper = (x - d) / 2
		}
		for y := upper; y < edge; y++ {
			// How far this pixel is along each of the top's two edges
			// from the front corner, which is at (w, rise).
			across, down := float64(x)+0.5-float64(w), float64(rise)-(float64(y)+0.5)
			a, b := down-across/2, down+across/2
			tx, ty := w-1-min(max(int(a), 0), w-1), d-1-min(max(int(b), 0), d-1)
			if c := top.NRGBAAt(tx, ty); c.A != 0 {
				out.SetNRGBA(x, y, shade(c, shadeTop))
			}
		}
	}
	return out
}

func (b *Block) render(textures map[string]*image.NRGBA) (*image.NRGBA, error) {
	// A block's own textures are one pixel to the unit; a pack drawn
	// finer gives a picture finer by the same whole number.
	scale := 0
	side := func(layers []Layer, w, h int) (*image.NRGBA, error) {
		tex := textures[layers[0].Texture]
		if tex == nil {
			return nil, fmt.Errorf("texture %s is not held", layers[0].Texture)
		}
		k, err := scaleOf(layers[0], tex)
		if err != nil {
			return nil, err
		}
		if scale == 0 {
			scale = k
		}
		if k != scale {
			return nil, errors.New("a block's sides are drawn at different resolutions")
		}
		return compose(layers, w, h, scale, textures, layers[0].Texture, false)
	}
	top, err := side(b.Top, b.W, b.D)
	if err != nil {
		return nil, err
	}
	left, err := side(b.Left, b.W, b.H)
	if err != nil {
		return nil, err
	}
	right, err := side(b.Right, b.D, b.H)
	if err != nil {
		return nil, err
	}
	img := isometric(top, left, right, b.W*scale, b.H*scale, b.D*scale)
	// A bell's sides are mostly air round the bell.
	if !covered(img, img.Rect, 8) {
		return nil, errImplausible
	}
	return img, nil
}
