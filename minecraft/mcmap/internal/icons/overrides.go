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
	// over is further models of the definition laid over the first, each
	// with its textures.
	over []overlay
	// rect is the face as a rectangle of the first of textures, in units,
	// for a mob whose model gives no box to take it from.
	rect  *[4]int
	units [2]int
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
	"ghast":          {why: "it has no head: its body is its face, and its tentacles hang far below", alone: true},
	"happy_ghast":    {why: "its body is its face, and the first texture its controller lists is its young's", textures: []string{"happy_ghast"}, alone: true},
	"slime":          {why: "it has no head: the face is on the core its jelly surrounds", bone: "cube"},
	"magma_cube":     {why: "it has no head: its face is the fronts of the eight slices it is cut into", bone: "insideCube"},
	"sulfur_cube":    {why: "it has no head: the cube is all there is", bone: "sulfur_cube", alone: true},
	"silverfish":     {why: "a row of segments with no face; from the side it is a grey sliver too long to read at a marker's size", egg: true},
	"endermite":      {why: "a row of segments with no face, and all of one purple", egg: true},
	"cod":            {why: "a fish is known by its side, which is 17 pixels long and 4 tall: a line at a marker's size", egg: true},
	"salmon":         {why: "as the cod, and 25 pixels long", egg: true},
	"tropicalfish":   {why: "its colours are laid on by the game, so its texture is a grey fish", egg: true},
	"creaking":       {why: "its face is black but for eyes the game lights from another texture", egg: true},
	"dolphin":        {why: "its front is a pale blank; it is known by its beak, from the side", egg: true},
	"frog":           {why: "its head is a flat lip, and its eyes are on another bone above it", egg: true},
	"allay":          {why: "its head's texture is drawn by a see-through material and comes out in pieces", egg: true},
	"bat":            {why: "its head is four pixels wide", egg: true},
	"turtle":         {why: "its eyes are on the sides of its head: the front is a plain green square", egg: true},
	"pufferfish":     {why: "its smallest form is three pixels across; the largest is the one that looks like a pufferfish", geometry: "large", textures: []string{"default"}, bone: "body", alone: true},
	"horse":          {why: "its head is a narrow box with its eyes on the sides: from the front it is a post, and from the side a bar with an eye", egg: true},
	"donkey":         {why: "as the horse", egg: true},
	"mule":           {why: "as the horse", egg: true},
	"skeleton_horse": {why: "as the horse", egg: true},
	"zombie_horse":   {why: "as the horse", egg: true},
	"sheep":          {why: "its head is two boxes, the bare face and the wool round it, in two models", geometry: "sheared", textures: []string{"default"}, over: []overlay{{"default", []string{"default"}}}},
	"wither":         {why: "its controller picks the pale skin of one just summoned, and others draw the armour it wears only when half dead", textures: []string{"default"}, bone: "head1"},
	"shulker":        {why: "it is known by its shell; the head inside is seen only when it opens", textures: []string{"undyed"}, bone: "base", reach: 8, skip: []string{"head"}},
	"creeper":        {why: "its second controller draws the charge only a struck one carries", drop: []string{"controller.render.creeper_armor"}},
	"hoglin":         {why: "its head hangs down, so the face is the top of the box", face: faceUp},
	"zoglin":         {why: "as the hoglin", face: faceUp},
	"guardian":       {why: "its spikes would leave the eye a speck among them", skip: spikes},
	"elder_guardian": {why: "as the guardian", skip: spikes},
	"armadillo":      {why: "its head is three pixels wide and set at an angle", egg: true},
	"goat":           {why: "its head is set at an angle, so its front is no rectangle of the texture", egg: true},
	"parrot":         {why: "its head is two pixels wide", egg: true},
	"tadpole":        {why: "it is three pixels wide", egg: true},
}

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

// structureFaces is the mob whose face stands for a kind of structure: the
// one a player meets there and nowhere else. A kind not listed, or whose
// mob has no face, keeps the item it has always been drawn as.
var structureFaces = map[string]string{
	"fortress":      "blaze",
	"monument":      "elder_guardian",
	"outpost":       "pillager",
	"witch_hut":     "witch",
	"village":       villagerKind,
	"trial_chamber": "breeze",
}
