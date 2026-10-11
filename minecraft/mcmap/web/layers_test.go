package web

import (
	"bytes"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := fs.ReadFile(FS, name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A row's label, its note and a group's title may all carry a name from the
// world, which a player chose. The panel builds every node itself and sets
// its text, so it may use none of the ways the DOM has to parse a string.
func TestLayerPanelNeverHandsTextOverAsMarkup(t *testing.T) {
	js := read(t, "layers.js")
	if !regexp.MustCompile(`const node = document\.createElement\(tag\);[^}]*node\.textContent = text;`).Match(js) {
		t.Fatal("layers.js no longer builds its nodes with textContent")
	}
	for _, sink := range []string{
		"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "DOMParser", "createContextualFragment",
		"srcdoc", "eval(", "new Function", "setAttribute('on", "javascript:", "fetch(",
	} {
		if bytes.Contains(js, []byte(sink)) {
			t.Errorf("layers.js uses %s", sink)
		}
	}
}

// Other layers are written against this interface, so a name dropped from
// it breaks a script that is not in this directory's history yet.
func TestLayerPanelKeepsItsRegistrationInterface(t *testing.T) {
	js := read(t, "layers.js")
	for _, need := range []string{
		"function register({ group, id, label, enabled = true, order, groupLabel",
		"get enabled()", "setCount(n)", "setNote(said)", "onToggle(fn)", "setAvailable(available)", "remove()",
		"app.layers.register = register;", "new CustomEvent('mcmap:layers')",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("layers.js no longer has %s", need)
		}
	}
	for _, group := range []string{"live", "markers", "structures", "biomes", "overlays"} {
		if !bytes.Contains(js, []byte("['"+group+"', '")) {
			t.Errorf("layers.js has no section for the %s group", group)
		}
	}
	// ready has to exist before the panel's script runs, or a script loaded
	// ahead of it has nothing to wait on.
	app := read(t, "app.js")
	if !regexp.MustCompile(`layers\.ready = new Promise\(`).Match(app) || !bytes.Contains(app, []byte("'mcmap:layers'")) {
		t.Error("app.js no longer offers layers.ready")
	}
}

// The filters each layer kept under a key of its own are read once more,
// by the script that keeps everything now, so that nobody's choices reset.
func TestLayerPanelCarriesOverTheOldFilters(t *testing.T) {
	js := read(t, "settings.js")
	for _, key := range []string{"'mcmap.live'", "'mcmap.markers'", "'mcmap.structures'"} {
		if !bytes.Contains(js, []byte(key)) {
			t.Errorf("settings.js no longer reads the choices saved under %s", key)
		}
	}
	// The one Structures switch hid these three, and still does for whoever
	// had it off.
	if !bytes.Contains(js, []byte("structures: { key: 'mcmap.structures', master: ['recorded', 'predicted', 'candidate'] },")) {
		t.Error("settings.js no longer carries the old Structures switch over to the layers it hid")
	}
	// The ids those choices were saved under are the ids the rows register.
	for script, ids := range map[string][]string{
		"live.js":       {"players", "hostile", "passive", "villager", "other"},
		"markers.js":    {"waypoints", "beds", "containers", "mobs"},
		"structures.js": {"recorded", "predicted", "candidate", "fortress", "monument", "outpost", "witch_hut"},
	} {
		body := read(t, script)
		if !bytes.Contains(body, []byte("app.layers.register({ group: '"+strings.TrimSuffix(script, ".js")+"', id")) {
			t.Errorf("%s no longer registers its rows with the panel", script)
		}
		for _, id := range ids {
			if !regexp.MustCompile(`\b` + id + `\b['\]]?: |\['` + id + `', '`).Match(body) {
				t.Errorf("%s no longer has a layer with the id %s", script, id)
			}
		}
	}
	if !bytes.Contains(js, []byte("raw.live = live || { paused: Boolean(older) && older.on === false };")) {
		t.Error("settings.js no longer reads the old Live switch, which now means paused")
	}
}

func TestPageHasOnePanelAndNoFilterRows(t *testing.T) {
	page := read(t, "index.html")
	for _, id := range []string{`id="layers"`, `id="layers-toggle"`, `id="layers-body"`, `id="refresh"`, `id="live-pause"`, `id="live-interval"`, `id="frozen"`} {
		if !bytes.Contains(page, []byte(id)) {
			t.Errorf("index.html has no element with %s", id)
		}
	}
	for _, gone := range []string{"data-live=", "data-marker=", "data-structures=", `class="chips"`} {
		if bytes.Contains(page, []byte(gone)) {
			t.Errorf("index.html still has a row of filters (%s)", gone)
		}
	}
	// The content security policy allows neither, and would drop them
	// without a word.
	for _, inline := range []string{` style="`, ` onclick=`, ` onchange=`, "<style", "<script>"} {
		if bytes.Contains(page, []byte(inline)) {
			t.Errorf("index.html has inline %s", strings.TrimSpace(inline))
		}
	}
	// The panel comes after the map it switches and before every layer
	// that puts a row in it.
	at := func(name string) int { return bytes.Index(page, []byte(`src="`+name+`"`)) }
	if at("layers.js") < at("app.js") {
		t.Error("index.html must load layers.js after app.js")
	}
	for _, layer := range []string{"live.js", "markers.js", "structures.js"} {
		if at(layer) < at("layers.js") {
			t.Errorf("index.html must load %s after layers.js", layer)
		}
	}
}

// The pace is chosen from a menu the live layer builds from its own list,
// or typed, between bounds it states: the shortest is the server's own
// pace, and whatever was kept before the menu changed is still a pace.
func TestLiveIntervalsOfferedAreTheOnesAccepted(t *testing.T) {
	js := read(t, "live.js")
	m := regexp.MustCompile(`const INTERVALS = \[([0-9, ]+)\];`).FindSubmatch(js)
	if m == nil {
		t.Fatal("live.js no longer lists its intervals")
	}
	if string(m[1]) != "1, 2, 5, 10, 30, 60, 300" {
		t.Errorf("live.js offers intervals %s, want 1, 2, 5, 10, 30, 60, 300", m[1])
	}
	for _, need := range []string{
		"const MIN_INTERVAL = 1;",
		"const MAX_INTERVAL = 86_400;",
		"presets: INTERVALS,",
		"const paceOf = (kept) => (Number.isFinite(kept) ? Math.min(MAX_INTERVAL, Math.max(MIN_INTERVAL, Math.round(kept))) : MIN_INTERVAL);",
		"el.interval.replaceChildren(pace.node);",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("live.js no longer has %s", need)
		}
	}
	if !bytes.Contains(read(t, "index.html"), []byte(`<span id="live-interval"></span>`)) {
		t.Error("index.html has no place for the live layer's pace")
	}
	// Pausing has to close the stream, not merely stop drawing it.
	if !regexp.MustCompile(`const want = here && !control\.paused && !document\.hidden \? here : null;\s*if \(!want\) \{\s*close\(\);`).Match(js) {
		t.Error("live.js no longer closes the stream while paused or hidden")
	}
}

