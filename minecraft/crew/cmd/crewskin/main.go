// Command crewskin renders and exports unit skins.
//
//	crewskin export -name Agate -role miner -o Agate.png
//	crewskin sheet -o roster.png [-names Agate,Basalt,...]
//
// export writes the 64x64 skin a unit would wear, which is the starting point
// for a hand-painted override: edit it, drop it in the unit's SKIN_DIR as
// <name>.png, and the unit wears it from its next join. sheet draws every
// outfit front and back, enlarged, to judge a change before it ships.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"strings"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/skin"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "export":
		err = export(os.Args[2:])
	case "sheet":
		err = sheet(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "crewskin:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: crewskin export -name N -role R [-squad-role S] -o FILE | crewskin sheet -o FILE [-names A,B]")
	os.Exit(2)
}

func export(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	name := fs.String("name", "", "unit name")
	role := fs.String("role", "", "miner, chopper, hauler, shaft or warrior")
	squad := fs.String("squad-role", "", "vanguard, striker or ranger (warriors)")
	out := fs.String("o", "", "output PNG")
	_ = fs.Parse(args)
	if *name == "" || *out == "" {
		usage()
	}
	p := skin.Persona{Role: *role, SquadRole: *squad}
	if !p.Known() {
		return fmt.Errorf("no outfit for role %q squad-role %q", *role, *squad)
	}
	b, err := skin.ForPersona(*name, p).PNG()
	if err != nil {
		return err
	}
	return os.WriteFile(*out, b, 0o644)
}

var roster = []skin.Persona{
	{Role: "miner"}, {Role: "chopper"}, {Role: "hauler"}, {Role: "shaft"},
	{Role: "mender"}, {Role: "builder"},
	{Role: "warrior", SquadRole: "vanguard"}, {Role: "warrior", SquadRole: "striker"}, {Role: "warrior", SquadRole: "ranger"},
}

func sheet(args []string) error {
	fs := flag.NewFlagSet("sheet", flag.ExitOnError)
	out := fs.String("o", "", "output PNG")
	names := fs.String("names", "Agate,Basalt,Cobble", "unit names to draw per outfit")
	scale := fs.Int("scale", 8, "pixel scale")
	_ = fs.Parse(args)
	if *out == "" {
		usage()
	}
	list := strings.Split(*names, ",")
	const cellW, cellH = 40, 36
	img := image.NewNRGBA(image.Rect(0, 0, cellW*len(list)*1, cellH*len(roster)))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.NRGBA{40, 44, 52, 255}), image.Point{}, draw.Src)
	for r, p := range roster {
		for c, n := range list {
			sk := skin.ForPersona(strings.TrimSpace(n), p)
			raw, err := skinPixels(sk)
			if err != nil {
				return err
			}
			ox, oy := c*cellW+2, r*cellH+2
			figure(img, raw, ox, oy, false)
			figure(img, raw, ox+18, oy, true)
		}
	}
	big := image.NewNRGBA(image.Rect(0, 0, img.Bounds().Dx()**scale, img.Bounds().Dy()**scale))
	for y := 0; y < big.Bounds().Dy(); y++ {
		for x := 0; x < big.Bounds().Dx(); x++ {
			big.SetNRGBA(x, y, img.NRGBAAt(x / *scale, y / *scale))
		}
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return png.Encode(f, big)
}

func skinPixels(s skin.Skin) (*image.NRGBA, error) {
	b, err := s.PNG()
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	n := image.NewNRGBA(img.Bounds())
	draw.Draw(n, n.Bounds(), img, image.Point{}, draw.Src)
	return n, nil
}

// figure draws the front (or back) of a skin as the game stands the model up,
// each overlay layer drawn over its base.
func figure(dst, sk *image.NRGBA, ox, oy int, back bool) {
	cut := func(sx, sy, w, h, dx, dy int) {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.SetNRGBA(ox+dx+x, oy+dy+y, sk.NRGBAAt(sx+x, sy+y))
			}
		}
	}
	if !back {
		cut(8, 8, 8, 8, 4, 0)
		cut(20, 20, 8, 12, 4, 8)
		cut(44, 20, 4, 12, 0, 8)
		cut(36, 52, 4, 12, 12, 8)
		cut(4, 20, 4, 12, 4, 20)
		cut(20, 52, 4, 12, 8, 20)
		return
	}
	cut(24, 8, 8, 8, 4, 0)
	cut(32, 20, 8, 12, 4, 8)
	cut(52, 20, 4, 12, 12, 8)
	cut(44, 52, 4, 12, 0, 8)
	cut(12, 20, 4, 12, 8, 20)
	cut(28, 52, 4, 12, 4, 20)
}
