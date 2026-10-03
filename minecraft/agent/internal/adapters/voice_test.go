package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeNames is a NameResolver over a fixed, in-test map.
type fakeNames map[string]string

func (f fakeNames) NameFor(xuid string) (string, bool) {
	name, ok := f[xuid]
	return name, ok
}

func TestBridgeVoice_Tell_Success(t *testing.T) {
	var gotCommand string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req commandRequest
		json.NewDecoder(r.Body).Decode(&req)
		gotCommand = req.Command
		json.NewEncoder(w).Encode(commandResponse{Rule: "tellraw"})
	}))
	defer srv.Close()

	v := NewBridgeVoice(NewBridgeClient(srv.URL, "tok", time.Second), fakeNames{"111": "Steve"})
	if err := v.Tell(context.Background(), "111", "hello there"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasPrefix(gotCommand, `tellraw @a[name="Steve"] `) {
		t.Errorf("command = %q, want a tellraw targeted at Steve", gotCommand)
	}
	payloadJSON := strings.TrimPrefix(gotCommand, `tellraw @a[name="Steve"] `)
	var payload tellrawPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v (%s)", err, payloadJSON)
	}
	if len(payload.RawText) != 1 || payload.RawText[0].Text != "hello there" {
		t.Errorf("payload = %+v, want one run with text %q", payload, "hello there")
	}
}

func TestBridgeVoice_Tell_UnknownXUIDFailsWithoutCallingBridge(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		json.NewEncoder(w).Encode(commandResponse{})
	}))
	defer srv.Close()

	v := NewBridgeVoice(NewBridgeClient(srv.URL, "tok", time.Second), fakeNames{})
	err := v.Tell(context.Background(), "unknown-xuid", "hi")
	if err == nil {
		t.Fatal("expected an error for an XUID not in the roster")
	}
	if called {
		t.Error("bridge was called despite no known name to target — should fail before any HTTP call")
	}
}

func TestBridgeVoice_Tell_MessageWithSpecialCharactersStaysValidJSON(t *testing.T) {
	var gotCommand string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req commandRequest
		json.NewDecoder(r.Body).Decode(&req)
		gotCommand = req.Command
		json.NewEncoder(w).Encode(commandResponse{})
	}))
	defer srv.Close()

	v := NewBridgeVoice(NewBridgeClient(srv.URL, "tok", time.Second), fakeNames{"111": "Steve"})
	message := `she said "hi" and left <a note>` + "\nline two"
	if err := v.Tell(context.Background(), "111", message); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.ContainsAny(gotCommand, "\n\r") {
		t.Error("command contains a raw newline/CR — the bridge's allowlist refuses this outright")
	}
	payloadJSON := strings.TrimPrefix(gotCommand, `tellraw @a[name="Steve"] `)
	var payload tellrawPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v (%s)", err, payloadJSON)
	}
	if payload.RawText[0].Text != message {
		t.Errorf("round-tripped text = %q, want %q", payload.RawText[0].Text, message)
	}
}

func TestBridgeVoice_Tell_NameWithQuoteRefused(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	v := NewBridgeVoice(NewBridgeClient(srv.URL, "tok", time.Second), fakeNames{"111": `Ste"ve`})
	if err := v.Tell(context.Background(), "111", "hi"); err == nil {
		t.Fatal("expected an error for a gamertag containing a double quote")
	}
	if called {
		t.Error("bridge was called with an unvalidated quoted name")
	}
}

func TestBridgeVoice_Tell_BridgeRefusalPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "command not allowed", http.StatusForbidden)
	}))
	defer srv.Close()

	v := NewBridgeVoice(NewBridgeClient(srv.URL, "tok", time.Second), fakeNames{"111": "Steve"})
	if err := v.Tell(context.Background(), "111", "hi"); err == nil {
		t.Fatal("expected the bridge's refusal to surface as an error")
	}
}

func TestBridgeVoice_Say_Success(t *testing.T) {
	var gotCommand string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req commandRequest
		json.NewDecoder(r.Body).Decode(&req)
		gotCommand = req.Command
		json.NewEncoder(w).Encode(commandResponse{Rule: "say"})
	}))
	defer srv.Close()

	v := NewBridgeVoice(NewBridgeClient(srv.URL, "tok", time.Second), fakeNames{})
	if err := v.Say(context.Background(), "server restarting soon"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCommand != "say server restarting soon" {
		t.Errorf("command = %q, want %q", gotCommand, "say server restarting soon")
	}
}

func TestBridgeVoice_Say_DeliveryFailurePropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "console not connected", http.StatusBadGateway)
	}))
	defer srv.Close()

	v := NewBridgeVoice(NewBridgeClient(srv.URL, "tok", time.Second), fakeNames{})
	if err := v.Say(context.Background(), "hi"); err == nil {
		t.Fatal("expected the delivery failure to surface as an error")
	}
}

