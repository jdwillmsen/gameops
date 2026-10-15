package web

import (
	"bytes"
	"os/exec"
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
