// Package icons is the artwork the live layer draws in place of dots: one
// icon per mob type, fetched from Mojang's published samples at runtime and
// never shipped with this service, and one head per online player, cropped
// by the agent from the skin the game server sent it.
package icons

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
)

var errNotPNG = errors.New("not a PNG")

// Clean decodes a PNG that came from outside and encodes it again, so that
// what a browser is given is this service's own encoding of the pixels and
// nothing else the file carried. The header is read first and the pixels
// only if the dimensions are within bounds, since a few bytes can declare
// an image of any size.
func Clean(raw []byte, minSide, maxSide int, square bool) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || format != "png" {
		return nil, errNotPNG
	}
	if cfg.Width < minSide || cfg.Height < minSide || cfg.Width > maxSide || cfg.Height > maxSide {
		return nil, fmt.Errorf("image is %dx%d, outside %d to %d a side", cfg.Width, cfg.Height, minSide, maxSide)
	}
	if square && cfg.Width != cfg.Height {
		return nil, fmt.Errorf("image is %dx%d, not square", cfg.Width, cfg.Height)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errNotPNG
	}
	plain := image.NewNRGBA(img.Bounds())
	draw.Draw(plain, plain.Bounds(), img, img.Bounds().Min, draw.Src)
	var out bytes.Buffer
	if err := png.Encode(&out, plain); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
