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
	safe := regexp.MustCompile(`^(text\(|\(marker\) => text\()`)
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
