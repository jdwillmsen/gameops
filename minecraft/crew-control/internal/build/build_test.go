package build

import (
	"encoding/base64"
	"encoding/binary"
	"slices"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

// rle encodes (run, index) pairs the way a unit does.
func rle(pairs ...int) string {
	var b []byte
	for i := 0; i+1 < len(pairs); i += 2 {
		b = binary.AppendUvarint(b, uint64(pairs[i]))
		b = append(b, byte(pairs[i+1]))
	}
	return base64.StdEncoding.EncodeToString(b)
}

func observed(size [3]int32, palette []string, cells string) work.Blocks {
	return work.Blocks{Min: [3]int32{0, 64, 0}, Size: size, Palette: palette, Cells: cells}
}

func TestDecodeExpandsRunLengthCodedCells(t *testing.T) {
	g, err := Decode(observed([3]int32{3, 1, 2}, []string{"air", "cobblestone"}, rle(2, 1, 4, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if g.Size != [3]int{3, 1, 2} || len(g.Palette) != 2 {
		t.Fatalf("grid = %+v", g)
	}
	if want := []byte{1, 1, 0, 0, 0, 0}; !slices.Equal(g.Cells, want) {
		t.Errorf("cells = %v, want %v", g.Cells, want)
	}
}

func TestDecodeHandlesRunsLongerThan127(t *testing.T) {
	g, err := Decode(observed([3]int32{300, 1, 1}, []string{"dirt"}, rle(300, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Cells) != 300 || slices.ContainsFunc(g.Cells, func(b byte) bool { return b != 0 }) {
		t.Fatalf("cells = %v, want one 300-long run of dirt", g.Cells)
	}
}

func TestDecodeRefusesWhatDoesNotFillTheBox(t *testing.T) {
	palette := []string{"air"}
	cases := map[string]work.Blocks{
		"size axis zero":   observed([3]int32{0, 1, 1}, palette, rle(1, 0)),
		"size axis huge":   observed([3]int32{2147483647, 2147483647, 2147483647}, palette, rle(1, 0)),
		"size over cells":  observed([3]int32{5000, 1, 1}, palette, rle(1, 0)),
		"empty palette":    observed([3]int32{1, 1, 1}, nil, rle(1, 0)),
		"cells not base64": observed([3]int32{1, 1, 1}, palette, "###"),
		"cells short":      observed([3]int32{2, 1, 1}, palette, rle(1, 0)),
		"cells long":       observed([3]int32{2, 1, 1}, palette, rle(3, 0)),
		"index outside":    observed([3]int32{1, 1, 1}, palette, rle(1, 5)),
		"zero run":         observed([3]int32{1, 1, 1}, palette, rle(0, 0)),
		"truncated pair":   observed([3]int32{1, 1, 1}, palette, base64.StdEncoding.EncodeToString([]byte{2})),
		"no cells at all":  observed([3]int32{1, 1, 1}, palette, ""),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(b); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func grid(size [3]int, palette []string, cells ...byte) Grid {
	return Grid{Size: size, Palette: palette, Cells: cells}
}

func TestComparePassesASoundBuild(t *testing.T) {
	expected := grid([3]int{2, 2, 1}, []string{"air", "cobblestone"}, 0, 0, 1, 1)
	ok, mismatches, unknown := Compare(expected, expected, 0)
	if !ok || mismatches != 0 || unknown != 0 {
		t.Fatalf("ok %v, %d mismatches, %d unknown", ok, mismatches, unknown)
	}
}

func TestCompareCountsOneMismatch(t *testing.T) {
	expected := grid([3]int{2, 2, 1}, []string{"air", "cobblestone"}, 0, 0, 1, 1)
	observed := grid([3]int{2, 2, 1}, []string{"air", "cobblestone"}, 0, 0, 0, 1)
	if ok, mismatches, unknown := Compare(expected, observed, 0); ok || mismatches != 1 || unknown != 0 {
		t.Fatalf("ok %v, %d mismatches, %d unknown", ok, mismatches, unknown)
	}
}

func TestCompareAllowsMismatchesWithinTolerance(t *testing.T) {
	expected := grid([3]int{5, 2, 1}, []string{"air", "cobblestone"}, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	observed := grid([3]int{5, 2, 1}, []string{"air", "cobblestone"}, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0)
	if ok, m, _ := Compare(expected, observed, 0.2); !ok || m != 2 {
		t.Fatalf("two of ten off a fifth: ok %v, %d mismatches", ok, m)
	}
	observed.Cells[8] = 1
	if ok, m, _ := Compare(expected, observed, 0.2); ok || m != 3 {
		t.Fatalf("three of ten off a fifth: ok %v, %d mismatches", ok, m)
	}
}

func TestCompareTreatsUnknownAsAirButAsNothingElse(t *testing.T) {
	expected := grid([3]int{2, 2, 1}, []string{"air", "cobblestone"}, 0, 0, 1, 1)
	// The unreadable cells sit over the blueprint's air: the server never
	// streams an all-air sub-chunk, so this is what a sound build reports.
	observed := grid([3]int{2, 2, 1}, []string{"unknown", "cobblestone"}, 0, 0, 1, 1)
	if ok, m, u := Compare(expected, observed, 0); !ok || m != 0 || u != 2 {
		t.Fatalf("unknown over air: ok %v, %d mismatches, %d unknown", ok, m, u)
	}
	// The same unreadable cells over solid blocks are defects the unit may
	// only have failed to see, so they count.
	solid := grid([3]int{2, 2, 1}, []string{"air", "cobblestone"}, 1, 1, 1, 1)
	if ok, m, u := Compare(solid, observed, 0); ok || m != 2 || u != 2 {
		t.Fatalf("unknown over cobblestone: ok %v, %d mismatches, %d unknown", ok, m, u)
	}
}

func TestCompareFailsAGridOfAnotherSize(t *testing.T) {
	expected := grid([3]int{2, 2, 1}, []string{"air"}, 0, 0, 0, 0)
	observed := grid([3]int{2, 1, 1}, []string{"air"}, 0, 0)
	if ok, m, u := Compare(expected, observed, 1); ok || m != 0 || u != 0 {
		t.Fatalf("ok %v, %d mismatches, %d unknown", ok, m, u)
	}
}
