package web

import (
	"bytes"
	"regexp"
	"strconv"
	"testing"
)

// A trail is labelled with a gamertag, which a player chose. The viewer's
// own is drawn in the colour the live layer gives them, by live.js's one
// function, so the two can never disagree; nobody else's may come near it.
func TestTrailsAreColouredByPlayerAndNamedAsText(t *testing.T) {
	js := usesNoMarkupSink(t, "trails.js")
	loadedAfterThePanel(t, "trails.js")
	for _, need := range []string{
		"span.textContent = s;",
		"lines.bindTooltip((line) => text(labelOf(line)),",
		"if (app.isMe && app.playerColour && app.isMe(name)) return app.playerColour(name);",
		"const first = hash(name) % PALETTE.length;",
		"name.textContent = trail.name;",
		// Under the markers and still hovered: only on their canvas.
		"const shared = app.liveRenderer || null;",
		"casing.bringToBack();",
		"app.layers.register({ group: 'overlays', id: 'trails', label: 'Trails', enabled: false,",
		// A service without trails answers 404, and the row is never made.
		"if (res.status === 404) {",
		"document.addEventListener('mcmap:players', follow);",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("trails.js no longer has %s", need)
		}
	}
	palette := regexp.MustCompile(`const PALETTE = \[([^\]]+)\];`).FindSubmatch(js)
	if palette == nil {
		t.Fatal("trails.js no longer lists the colours it gives the other players")
	}
	colours := regexp.MustCompile(`#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})`).FindAllSubmatch(palette[1], -1)
	if len(colours) < 6 {
		t.Fatalf("trails.js has %d colours for the other players, want at least 6", len(colours))
	}
	channel := func(b []byte) int { v, _ := strconv.ParseInt(string(b), 16, 0); return int(v) }
	for _, c := range colours {
		// The viewer's own green is #6ecf7a.
		dr, dg, db := channel(c[1])-0x6e, channel(c[2])-0xcf, channel(c[3])-0x7a
		if dr*dr+dg*dg+db*db < 100*100 {
			t.Errorf("trails.js gives another player %s, too close to the viewer's own green", c[0])
		}
	}
	if regexp.MustCompile(`#6ecf7a|const ME\b`).Match(js) {
		t.Error("trails.js has a copy of the live layer's colour for the viewer's own marker")
	}
	live := read(t, "live.js")
	for _, need := range []string{
		"const playerColour = (name) => (isMe(name) ? ME : CATEGORIES.players);",
		"app.playerColour = playerColour;",
		"app.isMe = isMe;",
		"app.liveRenderer = renderer;",
		"new CustomEvent('mcmap:players', { detail: { dimension: pictured, players: frame.players || [] } })",
	} {
		if !bytes.Contains(live, []byte(need)) {
			t.Errorf("live.js no longer has %s", need)
		}
	}
}

// Whether the layer is on is the panel's to keep, under mcmap.layers. The
// one thing kept here is the window, which is not a switch.
func TestTrailsAskNoMoreOftenThanEveryHalfMinute(t *testing.T) {
	js := read(t, "trails.js")
	m := regexp.MustCompile(`const REFRESH_MS = ([0-9_]+);`).FindSubmatch(js)
	if m == nil {
		t.Fatal("trails.js no longer says how often it asks")
	}
	if ms, _ := strconv.Atoi(string(bytes.ReplaceAll(m[1], []byte("_"), nil))); ms < 30_000 {
		t.Errorf("trails.js asks every %d ms, want no more often than every 30 s", ms)
	}
	if got := bytes.Count(js, []byte("fetch(")); got != 1 {
		t.Errorf("trails.js has %d fetches, want the one that is held to the interval", got)
	}
	keys := regexp.MustCompile(`'mcmap\.[a-zA-Z]+'`).FindAll(js, -1)
	if len(keys) != 1 || string(keys[0]) != "'mcmap.trails'" {
		t.Errorf("trails.js keeps %s in the browser, want only the window under 'mcmap.trails'", keys)
	}
	if !bytes.Contains(js, []byte("JSON.stringify({ seconds })")) {
		t.Error("trails.js keeps more than the window under its key")
	}
}