// TestBridgeVoice_Say_MultiLineMessageIsFlattened covers the reply path a
// player takes when !players relays the console's own `list` output, which
// is several lines. `say` consumes the rest of the console line, so the
// bridge refuses any command containing a line break — an unflattened
// message would be dropped with the reply never reaching anyone.
func TestBridgeVoice_Say_MultiLineMessageIsFlattened(t *testing.T) {
	var gotCommand string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req commandRequest
		json.NewDecoder(r.Body).Decode(&req)
		gotCommand = req.Command
		json.NewEncoder(w).Encode(commandResponse{Rule: "say"})
	}))
	defer srv.Close()

	v := NewBridgeVoice(NewBridgeClient(srv.URL, "tok", time.Second), fakeNames{})
	if err := v.Say(context.Background(), "There are 2/10 players online:\r\nSteve\nAlex\n"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.ContainsAny(gotCommand, "\n\r") {
		t.Errorf("command = %q, want no line break — the bridge refuses those outright", gotCommand)
	}
	if gotCommand != "say There are 2/10 players online: Steve Alex" {
		t.Errorf("command = %q, want the message flattened onto one line", gotCommand)
	}
}

func TestBridgeVoice_Say_BlankMessageRefusedWithoutCallingBridge(t *testing.T) {
	for _, message := range []string{"", "   ", "\n", " \r\n "} {
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			json.NewEncoder(w).Encode(commandResponse{Rule: "say"})
		}))

		v := NewBridgeVoice(NewBridgeClient(srv.URL, "tok", time.Second), fakeNames{})
		err := v.Say(context.Background(), message)
		srv.Close()

		if err == nil {
			t.Errorf("Say(%q) = nil error, want an error — there is nothing to broadcast", message)
		}
		if called {
			t.Errorf("Say(%q) reached the bridge, want it refused locally", message)
		}
	}
}

func TestBridgeVoice_Tell_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		json.NewEncoder(w).Encode(commandResponse{})
	}))
	defer srv.Close()

	v := NewBridgeVoice(NewBridgeClient(srv.URL, "tok", 5*time.Millisecond), fakeNames{"111": "Steve"})
	if err := v.Tell(context.Background(), "111", "hi"); err == nil {
		t.Fatal("expected a timeout error")
	}
}

// sayRecorder is a bridge that accepts every command and remembers nothing
// else: these tests are about what the voice remembers, not what the server
// does with it.
func sayRecorder(t *testing.T) *BridgeVoice {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rule":"say"}`))
	}))
	t.Cleanup(srv.Close)
	return NewBridgeVoice(NewBridgeClient(srv.URL, "tok", time.Second), fakeNames{})
}

func TestBridgeVoice_JustSaid_RecognisesTheLineAsBroadcast(t *testing.T) {
	voice := sayRecorder(t)
	// Multi-line input is the normal case for a relayed console reply, and
	// what comes back from the server is the flattened single line -- so
	// that, not the caller's text, is what has to be recognised.
	if err := voice.Say(context.Background(), "players online:\n Steve"); err != nil {
		t.Fatalf("Say: %v", err)
	}
	if voice.JustSaid("players online:\n Steve") {
		t.Error("the caller's unflattened text never reaches chat and must not match")
	}
	if !voice.JustSaid("players online:  Steve") {
		t.Fatal("the flattened line the server broadcast was not recognised as the agent's own")
	}
	if voice.JustSaid("players online:  Steve") {
		t.Error("one broadcast is echoed once; a second match would mute an operator repeating it")
	}
}

func TestBridgeVoice_JustSaid_IsFalseForALineItNeverSaid(t *testing.T) {
	voice := sayRecorder(t)
	if voice.JustSaid("!shutdown") {
		t.Fatal("a voice that has said nothing must not claim an operator's command as its own")
	}
}

func TestBridgeVoice_JustSaid_ForgetsLinesNoEchoCanStillBeComing(t *testing.T) {
	voice := sayRecorder(t)
	at := time.Now()
	voice.now = func() time.Time { return at }
	if err := voice.Say(context.Background(), "!shutdown is operator-only"); err != nil {
		t.Fatalf("Say: %v", err)
	}
	at = at.Add(sayEchoWindow + time.Second)
	if voice.JustSaid("!shutdown is operator-only") {
		t.Fatal("a line this old was never echoed; holding it would veto an operator typing the same thing")
	}
}

func TestBridgeVoice_JustSaid_MemoryIsBounded(t *testing.T) {
	voice := sayRecorder(t)
	for i := 0; i < sayEchoMemory*3; i++ {
		if err := voice.Say(context.Background(), fmt.Sprintf("line %d", i)); err != nil {
			t.Fatalf("Say: %v", err)
		}
	}
	voice.mu.Lock()
	kept := len(voice.said)
	voice.mu.Unlock()
	if kept > sayEchoMemory {
		t.Fatalf("remembered %d broadcasts, want at most %d", kept, sayEchoMemory)
	}
	if !voice.JustSaid(fmt.Sprintf("line %d", sayEchoMemory*3-1)) {
		t.Error("the newest broadcast is the one an echo is still coming for and must be kept")
	}
}
