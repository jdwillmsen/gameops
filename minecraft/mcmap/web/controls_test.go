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
