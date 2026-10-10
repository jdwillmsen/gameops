package web

import (
	"bytes"
	"regexp"
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
		// And for a kind an opened chest stops saying anything of.
		"That is not the world saying there is none:",
		// Struck through only where the world would have recorded one.
		"const doubted = p.generated && !quiet(p.kind);",
		// Which kinds are so is the server's to say, and the village's for
		// a server that does not.
		"const quiet = (kind) => (catalog !== null && catalog.has(kind) ? catalog.get(kind).quiet : RECORDED_LATE.has(kind));",
		".addTo(groupOf(sort, p.kind));",
		// Each kind is listed once, with how many are known and under it
		// how many more are predicted or possible, and why none is.
		"count: surveyed ? n.recorded : null,",
		"const more = [...(n.predicted > 0 ? [`${fmt(n.predicted)} predicted`] : []), ...(n.candidate > 0 ? [`${fmt(n.candidate)} possible`] : [])].join(', ');",
		"const why = surveyed && state === 'verified' ? WHY_NOT_KIND[checks[kind]] || '' : '';",
		"kindList.setItems(kindsNow());",
		// A mark is on the map when its kind and its certainty both are,
		// so one kind alone is that kind however sure the map is of it.
		"const want = on(sort) && kindOn(kind);",
		"bare: true, facet: 'kinds',",
		"heading: 'How sure',",
		// Off for a viewer who turned the predicted ones off.
		"const enabled = id !== 'candidate' || rows.get('predicted').enabled;",
		// Off for everybody until they ask: a stronghold is for finding.
		"const OPT_IN = new Set(['stronghold']);",
		"app.layers.facet('structures', 'kinds', { off: [...OPT_IN] })",
		"off: asked,",
		// And for a panel from before it listed kinds.
		"{ shows: (kind) => !OPT_IN.has(kind), onChange() {} };",
		"shows: (kind) => on('recorded') && kindOn(kind),",
		"asked: () => (on('recorded') ? [...OPT_IN].filter((kind) => choice.shows(kind)) : []),",
		// One player's answer is not kept for the next to log in.
		"forgetDetail();\n      if (sheet && view.dialog.open) view.dialog.close();",
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
		// And listed by a search only for a viewer with that row on.
		"app.structures.shows('stronghold') ? '&strongholds=1' : ''",
		"app.structures.asked().length > 0 ? `&asked=${encodeURIComponent(app.structures.asked().join(','))}` : ''",
		"const UNSURE = { predicted: 'predicted', candidate: 'possible site' };",
		"return Object.hasOwn(UNSURE, hit.certainty) ? `${what} (${UNSURE[hit.certainty]})` : what;",
	} {
		if !bytes.Contains(search, []byte(need)) {
			t.Errorf("search.js no longer has %s", need)
		}
	}
}

// The details of a structure say only what the server sent and what the
// loaded layers hold, each as text, and name it in the address by its
// kind and middle so that a link can open it again.
func TestStructureDetailsAreTextAndCanBeLinkedTo(t *testing.T) {
	js := usesNoMarkupSink(t, "structures.js")
	for _, need := range []string{
		"if (subject) marker.on('click', () => detail(subject));",
		"if (value instanceof Node) dd.append(value); else dd.textContent = String(value);",
		"return `structure~${kindOf(subject)}~${subject.recorded ? 'r' : 'p'}~${at.x}~${at.z}`;",
		"const m = /^structure~([a-z0-9_]{1,40})~([rp])~(-?\\d{1,9})~(-?\\d{1,9})$/.exec(app.link.get());",
		"if (!view.dialog.open) view.dialog.showModal();",
		"if (e.target === view.dialog) view.dialog.close();",
		"['Counts', 'Not counted by the game yet:",
		"const kept = app.markers ? app.markers.within(s) : null;",
		"const live = app.inspect ? app.inspect.within(s) : null;",
		"`api/biomes/at?dimension=${encodeURIComponent(shown)}&x=${at.x}&z=${at.z}`",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("structures.js no longer has %s", need)
		}
	}
	page := read(t, "index.html")
	for _, id := range []string{"structure", "structure-picture", "structure-title", "structure-standing", "structure-body", "structure-go", "structure-copy", "structure-link", "structure-said", "structure-close"} {
		if !bytes.Contains(page, []byte(`id="`+id+`"`)) {
			t.Errorf("index.html has no element with id %s", id)
		}
	}
	// The address keeps a fifth part through every rewrite, and takes
	// nothing there that is not a short plain token.
	app := read(t, "app.js")
	for _, need := range []string{
		"const EXTRA = /^[a-z0-9_~.-]{1,96}$/;",
		"${map.getZoom()}${extra ? `/${extra}` : ''}`;",
		"document.dispatchEvent(new CustomEvent('mcmap:link'));",
	} {
		if !bytes.Contains(app, []byte(need)) {
			t.Errorf("app.js no longer has %s", need)
		}
	}
}

// A structure asked for before its dimension has answered is a request to
// open a sheet now. It is let go when the answer fails, when the map leaves
// that dimension, and after a few seconds, so that no sheet opens later
// over whatever the viewer has moved on to.
func TestAStructureAskedForDoesNotOpenLater(t *testing.T) {
	js := read(t, "structures.js")
	for _, need := range []string{
		"wantedTimer = setTimeout(() => unwant(",
		"if (wanted && (locked || dimension !== wanted.dimension)) unwant('');",
		"unwant('The structure’s details could not be fetched. Choose it again to try once more.');",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("structures.js no longer has %s", need)
		}
	}
	// Nothing sets it but the one function that also starts its clock.
	if n := len(regexp.MustCompile(`\bwanted = `).FindAll(js, -1)); n != 3 {
		t.Errorf("structures.js assigns wanted in %d places, want its declaration, want() and unwant()", n)
	}
}

// The kinds listed are the ones the dimension on screen can hold, as the
// server says and not as this script supposes: a kind of another dimension
// is no row here, and its choice is kept for when it is one.
func TestKindsAreListedByTheDimensionTheServerPutsThemIn(t *testing.T) {
	js := usesNoMarkupSink(t, "structures.js")
	for _, need := range []string{
		"learn(data.catalog);",
		"next.set(k.kind, { dimensions, asked: k.asked === true, quiet: k.quiet === true });",
		// Off until asked for from before its row is drawn.
		"if (k.asked === true) OPT_IN.add(k.kind);",
		"if (app.layers.facet) app.layers.facet('structures', 'kinds', { off: [...OPT_IN] });",
		// Every kind in every dimension for a server that says nothing.
		"const kindsIn = (dimension) => (catalog === null ? Object.keys(KINDS) : [...catalog].filter(([, k]) => k.dimensions.includes(dimension)).map(([kind]) => kind));",
		"for (const kind of kindsIn(dimension)) {",
		"if (kindList.setElsewhere && catalog !== null) kindList.setElsewhere([...catalog.keys()]);",
		// And the server is told this page lists what it is told to.
		"`api/structures?dimension=${encodeURIComponent(dimension)}&kinds=all`",
	} {
		if !bytes.Contains(js, []byte(need)) {
			t.Errorf("structures.js no longer has %s", need)
		}
	}
	layers := read(t, "layers.js")
	for _, need := range []string{
		"const all = new Set([...known(f), ...[...f.rows].flatMap((row) => row.elsewhere)]);",
		"row.elsewhere = Array.isArray(ids) ? ids.filter(text).slice(0, MAX_ELSEWHERE) : [];",
	} {
		if !bytes.Contains(layers, []byte(need)) {
			t.Errorf("layers.js no longer has %s", need)
		}
	}
}
