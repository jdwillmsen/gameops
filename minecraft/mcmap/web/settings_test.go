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
func TestOnlyTheSettingsScriptTouchesTheBrowsersStorage(t *testing.T) {
	scripts, err := fs.Glob(FS, "*.js")
	if err != nil || len(scripts) < 10 {
		t.Fatalf("found %d scripts (%v); the pattern no longer matches the page", len(scripts), err)
	}
	for _, name := range scripts {
		if name == "settings.js" {
			continue
		}
		body := read(t, name)
		for _, store := range []string{"localStorage", "sessionStorage", "indexedDB", "document.cookie"} {
			if bytes.Contains(body, []byte(store)) {
				t.Errorf("%s uses %s; what the page keeps goes through settings.js", name, store)
			}
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
		"if (raw && Number.isInteger(raw.v) && raw.v > VERSION) kept = 'newer';",
		"if (!store || kept === 'newer') return;",
		// Storage refused, and storage full, each leave the page working.
		"store = null;\n    kept = 'no';",
		"kept = 'full';",
		// Another tab's write is taken in, not written over.
		"addEventListener('storage', (e) => {",
		"window.mcmap.settings = {",
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
		"try { store.setItem(key, now); } catch { /* the record itself was kept */ }",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("settings.js no longer has %s", need)
		}
	}
	// Nothing ever takes an old key away: a page from before still reads it.
	if bytes.Contains(js, []byte("removeItem")) || bytes.Contains(js, []byte(".clear()")) {
		t.Error("settings.js removes something from the browser's storage")
	}
	// A script that meets a page from before the record works without it.
	for _, script := range []string{"app.js", "layers.js", "live.js", "markers.js", "icons.js", "trails.js", "biomes.js", "slime.js", "chunk.js", "menu.js"} {
		body := read(t, script)
		if !regexp.MustCompile(`const settings = (\(window\.mcmap && window\.mcmap\.settings\)|app\.settings) \|\| null;`).Match(body) {
			t.Errorf("%s no longer allows for a page with no settings script", script)
		}
	}
	// The script that makes the page's object must keep what the head
	// already put there.
	if !bytes.Contains(read(t, "app.js"), []byte("window.mcmap = Object.assign(window.mcmap || {}, {")) {
		t.Error("app.js no longer keeps the settings the head script put on window.mcmap")
	}
}

// The grid and the biome picked out were not kept before the record and
// are now. A page that is only starting has no dimension yet, which is the
// state a logged-out page is in as well: only the second lets go of what
// was kept.
func TestTheGridAndTheBiomePickedOutSurviveAPageStart(t *testing.T) {
	biomes := read(t, "biomes.js")
	for _, need := range []string{
		"let only = settings ? settings.get('biome').only : null;",
		"if (locked) only = null;",
		"if (settings && settings.get('biome').only !== only) settings.set('biome', { only: only !== null && NAME.test(only) ? only : null });",
	} {
		if !bytes.Contains(biomes, []byte(need)) {
			t.Errorf("biomes.js no longer has %s", need)
		}
	}
	if regexp.MustCompile(`clear\(\);\s*only = null;\s*queued = false;`).Match(biomes) {
		t.Error("biomes.js lets go of the biome kept from last time while the page is still starting")
	}
	app := read(t, "app.js")
	if !regexp.MustCompile(`if \(settings && settings\.get\('grid'\)\.on\) \{\s*el\.grid\.checked = true;\s*grid\.addTo\(map\);`).Match(app) || !bytes.Contains(app, []byte("if (settings) settings.set('grid', { on: el.grid.checked });")) {
		t.Error("app.js no longer keeps the grid's switch and puts it back")
	}
}
