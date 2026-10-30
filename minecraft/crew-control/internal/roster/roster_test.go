package roster

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseAcceptsChartShape(t *testing.T) {
	r, err := Parse(`[
		{"id":"crew-01","name":"Agate","url":"http://crew-01:8080"},
		{"id":"crew-02","name":"Basalt","url":"http://crew-02:8080/"},
		{"id":"crew-10","name":"Chert","url":"http://crew-10.crew.svc:8080"}]`)
	if err != nil {
		t.Fatal(err)
	}
	units := r.Units()
	if len(units) != 3 {
		t.Fatalf("got %d units", len(units))
	}
	for i, id := range []string{"crew-01", "crew-02", "crew-10"} {
		if units[i].ID != id {
			t.Errorf("unit %d = %s, want %s", i, units[i].ID, id)
		}
	}
	u, ok := r.Get("crew-02")
	if !ok || u.Name != "Basalt" || u.URL.Host != "crew-02:8080" {
		t.Errorf("Get = %+v, %v", u, ok)
	}
	if _, ok := r.Get("crew-99"); ok {
		t.Error("Get found an unknown unit")
	}
}

func TestParseRejects(t *testing.T) {
	many := make([]string, 27)
	for i := range many {
		many[i] = fmt.Sprintf(`{"id":"crew-%02d","name":"n","url":"http://h:1"}`, i+1)
	}
	unit := func(id, name, url string) string {
		return fmt.Sprintf(`[{"id":%q,"name":%q,"url":%q}]`, id, name, url)
	}
	cases := map[string]string{
		"invalid json":   `not json`,
		"empty list":     `[]`,
		"too many":       "[" + strings.Join(many, ",") + "]",
		"bad id":         unit("crew-1", "A", "http://h:1"),
		"bad id prefix":  unit("unit-01", "A", "http://h:1"),
		"id trailing":    unit("crew-01x", "A", "http://h:1"),
		"duplicate":      `[{"id":"crew-01","name":"A","url":"http://h:1"},{"id":"crew-01","name":"B","url":"http://h:2"}]`,
		"empty name":     unit("crew-01", "", "http://h:1"),
		"long name":      unit("crew-01", strings.Repeat("a", 17), "http://h:1"),
		"https":          unit("crew-01", "A", "https://h:1"),
		"path":           unit("crew-01", "A", "http://h:1/x"),
		"userinfo":       unit("crew-01", "A", "http://u:p@h:1"),
		"query":          unit("crew-01", "A", "http://h:1/?a=b"),
		"no host":        unit("crew-01", "A", "http://"),
		"id-like name":   unit("crew-04", "crew-01", "http://h:1"),
		"own id as name": unit("crew-01", "crew-01", "http://h:1"),
		"long id name":   unit("crew-04", "crew-100", "http://h:1"),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(raw); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestParseAcceptsMaxAndMultibyteName(t *testing.T) {
	var parts []string
	for i := 1; i <= 26; i++ {
		parts = append(parts, fmt.Sprintf(`{"id":"crew-%02d","name":%q,"url":"http://h:1"}`, i, strings.Repeat("é", 16)))
	}
	if _, err := Parse("[" + strings.Join(parts, ",") + "]"); err != nil {
		t.Fatal(err)
	}
}

func TestParseTakesAUnitsPlayerId(t *testing.T) {
	r, err := Parse(`[{"id":"crew-01","name":"Agate","url":"http://crew-01:8080","player":"-4294967290"},
		{"id":"crew-02","name":"Basalt","url":"http://crew-02:8080"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := r.Get("crew-01"); u.Player != "-4294967290" {
		t.Errorf("player = %q", u.Player)
	}
	if u, _ := r.Get("crew-02"); u.Player != "" {
		t.Errorf("player without one = %q", u.Player)
	}
	for _, bad := range []string{"x", "abc", "1e3", "99999999999999999999999"} {
		if _, err := Parse(fmt.Sprintf(`[{"id":"crew-01","name":"A","url":"http://h:1","player":%q}]`, bad)); err == nil {
			t.Errorf("player %q was accepted", bad)
		}
	}
}

func TestResolveFindsAnIdOrAUniqueName(t *testing.T) {
	r, err := Parse(`[
		{"id":"crew-01","name":"Rusty","url":"http://crew-01:8080"},
		{"id":"crew-02","name":"Twin","url":"http://crew-02:8080"},
		{"id":"crew-03","name":"Twin","url":"http://crew-03:8080"}]`)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		key, id string
		ok      bool
	}{
		{"crew-02", "crew-02", true},
		{"Rusty", "crew-01", true},
		{"rusty", "", false},
		{"Twin", "", false},
		{"crew-01", "crew-01", true},
		{"crew-04", "", false},
		{"nobody", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		u, ok := r.Resolve(c.key)
		if ok != c.ok || u.ID != c.id {
			t.Errorf("Resolve(%q) = %q, %v; want %q, %v", c.key, u.ID, ok, c.id, c.ok)
		}
	}
}

func TestResolvePrefersAnIdOverAName(t *testing.T) {
	// Parse refuses such a roster; this is the lookup's own rule, held for a
	// roster built any other way.
	r := Roster{
		units: []Unit{{ID: "crew-01", Name: "Rusty"}, {ID: "crew-04", Name: "crew-01"}},
		byID:  map[string]int{"crew-01": 0, "crew-04": 1},
	}
	if u, ok := r.Resolve("crew-01"); !ok || u.ID != "crew-01" {
		t.Errorf("Resolve(crew-01) = %q, %v; want the unit with that id", u.ID, ok)
	}
}

func TestNameOfIsTheInGameNameOfAKnownUnit(t *testing.T) {
	r, err := Parse(`[{"id":"crew-18","name":"Rusty","url":"http://crew-18:8080"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := r.NameOf("crew-18"); !ok || n != "Rusty" {
		t.Errorf("NameOf(crew-18) = %q, %v", n, ok)
	}
	if n, ok := r.NameOf("Rusty"); ok {
		t.Errorf("a name is not an id: NameOf(Rusty) = %q, true", n)
	}
	if _, ok := r.NameOf("crew-99"); ok {
		t.Error("an unknown unit has a name")
	}
}
