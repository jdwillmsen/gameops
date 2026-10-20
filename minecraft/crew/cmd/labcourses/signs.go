package main

// Name-tagged armor stands stand in for signs: Bedrock has no console command
// that writes sign text, and the server ignores block placement from a bot
// client, so a sign could only be written by hand in game.
type label struct {
	x, z int
	text string
}

const (
	westText    = "§b<< WEST§r  Crew courses: physics, shapes, pads; trees beyond"
	eastText    = "§aEAST >>§r  Crew test grounds: work yards; works to the north"
	coursesText = "§bCREW COURSES§r  x -294..-151, z -500..-357"
	groundsText = "§aCREW TEST GROUNDS§r  x 150..293, z -500..-357"
	treesText   = "§2CREW TREES§r  x -444..-301, z -500..-357"
	worksText   = "§6CREW WORKS§r  x 150..293, z -700..-557  (rails, boats, arena, climbing)"
)

// The credit faces the spawn road; the rest stand inside each gate and at
// each field's end of the gate paths (z -440..-436 and z -402..-398).
var labels = []label{
	{-1, -402, "§6Medieval Hub Spawn§r - map by Jusser (Planet Minecraft)"},
	{-66, -403, westText}, {-66, -441, westText},
	{66, -403, eastText}, {66, -441, eastText},
	{-148, -403, coursesText}, {-148, -441, coursesText},
	{148, -403, groundsText}, {148, -441, groundsText},
	{-297, -403, treesText}, {-297, -441, treesText},
	{worksPathX + 2, -505, worksText},
}

// buildSigns removes the stand in each label's block before summoning its
// replacement, so a rebuild never stacks a second one.
func buildSigns(b *builder) {
	for _, l := range labels {
		b.raw("kill @e[type=armor_stand,x=%d,y=%d,z=%d,dx=0,dy=1,dz=0]", l.x, ground+1, l.z)
		b.raw("summon armor_stand %d %d %d 0 0 minecraft:entity_spawned \"%s\"", l.x, ground+1, l.z, l.text)
	}
}

func signsAreas() []box {
	var out []box
	for _, r := range []box{{-297, -441, 148, -402}, {worksPathX + 2, -505, worksPathX + 2, -505}} {
		out = append(out, tickingAreas(r)...)
	}
	return out
}
