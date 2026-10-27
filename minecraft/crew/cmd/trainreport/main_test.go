package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture produces a training log a trainer would have left behind,
// including the aborted fights and the torn last line the report must
// tolerate.
func writeFixture(t *testing.T, lines string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "training.jsonl")
	if err := os.WriteFile(path, []byte(lines), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

var (
	// Two profiles, one family, paired on seeds 101 and 102; alpha leaves
	// a positive score gap on each equal seed.
	alpha = `{"ts":"2026-10-27T10:01:00Z","runId":"r1","scenario":"mob","family":"zombie","profile":"alpha","outcome":"win","seconds":30,"seed":101}
`
	beta = `{"ts":"2026-10-27T10:02:00Z","runId":"r1","scenario":"mob","family":"zombie","profile":"beta","outcome":"win","seconds":60,"seed":101}
`
	secondSeedA = `{"ts":"2026-10-27T10:11:00Z","family":"zombie","profile":"alpha","outcome":"win","seconds":30,"seed":102}
`
	secondSeedB = `{"ts":"2026-10-27T10:12:00Z","family":"zombie","profile":"beta","outcome":"loss","seconds":20,"seed":102}
`
	skullSeed = `{"ts":"2026-10-27T10:21:00Z","family":"skeleton","profile":"alpha","outcome":"win","seconds":45,"seed":103}
`

	aborted = `{"ts":"2026-10-27T10:31:00Z","family":"zombie","profile":"alpha","outcome":"aborted"}
`
	tornTail = `{"ts":"2026-10-27T10:41:00Z","family":"zombie","profile":"alpha","outcome`
)

func seedingLines() string {
	return alpha + beta + secondSeedA + secondSeedB + skullSeed + aborted + tornTail
}

func TestRunRendersTable(t *testing.T) {
	path := writeFixture(t, seedingLines())
	var out, errOut bytes.Buffer
	code := run([]string{path}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
	text := out.String()
	for _, want := range []string{
		"FAMILY", "zombie", "skeleton", "alpha", "beta",
		"6 fights read", "1 aborted skipped",
		"Best profile: alpha",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("table missing %q:\n%s", want, text)
		}
	}
}

func TestRunTornTailTolerated(t *testing.T) {
	path := writeFixture(t, seedingLines())
	var out, errOut bytes.Buffer
	if code := run([]string{"-json", path}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), `"tornLines": 1`) {
		t.Errorf("want tornLines 1 in json:\n%s", out.String())
	}
}

func TestRunEmptyState(t *testing.T) {
	path := writeFixture(t, `{"outcome":"aborted"}`+"\n")
	var out, errOut bytes.Buffer
	if code := run([]string{path}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
	if out.String() != "no fights recorded\n" {
		t.Fatalf("empty state %q", out.String())
	}
}

func TestRunJSONAndVs(t *testing.T) {
	path := writeFixture(t, seedingLines())
	var out, errOut bytes.Buffer
	if code := run([]string{"-json", "-vs", "alpha,beta", path}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
	body := out.String()
	for _, want := range []string{
		`"comparison"`, `"pairs": 2`, `"profileA": "alpha"`,
		`"verdict"`, `"rows"`, `"bestProfile": "alpha"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("json missing %q:\n%s", want, body)
		}
	}
}

func TestRunVsTextVerdict(t *testing.T) {
	path := writeFixture(t, seedingLines())
	var out, errOut bytes.Buffer
	if code := run([]string{"-vs", "alpha,beta", path}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d stderr %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "verdict:") {
		t.Fatalf("want verdict in %q", out.String())
	}
	if !strings.Contains(out.String(), "pairs=2") {
		t.Errorf("want pairs=2 in %q", out.String())
	}
}

func TestRunBrokenMiddleLineIsStructured(t *testing.T) {
	path := writeFixture(t, alpha+"\n{bad json}\n"+beta)
	var out, errOut bytes.Buffer
	if code := run([]string{path}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errOut.String(), "record 2") {
		t.Fatalf("structured error wanted, got %q", errOut.String())
	}
}

func TestRunUsages(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{}, &out, &errOut); code != 2 {
		t.Fatalf("bare invocation exit %d", code)
	}
	if !strings.Contains(errOut.String(), "usage: trainreport") {
		t.Fatalf("usage text %q", errOut.String())
	}
	if code := run([]string{"-vs", "solo"}, &out, &errOut); code != 2 {
		t.Fatalf("bad --vs exit %d", code)
	}
}
