package web

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The page works slime chunks out for itself, by the function the README
// gives and internal/slime is tested against. A Go test cannot run the
// page's copy, so this holds it to the README's letter for letter: a change
// to either is then a change someone has to make to both, and check.
func TestSlimeChunksAreWorkedOutByThePublishedFunction(t *testing.T) {
	js := usesNoMarkupSink(t, "slime.js")
	loadedAfterThePanel(t, "slime.js")
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)```js\n(function isSlimeChunk\\(cx, cz\\) \\{.*?\n\\})\n```").FindSubmatch(readme)
	if m == nil {
		t.Fatal("the README no longer gives the slime chunk function")
	}
	squash := func(b []byte) string { return strings.Join(strings.Fields(string(b)), " ") }
	if !strings.Contains(squash(js), squash(m[1])) {
		t.Errorf("slime.js no longer has the README's function as written:\n%s", m[1])
	}
	for _, need := range []string{
		// Drawn tile by tile, so only for what is in view.
		"L.GridLayer.extend({",
		"for (let cz = Math.floor(z0 / side); cz * side < z0 + TILE; cz++) {",
		"minZoom: MIN_ZOOM,",
		"const here = dimension === 'overworld';",
		"app.layers.register({ group: 'overlays', id: 'slime', label: 'Slime chunks', enabled: false,",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("slime.js no longer has %s", need)
		}
	}
	for _, never := range []string{"fetch(", "localStorage"} {
		if bytes.Contains(js, []byte(never)) {
			t.Errorf("slime.js uses %s; slime chunks need neither the server nor a key of their own", never)
		}
	}
}
