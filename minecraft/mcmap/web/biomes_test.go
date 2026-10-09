package web

import (
	"bytes"
	"testing"
)

// Every way the DOM has to parse a string as markup or run it. A script
// that shows names from the world or from a player may use none of them.
var markupSinks = []string{
	"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "DOMParser", "createContextualFragment",
	"srcdoc", "eval(", "new Function", "setAttribute('on", "javascript:",
}

func usesNoMarkupSink(t *testing.T, name string) []byte {
	t.Helper()
	js := read(t, name)
	for _, sink := range markupSinks {
		if bytes.Contains(js, []byte(sink)) {
			t.Errorf("%s uses %s", name, sink)
		}
	}
	return js
}

func loadedAfterThePanel(t *testing.T, script string) {
	t.Helper()
	page := read(t, "index.html")
	at := bytes.Index(page, []byte(`src="`+script+`"`))
	if at < 0 || at < bytes.Index(page, []byte(`src="layers.js"`)) {
		t.Errorf("index.html must load %s after layers.js", script)
	}
}

// The overlay lines up with the terrain only while its tiles are asked for
// by the terrain's own addresses, and is kept by the browser only while
// each address carries the reading's version.
func TestBiomeOverlayAsksForTilesAsTheTerrainDoes(t *testing.T) {
	js := usesNoMarkupSink(t, "biomes.js")
	loadedAfterThePanel(t, "biomes.js")
	for _, need := range []string{
		"`api/biomes/tiles/${o.dimension}/${c.z}/${c.x}/${c.y}.png?${o.pick}v=${encodeURIComponent(o.version)}`",
		"`biome=${encodeURIComponent(choice.only)}&`",
		// Several at once: whichever list is the shorter, in one order, and
		// never more names than the server takes.
		"const [how, list] = drawn.length > 0 && drawn.length < hidden.length ? ['biomes', drawn] : ['except', hidden];",
		"return `${how}=${list.sort().map(encodeURIComponent).join(',')}&`;",
		"const MAX_NAMED = 128;",
		"if (list.length > MAX_NAMED) {",
		"whole: true, facet: 'items' });",
		"tileSize: listing.tiles.size,",
		"app.layers.register({ group: 'biomes', id: 'overlay', label: 'Biome overlay', enabled: false,",
		// A service without biomes answers 404, and the row goes.
		"if (res.status === 404) {",
		"new AbortController()",
		"app.biomes = {",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("biomes.js no longer has %s", need)
		}
	}
	if !bytes.Contains(read(t, "app.js"), []byte("return `tiles/${this.options.dimension}/${c.z}/${c.x}/${c.y}.${this.options.format}`;")) {
		t.Error("app.js no longer addresses terrain tiles the way biomes.js assumes")
	}
	// Whether the overlay is on is the panel's to keep.
	if bytes.Contains(js, []byte("localStorage")) {
		t.Error("biomes.js keeps a choice of its own; the panel keeps them under mcmap.layers")
	}
	if !bytes.Contains(read(t, "index.html"), []byte(`<output id="biome"`)) {
		t.Error("index.html has nowhere to name the biome under the pointer")
	}
}

// What the later layers are written against, beyond the first interface.
func TestLayerPanelLetsAScriptSwitchARowAndShowControlsUnderIt(t *testing.T) {
	js := read(t, "layers.js")
	for _, need := range []string{"setEnabled(on)", "setBody(node)", "if (row.available && set(row, on)) remember({ [row.key]: row.on });"} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("layers.js no longer has %s", need)
		}
	}
	app := read(t, "app.js")
	for _, need := range []string{"extent: () => extent,", "go(id, x, z) {"} {
		if !bytes.Contains(app, []byte(need)) {
			t.Errorf("app.js no longer offers %s", need)
		}
	}
}
