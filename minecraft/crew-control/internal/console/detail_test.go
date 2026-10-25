package console

import (
	"path"
	"strings"
	"testing"
)

// The unit detail page serves exactly like the fleet overview does, and it
// opens from the same rows and toggles the other views do.

func TestUnitDetailAssetsAreServed(t *testing.T) {
	for p, want := range map[string]string{
		"/static/detail.js":    "javascript",
		"/static/detail-ui.js": "javascript",
		"/static/detail.css":   "text/css",
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
	if rec := serve(t, "/static/detail.test.mjs"); rec.Code == 200 {
		t.Error("node test files must stay out of the binary")
	}
}

func TestIndexLoadsTheUnitDetail(t *testing.T) {
	body := serve(t, "/").Body.String()
	for _, want := range []string{`href="/static/detail.css"`, `id="detail-toggle"`} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
}

// The detail page is reached from the fleet table and the roster as well as
// its own toggle and deep link, so their hosts must be able to open it.
func TestFleetAndRosterCanOpenTheDetail(t *testing.T) {
	if body := serve(t, "/static/ops.js").Body.String(); !strings.Contains(body, "host.detail") {
		t.Error("ops.js does not ask its host to open the detail page")
	}
	if body := serve(t, "/static/overview-ui.js").Body.String(); !strings.Contains(body, "host.detail") {
		t.Error("overview-ui.js does not ask its host to open the detail page")
	}
}

// A module the binary does not embed fails only in the browser, so the whole
// graph of the detail page is walked, relative ways included.
func TestUnitDetailImportGraphIsServed(t *testing.T) {
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
	walk("/static/detail-ui.js")
	if !seen["/static/detail.js"] {
		t.Error("/static/detail.js is not reachable from detail-ui.js")
	}
}
