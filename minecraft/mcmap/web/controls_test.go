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
