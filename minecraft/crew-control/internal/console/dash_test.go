package console

import (
	"strings"
	"testing"
)

func TestDashboardAssetsAreServedAndLinked(t *testing.T) {
	for p, want := range map[string]string{
		"/static/dash.js":        "javascript",
		"/static/dash-data.js":   "javascript",
		"/static/dash-panels.js": "javascript",
		"/static/dash-ui.js":     "javascript",
		"/static/dash.css":       "text/css",
	} {
		rec := serve(t, p)
		if rec.Code != 200 || rec.Body.Len() == 0 {
			t.Fatalf("%s: status %d, %d bytes", p, rec.Code, rec.Body.Len())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, want) {
			t.Fatalf("%s: content type %q", p, ct)
		}
	}
	for _, p := range []string{"/static/dash.test.mjs", "/static/dash-data.test.mjs"} {
		if rec := serve(t, p); rec.Code == 200 {
			t.Errorf("%s: node test files must stay out of the binary", p)
		}
	}
	body := serve(t, "/").Body.String()
	for _, want := range []string{`href="/static/dash.css"`, `id="dash-toggle"`} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
}
