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

// A site the seed gives is drawn as what it is: predicted, or only
// possible, each in a layer of its own with the reason in words, and a
// kind held back says why in its row.
func TestPredictionsAreToldFromPossibleSites(t *testing.T) {
	js := usesNoMarkupSink(t, "structures.js")
	for _, need := range []string{
		"['candidate', 'Possible', 'key candidate'],",
		"const sort = p.candidate ? 'candidate' : 'predicted';",
		"p.candidate ? 'possible here' : 'predicted from the seed'",
		"this terrain is not generated yet.",
		"the game has no record of one here: it keeps one only for a village a player has been near.",
		// Struck through only where the world would have recorded one.
		"const doubted = p.generated && !RECORDED_LATE.has(p.kind);",
		".addTo(groupOf(sort, p.kind));",
		// The counts in a kind's row, and why it has no predictions.
		"`${fmt(n.predicted)} predicted`",
		"`${fmt(n.candidate)} possible`",
		"const why = surveyed && state === 'verified' ? WHY_NOT_KIND[checks[kind]] : '';",
		// Off for a viewer who turned the predicted ones off.
		"const enabled = id !== 'candidate' || rows.get('predicted').enabled;",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("structures.js no longer has %s", need)
		}
	}
	css := read(t, "style.css")
	for _, need := range []string{".structure.predicted.candidate span {", ".key.candidate {"} {
		if !bytes.Contains(css, []byte(need)) {
			t.Errorf("style.css no longer has %s", need)
		}
	}
	search := usesNoMarkupSink(t, "search.js")
	for _, need := range []string{
		"const UNSURE = { predicted: 'predicted', candidate: 'possible site' };",
		"return Object.hasOwn(UNSURE, hit.certainty) ? `${what} (${UNSURE[hit.certainty]})` : what;",
	} {
		if !bytes.Contains(search, []byte(need)) {
			t.Errorf("search.js no longer has %s", need)
		}
	}
}
