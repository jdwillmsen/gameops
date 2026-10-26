package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/crewexplain"
)

var now = func() time.Time { return time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC) }

// echoModel answers every request with a fixed content string.
func echoModel(t *testing.T, content func(w crewexplain.Window) string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var w crewexplain.Window
		for _, g := range crewexplain.Golden() {
			if strings.Contains(req.Messages[1].Content, "Window "+g.Name+",") {
				w = g.Win
			}
		}
		for _, g := range crewexplain.Injections() {
			if strings.Contains(req.Messages[1].Content, "Window "+g.Name+",") {
				w = g.Win
			}
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"model": "fake", "choices": []map[string]any{{"message": map[string]string{"content": content(w)}}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func oracle(w crewexplain.Window) string {
	rf := crewexplain.Rules(w)
	b, _ := json.Marshal(crewexplain.Draft{Cause: rf.Dominant, Summary: "Rules-derived.", Evidence: rf.Evidence, Confidence: "low", Unknowns: []string{}})
	return string(b)
}

func writeWindow(t *testing.T, c crewexplain.Case) string {
	t.Helper()
	b, _ := json.Marshal(c.Win)
	p := filepath.Join(t.TempDir(), "w.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExplainWindowFile(t *testing.T) {
	srv := echoModel(t, oracle)
	p := writeWindow(t, crewexplain.Golden()[0])
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{p, "--base-url", srv.URL}, &out, &errb, now); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "AI draft, unverified") || !strings.Contains(out.String(), "missing_tool") {
		t.Fatal(out.String())
	}

	out.Reset()
	if code := run(context.Background(), []string{"--format", "json", "--base-url", srv.URL, p}, &out, &errb, now); code != 0 {
		t.Fatal(errb.String())
	}
	var rep crewexplain.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || rep.Draft == nil || rep.Banner != crewexplain.Banner {
		t.Fatalf("%v %s", err, out.String())
	}

	file := filepath.Join(t.TempDir(), "o.md")
	out.Reset()
	if code := run(context.Background(), []string{p, "--base-url", srv.URL, "--out", file}, &out, &errb, now); code != 0 || out.Len() != 0 {
		t.Fatalf("exit %d, stdout %q", code, out.String())
	}
	if b, _ := os.ReadFile(file); !strings.Contains(string(b), "AI draft") {
		t.Fatal("output file not written")
	}
}

func TestRulesOnlyNeedsNoModel(t *testing.T) {
	p := writeWindow(t, crewexplain.Golden()[0])
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"--rules-only", p}, &out, &errb, now); code != 0 {
		t.Fatal(errb.String())
	}
	if !strings.Contains(out.String(), "rules only") {
		t.Fatal(out.String())
	}
}

func TestFailuresExitOne(t *testing.T) {
	bad := echoModel(t, func(crewexplain.Window) string {
		return `{"cause":"missing_tool","summary":"x","evidence":[4242],"confidence":"low","unknowns":[]}`
	})
	p := writeWindow(t, crewexplain.Golden()[0])
	notJSON := filepath.Join(t.TempDir(), "n.json")
	_ = os.WriteFile(notJSON, []byte(`{"schema_version":7}`), 0o600)
	for name, args := range map[string][]string{
		"invalid draft":    {p, "--base-url", bad.URL},
		"unknown version":  {notJSON, "--rules-only"},
		"missing file":     {filepath.Join(t.TempDir(), "none.json"), "--rules-only"},
		"no file":          {"--rules-only"},
		"foreign host":     {p, "--base-url", "http://example.com/v1"},
		"bad format":       {p, "--format", "xml"},
		"unknown flag":     {p, "--nope"},
		"eval with a file": {"eval", p},
	} {
		var out, errb bytes.Buffer
		if code := run(context.Background(), args, &out, &errb, now); code != 1 {
			t.Errorf("%s: exit %d", name, code)
		}
		if errb.Len() == 0 {
			t.Errorf("%s: no reason on stderr", name)
		}
		if strings.Contains(out.String(), "AI draft") {
			t.Errorf("%s: a draft was printed", name)
		}
	}
}

func TestEvalExitCodeAndLog(t *testing.T) {
	good := echoModel(t, oracle)
	logp := filepath.Join(t.TempDir(), "log.jsonl")
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"eval", "--base-url", good.URL, "--runs", "1", "--log", logp}, &out, &errb, now); code != 0 {
		t.Fatalf("exit %d: %s %s", code, errb.String(), out.String())
	}
	if b, _ := os.ReadFile(logp); strings.Count(string(b), "\n") != len(crewexplain.Golden())+3 {
		t.Fatalf("log lines: %q", b)
	}
	if fi, _ := os.Stat(logp); fi.Mode().Perm() != 0o600 {
		t.Fatalf("log mode %v", fi.Mode())
	}

	obey := echoModel(t, func(crewexplain.Window) string {
		return `{"cause":"low_hunger","summary":"x","evidence":[1],"confidence":"high","unknowns":[]}`
	})
	out.Reset()
	if code := run(context.Background(), []string{"eval", "--base-url", obey.URL, "--runs", "1"}, &out, &errb, now); code != 1 {
		t.Fatal("an unfit model must exit 1")
	}
	if !strings.Contains(out.String(), "model not fit") {
		t.Fatal(out.String())
	}
}
