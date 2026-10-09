package web

import (
	"bytes"
	"io/fs"
	"regexp"
	"testing"
)

// Everything the page keeps goes through one script, which checks it on the
// way in. A second script that reads storage for itself reads it unchecked,
// and is one more key nobody carries over the next time the record changes.
// The four scripts that had a key of their own before still know it, for
// the one case of a page from before the settings script, and touch it in
// no other.
func TestOnlyTheSettingsScriptTouchesTheBrowsersStorage(t *testing.T) {
	scripts, err := fs.Glob(FS, "*.js")
	if err != nil || len(scripts) < 10 {
		t.Fatalf("found %d scripts (%v); the pattern no longer matches the page", len(scripts), err)
	}
	// How many times each may name the browser's storage: once to read and
	// once to write each key it had.
	fallback := map[string]int{"layers.js": 2, "live.js": 3, "trails.js": 2, "menu.js": 2}
	for _, name := range scripts {
		if name == "settings.js" {
			continue
		}
		body := read(t, name)
		for _, store := range []string{"sessionStorage", "indexedDB", "document.cookie"} {
			if bytes.Contains(body, []byte(store)) {
				t.Errorf("%s uses %s; what the page keeps goes through settings.js", name, store)
			}
		}
		if got := bytes.Count(body, []byte("localStorage")); got != fallback[name] {
			t.Errorf("%s names localStorage %d times, want %d; what the page keeps goes through settings.js", name, got, fallback[name])
		}
	}
	js := usesNoMarkupSink(t, "settings.js")
	for _, need := range []string{
		"const KEY = 'mcmap.settings';",
		"const VERSION = 1;",
		"const MAX_CHARS = 200_000;",
		// A record too large is refused whole, and one from a later
		// version of the page is read and left as it is.
		"if (JSON.stringify(next).length > MAX_CHARS) return false;",
		"} else if (raw && Number.isInteger(raw.v) && raw.v > VERSION) {\n      kept = 'newer';",
		"if (!store || kept === 'newer' || kept === 'large') return;",
		// Storage refused, and storage full, each leave the page working.
		"store = null;\n    kept = 'no';",
		"kept = 'full';",
		// Another tab's write is taken in, not written over.
		"addEventListener('storage', (e) => {",
		"window.mcmapSettings = {",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("settings.js no longer has %s", need)
		}
	}
	// Read strictly, anything wrong anywhere fails the whole; read
	// leniently, an unknown key is dropped and a bad value left out for its
	// default to fill.
	if !regexp.MustCompile(`const kept = Object\.hasOwn\(fields, name\) \? fields\[name\]\(v\[name\], strict\) : BAD;\s*if \(kept !== BAD\) out\[name\] = kept;\s*else if \(strict\) return BAD;`).Match(js) {
		t.Error("settings.js no longer drops an unknown key when lenient and fails on one when strict")
	}
	if !bytes.Contains(js, []byte("const out = { ...DEFAULTS[section](), ...(kept === BAD ? {} : kept) };")) {
		t.Error("settings.js no longer puts a default in the place of what it dropped")
	}
	// It runs before the page is drawn, so the theme is there from the
	// first frame, and before every script that asks it for its part.
	page := read(t, "index.html")
	head := bytes.Index(page, []byte("</head>"))
	at := bytes.Index(page, []byte(`<script src="settings.js"></script>`))
	if at < 0 || at > head || at < bytes.Index(page, []byte(`href="style.css"`)) {
		t.Error("index.html must load settings.js in the head, after the stylesheet it reads the colours of")
	}
}

// The keys each script kept for itself are read once, to start the record
// from, and are still written in the form they had: the page and its
// scripts are cached apart, and a script from before reads only those.
func TestOldKeysAreCarriedOverAndKeptUpToDate(t *testing.T) {
	js := read(t, "settings.js")
	for _, key := range []string{"mcmap.layers", "mcmap.panel", "mcmap.liveControl", "mcmap.trails", "mcmap.shortcuts", "mcmap.live", "mcmap.markers", "mcmap.structures"} {
		if !bytes.Contains(js, []byte("'"+key+"'")) {
			t.Errorf("settings.js no longer carries over what was kept under %s", key)
		}
	}
	for _, need := range []string{
		"state = wholeRecord(raw || carriedOver());",
		// Hours, from when the trails' window was one of three.
		"if (trails) raw.trails = { seconds: Number.isFinite(trails.seconds) ? trails.seconds : trails.hours * 3600 };",
		"for (const [section, key] of Object.entries(OLD)) {",
		"state.old[section] = stamp(key);",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("settings.js no longer has %s", need)
		}
	}
	// Nothing ever takes an old key away: a page from before still reads it.
	if bytes.Contains(js, []byte("removeItem")) || bytes.Contains(js, []byte(".clear()")) {
		t.Error("settings.js removes something from the browser's storage")
	}
	// A script that meets a page from before the record works without it,
	// and finds it under a name of its own: the script that makes the
	// page's object may itself be from before, and would put its own object
	// in the place of anything the record had been hung on.
	for _, script := range []string{"app.js", "layers.js", "live.js", "markers.js", "icons.js", "trails.js", "biomes.js", "slime.js", "chunk.js", "menu.js"} {
		if !bytes.Contains(read(t, script), []byte("const settings = window.mcmapSettings || null;")) {
			t.Errorf("%s no longer allows for a page with no settings script", script)
		}
	}
	for _, script := range []string{"settings.js", "views.js", "appearance.js"} {
		if regexp.MustCompile(`(window\.mcmap|app)\.settings\b`).Match(read(t, script)) {
			t.Errorf("%s reaches the settings through the page's object, which an older app.js replaces", script)
		}
	}
}

