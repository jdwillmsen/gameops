package presence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jdwillmsen/minecraft-server-agent/presenceapi"
)

const contractModule = "github.com/jdwillmsen/minecraft-server-agent/presenceapi"

// goldenDir finds the agent's golden files inside the pinned module, so this
// test reads the very bytes the agent's own contract test reads at that
// version rather than a copy that could drift.
func goldenDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", contractModule).Output()
	if err != nil {
		t.Fatalf("go list %s: %v", contractModule, err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatalf("%s is not downloaded; run go mod download", contractModule)
	}
	return filepath.Join(dir, "testdata")
}

func golden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenDir(t), name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return b
}

// Strict decoding proves the pinned types and the pinned goldens agree field
// for field; the round trip proves nothing is lost on the way back out.
// All six golden files are covered (amendment B1), not just the ones this
// bot's client uses, so a contract change anywhere shows up here.
func TestGoldenFilesRoundTrip(t *testing.T) {
	for name, target := range map[string]any{
		"presence_parked.json":  &presenceapi.Presence{},
		"presence_default.json": &presenceapi.Presence{},
		"status.json":           &presenceapi.Status{},
		"error_conflict.json":   &presenceapi.Error{},
		"set_request.json":      &presenceapi.SetRequest{},
		"actors.json":           &[]presenceapi.ActorView{},
	} {
		t.Run(name, func(t *testing.T) {
			raw := golden(t, name)
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(target); err != nil {
				t.Fatalf("decode: %v", err)
			}
			again, err := json.Marshal(target)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			var want, got any
			_ = json.Unmarshal(raw, &want)
			_ = json.Unmarshal(again, &got)
			if !reflect.DeepEqual(want, got) {
				t.Errorf("round trip changed the document:\nwant %s\ngot  %s", raw, again)
			}
		})
	}
}

func serveGolden(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"1-golden"`)
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGoldenParkedDecodesThroughTheClient(t *testing.T) {
	srv := serveGolden(t, http.StatusOK, golden(t, "presence_parked.json"))

	got, err := NewClient(srv.URL, "afk-bot-1", "tok", srv.Client()).Fetch(context.Background(), "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Presence.Effective != presenceapi.StateParked {
		t.Errorf("Effective = %q, want parked", got.Presence.Effective)
	}
	if got.Presence.Override == nil || got.Presence.Override.State != presenceapi.StateParked {
		t.Errorf("Override = %+v, want a parked override", got.Presence.Override)
	}
}

func TestGoldenDefaultDecodesThroughTheClient(t *testing.T) {
	srv := serveGolden(t, http.StatusOK, golden(t, "presence_default.json"))

	got, err := NewClient(srv.URL, "afk-bot-1", "tok", srv.Client()).Fetch(context.Background(), "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Presence.Override != nil {
		t.Errorf("Override = %+v, want none", got.Presence.Override)
	}
	if got.Presence.Effective != got.Presence.Default {
		t.Errorf("Effective = %q, want it to equal Default %q", got.Presence.Effective, got.Presence.Default)
	}
}

func TestGoldenConflictDecodesIntoHTTPError(t *testing.T) {
	srv := serveGolden(t, http.StatusConflict, golden(t, "error_conflict.json"))

	_, err := NewClient(srv.URL, "afk-bot-1", "tok", srv.Client()).Fetch(context.Background(), "")
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if he.Body.Code != presenceapi.CodeConflict || he.Body.Current == nil {
		t.Errorf("HTTPError body = %+v, want code conflict with the current presence", he.Body)
	}
}

// The agent validates what the bot posts against this shape; a key the bot
// omits or renames would be rejected there, not here.
func TestReportSendsTheGoldenStatusShape(t *testing.T) {
	reqs := make(chan seen, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(r, reqs)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var st presenceapi.Status
	if err := json.Unmarshal(golden(t, "status.json"), &st); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	if err := NewClient(srv.URL, "afk-bot-1", "tok", srv.Client()).Report(context.Background(), st); err != nil {
		t.Fatalf("Report: %v", err)
	}
	var posted, want map[string]any
	if err := json.Unmarshal((<-reqs).body, &posted); err != nil {
		t.Fatalf("decode posted body: %v", err)
	}
	_ = json.Unmarshal(golden(t, "status.json"), &want)
	if !reflect.DeepEqual(posted, want) {
		t.Errorf("posted %v, want the golden %v", posted, want)
	}
}
