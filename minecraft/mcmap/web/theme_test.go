package web

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// The switch is built by theme.js into an empty holder, so a page that has
// the holder and not the script, or the script and not the holder, shows
// nothing at all.
func TestEveryPageWithAThemeSwitchLoadsItsScript(t *testing.T) {
	for _, name := range append([]string{"index"}, aboutPages...) {
		page := read(t, name+".html")
		if !bytes.Contains(page, []byte(`data-theme-switch`)) {
			t.Errorf("%s has no holder for the theme switch", name)
		}
		if !bytes.Contains(page, []byte(`<script src="theme.js"></script>`)) {
			t.Errorf("%s does not load theme.js", name)
		}
		if at, settings := bytes.Index(page, []byte(`src="theme.js"`)), bytes.Index(page, []byte(`src="settings.js"`)); at < settings {
			t.Errorf("%s loads theme.js before settings.js, which it asks for the record", name)
		}
	}
}

func TestThemeSwitchDoesWhatItSaysWhenRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on this machine to run the theme switch with")
	}
	out, err := exec.Command(node, "testdata/theme.test.cjs").CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			t.Log(line)
		}
	}
	if err != nil {
		t.Errorf("the theme switch did not do what is asked of it: %v", err)
	}
}
