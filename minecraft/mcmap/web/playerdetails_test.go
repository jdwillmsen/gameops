package web

import (
	"bytes"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The panel is run and not read: what it asks for, what it draws of the
// save and of the trails, and that player text stays text. Skipped where
// there is no node to run it with.
func TestPlayerDetailsDoWhatTheySayWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the player details' script with")
	}
	out, err := exec.Command(node, "testdata/playerdetails.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the player details' script did not do what is asked of it: %v", err)
	}
}

// The panel is its own dialog and is shown without the page being taken;
// the card offers it only for a player the live picture has an id for.
func TestPlayerDetailsAreOfferedFromThePlayersCard(t *testing.T) {
	js := usesNoMarkupSink(t, "playerdetails.js")
	for _, need := range []string{
		"view.dialog.show();",
		"fetch(`api/players/${encodeURIComponent(subject.id)}${named}`, { cache: 'no-store', signal: mine.signal })",
		"app.playerDetails = { open, shut,",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("playerdetails.js no longer has %s", need)
		}
	}
	if bytes.Contains(js, []byte("showModal")) {
		t.Error("playerdetails.js takes the page with a modal")
	}
	live := read(t, "live.js")
	for _, need := range []string{
		"app.playerDetails.open({ id: picked.key.slice(2),",
		"card.details.hidden = !(player && /^p:-?\\d{1,20}$/.test(picked.key) && app.playerDetails);",
	} {
		if !bytes.Contains(live, []byte(need)) {
			t.Errorf("live.js no longer has %s", need)
		}
	}
	page := read(t, "index.html")
	for _, need := range []string{`id="inspect-details"`, `id="player" class="sheet aside player"`, `src="playerdetails.js"`} {
		if !bytes.Contains(page, []byte(need)) {
			t.Errorf("index.html no longer has %s", need)
		}
	}
	if at, live := bytes.Index(page, []byte(`src="playerdetails.js"`)), bytes.Index(page, []byte(`src="live.js"`)); at < 0 || at > live {
		t.Error("index.html must load playerdetails.js before live.js")
	}
}

// What the panel draws is styled for what it is: the wash on an enchanted
// item's picture and not only its border, the bar that stands in where
// there are no marks, and the worn and held items in two rows of their own.
// The motion setting stops the glint, the system's does where there is none.
func TestThePanelsItemsMarksAndGlintAreStyled(t *testing.T) {
	css := read(t, "style.css")
	for _, need := range []string{
		".sheet.player .slot.enchanted .picture { animation: glint 2.4s ease-in-out infinite; }",
		".sheet.player .slot.enchanted { border-color: var(--glint);",
		":root[data-motion=\"reduce\"] .sheet.player .slot.enchanted .picture { animation: none;",
		"@media (prefers-reduced-motion: reduce) { :root:not([data-motion]) .sheet.player .slot.enchanted .picture { animation: none;",
		".sheet.player .gauge:has(.marks .picture:not([hidden])) .meter { display: none; }",
		".sheet.player .gauge:not(:has(.marks .picture:not([hidden]))) .marks { display: none; }",
		".sheet.player .equipment .worn { grid-template-columns: repeat(4, minmax(0, 1fr)); }",
		".sheet.player .equipment .held { grid-template-columns: repeat(2, minmax(0, 1fr)); }",
		".sheet.player .slot .wear { position: absolute; left: 3px; right: 3px; top: 2px;",
	} {
		if !bytes.Contains(css, []byte(need)) {
			t.Errorf("style.css no longer has %s", need)
		}
	}
	// A slot's bottom left is clear: the count is at the bottom right and the wear along the top.
	if bytes.Contains(css, []byte(".sheet.player .slot .wear { position: absolute; left: 3px; right: 3px; bottom:")) || bytes.Contains(css, []byte(".sheet.player .slot .count { position: absolute; left:")) {
		t.Error("style.css puts something in a slot's bottom left corner")
	}
	// The wash is one a theme chooses, and each of the three has one.
	if n := bytes.Count(css, []byte("  --glint: ")); n != 3 {
		t.Errorf("%d themes give the glint a colour, want all 3", n)
	}
}

