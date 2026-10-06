package slime

import (
	"strings"
	"testing"
)

// The generator is the standard one, so the standard's own first numbers
// have to come out of it.
func TestFirstIsTheMersenneTwistersFirstNumber(t *testing.T) {
	for seed, want := range map[uint32]uint32{0: 2357136044, 1: 1791095845, 5489: 3499211612} {
		if got := first(seed); got != want {
			t.Errorf("first(%d) = %d, want %d", seed, got, want)
		}
	}
}

// vectors is every chunk from -8 to 7 on both axes, north at the top and
// west on the left: row z, column x, # a slime chunk. Another
// implementation, such as the page's, is right when it draws the same.
const vectors = `
...............#
..#.............
............#...
........#.......
#.............#.
...............#
.....#..##......
#...##..........
..#....#...#....
...........#....
....#...........
.#..............
#.......#.......
..............#.
.#......#.......
............#...`

func TestChunkDrawsTheKnownGrid(t *testing.T) {
	rows := strings.Fields(vectors)
	for z := int32(-8); z < 8; z++ {
		for x := int32(-8); x < 8; x++ {
			if want := rows[z+8][x+8] == '#'; Chunk(x, z) != want {
				t.Errorf("chunk %d, %d: slime %v, want %v", x, z, !want, want)
			}
		}
	}
}

func TestChunkAgreesWithKnownChunks(t *testing.T) {
	for _, c := range []struct {
		x, z int32
		want bool
	}{
		// The four the slime finder this was checked against is tested with.
		{-1, 0, true}, {109, 3, true}, {0, 0, false}, {110, 3, false},
		// Chunks of the FWB world holding slimes below y 40 and outside a
		// trial chamber, on 2026-10-05.
		{-80, 48, true}, {-128, 155, true}, {-33, 49, true}, {-35, 53, true}, {129, 5, true},
		{13, 14, true}, {247, -256, true}, {65, 103, true},
		// The ends of the range, where a signed multiply would differ.
		{2_000_000, -2_000_000, false}, {-2147483648, 2147483647, false},
	} {
		if got := Chunk(c.x, c.z); got != c.want {
			t.Errorf("chunk %d, %d: slime %v, want %v", c.x, c.z, got, c.want)
		}
	}
}

func TestOneChunkInTenIsASlimeChunk(t *testing.T) {
	n := 0
	for z := int32(-200); z < 200; z++ {
		for x := int32(-200); x < 200; x++ {
			if Chunk(x, z) {
				n++
			}
		}
	}
	if n != 16304 {
		t.Errorf("%d of 160,000 chunks, want 16,304", n)
	}
}
