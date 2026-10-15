package web

import (
	"bytes"
	"io/fs"
	"regexp"
	"testing"
)

// The page is served from what is embedded, not from the working tree, and a
// directory left out of the embed (or out of version control) builds fine on
// the machine that has it. This fails wherever the files are actually absent.
func TestEveryAssetThePageLoadsIsEmbedded(t *testing.T) {
	page, err := fs.ReadFile(FS, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	refs := regexp.MustCompile(`(?:href|src)="([^"#:]+)"`).FindAllSubmatch(page, -1)
	if len(refs) < 4 {
		t.Fatalf("found only %d local references in index.html; the pattern no longer matches the page", len(refs))
	}
	for _, m := range refs {
		// A link to a page is a route, answered by the server and not a file.
		if _, route := Pages["/"+string(m[1])]; route {
			continue
		}
		if _, err := fs.Stat(FS, string(m[1])); err != nil {
			t.Errorf("index.html loads %s, which is not embedded: %v", m[1], err)
		}
	}
	for route, name := range Pages {
		about, err := fs.ReadFile(FS, name)
		if err != nil {
			t.Errorf("%s is served from %s, which is not embedded: %v", route, name, err)
			continue
		}
		for _, m := range regexp.MustCompile(`(?:href|src)="([^"#:./][^"#:]*)"`).FindAllSubmatch(about, -1) {
			if _, route := Pages["/"+string(m[1])]; route {
				continue
			}
			if _, err := fs.Stat(FS, string(m[1])); err != nil {
				t.Errorf("%s loads %s, which is not embedded: %v", name, m[1], err)
			}
		}
	}
	// The live layer is a file of its own, so that the map works the same
	// without it; the page has to ask for it, after the script it builds on.
	app, live := bytes.Index(page, []byte(`src="app.js"`)), bytes.Index(page, []byte(`src="live.js"`))
	if app < 0 || live < app {
		t.Errorf("index.html must load live.js after app.js (found at %d and %d)", live, app)
	}
	// So is the structures layer.
	if at := bytes.Index(page, []byte(`src="structures.js"`)); at < app {
		t.Errorf("index.html must load structures.js after app.js (found at %d and %d)", at, app)
	}
	// Referenced from Leaflet's stylesheet rather than from the page.
	for _, name := range []string{"lib/leaflet/images/layers.png", "lib/leaflet/LICENSE"} {
		if _, err := fs.Stat(FS, name); err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
		}
	}
}
