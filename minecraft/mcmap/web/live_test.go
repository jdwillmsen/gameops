package web

import (
	"bytes"
	"io/fs"
	"regexp"
	"testing"
)

// The live layer draws up to a thousand markers a second, and the markers
// that stay put are two thousand more on the same canvas. Pictures on them
// are stamped there from bitmaps the shared script decodes once; an element
// or an image per marker would be thousands of each, and a string handed
// to the page as markup would be a gamertag a player chose.
func TestMapPicturesAreDecodedOnceAndStampedOnTheCanvas(t *testing.T) {
	icons, live, markers := read(t, "icons.js"), read(t, "live.js"), read(t, "markers.js")
	for _, need := range []string{"createImageBitmap(", "ctx.drawImage(", "const Stamped = L.CircleMarker.extend(", "const bitmaps = new Map();", "const sprites = new Map();"} {
		if !bytes.Contains(icons, []byte(need)) {
			t.Errorf("icons.js no longer has %s", need)
		}
	}
	for _, need := range []string{"L.canvas(", "ctx.drawImage(", "const Mob = icons.Tagged;", "icons.mob(", "icons.sprite(pic, colour, HEAD_RADIUS, paintHead)", "app.liveRenderer = renderer;"} {
		if !bytes.Contains(live, []byte(need)) {
			t.Errorf("live.js no longer has %s", need)
		}
	}
	// The markers go on the live layer's canvas, where both can be hovered,
	// each as one stamp of a sprite it shares with every other of its sort.
	for _, need := range []string{"const renderer = app.liveRenderer ||", "const Pin = icons.Tagged;", "icons.plate(", "icons.mob("} {
		if !bytes.Contains(markers, []byte(need)) {
			t.Errorf("markers.js no longer has %s", need)
		}
	}
	for name, js := range map[string][]byte{"icons.js": icons, "live.js": live, "markers.js": markers} {
		for _, sink := range []string{
			"L.marker(", "L.icon(", "L.divIcon", "L.svg(", "new Image", "createElement('img')", "<img", "createImageBitmap(",
			"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function",
		} {
			// Only the shared script decodes.
			if sink == "createImageBitmap(" && name == "icons.js" {
				continue
			}
			if bytes.Contains(js, []byte(sink)) {
				t.Errorf("%s uses %s", name, sink)
			}
		}
		// Every picture is asked of this origin, by a relative address,
		// which is all the page's content security policy allows.
		for _, a := range regexp.MustCompile("fetch\\(([^)]*)\\)").FindAllSubmatch(js, -1) {
			if !regexp.MustCompile("^('api/[a-z/]+'|`api/markers\\?dimension=\\$\\{encodeURIComponent\\(dimension|address)(,|$)").Match(a[1]) {
				t.Errorf("%s fetches %s, which is not one of its own API's addresses", name, a[1])
			}
		}
	}
	if n := len(regexp.MustCompile("fetch\\(").FindAll(icons, -1)); n != 2 {
		t.Errorf("found %d fetches in icons.js, want the list and a picture", n)
	}
	address := regexp.MustCompile("`(api/icons/[^`]*)`")
	if built := address.FindAllSubmatch(icons, -1); len(built) != 2 {
		t.Errorf("found %d picture addresses in icons.js, want the mob's and the marker's", len(built))
	}
	if built := address.FindAllSubmatch(live, -1); len(built) != 1 {
		t.Errorf("found %d picture addresses in live.js, want the head's", len(built))
	}
	// Only a picture the server lists is asked for, and a listed one that
	// fails leaves whatever wanted it drawn as it was.
	// A full cache gives up only what no listed version can ask for, or
	// everything on the map would be fetched and decoded again.
	for _, need := range []string{
		"listing.mobs.types.has(type)", "listing.pictures.keys.has(key) && KEY.test(key)", "failed.set(address, Date.now());", "canvas.hidden = !drawn;",
		"if (bitmaps.size > 1024) prune();", "const stale = (address) => !listed.has(address.slice(address.lastIndexOf('v=') + 2));",
	} {
		if !bytes.Contains(icons, []byte(need)) {
			t.Errorf("icons.js no longer has %s", need)
		}
	}
}

// The card about one mob or player shows a gamertag or a name tag, which a
// player chose, and finds its entity again by the id the game gave it. A
// card that settled for the nearest marker would go on reporting some other
// entity's position under the first one's name.
func TestInspectCardSetsTextAndTracksById(t *testing.T) {
	page, err := fs.ReadFile(FS, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"inspect", "inspect-picture", "inspect-name", "inspect-kind", "inspect-coords",
		"inspect-x", "inspect-y", "inspect-z", "inspect-note", "inspect-follow", "inspect-copy", "inspect-close"} {
		if !bytes.Contains(page, []byte(`id="`+id+`"`)) {
			t.Errorf("index.html has no element with id %s", id)
		}
	}
	js, err := fs.ReadFile(FS, "live.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, need := range []string{
		"if (node.textContent !== s) node.textContent = s;",
		"const held = entities.get(picked.key);",
		"map.on('dragstart', unfollow);",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("live.js no longer has %s", need)
		}
	}
}

