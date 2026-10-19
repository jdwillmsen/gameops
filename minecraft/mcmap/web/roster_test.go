package web

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// The list of players is run and not read: what it asks for, what it
// draws, and that a gamertag stays text. Skipped where there is no node to
// run it with.
func TestRosterDoesWhatItSaysWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the roster's script with")
	}
	out, err := exec.Command(node, "testdata/roster.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the roster's script did not do what is asked of it: %v", err)
	}
}

// The list is its own dialog beside the map, opened from the menu, and
// hands a row to the player's details.
func TestRosterIsOfferedFromTheMenu(t *testing.T) {
	js := usesNoMarkupSink(t, "roster.js")
	for _, need := range []string{
		"fetch('api/players', { cache: 'no-store', signal: mine.signal })",
		"app.playerDetails.open({ id: text(p.id), name: text(p.name) })",
		"view.dialog.show();",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("roster.js no longer has %s", need)
		}
	}
	if bytes.Contains(js, []byte("showModal")) {
		t.Error("roster.js takes the page with a modal")
	}
	page := read(t, "index.html")
	for _, need := range []string{`id="roster-open"`, `id="roster" class="sheet aside roster"`, `src="roster.js"`} {
		if !bytes.Contains(page, []byte(need)) {
			t.Errorf("index.html no longer has %s", need)
		}
	}
}
