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
	}
	if !bytes.Contains(read(t, "live.js"), []byte("duration.picker({")) {
		t.Error("live.js no longer chooses its length with the shared picker")
	}
	// The trails' window is one of four lengths or a typed one, read by
	// the same rules and said back the same way, in a box of its own and
	// never in a row of the panel. It is bounded by what the server says
	// it keeps, and a length past that is not offered.
	trails := read(t, "trails.js")
	for _, need := range []string{
		"const WINDOWS = [3600, 6 * 3600, 24 * 3600, 7 * 86_400];",
		"const WINDOW_NAMES = ['1 h', '6 h', '24 h', '7 d'];",
		"const longest = () => (retention > 0 ? retention : MAX_UNKNOWN);",
		"const read = duration.settle(typed, bounds());",
		"disabled: length > longest() && length !== seconds,",
		"if (row.setControl) row.setControl(windowed());",
	} {
		if !bytes.Contains(trails, []byte(need)) {
			t.Errorf("trails.js no longer has %s", need)
		}
	}
	if bytes.Contains(trails, []byte("createElement('input')")) || bytes.Contains(trails, []byte("createElement('select')")) {
		t.Error("trails.js builds a box of its own; the panel draws its choice")
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
		"pinned: () => pinned,",
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
	if !regexp.MustCompile(`(?s)<div id="currency" class="currency" role="group" aria-label="How current the map is">.*<span class="stat-label">Live positions</span> <output id="live-age" class="slot".*<span class="stat-label">Terrain</span> <span id="status" class="slot" role="status"></span> <output id="refresh" class="refresh slot then" role="timer"`).Match(page) {
		t.Error("index.html no longer groups the live age and the terrain's refresh under their labels")
	}
	css := read(t, "style.css")
	for _, need := range []string{"#live-age { min-width:", "#status { min-width:", ".refresh { min-width:", ".currency {", "font-variant-numeric: tabular-nums;"} {
		if !bytes.Contains(css, []byte(need)) {
			t.Errorf("style.css no longer has %s", need)
		}
	}
	// The More sheet is put away by its class on a small screen, so nothing
	// in the footer may share it, or it is put away with the sheet.
	if regexp.MustCompile(`(?s)<footer.*class="[^"]*\bmore\b[^-"]*".*</footer>`).Match(page) {
		t.Error("index.html gives something in the footer the More sheet's class")
	}
	live := read(t, "live.js")
	for _, need := range []string{"say(el.age, state);", "say(el.more, extras.join(' · '));"} {
		if !bytes.Contains(live, []byte(need)) {
			t.Errorf("live.js no longer has %s", need)
		}
	}
}

// A shortcut is a single key, so it must never fire from anything that
// takes text, must leave the browser's and a screen reader's own keys
// alone, and must be something the viewer can switch off.
func TestShortcutsStayOutOfTheWayOfTyping(t *testing.T) {
	js := usesNoMarkupSink(t, "menu.js")
	for _, need := range []string{
		"if (!enabled || e.defaultPrevented || e.ctrlKey || e.metaKey || e.altKey || e.isComposing) return;",
		"if (document.body.classList.contains('locked') || typing(e.target)) return;",
		"(node.matches('input, textarea, select, [contenteditable]:not([contenteditable=\"false\"])') && !node.matches(NOT_TEXT)) || node.closest('dialog[open]') !== null",
		// A switch takes no text, so the focus resting on one silences nothing.
		"const NOT_TEXT = 'input[type=\"checkbox\"], input[type=\"radio\"],",
		"if (!node || node.disabled || node.closest('[hidden]')) return false;",
		"el.dialog.showModal();",
		"if (e.target === el.dialog) el.dialog.close();",
		"if (settings) settings.set('shortcuts', { on: enabled });",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("menu.js no longer has %s", need)
		}
	}
	// Space and Enter are a focused switch's and a focused button's own.
	if regexp.MustCompile(`(?m)^    ('?[ ]'?|Enter|' '): \(\) =>`).Match(js) {
		t.Error("menu.js makes a shortcut of Space or Enter")
	}
	// Every key the script acts on is in the list the viewer is shown.
	page := read(t, "index.html")
	for _, key := range regexp.MustCompile(`(?m)^    '?([a-z0-9/?+-])'?: \(\) =>`).FindAllSubmatch(js, -1) {
		shown := bytes.ToUpper(key[1])
		if !bytes.Contains(page, append(append([]byte("<kbd>"), shown...), []byte("</kbd>")...)) {
			t.Errorf("menu.js acts on %s, which the list in index.html does not show", key[1])
		}
	}
	if !regexp.MustCompile(`<button id="help-open"[^>]*aria-haspopup="dialog"[^>]*aria-label="Keyboard shortcuts and search tips"`).Match(page) ||
		!bytes.Contains(page, []byte(`<dialog id="help" class="sheet" aria-labelledby="help-title">`)) {
		t.Error("index.html has no labelled button that opens the shortcuts dialog")
	}
}

