package web

import (
	"bytes"
	"io/fs"
	"regexp"
	"testing"
)

// The marker layer shows names players chose: on an anvil, on a name tag,
// in chat. Leaflet takes a string given to a tooltip or popup as HTML, and
// the DOM has its own ways to parse one, so the script may use none of
// them: every piece of content it hands over is an element built by its
// text helper, which sets textContent.
func TestMarkerLayerNeverHandsTextOverAsMarkup(t *testing.T) {
	js, err := fs.ReadFile(FS, "markers.js")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`const text = \(s\) => \{\s*const span = document\.createElement\('span'\);\s*span\.textContent = s;\s*return span;\s*\};`).Match(js) {
		t.Fatal("markers.js no longer builds its tooltip content with textContent")
	}
	for _, sink := range []string{
		"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "DOMParser", "createContextualFragment",
		"srcdoc", "L.divIcon", "eval(", "new Function", "setAttribute('on", "javascript:",
	} {
		if bytes.Contains(js, []byte(sink)) {
			t.Errorf("markers.js uses %s, which reads text as markup or code", sink)
		}
	}
	content := regexp.MustCompile(`\b(bindTooltip|bindPopup|setTooltipContent|setPopupContent|setContent)\((.{0,24})`)
	safe := regexp.MustCompile(`^(text\(|\(marker\) => tip\(marker\))`)
	// tip is text and a picture drawn on a canvas, and nothing else.
	if !regexp.MustCompile(`function tip\(marker\) \{\s*const \{ kind, data \} = marker\.options;\s*const box = text\(.*\);\s*box\.prepend\(icons\.picture\(.*\)\);\s*return box;\s*\}`).Match(js) {
		t.Fatal("markers.js no longer builds a marker's tooltip from text and a picture")
	}
	calls := content.FindAllSubmatch(js, -1)
	if len(calls) < 2 {
		t.Fatalf("found %d tooltip calls in markers.js; the pattern no longer matches the script", len(calls))
	}
	for _, c := range calls {
		if !safe.Match(c[2]) {
			t.Errorf("markers.js gives %s content that is not built by text(): %s", c[1], c[0])
		}
	}
}

// A named mob is looked for by its name, so the name is on the map and in
// a list under the row, with what the mob is. The name is a player's: on
// the map it is drawn on a canvas, and in the list set as text.
func TestNamedMobsAreLabelledAndListedByName(t *testing.T) {
	js := read(t, "markers.js")
	for _, need := range []string{
		"tag: kind === 'mobs' ? icons.tag(m.n, style.color) : null,",
		"icons.mob(str(data.k), style.color, baby)",
		"const name = text(str(data.n) || names.entity(data.k));",
		"const what = text(names.kindOf(data.k, data.b));",
		"button.addEventListener('click', () => examine(entry, true));",
		"rows.get('mobs').setBody(roster);",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("markers.js no longer has %s", need)
		}
	}
	if !bytes.Contains(read(t, "icons.js"), []byte("ctx.fillText(said,")) {
		t.Error("icons.js no longer draws a name tag as text on a canvas")
	}
}

// The snapshot's mark of a named mob and the live layer's marker for the
// same animal are told to be one by the game's id for it. Going by name or
// by position would join two cats both called Biscuit, or split one that
// has walked off. The mark opens the same card the live marker does, and
// its label is part of it to the pointer.
func TestNamedMobIsOneMarkerAndOpensTheCard(t *testing.T) {
	markers, live, icons := read(t, "markers.js"), read(t, "live.js"), read(t, "icons.js")
	for _, need := range []string{
		"const idOf = (data) => (typeof data.i === 'string' && data.i !== '' ? data.i : null);",
		"const live = card.drawn(id);",
		"if (live) layers.mobs.removeLayer(entry.marker);",
		"layers.mobs.on('click', (e) => {",
		"card.open({ kind: 'mob', id, name: str(data.n), type: str(data.k), baby: data.b === true, ...at, dimension, savedAt: snapshotAt ?? undefined });",
		"document.addEventListener('mcmap:live', aside);",
	} {
		if !bytes.Contains(markers, []byte(need)) {
			t.Errorf("markers.js no longer has %s", need)
		}
	}
	for _, need := range []string{
		"const held = entities.get(`m:${id}`);",
		"const key = id ? `${player ? 'p' : 'm'}:${id}` : `s:${dimension}:${known.x}:${known.y}:${known.z}`;",
		"This is its last saved position, from ${age(picked.savedAt)}.",
		"'Not loaded right now.'",
		"tag: tagOf(e.n),",
	} {
		if !bytes.Contains(live, []byte(need)) {
			t.Errorf("live.js no longer has %s", need)
		}
	}
	if !regexp.MustCompile(`_containsPoint\(p\) \{\s*if \(L\.CircleMarker\.prototype\._containsPoint\.call\(this, p\)\) return true;`).Match(icons) {
		t.Error("icons.js no longer counts a marker's name label as part of it under the pointer")
	}
	if !bytes.Contains(read(t, "index.html"), []byte(`id="inspect-go"`)) {
		t.Error("index.html has no button to go to the inspected entity")
	}
}

func TestMarkerLayerIsLoadedAfterTheMapItBuildsOn(t *testing.T) {
	page, err := fs.ReadFile(FS, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	app, markers := bytes.Index(page, []byte(`src="app.js"`)), bytes.Index(page, []byte(`src="markers.js"`))
	if app < 0 || markers < app {
		t.Errorf("index.html must load markers.js after app.js (found at %d and %d)", markers, app)
	}
}
