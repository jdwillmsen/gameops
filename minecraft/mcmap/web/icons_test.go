package web

import (
	"bytes"
	"regexp"
	"testing"
)

// One script decides which picture a thing is drawn with and how, so the
// map, the panel, a card and a search result cannot disagree, and the
// panel has what it needs without reaching into this script's insides.
func TestOneRegistryChoosesEveryPictureAndIsOfferedToThePage(t *testing.T) {
	icons := read(t, "icons.js")
	for _, need := range []string{
		"const STYLES = ['dots', 'plates', 'large'];",
		"registry,\n    states: STATES,",
		"renditions,",
		"chosen,",
		"marker,",
		"picture,",
		"const mob = (type, ring, baby = false) => marker(`mob/${str(type)}`, ring, { baby });",
		"const addressOf = (key) => chosen(key);",
	} {
		if !bytes.Contains(icons, []byte(need)) {
			t.Errorf("icons.js no longer has %q", need)
		}
	}
	// Only what the server lists is ever asked for, whichever rendition.
	if !regexp.MustCompile("const address = \\(key\\) => \\(listing\\.pictures\\.keys\\.has\\(key\\) && KEY\\.test\\(key\\)").Match(icons) ||
		!regexp.MustCompile("const egg = \\(type\\) => \\(listing\\.mobs\\.types\\.has\\(type\\)").Match(icons) {
		t.Error("icons.js asks for a picture the server has not listed")
	}
	// A face unless the viewer would rather the egg, and whichever there
	// is where there is only one; a block only where there is no plate.
	if !bytes.Contains(icons, []byte("return (faces() ? has.face || has.egg : has.egg || has.face) || null;")) ||
		!bytes.Contains(icons, []byte("return (style === 'large' ? has.block || has.flat : has.flat || has.block) || null;")) {
		t.Error("icons.js no longer falls back from a face to an egg, or from a block to its flat picture")
	}
	// The other layers draw through it and hold no rule of their own for
	// which picture a mob or a marker has.
	for _, script := range []string{"live.js", "markers.js", "structures.js", "search.js"} {
		js := read(t, script)
		if bytes.Contains(js, []byte("api/icons/picture")) || bytes.Contains(js, []byte("api/icons/mob")) {
			t.Errorf("%s asks for a picture itself", script)
		}
	}
}

// Size is a choice apart from style, and a picture is enlarged by a whole
// number of screen pixels whatever either is.
func TestStyleAndSizeAreChosenApartAndPixelsStayWhole(t *testing.T) {
	icons := read(t, "icons.js")
	if !regexp.MustCompile(`const BOXES = \{\s*small: [^\n]+\n\s*normal: [^\n]+\n\s*large: [^\n]+\n\s*xlarge: [^\n]+\n\s*\};`).Match(icons) {
		t.Error("icons.js no longer has the four sizes of box")
	}
	if !bytes.Contains(icons, []byte("const BARE = { small: 24, normal: 32, large: 48, xlarge: 64 };")) {
		t.Error("icons.js no longer sizes a picture with no plate by the size chosen")
	}
	// Nothing drawn on the map is blended but a picture made smaller.
	if n := bytes.Count(icons, []byte("imageSmoothingEnabled = ")); n != 2 || !bytes.Contains(icons, []byte("ctx.imageSmoothingEnabled = smooth;")) {
		t.Errorf("icons.js sets smoothing in %d places; it is set where a picture is fitted and where a head is composed", n)
	}
	// A sprite is composed once and kept until the look changes.
	if !bytes.Contains(icons, []byte("let made = sprites.get(held);\n    if (!made) {")) {
		t.Error("icons.js composes a marker every time it is asked for")
	}
	page := read(t, "index.html")
	for _, need := range []string{
		`<select id="look-style" data-look="style">`, `<option value="dots">Dots</option>`, `<option value="plates">Pictures on plates</option>`, `<option value="large">Large pictures</option>`,
		`<select id="look-mob-picture" data-look="mobPicture">`, `<option value="faces">Faces</option>`, `<option value="eggs">Spawn eggs</option>`,
		`<option value="xlarge">Extra large</option>`,
	} {
		if !bytes.Contains(page, []byte(need)) {
			t.Errorf("index.html no longer has %s", need)
		}
	}
	if bytes.Contains(page, []byte("picturesLive")) || bytes.Contains(page, []byte("picturesMarkers")) {
		t.Error("index.html still offers the two switches the styles replaced")
	}
}

