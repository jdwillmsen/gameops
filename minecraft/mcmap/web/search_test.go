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
		"const name = text('span', 'name', title);",
		"item.append(name, text('span', 'where', whereOf(hit)));",
		// A picture is a canvas the shared script draws, put before the text.
		"if (picture) name.prepend(picture);",
		".bindTooltip(text('span', '', titleOf(hit)),",
		// A hit whose name is its kind says so once.
		"return title.toLowerCase().includes(kind.toLowerCase()) ? '' : kind;",
		// A named mob is titled as its marker is, baby and all.
		"if (hit.kind === 'mob') return names.mob(name, detail, hit.baby);",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("search.js no longer has %s", need)
		}
	}
	// A player and a named mob move, so choosing one opens the card that
	// tracks it by the game's id, with the gamertag passed on as it came.
	for _, need := range []string{
		"if (hit.kind === 'player') return name || KINDS.player;",
		"if ((hit.kind === 'player' || hit.kind === 'mob') && app.inspect) {",
		"kind: hit.kind, id: str(hit.id) || null, name: str(hit.name), type: str(hit.detail), baby: hit.baby === true,",
		"saved: hit.kind === 'mob' && hit.live !== true,",
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

// Clearing a search is one press that leaves the viewer where they were:
// in the box, with nothing of the last search on the page or the map.
func TestSearchClearsInOnePressAndKeepsTheCursor(t *testing.T) {
	js := read(t, "search.js")
	if !regexp.MustCompile(`function clear\(\) \{\s*el\.box\.value = '';\s*reset\(\);\s*unmark\(\);\s*offer\(\);\s*el\.box\.focus\(\);\s*\}`).Match(js) {
		t.Error("search.js no longer clears the box, the list and the mark and then focuses the box")
	}
	for _, need := range []string{
		"if (dirty()) clear();",
		"else map.getContainer().focus();",
		"el.clear.addEventListener('click', clear);",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("search.js no longer has %s", need)
		}
	}
	// A button with no text is named for whoever cannot see the cross.
	if !regexp.MustCompile(`<button id="search-clear"[^>]*type="button"[^>]*aria-label="Clear the search"`).Match(read(t, "index.html")) {
		t.Error("index.html has no labelled button to clear the search")
	}
}