// With no settings script on the page, each script that had a key of its
// own reads and writes that key as it always did: the viewer's choices do
// not go back to the defaults for the minutes a cached page lasts.
func TestAPageFromBeforeTheRecordKeepsItsChoicesInTheOldKeys(t *testing.T) {
	for script, needs := range map[string][]string{
		"layers.js": {
			"const unkept = settings ? null : readOld(CHOICES_KEY);",
			"else writeOld(CHOICES_KEY, choices);",
			"const view = settings ? settings.get('panel') : readOld(PANEL_KEY);",
			"else writeOld(PANEL_KEY, { open: view.open, folded: [...folded] });",
			"structures: { key: 'mcmap.structures', master: ['recorded', 'predicted', 'candidate'] },",
			"if (settings || !Object.hasOwn(LEGACY, group)) return fallback;",
		},
		"live.js": {
			"const CONTROL_KEY = 'mcmap.liveControl';",
			"const OLD_KEY = 'mcmap.live';",
			"control.paused = Boolean(old) && old.on === false;",
			"try { localStorage.setItem(CONTROL_KEY, JSON.stringify(control)); } catch { /* not kept, still applied */ }",
		},
		"trails.js": {
			"const WINDOW_KEY = 'mcmap.trails';",
			"else try { localStorage.setItem(WINDOW_KEY, JSON.stringify({ seconds })); } catch { /* not kept, still applied */ }",
		},
		"menu.js": {
			"const KEY = 'mcmap.shortcuts';",
			"else try { localStorage.setItem(KEY, JSON.stringify({ on: enabled })); } catch { /* not kept, still applied */ }",
		},
	} {
		body := read(t, script)
		for _, need := range needs {
			if !bytes.Contains(body, []byte(need)) {
				t.Errorf("%s no longer has %s", script, need)
			}
		}
	}
	// And what such a script writes is taken up by the record: an old key
	// that is not as the record last saw it holds the newer choice.
	js := read(t, "settings.js")
	for _, need := range []string{
		"if (!fresh && now !== 0 && now !== (state.old[section] || 0)) {",
		"if (theirs) state[section] = whole(section, theirs);",
		"if (changed) write();",
		// A stranger's file says nothing of this browser's old keys.
		"replace: (next) => commit({ ...wholeRecord(next), old: state.old }),",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("settings.js no longer has %s", need)
		}
	}
}

// The grid and the biome picked out were not kept before the record and
// are now. A page that is only starting has no dimension yet, which is the
// state a logged-out page is in as well: only the second lets go of what
// was kept.
func TestTheGridAndTheBiomePickedOutSurviveAPageStart(t *testing.T) {
	biomes := read(t, "biomes.js")
	for _, need := range []string{
		"let lone = settings ? settings.get('biome').only : null;",
		"if (locked && choice.only !== null) choice.solo(null);",
		"const only = choice.only !== null && NAME.test(choice.only) ? choice.only : null;",
		"if (settings && settings.get('biome').only !== only) settings.set('biome', { only });",
	} {
		if !bytes.Contains(biomes, []byte(need)) {
			t.Errorf("biomes.js no longer has %s", need)
		}
	}
	if regexp.MustCompile(`clear\(\);\s*(only = null|choice\.solo\(null\));\s*queued = false;`).Match(biomes) {
		t.Error("biomes.js lets go of the biome kept from last time while the page is still starting")
	}
	app := read(t, "app.js")
	if !regexp.MustCompile(`if \(settings && settings\.get\('grid'\)\.on\) \{\s*el\.grid\.checked = true;\s*grid\.addTo\(map\);`).Match(app) || !bytes.Contains(app, []byte("if (settings) settings.set('grid', { on: el.grid.checked });")) {
		t.Error("app.js no longer keeps the grid's switch and puts it back")
	}
}

// A key is the viewer's to write, or a stranger's, and must never be a
// name every object already has; a record larger than the page writes is
// not the page's, and is neither used nor written over; and another tab's
// change of theme is on this one at once, not at its next unrelated write.
func TestTheRecordIsNotFooledByItsOwnStorage(t *testing.T) {
	js := read(t, "settings.js")
	for _, need := range []string{
		"const inherited = (name) => name in Object.prototype;",
		"const out = Object.create(null);",
		"const kept = n < max && key.test(name) && !inherited(name) ? kind(v[name], strict, name) : BAD;",
		"if (text !== null && text.length > MAX_CHARS) {",
		"kept = 'large';\n      raw = {};",
		"if (e.storageArea !== store || e.key !== KEY || kept === 'large') return;",
		"kept = Number.isInteger(raw.v) && raw.v > VERSION ? 'newer' : 'yes';",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("settings.js no longer has %s", need)
		}
	}
	if !regexp.MustCompile(`if \(looked === JSON\.stringify\(state\.look\)\) return;\s*paint\(\);\s*tell\(\['look'\]\);`).Match(js) {
		t.Error("settings.js no longer puts another tab's look on this one when it arrives")
	}
	// The system changing its mind is told by what was on the page before.
	if !regexp.MustCompile(`const was = painted;\s*paint\(\);\s*if \(painted !== was\) tell\(\['look'\]\);`).Match(js) {
		t.Error("settings.js no longer compares the look with what was painted before the system changed")
	}
	if !bytes.Contains(read(t, "views.js"), []byte("let pending = settings.kept() === 'large' ?")) {
		t.Error("views.js no longer says once that an oversized record was left alone")
	}
}
