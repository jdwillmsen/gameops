package icons

// override is what is done for a mob whose face is not simply the front of
// the box its model calls its head. Each says why. Whatever a field leaves
// unset is worked out from the game's own files as for any other mob.
type override struct {
	why string
	// egg draws no face at all: the mob keeps its spawn egg.
	egg bool
	// geometry is the key, in the mob's definition, of the model to use,
	// and textures the keys of the textures to lay over it in order, in
	// place of what its render controllers choose.
	geometry string
	textures []string
	// swap draws one texture of the definition in place of another, which
	// is how a variant differs from the default.
	swap map[string]string
	// drop names render controllers that draw something a mob only
	// sometimes wears.
	drop []string
	// bone, cube and face pick the box and the side of it that is the
	// face; alone leaves out everything else on the head, and skip leaves
	// out the bones named.
	bone  string
	cube  *int
	face  string
	alone bool
	skip  []string
	// reach is how far from the head a box may lie and still be drawn,
	// where the half of the head's size that is usual is not enough.
	reach int
	// top keeps only that many units of the head's box from its top, and
	// with draws bones that do not hang off the head.
	top  int
	with []string
	// over is further models of the definition laid over the first, each
	// with its textures.
	over []overlay
	// rect is the face as a rectangle of the first of textures, in units,
	// for a mob whose model gives no box to take it from.
	rect  *[4]int
	units [2]int
	// profile draws the mob from its side, from the bones named in bones
	// and all that hang off them, or the whole of it; bone is then the
	// part of it that is its head.
	profile bool
	bones   []string
	// from is the side a profile is seen from, west unless it says up.
	// strip lets one be as thin as the mob is: a tadpole is a tail.
	from  string
	strip bool
	// tints is a colour by texture path, for a mob whose colours the game
	// lays on at run time and its files do not hold.
	tints map[string]string
}

// overlay is one model of a mob's definition and the textures drawn on it, by
// their keys there.
type overlay struct {
	geometry string
	textures []string
}

