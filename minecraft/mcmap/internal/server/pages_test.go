package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/mcmap/web"
)

func TestPagesAboutTheMapNeedNoLogin(t *testing.T) {
	s := withLogin(t)
	s.Static = web.FS
	h := s.Handler()

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
		if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'") {
			t.Errorf("GET %s lacks the page's security policy", path)
		}
	}
}

func TestPagesAboutTheMapAreOnlyRead(t *testing.T) {
	s := withLogin(t)
	s.Static = web.FS
	if rec := do(s.Handler(), "POST", "/about", "", nil); rec.Code == http.StatusOK {
		t.Errorf("POST /about = %d, want it refused", rec.Code)
	}
}
