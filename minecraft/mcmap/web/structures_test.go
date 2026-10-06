package web

import (
	"bytes"
	"testing"
)

// The world spawn is served with the overworld's structures, and is drawn
// from that answer under a row of its own.
func TestWorldSpawnIsDrawnFromTheStructuresAnswer(t *testing.T) {
	js := usesNoMarkupSink(t, "structures.js")
	for _, need := range []string{
		"['spawn', 'World spawn', 'key spawn'],",
		"const spawn = data.spawn;",
		"tip('World spawn', where)",
		"rows.get('spawn').setAvailable(!surveyed || spawned);",
		// A kind is its picture, with the letter behind it until there is one.
		"mark.append(icons.picture(icons.keyOf('structure', { kind })), letter);",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("structures.js no longer has %s", need)
		}
	}
}