// overrides is by mob type. It is data and not code so that what was
// decided for each mob, and why, can be read in one place.
var overrides = map[string]override{
	"copper_golem":    {why: "the first of its controllers draws the flower it sometimes holds, on a model with no face", textures: []string{"default"}},
	"nautilus":        nautilusFace,
	"zombie_nautilus": nautilusFace,
	"sniffer":         {why: "its face is behind a beak that fills the front of its head; from the side the beak is its profile", profile: true, bones: []string{"head"}, bone: "head"},
	"hoglin":          {why: "its head is a long box hung at a slant, which alone is a bar; from the side, whole, it is a boar", profile: true, bone: "head"},
	"zoglin":          {why: "as the hoglin", profile: true, bone: "head"},
	"horse":           horseFace,
	"donkey":          horseFace,
	"mule":            horseFace,
	"skeleton_horse":  horseFace,
	"zombie_horse":    horseFace,
	"llama":           llamaFace,
	"trader_llama":    llamaFace,
	"ravager":         {why: "its head is 16 by 20 with the jaw: the top 16 are the face, the size of the box every face is shown in", top: 16, alone: true},
	"frog":            {why: "its head is a flat lip with its eyes on a bone of their own; from the side, whole, it is a frog", profile: true},
	"bat":             {why: "its head is four pixels wide; it is known by its wings, from the front", profile: true, from: faceNorth},
	"dolphin":         {why: "its front is a pale blank; from the side, whole and long, it is a dolphin", profile: true, bone: "head", strip: true},
	"turtle":          {why: "its eyes are on the sides of its head; it is known by its shell, from the side, which is long and low", profile: true, bone: "head", strip: true},
	"phantom":         {why: "its head is a strip seven pixels by three; it is known by its wings, seen from above", profile: true, from: faceUp, bone: "body"},
	"armadillo":       {why: "its head is a strip three pixels by five; it is known by its banded shell, seen from the side", profile: true},
	"ghast":           {why: "it has no head: its body is its face, and its tentacles hang far below", alone: true},
	"happy_ghast":     {why: "its body is its face, and the first texture its controller lists is its young's", textures: []string{"happy_ghast"}, alone: true},
	"slime":           {why: "it has no head: the face is on the core its jelly surrounds", bone: "cube"},
	"magma_cube":      {why: "it has no head: its face is the fronts of the eight slices it is cut into", bone: "insideCube"},
	"sulfur_cube":     {why: "it has no head: the cube is all there is", bone: "sulfur_cube", alone: true},
	"silverfish":      {why: "a row of segments with no face; it is known from the side", profile: true},
	"endermite":       {why: "a row of segments with no face; it is known from the side", profile: true},
	"cod":             {why: "a fish is known by its side", profile: true},
	"salmon":          {why: "as the cod, and long", profile: true, strip: true},
	"tropicalfish":    {why: "a fish is known by its side, and the game colours its grey texture by its variant; it is drawn as the first, the clownfish, in the game's orange and white", profile: true, tints: map[string]string{"textures/entity/fish/tropical_a": "#f9801d", "textures/entity/fish/tropical_a_pattern_1": "#f9fffe"}},
	"camel":           {why: "the largest boxes of its head bone are its neck; from the side, whole, it is known by its hump", profile: true, bone: "head"},
	"camel_husk":      {why: "as the camel", profile: true, bone: "head"},
	"creaking":        {why: "its head is a narrow trunk among branches; it is known whole, from the front", profile: true, from: faceNorth, bone: "head"},
	"pufferfish":      {why: "its smallest form is three pixels across; the largest is the one that looks like a pufferfish", geometry: "large", textures: []string{"default"}, bone: "body", alone: true},
	"sheep":           {why: "its head is two boxes, the bare face and the wool round it, in two models", geometry: "sheared", textures: []string{"default"}, over: []overlay{{"default", []string{"default"}}}},
	"wither":          {why: "its controller picks the pale skin of one just summoned, and others draw the armour it wears only when half dead", textures: []string{"default"}, bone: "head1"},
	"shulker":         {why: "it is known by its shell; the head inside is seen only when it opens", textures: []string{"undyed"}, bone: "base", reach: 8, skip: []string{"head"}},
	"creeper":         {why: "its second controller draws the charge only a struck one carries", drop: []string{"controller.render.creeper_armor"}},
	"guardian":        {why: "its spikes would leave the eye a speck among them", skip: spikes},
	"elder_guardian":  {why: "as the guardian", skip: spikes},
	"parrot":          {why: "its head is two pixels wide; it is known by its side, perched", profile: true, bone: "body", swap: map[string]string{"blue": "red_blue"}},
	"tadpole":         {why: "it is three pixels wide, and a tail from the side", profile: true, strip: true},
}

func box(n int) *int { return &n }

// A horse is known in profile, and its profile is its neck: a bone the
// model sets 30 degrees off square, which a flat view cannot draw. What is
// left is the head alone, a bar with an eye from the side and a post from
// the front.
var horseFace = override{why: "it is known by its head and neck in profile, the neck set at a slant", profile: true, bones: []string{"Neck"}, bone: "Head", swap: map[string]string{"base_white": "base_brown"}}

// A nautilus is its shell, which is a spiral only from the side.
var nautilusFace = override{why: "it is known by the spiral of its shell, which is seen from the side", profile: true}

// A llama's head and neck are one box 18 tall, with the muzzle and the ears
// set on its top 8.
var llamaFace = override{why: "its head and neck are one box: the top of it is the head", bone: "head", cube: box(1), top: 8, reach: 4}

// spikes is the twelve spikes of a guardian, which hang off its head.
var spikes = []string{"spikepart0", "spikepart1", "spikepart2", "spikepart3", "spikepart4", "spikepart5", "spikepart6", "spikepart7", "spikepart8", "spikepart9", "spikepart10", "spikepart11", "tailpart0"}

// professions is a villager's professions as the save names them, and the
// key of each one's texture in the villager's definition.
var professions = map[string]string{
	"farmer": "farmer", "fisherman": "fisherman", "shepherd": "shepherd", "fletcher": "fletcher", "librarian": "librarian",
	"cartographer": "cartographer", "cleric": "cleric", "armorer": "armorer", "weaponsmith": "weapon_smith",
	"toolsmith": "tool_smith", "butcher": "butcher", "leatherworker": "leatherworker", "mason": "stonemason", "nitwit": "nitwit",
}

// The villager whose professions are drawn, and the texture a profession's
// takes the place of.
const (
	villagerKind      = "villager_v2"
	villagerUnskilled = "unskilled"
)
