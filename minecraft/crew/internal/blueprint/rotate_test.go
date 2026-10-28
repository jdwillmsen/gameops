package blueprint

import "testing"

func TestRotateStates(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		rot      int
		expected map[string]any
	}{
		{
			name:     "cardinal and minecraft:cardinal",
			input:    map[string]any{"cardinal_direction": "north", "minecraft:cardinal_direction": "south"},
			rot:      90,
			expected: map[string]any{"cardinal_direction": "east", "minecraft:cardinal_direction": "west"},
		},
		{
			name:     "facing direction",
			input:    map[string]any{"facing_direction": 2}, // north
			rot:      90,
			expected: map[string]any{"facing_direction": 5}, // east
		},
		{
			name:     "weirdo direction",
			input:    map[string]any{"weirdo_direction": 0}, // east
			rot:      90,
			expected: map[string]any{"weirdo_direction": 2}, // south
		},
		{
			name:     "weirdo direction 180",
			input:    map[string]any{"weirdo_direction": 0}, // east
			rot:      180,
			expected: map[string]any{"weirdo_direction": 1}, // west
		},
		{
			name:     "lever floor and wall",
			input:    map[string]any{"lever_direction": "up_north_south"},
			rot:      90,
			expected: map[string]any{"lever_direction": "up_east_west"},
		},
		{
			name:     "lever wall 180",
			input:    map[string]any{"lever_direction": "north"},
			rot:      180,
			expected: map[string]any{"lever_direction": "south"},
		},
		{
			name:     "rail flat and curved",
			input:    map[string]any{"rail_direction": "north_south"},
			rot:      90,
			expected: map[string]any{"rail_direction": "east_west"},
		},
		{
			name:     "rail ascending",
			input:    map[string]any{"rail_direction": "ascending_north"},
			rot:      90,
			expected: map[string]any{"rail_direction": "ascending_east"},
		},
		{
			name:     "rail curved 180",
			input:    map[string]any{"rail_direction": "south_east"},
			rot:      180,
			expected: map[string]any{"rail_direction": "north_west"},
		},
		{
			name:     "pillar axis",
			input:    map[string]any{"pillar_axis": "x"},
			rot:      90,
			expected: map[string]any{"pillar_axis": "z"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := RotateStates(tc.input, tc.rot)
			for k, want := range tc.expected {
				if got := res[k]; got != want {
					t.Errorf("key %q = %v, want %v", k, got, want)
				}
			}
		})
	}
}

// Bedrock's rail_direction is a number 0-9, the encoding the works field
// and imported structures use. Every value must move clockwise, where one
// quarter turn maps east to south.
func TestRotateRailInt(t *testing.T) {
	tests := []struct {
		start, turn, want int
	}{
		// 0 north-south and 1 east-west swap on odd turns
		{0, 0, 0}, {0, 1, 1}, {0, 2, 0}, {0, 3, 1},
		{1, 0, 1}, {1, 1, 0}, {1, 2, 1}, {1, 3, 0},
		// 2-5 ascending east, west, north, south
		{2, 0, 2}, {2, 1, 5}, {2, 2, 3}, {2, 3, 4},
		{3, 0, 3}, {3, 1, 4}, {3, 2, 2}, {3, 3, 5},
		{4, 0, 4}, {4, 1, 2}, {4, 2, 5}, {4, 3, 3},
		{5, 0, 5}, {5, 1, 3}, {5, 2, 4}, {5, 3, 2},
		// 6-9 curves south-east, south-west, north-west, north-east
		{6, 0, 6}, {6, 1, 7}, {6, 2, 8}, {6, 3, 9},
		{7, 0, 7}, {7, 1, 8}, {7, 2, 9}, {7, 3, 6},
		{8, 0, 8}, {8, 1, 9}, {8, 2, 6}, {8, 3, 7},
		{9, 0, 9}, {9, 1, 6}, {9, 2, 7}, {9, 3, 8},
	}
	for _, tc := range tests {
		if got := rotateRailInt(tc.start, tc.turn); got != tc.want {
			t.Errorf("rotateRailInt(%d, %d turn) = %d, want %d", tc.start, tc.turn, got, tc.want)
		}
	}
}

// Rotating a full turn must land every rotated state key back on its
// starting value, in both the numeric and string encodings.
func TestRotateStatesFourTurnsIdentity(t *testing.T) {
	values := map[string][]any{
		"facing_direction":   {0, 1, 2, 3, 4, 5},
		"direction":          {0, 1, 2, 3},
		"weirdo_direction":   {0, 1, 2, 3},
		"cardinal_direction": {"north", "east", "south", "west"},
		"lever_direction": {"up_north_south", "up_east_west", "down_north_south",
			"down_east_west", "north", "east", "south", "west"},
		"rail_direction": {0, 1, 2, 3, 4, 5, 6, 7, 8, 9,
			"north_south", "east_west",
			"ascending_north", "ascending_east", "ascending_south", "ascending_west",
			"south_east", "south_west", "north_west", "north_east"},
		"pillar_axis": {"x", "y", "z"},
	}
	for key, vals := range values {
		for _, v := range vals {
			cur := v
			for range 4 {
				cur = rotateState(key, cur, 1)
			}
			if cur != v {
				t.Errorf("%s %v: four quarter turns ended at %v", key, v, cur)
			}
		}
	}
}
