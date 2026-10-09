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
	if !regexp.MustCompile(`function tip\(marker\) \{\s*const \{ kind, data \} = marker\.options;[^}]*?const box = text\(.*\);\s*box\.prepend\(icons\.picture\(.*\)\);\s*return box;\s*\}`).Match(js) {
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
		"tag: kind === 'mobs' ? tagOf(m.n) : null,",
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
		"const live = entry.live !== null && card.drawn(entry.live.id);",
		"if (live) layers.mobs.removeLayer(entry.marker);",
		"layers.mobs.on('click', (e) => {",
		"card.open({ kind: 'mob', id, name: nameOf(entry), type: str(data.k), baby: data.b === true, ...at, dimension, saved: true, savedAt: snapshotAt });",
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

// The snapshot's mark of a named mob is where it was saved, up to a
// snapshot's interval ago, and is on the map only while the live layer is
// not drawing the mob: because it is not loaded, or its row or type is
// switched off, or it has died since. Drawn like a live marker it reads as
// a second, stuck one, so it is faded, in a broken ring, and its tooltip
// says how old it is.
func TestNamedMobMarkIsDrawnAndSaidAsASavedPosition(t *testing.T) {
	markers, live := read(t, "markers.js"), read(t, "live.js")
	for _, need := range []string{
		"const marker = new (saved ? Saved : Pin)([m.z + 0.5, m.x + 0.5], {",
		"...(saved ? { saved: true, opacity: SAVED_ALPHA, dashArray: SAVED_DASH.join(' ') } : {}),",
		"worn = fade(icons.mob(str(data.k), style.color, baby));",
		"const tagOf = (name) => (naming('labelMobs') === 'always' ? fade(icons.tag(name, NAMED)) : null);",
		"ctx.setLineDash(SAVED_DASH);",
		"return snapshotAt !== null && card && card.age ? `saved ${card.age(snapshotAt)}` : 'last saved position';",
		"const box = text(kind === 'mobs' ? `${title} · ${savedSaid()} · ${at(data)}` : `${title} · ${at(data)}`);",
		"const title = entry.live ? 'Loaded now' : `Last saved position: ${fmt(data.x)}, ${fmt(data.y)}, ${fmt(data.z)}`;",
	} {
		if !bytes.Contains(markers, []byte(need)) {
			t.Errorf("markers.js no longer has %s", need)
		}
	}
	if !regexp.MustCompile(`(?m)^    // How long ago a snapshot was, by the server's clock\.\n    age,$`).Match(live) {
		t.Error("live.js no longer tells the marker layer how old a snapshot is")
	}
}

// Which loaded mob a saved one is goes by the game's id wherever the
// snapshot has one, and then by nothing else: a loaded mob of the same name
// under another id is another animal, and hiding the saved mark for it
// would lose one. A mob saved without an id is found by name and type, and
// only while exactly one is saved and exactly one is loaded under them, so
// that two sharing a name are never taken for each other. A mob renamed
// since the snapshot is listed under the name it has now.
func TestNamedMobIsPairedByIdAndByNameOnlyWithoutOne(t *testing.T) {
	markers, live := read(t, "markers.js"), read(t, "live.js")
	if !regexp.MustCompile(`if \(id\) \{\s*entry\.live = byId\.get\(id\) \|\| null;\s*continue;\s*\}`).Match(markers) {
		t.Error("markers.js no longer goes by the id alone where the snapshot has one")
	}
	for _, need := range []string{
		"const guess = saved.get(same) === 1 && there.get(same) === 1 ? loaded.find((mob) => sameAs(mob.name, mob.type) === same) : null;",
		"entry.live = guess && !claimed.has(guess.id) ? guess : null;",
		"const nameOf = (entry) => (entry.live && str(entry.live.name)) || str(entry.data.n);",
		"if (entry.label.textContent !== name) entry.label.textContent = name;",
		"pair();\n    let back = false;",
	} {
		if !bytes.Contains(markers, []byte(need)) {
			t.Errorf("markers.js no longer has %s", need)
		}
	}
	if !bytes.Contains(live, []byte("if (held.category !== 'players' && held.name) out.push({ id: key.slice(2), name: held.name, type: held.type });")) {
		t.Error("live.js no longer lists the named mobs in the picture")
	}
	// A count of marks would say two for one animal; the row counts what
	// the snapshot holds, each once.
	if !bytes.Contains(markers, []byte("totals[kind] = made.length;")) {
		t.Error("markers.js no longer counts the named mobs the snapshot holds")
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