// The rule in css whose selector is exactly sel, as its declarations. The
// stylesheet puts each such rule's selector at the start of a line.
func declarationsOf(t *testing.T, css []byte, sel string) map[string]string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(sel) + ` \{([^}]*)\}`).FindSubmatch(css)
	if m == nil {
		t.Fatalf("style.css has no rule for %s", sel)
	}
	out := map[string]string{}
	plain := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAll(m[1], nil)
	for _, decl := range strings.Split(string(plain), ";") {
		if name, value, ok := strings.Cut(decl, ":"); ok {
			out[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	return out
}

func pixelsOf(t *testing.T, what, value string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSuffix(value, "px"))
	if err != nil || !strings.HasSuffix(value, "px") {
		t.Fatalf("%s is %q, not a size in pixels", what, value)
	}
	return n
}

// What the panel asks icons.js for and what the stylesheet draws it at are
// the same: a picture 16 pixels a side shown at 32 is two pixels to each,
// at 16 one, and one made for 32 and shown at 24 is neither. So the size
// asked for is held to the one the stylesheet gives, the smaller size the
// stylesheet gives where a slot is small is a whole division of it, and none
// is a share of its slot, which was a pixel and a half on a phone.
func TestThePanelsPicturesAreDrawnInWholeMultiplesOfTheirPixels(t *testing.T) {
	css, js := read(t, "style.css"), read(t, "playerdetails.js")
	constant := func(name string) int {
		m := regexp.MustCompile(`const ` + name + ` = (\d+);`).FindSubmatch(js)
		if m == nil {
			t.Fatalf("playerdetails.js has no %s", name)
		}
		n, _ := strconv.Atoi(string(m[1]))
		return n
	}
	slot := declarationsOf(t, css, ".sheet.player .slot .picture")
	width, height := pixelsOf(t, "a slot's picture width", slot["width"]), pixelsOf(t, "a slot's picture height", slot["height"])
	if width != constant("SLOT_ICON") || height != width {
		t.Errorf("a slot's picture is %dx%d on the page and was drawn for %d", width, height, constant("SLOT_ICON"))
	}
	if declarationsOf(t, css, ".sheet.player .slot")["container-type"] != "inline-size" {
		t.Error("a slot is no container, so its picture cannot be sized by it")
	}
	small := regexp.MustCompile(`@container \(max-width: (\d+)px\) \{ \.sheet\.player \.slot \.picture \{ width: (\d+)px; height: (\d+)px; \} \}`).FindSubmatch(css)
	if small == nil {
		t.Fatal("style.css gives a slot too small for its picture a smaller one")
	}
	least, _ := strconv.Atoi(string(small[1]))
	narrow, _ := strconv.Atoi(string(small[2]))
	// The whole size is shown only where the slot is wider than the limit, so
	// the limit must leave it room, and the smaller is a whole division of it.
	if narrow == 0 || width%narrow != 0 || narrow >= width || string(small[2]) != string(small[3]) || least < width {
		t.Errorf("a small slot's picture is %sx%s, and it applies up to %d: not a whole division of %d that leaves the whole size room", small[2], small[3], least, width)
	}
	mark := declarationsOf(t, css, ".sheet.player .mark .picture")
	if pixelsOf(t, "a mark's width", mark["width"]) != constant("MARK_ICON") || mark["height"] != mark["width"] {
		t.Errorf("a mark is %s by %s on the page and was drawn for %d", mark["width"], mark["height"], constant("MARK_ICON"))
	}
	if !bytes.Contains(js, []byte("icons.picture(key, { side: SLOT_ICON, glint: enchanted })")) {
		t.Error("playerdetails.js no longer draws an item at the size the stylesheet gives it")
	}
	// The hotbar is parted from the backpack by a little, and the hands are
	// not parted from the armour beside them.
	if !bytes.Contains(css, []byte(".sheet.player .grids > .grid + .grid { margin-top: 3px; }")) || bytes.Contains(css, []byte(".sheet.player .grid + .grid {")) {
		t.Error("style.css parts every grid from the one before it, which puts the hands under the armour")
	}
}
