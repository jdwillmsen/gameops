package web

import (
	"bytes"
	"regexp"
	"testing"
)

// Everything the page shows is called by the game's own name for it, which
// one script holds. A layer that made a name of its own from an id would
// show that id, tidied its own way, beside the proper name elsewhere.
func TestEveryNameOnThePageComesFromTheNamesScript(t *testing.T) {
	js := usesNoMarkupSink(t, "names.js")
	for _, need := range []string{
		"fetch('api/names', { cache: 'no-cache' })",
		"document.dispatchEvent(new CustomEvent('mcmap:names'));",
		"document.addEventListener('mcmap:icons',",
		// A version announced while the table was on its way is asked for after.
		"if (current !== asked) sync();",
		"app.names = { entity, container, bed, shulker, structure, holder, kindOf, mob, plural,",
		// A name is text of a bounded length whatever the answer holds.
		"if (typeof name === 'string' && name.trim() !== '') next[group][id] = name.slice(0, MAX_LENGTH);",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("names.js no longer has %s", need)
		}
	}
	uses := map[string][]string{
		"live.js":       {"names.mob(marker.options.name, marker.options.type)", "names.entity(picked.type)", "document.addEventListener('mcmap:names',"},
		"markers.js":    {"names.bed(m.c)", "names.holder(m.k, m.c, m.t)", "names.mob(m.n, m.k, m.b)", "document.addEventListener('mcmap:names',"},
		"structures.js": {"names.structure(s.kind)", "names.structure(p.kind)", "names.plural(names.structure(kind))", "document.addEventListener('mcmap:names',"},
		"search.js":     {"names.bed(hit.colour)", "names.holder(detail, hit.colour, hit.trapped)", "names.structure(detail)", "document.addEventListener('mcmap:names',"},
	}
	// The ways a script has made words of an id by itself.
	own := regexp.MustCompile(`replace\(/_/g|toUpperCase\(\)|label: '(Fortress|Monument|Outpost|Witch hut|Village)'`)
	for script, needs := range uses {
		body := read(t, script)
		for _, need := range needs {
			if !bytes.Contains(body, []byte(need)) {
				t.Errorf("%s no longer has %s", script, need)
			}
		}
		if m := own.Find(body); m != nil {
			t.Errorf("%s names something by itself: %s", script, m)
		}
	}
}

// The scripts that draw are written against these two, so each has to be
// on the page before them.
func TestNamesAndPicturesAreLoadedBeforeTheLayersThatUseThem(t *testing.T) {
	page := read(t, "index.html")
	at := func(name string) int { return bytes.Index(page, []byte(`src="`+name+`"`)) }
	for _, shared := range []string{"names.js", "icons.js"} {
		if at(shared) < at("app.js") {
			t.Errorf("index.html must load %s after app.js", shared)
		}
		for _, layer := range []string{"layers.js", "live.js", "markers.js", "structures.js", "search.js"} {
			if at(layer) < at(shared) {
				t.Errorf("index.html must load %s after %s", layer, shared)
			}
		}
	}
	// A page cached from before they existed must not break a newer script.
	for _, layer := range []string{"live.js", "markers.js", "structures.js", "search.js"} {
		if !bytes.Contains(read(t, layer), []byte("!app.icons || !app.names) return;")) {
			t.Errorf("%s no longer stands aside on a page without the shared scripts", layer)
		}
	}
}

// An inspected mob with no name tag is titled by its type, so the line
// under the title must not say the type again.
func TestInspectCardSaysAnUnnamedMobsTypeOnce(t *testing.T) {
	js := read(t, "live.js")
	if !bytes.Contains(js, []byte("say(card.kind, title === kind ? labelOf(picked.dimension) : `${kind} · ${labelOf(picked.dimension)}`);")) {
		t.Error("live.js no longer leaves the type out of the line under a title that is the type")
	}
}
