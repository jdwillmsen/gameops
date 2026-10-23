package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Mount(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestIndexServesHTMLWithCSP(t *testing.T) {
	rec := serve(t, "/")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type %q", ct)
	}
	const csp = "default-src 'self'; frame-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'"
	if got := rec.Header().Get("Content-Security-Policy"); got != csp {
		t.Fatalf("csp %q", got)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("missing no-cache")
	}
}

func TestSigninServesHTMLWithCSP(t *testing.T) {
	rec := serve(t, "/signin")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type %q", ct)
	}
	const csp = "default-src 'self'; frame-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'"
	if got := rec.Header().Get("Content-Security-Policy"); got != csp {
		t.Fatalf("csp %q", got)
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("the sign-in page must not leak its address")
	}
	if !strings.Contains(rec.Body.String(), "/static/signin.js") {
		t.Fatal("page does not load its script")
	}
}

func TestSigninHasNoInlineScript(t *testing.T) {
	body := serve(t, "/signin").Body.String() + serve(t, "/").Body.String()
	if strings.Contains(body, "<script>") || strings.Contains(body, "onclick=") {
		t.Fatal("inline script would break the CSP")
	}
}

func TestIndexOnlyAtRoot(t *testing.T) {
	if rec := serve(t, "/nope"); rec.Code != 404 {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestStaticFiles(t *testing.T) {
	for path, want := range map[string]string{
		"/static/console.js":  "javascript",
		"/static/state.js":    "javascript",
		"/static/signin.js":   "javascript",
		"/static/console.css": "text/css",
	} {
		rec := serve(t, path)
		if rec.Code != 200 {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, want) {
			t.Fatalf("%s: content type %q", path, ct)
		}
		if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: missing security headers", path)
		}
	}
}

func TestStaticDoesNotEscapeOrServeTests(t *testing.T) {
	for _, p := range []string{"/static/../go.mod", "/static/%2e%2e/go.mod", "/static/state.test.mjs", "/static/missing.js"} {
		rec := serve(t, p)
		// The mux answers an unclean path with a redirect to its cleaned
		// form; what matters is that following it lands on a 404.
		if loc := rec.Header().Get("Location"); rec.Code == http.StatusTemporaryRedirect && loc != "" {
			rec = serve(t, loc)
		}
		if rec.Code != 404 {
			t.Fatalf("%s: status %d", p, rec.Code)
		}
	}
}

func TestOldCameraBookmarkRedirects(t *testing.T) {
	rec := serve(t, "/camera")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("status %d location %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestEveryScriptTheConsoleImportsIsEmbedded(t *testing.T) {
	for _, f := range []string{"console.js", "state.js", "fleet.js", "ops.js", "console.css"} {
		if rec := serve(t, "/static/"+f); rec.Code != 200 || rec.Body.Len() == 0 {
			t.Errorf("/static/%s: status %d, %d bytes", f, rec.Code, rec.Body.Len())
		}
	}
	if rec := serve(t, "/static/fleet.test.mjs"); rec.Code == 200 {
		t.Error("node test files must stay out of the binary")
	}
}

func TestChartLibraryIsServed(t *testing.T) {
	for path, want := range map[string]string{
		"/static/charts/index.js":   "javascript",
		"/static/charts/line.js":    "javascript",
		"/static/charts/demo.js":    "javascript",
		"/static/charts/charts.css": "text/css",
		"/static/charts/demo.css":   "text/css",
		"/static/charts/demo.html":  "text/html",
	} {
		rec := serve(t, path)
		if rec.Code != 200 || rec.Body.Len() == 0 {
			t.Fatalf("%s: status %d, %d bytes", path, rec.Code, rec.Body.Len())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, want) {
			t.Fatalf("%s: content type %q", path, ct)
		}
		if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: missing security headers", path)
		}
	}
}

func TestChartLibraryEveryImportIsEmbedded(t *testing.T) {
	index := serve(t, "/static/charts/index.js").Body.String()
	for _, f := range []string{"line.js", "bar.js", "spark.js", "heatmap.js", "gauge.js", "range.js", "series.js", "scale.js", "geom.js", "panel.js", "tooltip.js", "dom.js", "palette.js", "synth.js"} {
		if rec := serve(t, "/static/charts/"+f); rec.Code != 200 {
			t.Errorf("/static/charts/%s: status %d", f, rec.Code)
		}
	}
	if !strings.Contains(index, "./line.js") {
		t.Fatal("index does not export the line chart")
	}
	if rec := serve(t, "/static/charts/palette.test.mjs"); rec.Code == 200 {
		t.Error("node test files must stay out of the binary")
	}
}

func TestChartDemoHasNoInlineScript(t *testing.T) {
	body := serve(t, "/static/charts/demo.html").Body.String()
	if strings.Contains(body, "<script>") || strings.Contains(body, "onclick=") {
		t.Fatal("inline script would break the CSP")
	}
}

func TestStaticChartsDoesNotEscape(t *testing.T) {
	for _, p := range []string{"/static/charts/missing.js", "/static/charts/..%2f..%2fgo.mod", "/static/charts/scale.test.mjs"} {
		if rec := serve(t, p); rec.Code != 404 {
			t.Fatalf("%s: status %d", p, rec.Code)
		}
	}
}
