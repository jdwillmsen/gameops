package atlas

import "testing"

// Bedrock draws mushroom and nether stems as full log-like cubes, while
// melon and pumpkin stems are crossed planes.
func TestModelForStems(t *testing.T) {
	tests := map[string]string{
		"mushroom_stem":         ModelCube,
		"crimson_stem":          ModelCube,
		"warped_stem":           ModelCube,
		"stripped_crimson_stem": ModelCube,
		"stripped_warped_stem":  ModelCube,
		"melon_stem":            ModelCross,
		"pumpkin_stem":          ModelCross,
	}
	for name, want := range tests {
		if got := modelFor(name); got != want {
			t.Errorf("modelFor(%q) = %q, want %q", name, got, want)
		}
	}
}