// Under each live row is what it holds, by type or by gamertag. A gamertag
// is a player's choice and a type the server's word, so each is set as
// text; and a frame of a thousand mobs must not pay for the filters more
// than a lookup each.
func TestLiveRowsListWhatTheyHoldAndFilterCheaply(t *testing.T) {
	js := read(t, "live.js")
	for _, need := range []string{
		"return f.only !== null ? f.only === sort : !f.hidden.has(sort);",
		"const visible = (held) => shown(held.category) && passes(held.category, held.sort);",
		"if (visible(held)) layerOf(category).addLayer(held.marker);",
		"it.name.textContent = label;",
		"app.layers.retain('live', domain, filtering(domain) ? { only: f.only, hidden: [...f.hidden].slice(0, MAX_HIDDEN) } : null);",
		"const busy = list.matches(':hover') || list.contains(document.activeElement);",
		// A new row is listed whoever is pointing; only the order waits.
		"for (const sort of wanted) if (items.get(sort).item.parentNode !== list) list.append(items.get(sort).item);",
		"if (key !== order && !busy) {",
		"Hidden on the map by your ${picked.category === 'players' ? 'player' : 'type'} filter.",
		"rows[id].setBody(breakdowns[id].node);",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("live.js no longer has %s", need)
		}
	}
	// The markers are placed when an entity appears and when a filter
	// changes, never looked over again for every frame.
	if n := len(regexp.MustCompile(`\brefilter\b`).FindAll(js, -1)); n != 3 {
		t.Errorf("live.js names refilter in %d places, want its definition, a row's switch and a filter's change", n)
	}
	panel := read(t, "layers.js")
	for _, need := range []string{"app.layers.recall = recall;", "app.layers.retain = retain;", "return Object.hasOwn(choices, key) ? choices[key] : null;"} {
		if !bytes.Contains(panel, []byte(need)) {
			t.Errorf("layers.js no longer has %s", need)
		}
	}
}

// Follow is turned off by more than its own button, so what the button
// says and what a screen reader is told both come from the one place the
// card is painted, whichever way the state changed.
func TestFollowSaysWhenItIsFollowing(t *testing.T) {
	js := read(t, "live.js")
	for _, need := range []string{
		"card.follow.setAttribute('aria-pressed', String(picked.follow));",
		"say(card.follow, picked.follow ? 'Following' : 'Follow');",
		"if (card.said) card.said.textContent = now !== null ? `Following ${now}.` : `No longer following ${following}.`;",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("live.js no longer has %s", need)
		}
	}
	if !regexp.MustCompile(`function paintCard\(\) \{\s*if \(!card\.root\) return;\s*announce\(\);`).Match(js) {
		t.Error("live.js no longer announces a change of following wherever the card is painted")
	}
	// Outside the card, which is hidden when it closes, and a hidden
	// region says nothing.
	page := read(t, "index.html")
	said, card := bytes.Index(page, []byte(`id="inspect-said" class="unseen" role="status"`)), bytes.Index(page, []byte(`<section id="inspect"`))
	if said < 0 || card < 0 || said < card+bytes.Index(page[card:], []byte("</section>")) {
		t.Error("index.html no longer has the following status outside the card")
	}
}

// Escape shuts one thing. Pressed in a dialog it is the dialog's, and must
// not also shut the card under it and stop what the card is following.
func TestEscapeInADialogIsTheDialogs(t *testing.T) {
	for _, script := range []string{"live.js", "search.js", "compact.js", "menu.js"} {
		js := read(t, script)
		handlers := regexp.MustCompile(`(?s)document\.addEventListener\('keydown', \(e\) => \{.*?\n  \}(, true)?\);|(?s)document\.addEventListener\('keydown', \(e\) => \{.*?\n    \}\);`).FindAll(js, -1)
		if len(handlers) == 0 {
			t.Errorf("%s has no document-level key handler; the pattern no longer matches the script", script)
		}
		for _, h := range handlers {
			if !bytes.Contains(h, []byte("dialog[open]")) && !bytes.Contains(h, []byte("typing(e.target)")) {
				t.Errorf("%s has a document-level key handler that does not stand aside for an open dialog:\n%s", script, h)
			}
		}
	}
	if !bytes.Contains(read(t, "menu.js"), []byte("node.closest('dialog[open]') !== null")) {
		t.Error("menu.js no longer counts an open dialog as somewhere shortcuts do not fire")
	}
}
