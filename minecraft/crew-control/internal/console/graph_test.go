package console

import (
	"path"
	"regexp"
	"strings"
	"testing"
)

var (
	scriptTag = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
	srcAttr   = regexp.MustCompile(`(?i)\bsrc="([^"]+)"`)
)

func TestJobGraphAssetsAreServed(t *testing.T) {
	for p, want := range map[string]string{
		"/static/graph/layout.js":    "javascript",
		"/static/graph/graph.js":     "javascript",
		"/static/graph/jobs.js":      "javascript",
		"/static/graph/jobs-ui.js":   "javascript",
		"/static/graph/ledger.js":    "javascript",
		"/static/graph/ledger-ui.js": "javascript",
		"/static/graph/ledger.css":   "text/css",
		"/static/graph/graph.css":    "text/css",
		"/static/graph/demo.html":    "text/html",
		"/static/graph/demo.js":      "javascript",
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
	for _, want := range []string{`href="/static/graph/graph.css"`, `href="/static/graph/ledger.css"`, `id="jobgraph-toggle"`, `id="ledger-toggle"`} {
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

// The console's CSP allows no inline script, so a demo that carries one is
// served blank; every script it needs must be a file the console also serves.
func TestGraphDemoRunsUnderTheConsoleCSP(t *testing.T) {
	page := serve(t, "/static/graph/demo.html")
	// With no script-src, inline script falls back to default-src 'self' and
	// is refused; the test is only meaningful while that holds.
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || strings.Contains(csp, "script-src") {
		t.Fatalf("csp %q no longer blocks inline script by default", csp)
	}
	scripts := scriptTag.FindAllStringSubmatch(page.Body.String(), -1)
	if len(scripts) == 0 {
		t.Fatal("the demo loads no script")
	}
	for _, s := range scripts {
		if strings.TrimSpace(s[2]) != "" {
			t.Errorf("inline script in the demo would be blocked by the CSP: %.60q", s[2])
			continue
		}
		m := srcAttr.FindStringSubmatch(s[1])
		if m == nil {
			t.Errorf("script tag without src and without a body: %q", s[1])
			continue
		}
		rec := serve(t, m[1])
		if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
			t.Errorf("%s: status %d, content type %q", m[1], rec.Code, rec.Header().Get("Content-Type"))
			continue
		}
		for _, imp := range importSpec.FindAllStringSubmatch(rec.Body.String(), -1) {
			if r := serve(t, imp[1]); r.Code != 200 {
				t.Errorf("%s imports %s: status %d", m[1], imp[1], r.Code)
			}
		}
	}
	if strings.Contains(strings.ToLower(page.Body.String()), "onclick=") {
		t.Error("inline event handlers are blocked by the CSP as well")
	}
}
