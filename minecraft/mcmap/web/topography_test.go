package web

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// The overlay lines up with the terrain only while its tiles are asked for
// by the terrain's own addresses, and is kept by the browser only while
// each address carries the reading's version.
func TestTopographyAsksForTilesAsTheTerrainDoes(t *testing.T) {
	js := usesNoMarkupSink(t, "topography.js")
	loadedAfterThePanel(t, "topography.js")
	for _, need := range []string{
		"`api/topography/tiles/${o.dimension}/${c.z}/${c.x}/${c.y}.png?${o.pick}&v=${encodeURIComponent(o.version)}`",
		"tileSize: listing.tiles.size,",
		"maxNativeZoom: listing.tiles.maxZoom,",
		// A service without it answers 404, and the rows go.
		"if (res.status === 404) {",
		"app.layers.register({",
		"app.biomes.surface(layer !== null)",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("topography.js no longer has %s", need)
		}
	}
	// Whether a row is on, and which bands are shown, is the panel's to keep.
	if bytes.Contains(js, []byte("localStorage")) {
		t.Error("topography.js keeps a choice of its own; the panel keeps them under mcmap.layers")
	}
	page := read(t, "index.html")
	if bytes.Index(page, []byte(`src="topography.js"`)) < bytes.Index(page, []byte(`src="biomes.js"`)) {
		t.Error("index.html must load topography.js after biomes.js, which it tells what the footer says")
	}
	if !bytes.Contains(read(t, "biomes.js"), []byte("surface(on) {")) {
		t.Error("biomes.js no longer lets the topography ask for the surface to be said")
	}
}

// The rows are in a saved view and a link, off in the views that come with
// the page, and its tint's bands are a choice kept like any other.
func TestTopographyIsKeptWithTheViewsAndSaysItIsOfToday(t *testing.T) {
	views := read(t, "views.js")
	// A link names a layer by its place in this list, which is only ever
	// added to at the end.
	if !bytes.Contains(views, []byte("['overlays/grid', 'Grid'], ['overlays/chunk', 'Chunk focus'],\n    ['topography/hillshade', 'Hillshade'], ['topography/contours', 'Contour lines'], ['topography/tint', 'Height tint'],\n  ];")) {
		t.Error("views.js no longer ends its list of the page's layers with the topography's rows")
	}
	if !bytes.Contains(views, []byte("'biomes#items', 'live#mobs', 'topography#tint'];")) {
		t.Error("views.js no longer carries which bands of the tint are shown")
	}
	settings := read(t, "settings.js")
	if got := bytes.Count(settings, []byte("...rows('topography', [], TOPOGRAPHY)")); got != 3 {
		t.Errorf("settings.js switches the topography off in %d of the 3 views that come with the page", got)
	}
	if !bytes.Contains(settings, []byte("'overlays#trails', 'topography#tint'];")) {
		t.Error("settings.js no longer knows the choice over the tint's bands")
	}
	// What lies over a past day's terrain is of today, and the control
	// says so for each thing that does.
	if !bytes.Contains(read(t, "history.js"), []byte("Players, markers, structures, biomes and topography are as they are now.")) {
		t.Error("history.js no longer says that the topography is of today")
	}
}

// The group is checked by doing it: its rows, its one layer for however
// many drawings are on, its legend, the dimensions that have it, and the
// footer's line. Skipped where there is no node to run it with.
func TestTopographyDoesWhatItSaysWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the topography's script with")
	}
	out, err := exec.Command(node, "testdata/topography.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the topography's script did not do what is asked of it: %v", err)
	}
}
