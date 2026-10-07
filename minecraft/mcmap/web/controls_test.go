package web

import (
	"bytes"
	"regexp"
	"testing"
)

// Go marks the block it went to, and Clear takes the numbers and the mark
// away together. The mark's label is made from numbers the viewer typed and
// is still set as text, like every other label on the map.
func TestGoToCoordinatesCanBeClearedAndTakesAPastedPair(t *testing.T) {
	js := usesNoMarkupSink(t, "app.js")
	for _, need := range []string{
		"label.textContent = `X ${fmt(x)}, Z ${fmt(z)}`;",
		"el.gotoX.addEventListener('paste', (e) => {",
		"const pasted = coordinates((e.clipboardData || window.clipboardData)?.getData('text') ?? '');",
		"if (parts.length < 2 || parts.length > 3 || !parts.every((p) => /^[+-]?\\d+(\\.\\d+)?$/.test(p))) return null;",
		"el.gotoX.focus();",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("app.js no longer has %s", need)
		}
	}
	if !regexp.MustCompile(`el\.gotoClear\.addEventListener\('click', \(\) => \{\s*el\.gotoX\.value = '';\s*el\.gotoZ\.value = '';\s*unmark\(\);`).Match(js) {
		t.Error("app.js no longer clears both coordinates and the mark Go left")
	}
	if !regexp.MustCompile(`<button id="goto-clear"[^>]*type="button"[^>]*aria-label="Clear the coordinates"`).Match(read(t, "index.html")) {
		t.Error("index.html has no labelled button to clear the coordinates")
	}
}

// A typed length is said back in words before it is used, refused in words
// when it is not one, and never shown as it was typed: the box is the only
// place the viewer's own text appears.
func TestTypedLengthsAreSaidBackAndBounded(t *testing.T) {
	js := usesNoMarkupSink(t, "duration.js")
	for _, need := range []string{
		"if (/^[-−]/.test(text)) return { error: 'A length of time cannot be negative.' };",
		"if (bare && parts > 1) return { error: `A number is missing its unit. ${HOW}` };",
		"if (rounded < min) return { seconds: min,",
		"if (rounded > max) return { seconds: max,",
		"said.setAttribute('role', 'status');",
		"said.textContent = text;",
		"app.duration = { parse, words, settle, picker };",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("duration.js no longer has %s", need)
		}
	}
	// No message is built from what was typed.
	if regexp.MustCompile("error: `[^`]*\\$\\{(typed|text|m\\[)").Match(js) {
		t.Error("duration.js puts what was typed into a message")
	}
	page := read(t, "index.html")
	at := func(name string) int { return bytes.Index(page, []byte(`src="`+name+`"`)) }
	for _, user := range []string{"live.js", "trails.js"} {
		if at("duration.js") < 0 || at(user) < at("duration.js") {
			t.Errorf("index.html must load %s after duration.js", user)
		}
		if !bytes.Contains(read(t, user), []byte("duration.picker({")) {
			t.Errorf("%s no longer chooses its length with the shared picker", user)
		}
	}
	// The trails' window is bounded by what the server says it keeps.
	if !bytes.Contains(read(t, "trails.js"), []byte("max: () => (retention > 0 ? retention : MAX_UNKNOWN),")) {
		t.Error("trails.js no longer bounds its window by the server's retention")
	}
}

// A chunk's coordinates are its blocks' divided by sixteen and rounded
// down. Truncating instead puts block -1 in chunk 0, which is the chunk on
// the other side of the axis.
func TestChunkFocusFloorsAndFallsBackToTheRegion(t *testing.T) {
	js := usesNoMarkupSink(t, "chunk.js")
	for _, need := range []string{
		"const cellAt = (latlng, unit) => ({ unit, x: Math.floor(latlng.lng / sizeOf(unit)), z: Math.floor(latlng.lat / sizeOf(unit)) });",
		"const unitNow = () => (CHUNK * 2 ** map.getZoom() >= MIN_PIXELS ? 'chunk' : 'region');",
		"if (dimension !== 'overworld') return `No slime chunks in ${app.label(dimension)}.`;",
		"if (pinned && pinnedIn !== app.dimension()) {",
		"app.chunk = { go, pinned: () => pinned };",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("chunk.js no longer has %s", need)
		}
	}
	if bytes.Contains(js, []byte("Math.trunc")) || bytes.Contains(js, []byte(">> 4")) || bytes.Contains(js, []byte("| 0")) {
		t.Error("chunk.js works out a chunk by something other than Math.floor")
	}
	page := read(t, "index.html")
	for _, id := range []string{"chunk", "chunk-title", "chunk-state", "chunk-blocks", "chunk-slime", "chunk-pointer", "chunk-go", "chunk-x", "chunk-z", "chunk-unpin"} {
		if !bytes.Contains(page, []byte(`id="`+id+`"`)) {
			t.Errorf("index.html has no element with id %s", id)
		}
	}
	// It asks the slime layer's own function, which is the one tested
	// against the service's.
	if at := func(name string) int { return bytes.Index(page, []byte(`src="`+name+`"`)) }; at("chunk.js") < at("slime.js") {
		t.Error("index.html must load chunk.js after slime.js")
	}
}

// The command copied is the text the page shows, so the two cannot differ,
// and "!map" with no code after it is never offered.
func TestLoginCommandIsCopiedAsItIsShown(t *testing.T) {
	js := read(t, "app.js")
	for _, need := range []string{
		"await navigator.clipboard.writeText(el.loginCommand.textContent);",
		"getSelection().selectAllChildren(el.loginCommand);",
		"if (!done) said = 'Selected: copy it';",
		"el.loginCopied.textContent = said;",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("app.js no longer has %s", need)
		}
	}
	if !regexp.MustCompile("el\\.loginCommand\\.textContent = `!map \\$\\{started\\.code\\}`;\\s*offerCopy\\(true\\);").Match(js) ||
		!regexp.MustCompile(`el\.loginCommand\.textContent = '!map';\s*offerCopy\(false\);`).Match(js) {
		t.Error("app.js no longer offers the copy only while there is a code")
	}
	page := read(t, "index.html")
	if !regexp.MustCompile(`<button id="login-copy" type="button" hidden>`).Match(page) || !bytes.Contains(page, []byte(`id="login-copied" class="copied" role="status"`)) {
		t.Error("index.html has no copy button, hidden until there is a code, with a status beside it")
	}
}

// The footer's timers are one group, each under a label that says which it
// is, with a slot of fixed width for every figure that counts.
func TestFooterGroupsItsTimersUnderLabels(t *testing.T) {
	page := read(t, "index.html")
	if !regexp.MustCompile(`(?s)<div id="currency" class="currency" role="group" aria-label="How current the map is">.*<span class="stat-label">Live positions</span> <output id="live-age" class="slot".*<span class="stat-label">Terrain</span> <span id="status" class="slot" role="status"></span> <output id="refresh" class="refresh slot more" role="timer"`).Match(page) {
		t.Error("index.html no longer groups the live age and the terrain's refresh under their labels")
	}
	css := read(t, "style.css")
	for _, need := range []string{"#live-age { min-width:", "#status { min-width:", ".refresh { min-width:", ".currency {", "font-variant-numeric: tabular-nums;"} {
		if !bytes.Contains(css, []byte(need)) {
			t.Errorf("style.css no longer has %s", need)
		}
	}
	live := read(t, "live.js")
	for _, need := range []string{"say(el.age, state);", "say(el.more, extras.join(' · '));"} {
		if !bytes.Contains(live, []byte(need)) {
			t.Errorf("live.js no longer has %s", need)
		}
	}
}
