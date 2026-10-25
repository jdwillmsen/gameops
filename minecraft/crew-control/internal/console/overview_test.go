package console

import (
	"path"
	"regexp"
	"strings"
	"testing"
)

var importSpec = regexp.MustCompile(`(?m)^(?:import|export)\s[^;]*?from\s+'([^']+)'`)

func TestFleetOverviewAssetsAreServed(t *testing.T) {
	for p, want := range map[string]string{
		"/static/overview.js":    "javascript",
		"/static/overview-ui.js": "javascript",
		"/static/overview.css":   "text/css",
	} {
		rec := serve(t, p)
		if rec.Code != 200 || rec.Body.Len() == 0 {
			t.Fatalf("%s: status %d, %d bytes", p, rec.Code, rec.Body.Len())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, want) {
			t.Fatalf("%s: content type %q", p, ct)
		}
		if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: missing security headers", p)
		}
	}
	if rec := serve(t, "/static/overview.test.mjs"); rec.Code == 200 {
		t.Error("node test files must stay out of the binary")
	}
}

func TestIndexLoadsTheFleetOverview(t *testing.T) {
	body := serve(t, "/").Body.String()
	for _, want := range []string{`href="/static/overview.css"`, `href="/static/charts/charts.css"`, `id="fleet-toggle"`} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
}

// A module that imports a file the binary does not embed fails in the
// browser, not at build time, so the whole graph from the console's entry
// point is walked.
func TestConsoleImportGraphIsServed(t *testing.T) {
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
	walk("/static/console.js")
	for _, want := range []string{"/static/overview-ui.js", "/static/overview.js", "/static/fleet.js", "/static/charts/spark.js", "/static/graph/jobs-ui.js"} {
		if !seen[want] {
			t.Errorf("%s is not reachable from the console", want)
		}
	}
}
