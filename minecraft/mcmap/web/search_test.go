package web

import (
	"bytes"
	"regexp"
	"strconv"
	"testing"
)

// A search result carries names players chose: a container's, a mob's, a
// waypoint's. Each is put on the page as text, and never as markup.
func TestSearchShowsEveryHitAsText(t *testing.T) {
	js := usesNoMarkupSink(t, "search.js")
	loadedAfterThePanel(t, "search.js")
	for _, need := range []string{
		"node.textContent = s;",
		"item.append(text('span', 'name', title), text('span', 'where', whereOf(hit)));",
		".bindTooltip(text('span', '', titleOf(hit)),",
		// A hit whose name is its kind says so once.
		"return same(kind, title) ? '' : kind;",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("search.js no longer has %s", need)
		}
	}
	// A divIcon given no html draws none; given a string, it parses it.
	if regexp.MustCompile(`divIcon\([^)]*html`).Match(js) {
		t.Error("search.js gives a marker's icon html")
	}
	page := read(t, "index.html")
	for _, id := range []string{"search", "search-box", "search-panel", "search-results", "search-note"} {
		if !bytes.Contains(page, []byte(`id="`+id+`"`)) {
			t.Errorf("index.html has no element with id %s", id)
		}
	}
	// The server refuses a query longer than this.
	if !bytes.Contains(page, []byte(`maxlength="64"`)) {
		t.Error("the search box no longer stops at the 64 characters the server accepts")
	}
}

// Typing is a search a keystroke unless the page waits, and an answer to
// what was typed before must not land on top of the answer to what is
// typed now.
func TestSearchWaitsAndAbortsWhatItSupersedes(t *testing.T) {
	js := read(t, "search.js")
	m := regexp.MustCompile(`const SETTLE_MS = (\d+);`).FindSubmatch(js)
	if m == nil {
		t.Fatal("search.js no longer says how long it waits after a keystroke")
	}
	if ms, _ := strconv.Atoi(string(m[1])); ms < 250 {
		t.Errorf("search.js waits %d ms after a keystroke, want at least 250", ms)
	}
	for _, need := range []string{
		"timer = setTimeout(() => run(query), SETTLE_MS);",
		"if (request) request.abort();",
		"const mine = new AbortController();",
		"signal: mine.signal",
		"if (request !== mine) return;",
		// What the viewer has to be told in words.
		"Nothing on the map is called",
		"more not shown",
		"data.waypoints === 'unavailable'",
		"e.key === 'ArrowDown' || e.key === 'ArrowUp'",
		"e.key === 'Escape'",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("search.js no longer has %s", need)
		}
	}
	if bytes.Contains(js, []byte("localStorage")) {
		t.Error("search.js keeps something in the browser; a search is not a choice to keep")
	}
}
