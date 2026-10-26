package console

import (
	"path"
	"strings"
	"testing"
)

// The chest browser is imported by console.js; a module the binary does not
// embed fails only in the browser, so its whole import graph is walked.
func TestContainerBrowserImportGraphIsServed(t *testing.T) {
	seen := map[string]bool{}
	var walk func(from string)
	walk = func(from string) {
		if seen[from] {
			return
		}
		seen[from] = true
		rec := serve(t, from)
		if rec.Code != 200 {
			t.Errorf("%s: status %d", from, rec.Code)
			return
		}
		for _, m := range importSpec.FindAllStringSubmatch(rec.Body.String(), -1) {
			spec := m[1]
			if !strings.HasPrefix(spec, "/") {
				spec = path.Join(path.Dir(from), spec)
			}
			walk(spec)
		}
	}
	walk("/static/containers-ui.js")
	for _, want := range []string{"/static/containers.js", "/static/detail.js"} {
		if !seen[want] {
			t.Errorf("%s is not reachable from containers-ui.js", want)
		}
	}
	if rec := serve(t, "/static/containers.css"); rec.Code != 200 {
		t.Errorf("containers.css: status %d", rec.Code)
	}
	if body := serve(t, "/").Body.String(); !strings.Contains(body, `href="/static/containers.css"`) {
		t.Error("index.html does not load containers.css")
	}
}
