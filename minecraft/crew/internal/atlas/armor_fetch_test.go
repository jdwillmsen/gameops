package atlas

import "testing"

func TestWantedArmourTextures(t *testing.T) {
	for p, want := range map[string]bool{
		"resource_pack/textures/models/armor/iron_1.png":              true,
		"resource_pack/textures/models/armor/iron_2.png":              true,
		"resource_pack/textures/models/armor/iron_1.texture_set.json": false,
		"resource_pack/textures/models/armor/iron_1_mers.tga":         false,
		"resource_pack/textures/models/armor/iron_baby.png":           false,
		"resource_pack/textures/models/armor/elytra.png":              false,
		"resource_pack/textures/models/armor/sub/iron_1.png":          false,
		"resource_pack/textures/models/armor/../x_1.png":              false,
		"behavior_pack/textures/models/armor/iron_1.png":              false,
	} {
		if got := wanted(p); got != want {
			t.Errorf("wanted(%q) = %v, want %v", p, got, want)
		}
	}
}
