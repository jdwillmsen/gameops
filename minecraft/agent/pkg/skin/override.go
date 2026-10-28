package skin

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxOverrideBytes bounds a hand-made skin file; a 64x64 PNG is a few KiB, so
// anything near this is not a skin.
const maxOverrideBytes = 256 << 10

// Override looks for a hand-made skin for a unit: <dir>/<name>.png, 64x64.
//
// It is the update path. Painting a skin and dropping the file in the
// directory replaces the generated outfit on the unit's next join, with no
// rebuild. found is false when no file exists, which is not an error: most
// units wear the generated outfit.
func Override(dir, name string) (s Skin, found bool, err error) {
	if dir == "" {
		return Skin{}, false, nil
	}
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return Skin{}, false, fmt.Errorf("skin: unit name %q cannot name a file", name)
	}
	path := filepath.Join(dir, name+".png")
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Skin{}, false, nil
	}
	if err != nil {
		return Skin{}, false, fmt.Errorf("skin: open override: %w", err)
	}
	defer func() { _ = f.Close() }()

	var buf bytes.Buffer
	n, err := buf.ReadFrom(io.LimitReader(f, maxOverrideBytes+1))
	if err != nil {
		return Skin{}, false, fmt.Errorf("skin: read override: %w", err)
	}
	if n > maxOverrideBytes {
		return Skin{}, false, fmt.Errorf("skin: override %s is larger than %d bytes", name, maxOverrideBytes)
	}
	s, err = FromPNG(buf.Bytes())
	if err != nil {
		return Skin{}, false, fmt.Errorf("skin: override %s: %w", name, err)
	}
	return s, true, nil
}

// FromPNG turns a 64x64 PNG into a Skin. The ID is derived from the pixels, so
// editing the file changes the ID and clients that cache by ID fetch the new
// look instead of showing the old one.
func FromPNG(b []byte) (Skin, error) {
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return Skin{}, fmt.Errorf("not a PNG: %w", err)
	}
	if bnd := img.Bounds(); bnd.Dx() != Width || bnd.Dy() != Height {
		return Skin{}, fmt.Errorf("is %dx%d, want %dx%d", bnd.Dx(), bnd.Dy(), Width, Height)
	}
	rgba := image.NewNRGBA(image.Rect(0, 0, Width, Height))
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
	sum := sha256.Sum256(rgba.Pix)
	return Skin{
		ID:     fmt.Sprintf("%x-custom", sum[:8]),
		Data:   base64.StdEncoding.EncodeToString(rgba.Pix),
		Width:  Width,
		Height: Height,
	}, nil
}

// PNG encodes a skin as a PNG, so a generated outfit can be exported, edited
// and dropped back in as an override.
func (s Skin) PNG() ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(s.Data)
	if err != nil {
		return nil, err
	}
	if len(raw) != s.Width*s.Height*4 {
		return nil, fmt.Errorf("skin data is %d bytes, want %d", len(raw), s.Width*s.Height*4)
	}
	img := &image.NRGBA{Pix: raw, Stride: s.Width * 4, Rect: image.Rect(0, 0, s.Width, s.Height)}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
