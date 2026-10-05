package live

import (
	"errors"
	"strings"
	"testing"
)

const playersLine = `[2026-10-05 12:00:00:123 INFO] [Scripting] MCMAP1 {"gen":417,"tick":8340,"dim":"overworld","kind":"players","part":0,"parts":1,"more":0,"items":[{"i":"-42949672","n":"Dotablaze","x":120.5,"y":64,"z":-310,"r":-37}]}`

func payload(line string) string {
	_, after, _ := strings.Cut(line, Sentinel)
	return after
}

func TestParseReadsARecordAsALineOrAsItsPayload(t *testing.T) {
	for name, in := range map[string]string{"line": playersLine, "payload": payload(playersLine)} {
		rec, stripped, err := Parse(in)
		if err != nil || stripped {
			t.Fatalf("%s: stripped=%v err=%v", name, stripped, err)
		}
		if rec.Gen != 417 || rec.Kind != Players || rec.Dimension != "overworld" || rec.Part != 0 || rec.Parts != 1 || len(rec.Items) != 1 {
			t.Fatalf("%s: record = %+v", name, rec)
		}
		it := rec.Items[0]
		if it.ID != "-42949672" || it.Name != "Dotablaze" || it.X != 120.5 || it.Y != 64 || it.Z != -310 || it.Yaw == nil || *it.Yaw != -37 {
			t.Errorf("%s: item = %+v", name, it)
		}
	}
}

func TestParseReadsAHeartbeat(t *testing.T) {
	rec, _, err := Parse(`{"gen":9,"kind":"tick","scan":3.5,"interval":1000,"ts":1790000000000}`)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Kind != Tick || rec.Gen != 9 || rec.ScanMillis != 3.5 || rec.IntervalMillis != 1000 || rec.SentMillis != 1790000000000 {
		t.Errorf("heartbeat = %+v", rec)
	}
}

// A record over 4 KB makes the server start the next line with a NUL. That
// next line is ours and is intact apart from it.
func TestParseStripsNulBytes(t *testing.T) {
	for name, in := range map[string]string{
		"leading":        "\x00" + playersLine,
		"before payload": strings.Replace(playersLine, Sentinel, Sentinel+"\x00", 1),
		"inside payload": strings.Replace(playersLine, `"gen"`, "\x00\"gen\"\x00", 1),
		"bare payload":   "\x00" + payload(playersLine),
	} {
		rec, stripped, err := Parse(in)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !stripped || rec.Gen != 417 || len(rec.Items) != 1 {
			t.Errorf("%s: stripped=%v record=%+v", name, stripped, rec)
		}
	}
}

func TestParseRejectsForeignScriptingLines(t *testing.T) {
	body := payload(playersLine)
	for name, in := range map[string]string{
		"no sentinel":           `[2026-10-05 12:00:00:123 INFO] [Scripting] ` + body,
		"another pack":          `[2026-10-05 12:00:00:123 INFO] [Scripting] hello from another pack`,
		"a later format":        `[2026-10-05 12:00:00:123 INFO] [Scripting] MCMAP2 ` + body,
		"sentinel not at start": `[2026-10-05 12:00:00:123 INFO] [Scripting] someone said MCMAP1 ` + body,
		"not a script line":     `[2026-10-05 12:00:00:123 INFO] Player connected: Steve, xuid: 1`,
		"empty":                 ``,
	} {
		if _, _, err := Parse(in); !errors.Is(err, ErrForeign) {
			t.Errorf("%s: err = %v, want ErrForeign", name, err)
		}
	}
}

// The payload carries names players choose. One that spells the sentinel
// must not be able to make its own record unreadable.
func TestParseIsNotFooledByASentinelInAName(t *testing.T) {
	in := `{"gen":1,"dim":"overworld","kind":"mobs","part":0,"parts":1,"more":0,"items":[{"i":"7","t":"cow","n":"MCMAP1 {","x":1,"y":2,"z":3}]}`
	rec, _, err := Parse(in)
	if err != nil || len(rec.Items) != 1 || rec.Items[0].Name != "MCMAP1 {" {
		t.Errorf("record = %+v, err = %v", rec, err)
	}
}

