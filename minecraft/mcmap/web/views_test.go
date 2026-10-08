package web

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// A view's name is the viewer's own text, or a stranger's from a link or a
// file. It is shown as text and nowhere used as anything else: not as
// markup, not as an address, not as a selector.
func TestViewsShowEveryNameAsTextAndUseNoneAsAnythingElse(t *testing.T) {
	js := usesNoMarkupSink(t, "views.js")
	usesNoMarkupSink(t, "appearance.js")
	if !regexp.MustCompile(`const node = document\.createElement\(tag\);[^}]*node\.textContent = text;`).Match(js) {
		t.Fatal("views.js no longer builds its nodes with textContent")
	}
	for _, never := range []string{"location.assign", "location.replace", "location.href =", "location.hash =", "window.open", ".src =", "querySelector(`", "querySelectorAll(`", "fetch(", "import("} {
		if bytes.Contains(js, []byte(never)) {
			t.Errorf("views.js uses %s", never)
		}
	}
	// The one address it makes is of the file it has just written itself.
	if n := bytes.Count(js, []byte(".href = ")); n != 1 || !bytes.Contains(js, []byte("link.href = address;")) {
		t.Errorf("views.js sets an address in %d places, want only the exported file's", n)
	}
	// What is refused in a name is what would make its text lie.
	settings := read(t, "settings.js")
	for _, need := range []string{
		// Control characters, and the format class: what takes no room, and
		// what turns the direction of the writing round.
		`const UNSAFE = '\\p{Cc}\\p{Cf}\\p{Cs}\\p{Co}\\p{Zl}\\p{Zp}';`,
		"const NAME_LENGTH = 40;",
		// And no more than three accents piled on one letter.
		"const MARKS = 3;",
		"if (HAS_UNSAFE.test(said) || PILED.test(said) || said !== said.trim()) return BAD;",
		"said = said.replace(ALL_UNSAFE, '').replace(ALL_PILED, '$1').replace(/\\s+/gu, ' ').trim();",
		"if (letters.length === 0 || (strict && letters.length > NAME_LENGTH)) return BAD;",
	} {
		if !bytes.Contains(settings, []byte(need)) {
			t.Errorf("settings.js no longer has %s", need)
		}
	}
	if !regexp.MustCompile(`<input id="views-name"[^>]*maxlength="40"`).Match(read(t, "index.html")) {
		t.Error("index.html no longer caps a view's name where it is typed")
	}
}