// What was kept, saved in a view or sent in a link before the styles is
// read as the style nearest it, and a script from before them is told
// what it asks.
func TestTheOldPicturesOrPlainChoiceBecomesAStyle(t *testing.T) {
	settings := read(t, "settings.js")
	for _, need := range []string{
		"style: oneOf('dots', 'plates', 'large'),",
		"mobPicture: oneOf('faces', 'eggs'),",
		"style: 'plates',\n    mobPicture: 'faces',",
		"const LEGACY_LOOK = { picturesLive: bool, picturesMarkers: bool };",
		"out.style = picturesLive === false ? 'dots' : 'plates';",
		"look.picturesLive = look.picturesMarkers = look.style !== 'dots';",
	} {
		if !bytes.Contains(settings, []byte(need)) {
			t.Errorf("settings.js no longer has %q", need)
		}
	}
	// Both the record and a view's own appearance are read that way.
	if n := bytes.Count(settings, []byte("look: lookOf,")); n != 2 {
		t.Errorf("settings.js reads the appearance through the migration in %d places, want the record and a view", n)
	}
	if bytes.Contains(settings, []byte("look: shape(LOOK)")) {
		t.Error("settings.js reads an appearance without the migration")
	}
	// A script that draws, from after the styles, on a page whose settings
	// are from before them, draws as the page says and throws nothing.
	icons := read(t, "icons.js")
	if !bytes.Contains(icons, []byte("if (STYLES.includes(chosen)) return chosen;\n    return look()[domain === 'live' ? 'picturesLive' : 'picturesMarkers'] === false ? 'dots' : 'plates';")) {
		t.Error("icons.js no longer stands down to pictures or plain where the settings know no style")
	}
	for _, script := range []string{"live.js", "markers.js"} {
		if !bytes.Contains(read(t, script), []byte("icons.composedAs ? icons.composedAs() : ''")) {
			t.Errorf("%s asks a script from before the styles for what only one after has", script)
		}
	}
}

// From far out everything is a dot, closer a picture, and closest a
// picture with its name; and the states a marker can be in are written
// down once.
func TestZoomTiersAndMarkerStatesAreDefinedOnce(t *testing.T) {
	icons := read(t, "icons.js")
	for _, need := range []string{
		"const PICTURES_FROM = { live: -1, markers: -2 };",
		"const LABELS_FROM = 0;",
		"if (style === 'dots' || (!always && tier(domain) === 'dot')) return null;",
		"return tier('live') === 'label' ? this.options.tag : null;",
		"hovered: { gap: 2, weight: 1.5 },",
		"selected: { gap: 4, weight: 2 },",
		"dimmed: { alpha: 0.55, dash: [3, 3] },",
	} {
		if !bytes.Contains(icons, []byte(need)) {
			t.Errorf("icons.js no longer has %q", need)
		}
	}
	// The layers are told when a tier is crossed, not at every step.
	if !regexp.MustCompile(`if \(tiered === tiers\(\)\) return;\s*tiered = tiers\(\);\s*tell\('mcmap:pictures'\);`).Match(icons) {
		t.Error("icons.js no longer tells the layers when the map crosses from dots to pictures")
	}
	markers, live := read(t, "markers.js"), read(t, "live.js")
	if !bytes.Contains(markers, []byte("(icons.states && icons.states.dimmed) || { alpha: 0.55, dash: [3, 3] }")) || !bytes.Contains(markers, []byte("icons.registry ? icons.registry.PICTURES_FROM.markers : -2")) {
		t.Error("markers.js keeps its own word for how a saved mark is dimmed, or for how far out a picture is drawn")
	}
	if !bytes.Contains(live, []byte("(icons.states && icons.states.selected.gap) || 4")) {
		t.Error("live.js keeps its own word for the ring round the inspected marker")
	}
	// A saved mark is still a faded picture in a broken ring, in every
	// style: the fading and the ring are laid over whatever is drawn.
	if !bytes.Contains(markers, []byte("worn = fade(icons.mob(str(data.k), style.color, baby));")) || !bytes.Contains(markers, []byte("ctx.setLineDash(SAVED_DASH);")) {
		t.Error("markers.js no longer draws a saved mark apart from a live one")
	}
}