func TestParseRejectsUnknownKindsAndDimensions(t *testing.T) {
	good := payload(playersLine)
	for name, in := range map[string]string{
		"unknown kind":      strings.Replace(good, `"kind":"players"`, `"kind":"blocks"`, 1),
		"no kind":           strings.Replace(good, `"kind":"players",`, ``, 1),
		"unknown dimension": strings.Replace(good, `"dim":"overworld"`, `"dim":"aether"`, 1),
		"no dimension":      strings.Replace(good, `"dim":"overworld",`, ``, 1),
		"dimension as path": strings.Replace(good, `"dim":"overworld"`, `"dim":"../overworld"`, 1),
		"no generation":     strings.Replace(good, `"gen":417,`, ``, 1),
		"part past parts":   strings.Replace(good, `"part":0`, `"part":1`, 1),
		"no parts":          strings.Replace(good, `"parts":1`, `"parts":0`, 1),
		"too many parts":    strings.Replace(good, `"parts":1`, `"parts":100000`, 1),
		"negative more":     strings.Replace(good, `"more":0`, `"more":-1`, 1),
		"no id":             strings.Replace(good, `"i":"-42949672",`, ``, 1),
		"not json":          `{"gen":417,`,
		"trailing text":     good + ` {"gen":418}`,
	} {
		if rec, _, err := Parse(in); err == nil {
			t.Errorf("%s: accepted as %+v", name, rec)
		}
	}
}

// JSON cannot say NaN or Infinity, so a script that serialises one writes
// null, and a null decoded into a number is zero: a marker at the origin.
func TestParseRejectsNonFiniteCoordinates(t *testing.T) {
	good := payload(playersLine)
	for name, in := range map[string]string{
		"null x":         strings.Replace(good, `"x":120.5`, `"x":null`, 1),
		"null z":         strings.Replace(good, `"z":-310`, `"z":null`, 1),
		"missing y":      strings.Replace(good, `"y":64,`, ``, 1),
		"overflowing x":  strings.Replace(good, `"x":120.5`, `"x":1e999`, 1),
		"x as a string":  strings.Replace(good, `"x":120.5`, `"x":"NaN"`, 1),
		"bare NaN":       strings.Replace(good, `"x":120.5`, `"x":NaN`, 1),
		"bare Infinity":  strings.Replace(good, `"z":-310`, `"z":-Infinity`, 1),
		"overflowing r":  strings.Replace(good, `"r":-37`, `"r":1e999`, 1),
		"heartbeat scan": `{"gen":1,"kind":"tick","scan":1e999,"interval":1000}`,
	} {
		if rec, _, err := Parse(in); err == nil {
			t.Errorf("%s: accepted as %+v", name, rec)
		}
	}
}

func TestParseRejectsAnOversizePayload(t *testing.T) {
	in := `{"gen":1,"dim":"overworld","kind":"mobs","part":0,"parts":1,"more":0,"items":[],"pad":"` + strings.Repeat("a", maxPayload) + `"}`
	if _, _, err := Parse(in); !errors.Is(err, errTooLong) {
		t.Errorf("err = %v, want the size limit", err)
	}
}

func TestParseClipsALongNameOnARuneBoundary(t *testing.T) {
	name := strings.Repeat("é", 100)
	rec, _, err := Parse(`{"gen":1,"dim":"end","kind":"mobs","part":0,"parts":1,"more":0,"items":[{"i":"1","t":"cow","n":"` + name + `","x":0,"y":0,"z":0}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Items[0].Name; got != strings.Repeat("é", maxText/2) {
		t.Errorf("name = %q (%d bytes)", got, len(got))
	}
}
