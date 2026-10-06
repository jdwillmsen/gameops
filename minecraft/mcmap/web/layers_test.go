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
		"get enabled()", "setCount(n)", "setNote(text)", "onToggle(fn)", "setAvailable(available)", "remove()",
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

// The filters each layer kept under a key of its own are read once more by
// the panel, so that nobody's choices reset.
func TestLayerPanelCarriesOverTheOldFilters(t *testing.T) {
	js := read(t, "layers.js")
	for _, key := range []string{"'mcmap.live'", "'mcmap.markers'", "'mcmap.structures'"} {
		if !bytes.Contains(js, []byte(key)) {
			t.Errorf("layers.js no longer reads the choices saved under %s", key)
		}
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
	if !bytes.Contains(read(t, "live.js"), []byte("const OLD_KEY = 'mcmap.live';")) {
		t.Error("live.js no longer reads the old Live switch, which now means paused")
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

// The intervals offered in the page are the ones the live layer accepts.
func TestLiveIntervalsOfferedAreTheOnesAccepted(t *testing.T) {
	js := read(t, "live.js")
	m := regexp.MustCompile(`const INTERVALS = \[([0-9, ]+)\];`).FindSubmatch(js)
	if m == nil {
		t.Fatal("live.js no longer lists its intervals")
	}
	if string(m[1]) != "1, 2, 5, 10, 30" {
		t.Errorf("live.js accepts intervals %s, want 1, 2, 5, 10, 30", m[1])
	}
	var offered []string
	for _, o := range regexp.MustCompile(`<option value="(\d+)">(\d+) s</option>`).FindAllSubmatch(read(t, "index.html"), -1) {
		if string(o[1]) != string(o[2]) {
			t.Errorf("the option for %s s is labelled %s s", o[1], o[2])
		}
		offered = append(offered, string(o[1]))
	}
	if got := strings.Join(offered, ", "); got != string(m[1]) {
		t.Errorf("index.html offers intervals %s, live.js accepts %s", got, m[1])
	}
	// Pausing has to close the stream, not merely stop drawing it.
	if !regexp.MustCompile(`const want = here && !control\.paused && !document\.hidden \? here : null;\s*if \(!want\) \{\s*close\(\);`).Match(js) {
		t.Error("live.js no longer closes the stream while paused or hidden")
	}
}
