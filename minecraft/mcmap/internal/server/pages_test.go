package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/web"
)

func pagesServer(t *testing.T) http.Handler {
	t.Helper()
	s := withLogin(t)
	s.Static = web.FS
	return s.Handler()
}

func TestPagesAboutTheMapNeedNoLogin(t *testing.T) {
	h := pagesServer(t)
	if rec := do(h, "GET", "/api/map", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/map with no session = %d, want 401", rec.Code)
	}
	for path := range web.Pages {
		rec := do(h, "GET", path, "", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s with no session = %d, want 200", path, rec.Code)
			continue
		}
		body := rec.Body.String()
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s Content-Type = %q", path, ct)
		}
		if strings.Contains(body, "{{") {
			t.Errorf("GET %s leaves a placeholder unfilled", path)
		}
		if !strings.Contains(body, web.Copyright) {
			t.Errorf("GET %s does not carry %q", path, web.Copyright)
		}
	}
	if body := do(h, "GET", "/terms", "", nil).Body.String(); !strings.Contains(body, web.Owner) {
		t.Errorf("/terms does not name %q, the owner", web.Owner)
	}
	if body := do(h, "GET", "/privacy", "", nil).Body.String(); !strings.Contains(body, `href="mailto:`+web.Contact+`"`) {
		t.Errorf("/privacy does not give %q to write to", web.Contact)
	}
}

func TestPagesAboutTheMapAreOnlyRead(t *testing.T) {
	rec := do(pagesServer(t), "POST", "/about", "", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /about = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); !strings.Contains(allow, "GET") || strings.Contains(allow, "POST") {
		t.Errorf("Allow = %q, want the methods that are read", allow)
	}
}

func TestPagesAboutTheMapAreAnsweredAsOneAddressAndWithTheHeadersOfAPage(t *testing.T) {
	h := pagesServer(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/about"}, {"GET", "/about?from=login"}, {"HEAD", "/about"},
	} {
		rec := do(h, c.method, c.path, "", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s = %d, want 200", c.method, c.path, rec.Code)
		}
		if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
			t.Errorf("%s %s: Content-Security-Policy = %q", c.method, c.path, got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "max-age=300" {
			t.Errorf("%s %s: Cache-Control = %q", c.method, c.path, got)
		}
	}
	for _, path := range []string{"/about/", "/about/..", "/about/../about.html"} {
		rec := do(h, "GET", path, "", nil)
		if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "{{") {
			t.Errorf("GET %s = %d, want it not to serve a page", path, rec.Code)
		}
	}
}

// The files are what the routes above fill in; served as they are, they
// would show the placeholders.
func TestPageFilesAreNotServedRaw(t *testing.T) {
	h := pagesServer(t)
	for _, name := range web.Pages {
		if rec := do(h, "GET", "/"+name, "", nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET /%s = %d, want 404", name, rec.Code)
		}
	}
}
