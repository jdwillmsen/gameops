package blueprint

import "strings"

// Rotate maps a cell of a w by d footprint through r degrees clockwise, so
// a rotated footprint stays anchored at the same minimum corner.
func Rotate(x, z, w, d, r int) (int, int) {
	switch r % 360 {
	case 90:
		return d - 1 - z, x
	case 180:
		return w - 1 - x, d - 1 - z
	case 270:
		return z, w - 1 - x
	default:
		return x, z
	}
}

// RotateStates returns a copy of a block's states turned with the block.
func RotateStates(in map[string]any, r int) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	turns := (r % 360) / 90
	if turns == 0 {
		return out
	}
	for k, v := range out {
		baseKey := strings.TrimPrefix(k, "minecraft:")
		switch baseKey {
		case "facing_direction", "direction", "weirdo_direction", "cardinal_direction", "lever_direction", "rail_direction", "pillar_axis":
			out[k] = rotateState(baseKey, v, turns)
		}
	}
	return out
}

func rotateState(key string, v any, n int) any {
	if s, ok := v.(string); ok {
		switch key {
		case "pillar_axis":
			if n%2 == 1 && (s == "x" || s == "z") {
				if s == "x" {
					return "z"
				}
				return "x"
			}
			return s
		case "cardinal_direction":
			arr := []string{"south", "west", "north", "east"}
			return arr[(indexString(arr, s)+n)%4]
		case "lever_direction":
			switch s {
			case "up_north_south":
				if n%2 == 1 {
					return "up_east_west"
				}
				return s
			case "up_east_west":
				if n%2 == 1 {
					return "up_north_south"
				}
				return s
			case "down_north_south":
				if n%2 == 1 {
					return "down_east_west"
				}
				return s
			case "down_east_west":
				if n%2 == 1 {
					return "down_north_south"
				}
				return s
			default:
				wallCycle := []string{"north", "east", "south", "west"}
				for i, d := range wallCycle {
					if d == s {
						return wallCycle[(i+n)%4]
					}
				}
				return s
			}
		case "rail_direction":
			return rotateRail(s, n)
		}
		return s
	}
	if num, ok := asInt(v); ok {
		switch key {
		case "facing_direction":
			cycle := []int{2, 5, 3, 4}
			for i, direction := range cycle {
				if direction == num {
					return cycle[(i+n)%4]
				}
			}
			return num
		case "direction":
			return (num + n) % 4
		case "weirdo_direction":
			cycle := []int{0, 2, 1, 3}
			for i, d := range cycle {
				if d == num {
					return cycle[(i+n)%4]
				}
			}
			return num
		case "rail_direction":
			return rotateRailInt(num, n)
		}
	}
	return v
}

// asInt widens every integer width the NBT and JSON loaders hand out, so
// no numeric state slips past rotation and keeps its old facing.
func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		return int(n), true
	}
	return 0, false
}

func rotateRail(s string, n int) string {
	if s == "north_south" || s == "east_west" {
		if n%2 == 1 {
			if s == "north_south" {
				return "east_west"
			}
			return "north_south"
		}
		return s
	}
	ascCycle := []string{"ascending_north", "ascending_east", "ascending_south", "ascending_west"}
	for i, d := range ascCycle {
		if d == s {
			return ascCycle[(i+n)%4]
		}
	}
	curveCycle := []string{"south_east", "south_west", "north_west", "north_east"}
	for i, d := range curveCycle {
		if d == s {
			return curveCycle[(i+n)%4]
		}
	}
	return s
}

// rotateRailInt rotates the numeric rail_direction the works field and
// imported structures use: 0 north-south, 1 east-west, 2-5 ascending
// east, west, north and south, 6-9 the curves south-east, south-west,
// north-west and north-east. One turn is clockwise, matching rotate():
// east (+x) becomes south (+z).
func rotateRailInt(d, n int) int {
	switch {
	case d == 0 || d == 1:
		if n%2 == 1 {
			return 1 - d
		}
		return d
	case d >= 2 && d <= 5:
		asc := []int{2, 5, 3, 4} // ascending east, south, west, north in clockwise order
		for i, v := range asc {
			if v == d {
				return asc[(i+n)%4]
			}
		}
	case d >= 6 && d <= 9:
		return 6 + (d-6+n)%4
	}
	return d
}

func indexString(arr []string, v string) int {
	for i, s := range arr {
		if s == v {
			return i
		}
	}
	return 0
}