// A link's view is read against a closed description before any of it is
// used, is never applied or saved by being opened, and is one more part of
// the address after the ones older links have.
func TestAViewInALinkIsBoundedCheckedAndOnlyOffered(t *testing.T) {
	js := read(t, "views.js")
	for _, need := range []string{
		"const MAX_LINK = 1800;",
		"if (part.length > MAX_LINK) return { error: 'it was longer than a link’s view may be' };",
		"if (!/^v1\\.[A-Za-z0-9_-]+$/.test(part)) return bad;",
		"new TextDecoder('utf-8', { fatal: true })",
		"const allowed = ['n', 'l', 'x', 'm', 'b', 't', 'i', 'g', 'p', 'q', 'a'];",
		"if (Object.keys(raw).some((key) => !allowed.includes(key))) return bad;",
		"const read = settings.check.view(view);",
		// The sixth part, so the five an older link has read as before.
		"return hash.slice(1).split('/')[5] || '';",
		// Too long to be a link is said, not cut short.
		"if (part.length > MAX_LINK) return null;",
		// Gamertags are not the viewer's to hand round.
		"if (view.mobs) out.m = { o: view.mobs.only, h: view.mobs.hidden };",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("views.js no longer has %s", need)
		}
	}
	if bytes.Contains(js, []byte("out.players")) || regexp.MustCompile(`out\.\w+ = [^;]*view\.players`).Match(js) {
		t.Error("views.js puts the hidden players in a link")
	}
	// No sentence about a refused link is made from the link.
	if regexp.MustCompile("error: `").Match(js) {
		t.Error("views.js builds a refusal from what it was given")
	}
	// Opening a link holds its view to one side; only a press shows it,
	// and saving it adds a view and replaces none.
	offer := regexp.MustCompile(`(?s)function offerFrom\(address\) \{.*?\n  \}`).Find(js)
	if offer == nil || bytes.Contains(offer, []byte("apply(")) || bytes.Contains(offer, []byte("settings.set(")) || bytes.Contains(offer, []byte("switchTo(")) {
		t.Error("views.js applies or saves a link's view as it is opened")
	}
	if !regexp.MustCompile(`el\.offerShow\.addEventListener\('click', \(\) => \{\s*if \(offered\) switchTo\(offered, true\);`).Match(js) {
		t.Error("views.js no longer shows a link's view only when asked")
	}
	if !bytes.Contains(js, []byte("views.list.push({ ...view, id, name: clean });")) {
		t.Error("views.js no longer saves a view as a new one")
	}
	// A link says which layers are on by their place in a list, and the
	// appearance settings by theirs. Either list may only grow at its end,
	// or every link already sent means something else.
	if !bytes.Contains(js, []byte(strings.Join([]string{
		"['live/players', 'Players'], ['live/hostile', 'Hostile'], ['live/passive', 'Passive'], ['live/villager', 'Villagers'], ['live/other', 'Other'],",
		"    ['markers/waypoints', 'Waypoints'], ['markers/beds', 'Beds'], ['markers/containers', 'Containers'], ['markers/mobs', 'Named mobs'],",
		"    ['structures/recorded', 'Known structures'], ['structures/predicted', 'Predicted structures'], ['structures/candidate', 'Possible structures'],",
		"    ['structures/fortress', 'Fortresses'], ['structures/monument', 'Monuments'], ['structures/outpost', 'Outposts'],",
		"    ['structures/witch_hut', 'Witch huts'], ['structures/village', 'Villages'], ['structures/spawn', 'World spawn'],",
		"    ['biomes/overlay', 'Biome overlay'], ['overlays/slime', 'Slime chunks'], ['overlays/trails', 'Trails'],",
	}, "\n"))) {
		t.Error("views.js lists the layers a link speaks of in another order; add new ones at the end only")
	}
	if !bytes.Contains(js, []byte("const LOOKS = ['theme', 'size', 'text', 'labelMobs', 'labelPlayers', 'labelWaypoints', 'picturesLive', 'picturesMarkers',\n    'opacityBiomes', 'opacityTrails', 'opacitySlime', 'density', 'motion', 'coords'")) {
		t.Error("views.js lists the appearance settings a link speaks of in another order; add new ones at the end only")
	}
	// The map's own script reads an address that names no dimension as no
	// place at all, which is what a view saved without one sends.
	app := read(t, "app.js")
	if !bytes.Contains(app, []byte("if (!Object.hasOwn(LABELS, id) || n.some((v) => !Number.isFinite(v))) return null;")) {
		t.Error("app.js no longer reads only a dimension it has as a place")
	}
	if !bytes.Contains(js, []byte("`${view.place.d}/${view.place.x}/${view.place.z}/${view.place.zoom}` : '-/0/0/0';")) {
		t.Error("views.js no longer sends a view without a place as no place")
	}
}

// A file is read as strictly as a link, and its views are added beside the
// viewer's own.
func TestSettingsFilesAreReadStrictlyAndReplaceNoView(t *testing.T) {
	js := read(t, "views.js")
	for _, need := range []string{
		"const MAX_FILE = 300_000;",
		"if (file.size > MAX_FILE) {",
		"if (Object.keys(doc).some((key) => !['app', 'kind', 'v', 'exported', 'record'].includes(key))) return bad;",
		"const record = settings.check.record(doc.record);",
		"if (next.record.views.list.length > settings.MAX_VIEWS) {",
		"views: { ...mine, list: [...mine.list, ...fresh], order: [...mine.order, ...fresh.map((view) => view.id)], start: mine.start, active: null },",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("views.js no longer has %s", need)
		}
	}
	settings := read(t, "settings.js")
	if !bytes.Contains(settings, []byte("const read = shape({ v: int(VERSION, VERSION), ...PORTABLE })(v, true);")) {
		t.Error("settings.js no longer reads a whole record strictly")
	}
	// Nothing is taken from a file until the viewer has said so.
	if !regexp.MustCompile(`el\.incomingTake\.addEventListener\('click', take\);`).Match(js) || bytes.Count(js, []byte("settings.replace(")) != 1 {
		t.Error("views.js no longer waits to be told before it takes a file's settings")
	}
}