// What the search box takes besides a name goes to a place on the map and
// nowhere else: nothing typed there becomes a command for the game.
func TestSearchShorthandGoesToPlacesAndWritesNoCommands(t *testing.T) {
	js := read(t, "search.js")
	for _, need := range []string{
		"const at = app.coordinates ? app.coordinates(typed) : null;",
		"if (/^spawn$/i.test(typed)) return { ask: { query: 'world spawn', kind: 'spawn' },",
		"return { ask: { query: name, kind: 'player', mine: true },",
		// Only @name is the whole question. Everything else typed is also
		// looked up as the name it may be, and listed beneath.
		"short && short.only ? null : ask(typed, '', mine)",
		"const list = [...found, ...rest];",
		"if (typed.startsWith('@')) {",
		"return short.ask.mine ? answer.list.filter((h) => app.isMe && app.isMe(h.name)) : answer.list;",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("search.js no longer has %s", need)
		}
	}
	// Shorthand that is the whole question is said so in one place only.
	if n := bytes.Count(js, []byte("only: true")); n != 2 {
		t.Errorf("search.js makes %d kinds of shorthand the whole question, want the two forms of @name", n)
	}
	// A shortcut goes to a place with a request and an answer of its own:
	// the list in the box, and the rows in it, are not its to replace.
	jump := regexp.MustCompile(`(?s)async jump\(what\) \{.*?\n    \},`).Find(js)
	if jump == nil {
		t.Fatal("search.js no longer has jump(); the pattern no longer matches the script")
	}
	for _, shared := range []string{"hits =", "show(", "choose(", "request ="} {
		if bytes.Contains(jump, []byte(shared)) {
			t.Errorf("search.js jump() uses %s, which is the result list's own state", shared)
		}
	}
	if !regexp.MustCompile(`function choose\(at\) \{\s*const hit = hits\[at\];\s*if \(!hit\) return;\s*open\(false\);[^}]*if \(!visit\(hit\)\) show\(hits, unrendered\(hit\)\);\s*\}`).Match(js) {
		t.Error("search.js no longer puts the list back, whole, when the map cannot go to the hit chosen")
	}
	for _, name := range []string{"search.js", "menu.js", "chunk.js", "live.js", "app.js", "index.html"} {
		body := read(t, name)
		for _, command := range []string{"/tp ", "/teleport", "/locate", "/give ", "/summon", "/gamemode"} {
			if bytes.Contains(body, []byte(command)) {
				t.Errorf("%s writes the game command %s", name, command)
			}
		}
	}
}