// The panel's shell: beside the map where there is room for both, sized by
// its edge between two bounds, put away to a rail, and on a small screen a
// sheet that stops at three heights. Dragging is never the only way: the
// edge takes the arrow keys and the menu has three widths.
func TestLayerPanelShellIsSizedDockedAndPutAway(t *testing.T) {
	js := read(t, "layers.js")
	for _, need := range []string{
		"const DOCKED = '(min-width: 960px)';",
		"const MIN_WIDTH = 240;", "const MAX_WIDTH = 480;", "const MAX_SHARE = 0.45;",
		"const WIDTHS = [['Small', 280], ['Medium', 340], ['Large', 400]];",
		"const DETENTS = ['peek', 'half', 'full'];",
		"sizer.setAttribute('role', 'separator');", "sizer.setAttribute('aria-orientation', 'vertical');",
		"sizer.setAttribute('aria-controls', el.body.id);", "sizer.tabIndex = 0;",
		"sizer.setAttribute('aria-valuemin', String(MIN_WIDTH));", "sizer.setAttribute('aria-valuemax', String(widest()));",
		"sizer.setAttribute('aria-valuenow', String(width));",
		"if (e.key === 'ArrowLeft') to = width + STEP;", "else if (e.key === 'Home') to = MIN_WIDTH;", "else if (e.key === 'End') to = widest();",
		"sizer.addEventListener('dblclick', () => setWidth(null, true));",
		"sizer.setPointerCapture(e.pointerId);", "grab.setPointerCapture(e.pointerId);",
		// A drag is drawn once a frame and ends however the pointer is lost.
		"dragging.frame = requestAnimationFrame(() => {", "pulling.frame = requestAnimationFrame(() => {",
		"for (const end of ['pointerup', 'pointercancel', 'lostpointercapture']) sizer.addEventListener(end, dropped);",
		"for (const end of ['pointerup', 'pointercancel', 'lostpointercapture']) grab.addEventListener(end, released);",
		"addEventListener('blur', () => {",
		// What a full sheet covers is out of reach until it is lower.
		"const covered = now === 'sheeted' && open && shell.detent === 'full';", "under.inert = covered;",
		// The width is kept where a record-keeping script from before the
		// panel had one will not drop it.
		"retain('panel', 'shell', {",
		"menu.setAttribute('role', 'menu');", "item.setAttribute('role', chosen ? 'menuitemradio' : 'menuitem');",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("layers.js no longer has %s", need)
		}
	}
	css := read(t, "style.css")
	for _, need := range []string{
		"--w: clamp(240px, var(--panel-width), min(480px, 45vw));",
		"--rail: 40px;",
		".layers { --rail: 44px; }",
		"max-width: calc(100% - var(--dock) - 2rem);",
		".layers.open.docked { width: var(--w); }",
		".detent-peek .panel { height: calc(var(--head-h) + 28px); }",
		".detent-full .panel { height: 100%; border-radius: 0; }",
		// The sheet takes the touches that land on it and no others.
		"pointer-events: none;", ".layers > * { pointer-events: auto; }",
		`:root[data-motion="full"] .layers:not(.sizing) .panel { transition: height 160ms ease-out; }`,
		`:root:not([data-motion]) .layers:not(.sizing) .panel { transition: height 160ms ease-out; }`,
		".panel-scroll { padding-bottom: env(safe-area-inset-bottom); }",
	} {
		if !bytes.Contains(css, []byte(need)) {
			t.Errorf("style.css no longer has %s", need)
		}
	}
	// The map is beside the panel, not under it: both are in the stage's
	// one row, and the map's script hears of its new size by itself.
	if !bytes.Contains(css, []byte(".stage { position: relative; flex: 1; min-height: 0; display: flex; }")) || !bytes.Contains(css, []byte("#map { flex: 1; min-width: 0;")) {
		t.Error("style.css no longer lays the map and the panel out in one row")
	}
	if !regexp.MustCompile(`new ResizeObserver\(\(\) => \{\s*if \(resized !== 0\) return;\s*resized = requestAnimationFrame\(\(\) => \{\s*resized = 0;\s*map\.invalidateSize\(\);`).Match(read(t, "app.js")) {
		t.Error("app.js no longer tells the map when its box changes size, once a frame")
	}
	// A touch on the map does not put the sheet away.
	if bytes.Contains(read(t, "compact.js"), []byte("if (panelOpen() && !within(el.layers)) shutPanel();")) {
		t.Error("compact.js shuts the layer panel on a touch outside it; below full height the map is there to be used")
	}
}

