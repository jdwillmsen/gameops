package web

import (
	"bytes"
	"io/fs"
	"regexp"
	"testing"
)

// The live layer draws up to a thousand markers a second. Pictures on them
// are stamped onto its one canvas from bitmaps decoded once; an element or
// an image per marker would be a thousand of each, and a string handed to
// the page as markup would be a gamertag a player chose.
func TestLiveLayerDrawsPicturesOnItsCanvas(t *testing.T) {
	js, err := fs.ReadFile(FS, "live.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, need := range []string{"createImageBitmap(", "ctx.drawImage(", "L.canvas("} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("live.js no longer uses %s", need)
		}
	}
	for _, sink := range []string{
		"L.marker(", "L.icon(", "L.divIcon", "new Image", "createElement('img')", "<img",
		"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function",
	} {
		if bytes.Contains(js, []byte(sink)) {
			t.Errorf("live.js uses %s", sink)
		}
	}
	// Every picture is asked of this origin, by a relative address, which
	// is all the page's content security policy allows.
	addresses := regexp.MustCompile("fetch\\(([^)]*)\\)").FindAllSubmatch(js, -1)
	if len(addresses) < 3 {
		t.Fatalf("found %d fetches in live.js; the pattern no longer matches the script", len(addresses))
	}
	for _, a := range addresses {
		if !regexp.MustCompile(`^('api/[a-z/]+'|address)(,|$)`).Match(a[1]) {
			t.Errorf("live.js fetches %s, which is not one of its own API's addresses", a[1])
		}
	}
	built := regexp.MustCompile("return [^;]*`(api/icons/[^`]*)`").FindAllSubmatch(js, -1)
	if len(built) != 2 {
		t.Fatalf("found %d picture addresses in live.js, want the mob's and the head's", len(built))
	}
}
