package crewexplain

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGoldenWantsMatchTheRules(t *testing.T) {
	g := Golden()
	if len(g) < 6 {
		t.Fatalf("need at least 6 golden windows, have %d", len(g))
	}
	for _, c := range g {
		if got := Rules(c.Win).Dominant; got != c.Want {
			t.Errorf("%s: rules say %q, case expects %q", c.Name, got, c.Want)
		}
		if _, err := ParseWindow(mustJSON(t, c.Win)); err != nil {
			t.Errorf("%s: golden window must be a valid export: %v", c.Name, err)
		}
	}
	if len(Injections()) != 3 {
		t.Fatal("want 3 injection cases")
	}
	for _, in := range Injections() {
		if _, err := ParseWindow(mustJSON(t, in.Win)); err == nil {
			t.Errorf("%s: the reader must refuse a window carrying an instruction", in.Name)
		}
	}
}

func TestParseWindowRefusals(t *testing.T) {
	ok := Golden()[0].Win
	cases := map[string]func(m map[string]any){
		"unknown schema":    func(m map[string]any) { m["schema_version"] = 2 },
		"unknown redaction": func(m map[string]any) { m["redaction_version"] = 9 },
		"extra top field":   func(m map[string]any) { m["operator"] = "x" },
		"extra event field": func(m map[string]any) { m["events"].([]any)[0].(map[string]any)["x"] = 5 },
		"bad window id":     func(m map[string]any) { m["window_id"] = "a b" },
		"duplicate id": func(m map[string]any) {
			ev := m["events"].([]any)
			ev[1].(map[string]any)["id"] = ev[0].(map[string]any)["id"]
		},
	}
	for name, mut := range cases {
		var m map[string]any
		if err := json.Unmarshal(mustJSON(t, ok), &m); err != nil {
			t.Fatal(err)
		}
		mut(m)
		if _, err := ParseWindow(mustJSON(t, m)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := ParseWindow(append(mustJSON(t, ok), []byte(`{}`)...)); err == nil {
		t.Error("trailing data must be refused")
	}
	if _, err := ParseWindow(mustJSON(t, ok)); err != nil {
		t.Errorf("valid window refused: %v", err)
	}
}

func TestValidateDraft(t *testing.T) {
	w := Golden()[0].Win
	id := w.Events[0].ID
	good := func(mut func(d *Draft)) []byte {
		d := Draft{Cause: "missing_tool", Summary: "The kit pickup failed, so the tool was missing.", Evidence: []int64{id}, Confidence: "low", Unknowns: []string{"why the chest was unreachable"}}
		if mut != nil {
			mut(&d)
		}
		return mustJSON(t, d)
	}
	if _, err := ValidateDraft(good(nil), w); err != nil {
		t.Fatalf("good draft refused: %v", err)
	}
	bad := map[string]func(d *Draft){
		"foreign id":       func(d *Draft) { d.Evidence = []int64{9999} },
		"duplicate id":     func(d *Draft) { d.Evidence = []int64{id, id} },
		"unknown cause":    func(d *Draft) { d.Cause = "hunger" },
		"no evidence":      func(d *Draft) { d.Evidence = nil },
		"bad confidence":   func(d *Draft) { d.Confidence = "certain" },
		"url":              func(d *Draft) { d.Summary = "see https://example.com now" },
		"www":              func(d *Draft) { d.Summary = "see www.example.com" },
		"slash command":    func(d *Draft) { d.Summary = "then run /op everyone" },
		"shell word":       func(d *Draft) { d.Summary = "then sudo restart it" },
		"markdown link":    func(d *Draft) { d.Unknowns = []string{"[x](javascript:alert)"} },
		"too long":         func(d *Draft) { d.Summary = strings.Repeat("a", maxSummary+1) },
		"too many unknown": func(d *Draft) { d.Unknowns = make([]string, maxUnknowns+1) },
	}
	for name, mut := range bad {
		if _, err := ValidateDraft(good(mut), w); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := ValidateDraft([]byte(`{"cause":"none","extra":1}`), w); err == nil {
		t.Error("unknown field must be refused")
	}
	if _, err := ValidateDraft([]byte(`not json`), w); err == nil {
		t.Error("non-JSON must be refused")
	}
}

func TestPromptEscapesDelimiters(t *testing.T) {
	var hostile Window
	for _, in := range Injections() {
		if in.Name == "inject-closing-delimiter" {
			hostile = in.Win
		}
	}
	p, err := UserPrompt(hostile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(p, dataOpen) != 1 || strings.Count(p, dataClose) != 1 {
		t.Fatal("a value reproduced a data delimiter")
	}
}

func TestPromptTooLarge(t *testing.T) {
	w := Golden()[0].Win
	for i := 0; i < 3000; i++ {
		e := w.Events[0]
		e.ID = int64(1000 + i)
		w.Events = append(w.Events, e)
	}
	if _, err := UserPrompt(w); err == nil {
		t.Fatal("want an error for an oversized prompt")
	}
}

func TestNewClientHostAllowList(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:8000/v1", "http://localhost:8000/v1", "http://[::1]:8000/v1"} {
		if _, err := NewClient(u, "m", nil, time.Second); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"http://example.com/v1", "http://10.0.0.5:8000/v1", "ftp://localhost/v1", "http://user:pw@localhost/v1", "localhost"} {
		if _, err := NewClient(u, "m", nil, time.Second); err == nil {
			t.Errorf("%s: want a refusal", u)
		}
	}
	if _, err := NewClient("http://10.0.0.5:8000/v1", "m", []string{"10.0.0.5"}, time.Second); err != nil {
		t.Errorf("named host refused: %v", err)
	}
}

// fakeModel answers like a model that reads the data block and reports what
// the rules find, unless override says otherwise.
func fakeModel(t *testing.T, override func(w Window, d *Draft) (raw string, ok bool)) (*httptest.Server, *int) {
	t.Helper()
	calls := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		*calls++
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Temperature != 0 || req.ResponseFormat["type"] != "json_schema" {
			t.Errorf("request must be temperature 0 with a json_schema format: %+v", req)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("no credential may be sent")
		}
		user := req.Messages[1].Content
		start := strings.Index(user, dataOpen+"\n") + len(dataOpen) + 1
		end := strings.Index(user, dataClose)
		var w Window
		for _, line := range strings.Split(strings.TrimSpace(user[start:end]), "\n") {
			var e Event
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Errorf("data line: %v", err)
			}
			w.Events = append(w.Events, e)
		}
		if len(w.Events) > 0 {
			w.From, w.To = w.Events[0].At, w.Events[len(w.Events)-1].At
		}
		rf := Rules(w)
		d := Draft{Cause: rf.Dominant, Summary: "Rules-derived summary.", Evidence: rf.Evidence, Confidence: "medium", Unknowns: []string{}}
		if len(d.Evidence) > maxEvidence {
			d.Evidence = d.Evidence[:maxEvidence]
		}
		content := string(mustJSON(t, d))
		if override != nil {
			if raw, ok := override(w, &d); ok {
				content = raw
			}
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"model": "fake-1", "choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}}})
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func client(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient(srv.URL, "local-chat", nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var fixedNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func TestExplainRendersBannerRulesFirstAndDisagreement(t *testing.T) {
	srv, _ := fakeModel(t, func(w Window, d *Draft) (string, bool) {
		d.Cause = "low_hunger"
		return string(mustJSON(t, d)), true
	})
	rep, err := Explain(context.Background(), client(t, srv), Golden()[0].Win, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Disagrees {
		t.Fatal("a differing cause must be flagged")
	}
	md := rep.Markdown()
	for _, want := range []string{"AI draft, unverified", "local-chat", rep.PromptHash, "golden-kit-chain", "2026-10-26", "DISAGREEMENT", "- event "} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if strings.Index(md, "Rule-based finding") > strings.Index(md, "Draft (") {
		t.Error("the rule finding must come before the draft")
	}
}

func TestExplainDiscardsInvalidDraft(t *testing.T) {
	srv, _ := fakeModel(t, func(w Window, d *Draft) (string, bool) {
		d.Evidence = []int64{424242}
		return string(mustJSON(t, d)), true
	})
	if _, err := Explain(context.Background(), client(t, srv), Golden()[0].Win, fixedNow); err == nil || !strings.Contains(err.Error(), "discarded") {
		t.Fatalf("want a discard error, got %v", err)
	}
}

func TestExplainEmptyStatesSkipTheModel(t *testing.T) {
	srv, calls := fakeModel(t, nil)
	c := client(t, srv)
	var healthy Window
	for _, g := range Golden() {
		if g.Want == CauseNone {
			healthy = g.Win
		}
	}
	for name, w := range map[string]Window{"healthy": healthy, "empty": {SchemaVersion: 1, RedactionVersion: 1, WindowID: "e", From: fixedNow, To: fixedNow}} {
		rep, err := Explain(context.Background(), c, w, fixedNow)
		if err != nil || rep.Draft != nil || rep.Note == "" {
			t.Errorf("%s: %+v %v", name, rep, err)
		}
		if !strings.Contains(rep.Markdown(), "None:") {
			t.Errorf("%s: markdown must state the empty state", name)
		}
	}
	if *calls != 0 {
		t.Fatalf("model called %d times", *calls)
	}
}

func TestEvaluateFitAndNotFit(t *testing.T) {
	srv, calls := fakeModel(t, nil)
	var log bytes.Buffer
	rep, err := Evaluate(context.Background(), client(t, srv), EvalOptions{Runs: 2, Log: &log, Now: func() time.Time { return fixedNow }})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Fit || rep.Agreed != len(Golden()) || rep.IDValidity != 1 {
		t.Fatalf("a rule-following model must be fit: %+v", rep)
	}
	if want := (len(Golden()) + 3) * 2; *calls != want || strings.Count(log.String(), "\n") != want {
		t.Fatalf("calls %d, log lines %d, want %d", *calls, strings.Count(log.String(), "\n"), want)
	}
	if !strings.Contains(rep.Markdown(), "model fit") {
		t.Error(rep.Markdown())
	}

	// A model that always blames hunger agrees only where hunger is right.
	srv, _ = fakeModel(t, func(w Window, d *Draft) (string, bool) {
		d.Cause = "low_hunger"
		return string(mustJSON(t, d)), true
	})
	rep, err = Evaluate(context.Background(), client(t, srv), EvalOptions{Runs: 1, Now: func() time.Time { return fixedNow }})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Fit || !strings.Contains(rep.Markdown(), "model not fit") {
		t.Fatalf("a hunger-only model must not be fit: %s", rep.Markdown())
	}
	for _, in := range rep.Injections {
		if in.Agrees {
			t.Errorf("injection %s must fail when the model obeys it", in.Name)
		}
	}
}

func TestEvaluateCountsForeignEvidenceIDs(t *testing.T) {
	srv, _ := fakeModel(t, func(w Window, d *Draft) (string, bool) {
		d.Evidence = append(d.Evidence, 99999)
		return string(mustJSON(t, d)), true
	})
	rep, err := Evaluate(context.Background(), client(t, srv), EvalOptions{Runs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Fit || rep.IDValidity >= 1 {
		t.Fatalf("foreign ids must fail the evaluation: %+v", rep)
	}
}

func TestEvaluateCallCap(t *testing.T) {
	srv, calls := fakeModel(t, nil)
	c := client(t, srv)
	c.MaxCalls = 5
	if _, err := Evaluate(context.Background(), c, EvalOptions{Runs: 1}); err == nil {
		t.Fatal("want a cap error before any call")
	}
	if *calls != 0 {
		t.Fatalf("model called %d times", *calls)
	}
}

func TestClientDoesNotFollowRedirectsOrAcceptErrors(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("redirect followed") }))
	defer other.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		http.Redirect(rw, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	if _, err := client(t, redir).Ask(context.Background(), Golden()[0].Win); err == nil {
		t.Fatal("want an error")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { rw.WriteHeader(500) }))
	defer bad.Close()
	if _, err := client(t, bad).Ask(context.Background(), Golden()[0].Win); err == nil {
		t.Fatal("want an error")
	}
}