// Every line of the panel, a section's, a layer's or an item's, is made by
// one function and has the same parts. The lines are a list of checkboxes
// that open, each control what a screen reader already knows it to be, and
// not a tree widget; a checkbox says "mixed" when some of what is under it
// is hidden. No layer's script builds any of the panel itself.
func TestLayerPanelDrawsEveryLineWithOneFunction(t *testing.T) {
	js := read(t, "layers.js")
	for _, need := range []string{
		"function line(node, depth) {",
		"row.append(twist, check, pic, name, detail, count, onlyButton, more);",
		"check.setAttribute('role', 'checkbox');",
		"put(dom, 'state', state, (v) => dom.check.setAttribute('aria-checked', v));",
		"? 'mixed' : 'true';",
		"kids.setAttribute('role', 'list');",
		// A menu holds groups, items and rules, and nothing else.
		"into.setAttribute('role', 'group');", "words.setAttribute('aria-hidden', 'true');",
		"twist.setAttribute('aria-controls', kids.id);",
		"check.setAttribute('aria-labelledby', name.id);",
		"more.setAttribute('aria-haspopup', 'menu');",
		// One stop for the Tab key, moved by the arrow keys.
		"dom.check.tabIndex = v ? 0 : -1;",
		"if (e.key === 'ArrowDown') to(all[at + 1]);", "else if (e.key === 'Home') to(all[0]);", "else if (e.key === 'End') to(all[all.length - 1]);",
		"else if (e.key === 'ArrowRight') {", "} else if (e.key === 'ArrowLeft') {", "} else if (e.key === 'Enter') {",
		"} else if (/^[\\p{L}\\p{N}]$/u.test(e.key)) {",
		// What the button at the end of a line offers.
		"'Back to before'", "'Only this'", "'Show all in group'", "'Hide all in group'", "'Zoom to'", "'Go to'",
		// What is hidden is said where a shut or scrolled panel still shows it.
		"`${fmt(tally.on)} of ${fmt(tally.all)} shown`",
		"statusText.setAttribute('role', 'status');",
		"resetButton.hidden = !changed();",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("layers.js no longer has %s", need)
		}
	}
	for _, gone := range []string{"'role', 'tree'", "treeitem", "aria-selected", "type = 'checkbox'"} {
		if bytes.Contains(js, []byte(gone)) {
			t.Errorf("layers.js has %s; its lines are checkboxes that open, drawn by the page", gone)
		}
	}
	// Three depths and no more: a section, a layer, an item.
	for _, m := range regexp.MustCompile(`\bline\((\w+), (\w+)\)`).FindAllSubmatch(js, -1) {
		if d := string(m[2]); d != "1" && d != "2" && d != "depth" {
			t.Errorf("layers.js makes a line at depth %s", d)
		}
	}
	for _, m := range regexp.MustCompile(`itemLines\(\w+, (\d)`).FindAllSubmatch(js, -1) {
		if d := string(m[1]); d != "2" && d != "3" {
			t.Errorf("layers.js lists items at depth %s", d)
		}
	}
	for _, section := range []string{"players", "mobs", "markers", "structures", "biomes", "trails", "overlays"} {
		if !bytes.Contains(js, []byte("['"+section+"', '")) {
			t.Errorf("layers.js has no section for %s", section)
		}
	}
	for _, script := range []string{"live.js", "markers.js", "structures.js", "biomes.js", "trails.js", "slime.js", "chunk.js"} {
		body := read(t, script)
		if bytes.Contains(body, []byte("setBody(")) {
			t.Errorf("%s still builds controls of its own under its row", script)
		}
		if !bytes.Contains(body, []byte("app.layers.register({ group: '")) {
			t.Errorf("%s no longer registers a row with the panel", script)
		}
	}
	for script, facet := range map[string]string{
		"live.js": "app.layers.facet('live', name)", "markers.js": "app.layers.facet('markers', kind)", "structures.js": "app.layers.facet('structures', 'kinds',",
		"biomes.js": "app.layers.facet('biomes', 'items')", "trails.js": "app.layers.facet('overlays', 'trails')",
	} {
		body := read(t, script)
		// With a panel from before it listed items, each still draws.
		if !bytes.Contains(body, []byte("app.layers.facet ? "+facet)) && !bytes.Contains(body, []byte("if (app.layers.facet) return "+facet)) {
			t.Errorf("%s no longer asks the panel which of its items are shown, or no longer allows for a panel that cannot say", script)
		}
		if !bytes.Contains(body, []byte(".setItems)")) && !bytes.Contains(body, []byte("if (app.layers.facet) kindList = app.layers.register(")) {
			t.Errorf("%s gives its items to a panel without asking whether it takes them", script)
		}
	}
	// And the list of keys says how the list is worked from the keyboard.
	help := regexp.MustCompile(`(?s)<dialog id="help".*?</dialog>`).Find(read(t, "index.html"))
	for _, key := range []string{"<h3>In the layer panel</h3>", "<kbd>Space</kbd></dt><dd>Show or hide the line</dd>", "<dt>A letter</dt>"} {
		if !bytes.Contains(help, []byte(key)) {
			t.Errorf("the help no longer has %s", key)
		}
	}
	css := read(t, "style.css")
	for _, need := range []string{
		"min-height: var(--row-h);", ".d1 > .row { min-height: var(--head-h); }",
		".d2 { --inset: calc(var(--row-pad) + var(--indent)); }", ".d3 { --inset: calc(var(--row-pad) + var(--indent) * 2); }",
		`.check[aria-checked="true"], .check[aria-checked="mixed"] { background: var(--accent); border-color: var(--accent); }`,
		"border: 2px solid var(--dim);",
		".check::before { content: \"\"; position: absolute; inset: calc((var(--check) - var(--hit)) / 2 - 2px); }",
		".row:hover > .only { visibility: visible; }",
		// A line with nothing to open keeps the room of what would open it,
		// so nesting reads the right way round.
		".leaf > .row > .twist { visibility: hidden; }",
		// The panel is painted apart from the map, or a pan costs twice.
		"will-change: transform;",
	} {
		if !bytes.Contains(css, []byte(need)) {
			t.Errorf("style.css no longer has %s", need)
		}
	}
}

