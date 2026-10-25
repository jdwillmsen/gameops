package console

import (
	"path"
	"strings"
	"testing"
)

func TestJobGraphAssetsAreServed(t *testing.T) {
	for p, want := range map[string]string{
		"/static/graph/layout.js":  "javascript",
		"/static/graph/graph.js":   "javascript",
		"/static/graph/jobs.js":    "javascript",
		"/static/graph/jobs-ui.js": "javascript",
		"/static/graph/graph.css":  "text/css",
		"/static/graph/demo.html":  "text/html",
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
	for _, p := range []string{"/static/graph/layout.test.mjs", "/static/graph/jobs.test.mjs"} {
		if rec := serve(t, p); rec.Code == 200 {
			t.Errorf("%s: node test files must stay out of the binary", p)
		}
	}
}

func TestIndexLoadsTheJobGraph(t *testing.T) {
	body := serve(t, "/").Body.String()
	for _, want := range []string{`href="/static/graph/graph.css"`, `id="jobgraph-toggle"`} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
}

func TestTheJobGraphIsReachableFromTheConsole(t *testing.T) {
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
	walk("/static/graph/jobs-ui.js")
	for _, want := range []string{"/static/graph/layout.js", "/static/graph/graph.js", "/static/graph/jobs.js", "/static/state.js"} {
		if !seen[want] {
			t.Errorf("%s is not reachable from the job graph", want)
		}
	}
}