// Three views come with the page. They are not the viewer's to delete, and
// a view is switched to with every row set before any layer redraws.
func TestViewsComeBuiltInAndSwitchAtAStroke(t *testing.T) {
	settings := read(t, "settings.js")
	if !bytes.Contains(settings, []byte("const BUILT_IN = ['everything', 'exploring', 'base'];")) {
		t.Error("settings.js no longer has its three built-in views")
	}
	for _, name := range []string{"name: 'Everything',", "name: 'Exploring',", "name: 'Base',"} {
		if !bytes.Contains(settings, []byte(name)) {
			t.Errorf("settings.js no longer has a built-in view with %s", name)
		}
	}
	// The default view is in the record before any script reads its part.
	if !regexp.MustCompile(`const first = state\.views\.start \? find\(state\.views\.start\) : null;\s*if \(first\) \{\s*adopt\(first, true\);`).Match(settings) {
		t.Error("settings.js no longer applies the default view before the page's scripts start")
	}
	js := read(t, "views.js")
	for _, need := range []string{
		"if (builtIn) act('Hide', 'hide', () => hide(view.id, true));\n    else act('Delete', 'delete', () => remove(view.id));",
		"say(`Deleted “${view.name}”.`, true);",
		"el.undo.addEventListener('click', restore);",
		// A number picks a view, but not while a name is being typed.
		"if (e.target instanceof Element && e.target.matches('input, textarea, select')) return;",
		"const missing = app.layers.adopt(layers);",
		"if (el.open.offsetParent === null) el.more.focus();",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("views.js no longer has %s", need)
		}
	}
	panel := read(t, "layers.js")
	if !regexp.MustCompile(`row\.on = on;\s*row\.box\.checked = on;\s*for \(const fn of row\.listeners\) told\.set\(fn, on\);\s*\}\s*\}\s*for \(const \[fn, on\] of told\) \{`).Match(panel) {
		t.Error("layers.js no longer switches every row before it tells any layer, each layer once")
	}
	if !bytes.Contains(panel, []byte("return wanted.filter((key) => !found.has(key));")) {
		t.Error("layers.js no longer says which of a view's layers it has no row for")
	}
	// The shortcut is one of the page's own, listed with the rest.
	if !bytes.Contains(read(t, "menu.js"), []byte("v: () => press('views-open'),")) {
		t.Error("menu.js no longer opens the views with V")
	}
	page := read(t, "index.html")
	for _, need := range []string{
		`<dt><kbd>V</kbd></dt>`, `id="views-open"`, `id="appearance-open"`, `<dialog id="views" class="sheet" aria-labelledby="views-title">`,
		`<ol id="views-list"`, `id="views-undo"`, `id="views-place"`, `id="views-look"`, `id="views-offer"`, `id="views-export"`, `id="views-file" type="file"`,
	} {
		if !bytes.Contains(page, []byte(need)) {
			t.Errorf("index.html no longer has %s", need)
		}
	}
	// Each button is hidden as the page is sent and shown by its own
	// script, so a page whose scripts are from before offers neither.
	for _, button := range []string{` hidden>Views</button>`, ` hidden>Appearance</button>`} {
		if !bytes.Contains(page, []byte(button)) {
			t.Errorf("index.html no longer sends its button hidden:%s", button)
		}
	}
	if !bytes.Contains(js, []byte("el.open.hidden = false;")) || !bytes.Contains(read(t, "appearance.js"), []byte("el.open.hidden = false;")) {
		t.Error("the Views or the Appearance button is no longer shown by its script")
	}
	// On a small screen both buttons are in More, with everything else the
	// bar holds.
	more := page[bytes.Index(page, []byte(`<div id="more" class="more">`)):bytes.Index(page, []byte("</header>"))]
	if !bytes.Contains(more, []byte(`id="views-open"`)) || !bytes.Contains(more, []byte(`id="appearance-open"`)) {
		t.Error("index.html no longer has Views and Appearance inside More")
	}
	at := func(name string) int { return bytes.Index(page, []byte(`src="`+name+`"`)) }
	for _, before := range []string{"layers.js", "live.js", "trails.js", "biomes.js", "chunk.js", "menu.js"} {
		if at("views.js") < at(before) {
			t.Errorf("index.html must load views.js after %s", before)
		}
	}
	if at("compact.js") < at("views.js") {
		t.Error("index.html must load compact.js after views.js, so that it knows of every dialog")
	}
}
