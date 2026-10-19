package atlas

import (
	"encoding/binary"
	"hash/crc32"
	"image"
	"testing"
)

// hugePNGHeader is a signature and IHDR claiming a vast image, with no pixel
// data behind it.
func hugePNGHeader(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr, w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6
	out := []byte("\x89PNG\r\n\x1a\n")
	out = binary.BigEndian.AppendUint32(out, 13)
	chunk := append([]byte("IHDR"), ihdr...)
	out = append(out, chunk...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(chunk))
}

func hugeTGAHeader(w, h int) []byte {
	hd := make([]byte, 18)
	hd[2], hd[16] = 2, 32
	hd[12], hd[13], hd[14], hd[15] = byte(w), byte(w>>8), byte(h), byte(h>>8)
	return hd
}

func TestLoadImageRefusesHugeHeadersBeforeAllocating(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "textures/a.png", string(hugePNGHeader(1<<30, 1<<30)))
	writeFile(t, root, "textures/b.tga", string(hugeTGAHeader(65535, 65535)))
	writeFile(t, root, "textures/c.png", string(hugePNGHeader(maxTextureSide+1, 1)))
	b := &builder{dir: root, images: map[string]*image.NRGBA{}, formats: map[string]int{}}
	for _, p := range []string{"textures/a", "textures/b", "textures/c"} {
		if b.loadImage(p) != nil {
			t.Errorf("%s was loaded", p)
		}
	}
	if _, err := decodeTGA(hugeTGAHeader(65535, 65535)); err == nil {
		t.Error("decodeTGA accepted a huge header")
	}
}