// The small-screen layout is one condition, written in three places that
// have to agree: the stylesheet that lays the page out, the script that
// keeps track of what is open, and the panel that must not arrive open.
func TestSmallScreenLayoutIsOneConditionEverywhere(t *testing.T) {
	const condition = "(max-width: 720px), (max-height: 480px)"
	css := read(t, "style.css")
	if !bytes.Contains(css, []byte("@media "+condition+" {")) {
		t.Errorf("style.css no longer lays out small screens under %s", condition)
	}
	for _, script := range []string{"compact.js", "layers.js"} {
		if !bytes.Contains(read(t, script), []byte("'"+condition+"'")) {
			t.Errorf("%s no longer uses the stylesheet's condition, %s", script, condition)
		}
	}
	for _, need := range []string{
		"html, body { height: 100vh; height: 100dvh; }",
		"env(safe-area-inset-top)", "env(safe-area-inset-bottom)", "env(safe-area-inset-left)", "env(safe-area-inset-right)",
		"min-height: 44px;",
		"@media (prefers-reduced-motion: no-preference) {",
		".more, .more-part { display: contents; }",
		".searching .inspect, .more-shown .inspect, .currency-shown .inspect, .stage:has(.layers.open) .inspect { display: none; }",
		// Logged out, More has nothing in it, and a part whose controls
		// are all hidden goes with its title and its note.
		".locked .more-open, .locked.more-shown .more { display: none; }",
		".more-part:not(:has(> :not(.more-title):not(.more-note):not([hidden]))) { display: none; }",
	} {
		if !bytes.Contains(css, []byte(need)) {
			t.Errorf("style.css no longer has %s", need)
		}
	}
	// A browser without dvh drops a declaration that uses it, so each one
	// follows the same thing said in vh: a sheet with no limit cannot scroll.
	lines := bytes.Split(css, []byte("\n"))
	for i, line := range lines {
		if !bytes.Contains(line, []byte("dvh")) {
			continue
		}
		before := lines[max(i-1, 0)]
		same := bytes.Contains(line, []byte("height: 100vh; height: 100dvh;")) ||
			bytes.Equal(bytes.ReplaceAll(line, []byte("dvh"), []byte("vh")), before)
		if !same {
			t.Errorf("style.css line %d uses dvh with no vh declaration before it: %s", i+1, bytes.TrimSpace(line))
		}
	}
	js := usesNoMarkupSink(t, "compact.js")
	for _, need := range []string{
		"for (const other of Object.keys(PARTS)) if (other !== part) set(other, false);",
		"if (part !== 'panel') shutPanel();",
		"PARTS[part].setAttribute('aria-expanded', String(on));",
		"document.addEventListener('pointerdown', (e) => {",
		"media.addEventListener('change', relaid);",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("compact.js no longer has %s", need)
		}
	}
	// Nothing it opens is kept for the next visit.
	if bytes.Contains(js, []byte("localStorage")) {
		t.Error("compact.js keeps something in the browser; what is open on a small screen is not a choice to keep")
	}
	if !bytes.Contains(read(t, "layers.js"), []byte("let open = !compact.matches && (typeof view.open === 'boolean' ? view.open : true);")) {
		t.Error("layers.js no longer starts with the panel shut on a small screen")
	}
	// Nor is it left open over the map by a window that becomes small.
	if !regexp.MustCompile(`compact\.addEventListener\('change', \(\) => \{\s*open = !compact\.matches && \(typeof view\.open === 'boolean' \? view\.open : true\);\s*paint\(\);`).Match(read(t, "layers.js")) {
		t.Error("layers.js no longer shuts the panel when the layout becomes the small screen's")
	}
	page := read(t, "index.html")
	for _, need := range []string{
		`<button id="search-open" class="search-open compact-only" type="button" aria-label="Search the map" aria-expanded="false" aria-controls="search">`,
		`<button id="more-open" class="more-open compact-only" type="button" aria-expanded="false" aria-controls="more">`,
		`<button id="currency-open" class="currency-open compact-only" type="button" aria-expanded="false" aria-controls="currency"`,
		`<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">`,
	} {
		if !bytes.Contains(page, []byte(need)) {
			t.Errorf("index.html no longer has %s", need)
		}
	}
}
