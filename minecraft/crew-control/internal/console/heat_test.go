package console

import (
	"path"
	"strings"
	"testing"
)

func TestHeatmapAssetsAreServedAndLinked(t *testing.T) {
	seen := map[string]bool{}
	var walk func(from string)
	walk = func(from string) {
		if seen[from] {
			return
		}
		seen[from] = true
		rec := serve(t, from)
		if rec.Code != 200 || rec.Body.Len() == 0 {
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
	walk("/static/heat-ui.js")
	if !seen["/static/heat.js"] {
		t.Error("/static/heat.js is not reachable from heat-ui.js")
	}
	if rec := serve(t, "/static/heat.css"); rec.Code != 200 {
		t.Errorf("heat.css: %d", rec.Code)
	}
	body := serve(t, "/").Body.String()
	for _, want := range []string{`href="/static/heat.css"`, `id="heat-toggle"`} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	if rec := serve(t, "/static/heat.test.mjs"); rec.Code == 200 {
		t.Error("node test files must stay out of the binary")
	}
}