// Which items are shown is kept as what differs from everything showing,
// in the form the live layer always kept its own, so that hundreds of
// items cost a record nothing until some are hidden. A long list is not
// all put in the page, nothing is drawn more than once a turn, and the
// panel's search changes nothing that is kept or shown.
func TestLayerPanelKeepsOnlyWhatDiffersAndDrawsOnlyWhatChanged(t *testing.T) {
	js := read(t, "layers.js")
	for _, need := range []string{
		"retain(f.group, f.name, keptOf(f));",
		"const keptOf = (f) => (plain(f) ? null : {",
		"? { only: f.only, hidden: [], mode: 'just', just: [...f.just].slice(0, MAX_HIDDEN), ...asked(f) }",
		": { only: f.only, hidden: [...f.hidden].slice(0, MAX_HIDDEN), ...asked(f) });",
		"if (f.just !== null) return f.just.has(id);",
		"if (f.off.has(id) && !f.shown.has(id)) return false;",
		"const GRACE_DAYS = 30;",
		"const MAX_HIDDEN = 200;", "const LIST_CAP = 40;",
		"const some = row.all ? listed : listed.slice(0, LIST_CAP);",
		"Promise.resolve().then(render);",
		"if (dom.was[what] === value) return;",
		// Nothing moves under the pointer or the focus.
		"const busy = list.children.length > 0 && (list.matches(':hover') || list.contains(document.activeElement));",
		"if (at === chosenAt) return;",
		"changes[row.key] = null;",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("layers.js no longer has %s", need)
		}
	}
	search := regexp.MustCompile(`(?s)box\.addEventListener\('input', \(\) => \{.*?\n  \}\);`).Find(js)
	unsearch := regexp.MustCompile(`(?s)const unsearch = \(\) => \{.*?\n  \};`).Find(js)
	if search == nil || unsearch == nil {
		t.Fatal("layers.js no longer has its search where this test looks for it")
	}
	for _, body := range [][]byte{search, unsearch} {
		for _, write := range []string{"remember(", "retain(", "keep(", "switchRow", "changeFacet", "twist("} {
			if bytes.Contains(body, []byte(write)) {
				t.Errorf("the panel's search calls %s; it narrows the panel and changes nothing else", write)
			}
		}
	}
	settings := read(t, "settings.js")
	for _, need := range []string{
		"items: record(CHOICE, chosen, 32),",
		"const CHOICES = ['structures#kinds', 'markers#containers', 'markers#beds', 'markers#mobs', 'markers#waypoints', 'biomes#items', 'overlays#trails'];",
		// A record and a view from before the lists are brought to them.
		"if (layers[BROUGHT] === 1) return record;",
		"const kinds = fromKindSwitches(view.layers);",
		"if (view.biome !== undefined) choose('biomes#items', view.biome === null ? null : { only: view.biome, hidden: [] });",
		"RETIRED: KIND_SWITCHES.map((kind) => `structures/${kind}`),",
	} {
		if !bytes.Contains(settings, []byte(need)) {
			t.Errorf("settings.js no longer has %s", need)
		}
	}
	views := read(t, "views.js")
	for _, need := range []string{
		"if (app.layers.items) view.items = app.layers.items();",
		// Game-named choices only: no gamertag, name tag or waypoint.
		"const SHARED = ['structures#kinds', 'markers#containers', 'markers#beds', 'biomes#items', 'live#mobs'];",
		"redraw(Object.keys(next.layers).filter((key) => !RETIRED.has(key)));",
	} {
		if !bytes.Contains(views, []byte(need)) {
			t.Errorf("views.js no longer has %s", need)
		}
	}
}
