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

func TestModelForPlantsTheGameDrawsCrossed(t *testing.T) {
	for _, name := range []string{"cave_vines_head_with_berries", "cave_vines_body_with_berries", "open_eyeblossom", "closed_eyeblossom", "pale_hanging_moss"} {
		if got := modelFor(name); got != ModelCross {
			t.Errorf("modelFor(%q) = %q, want cross", name, got)
		}
	}
}
