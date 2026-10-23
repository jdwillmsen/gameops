package squad

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

type squadStream struct{ sc *bufio.Scanner }

// event returns the raw data of the next squads event. One scanner lives for
// the stream, because a fresh one would swallow the events it buffered ahead.
func (s *squadStream) event(t *testing.T) string {
	t.Helper()
	for s.sc.Scan() {
		if data, ok := strings.CutPrefix(s.sc.Text(), "data: "); ok {
			return data
		}
	}
	t.Fatal("the stream ended before a squads event")
	return ""
}

func openSquadStream(t *testing.T, r *rig) *squadStream {
	t.Helper()
	// Anonymous: the console panel is public, so the stream must not need
	// the unit token either.
	resp := r.do(t, "GET", "/v1/squads/stream", "", map[string]string{"Authorization": ""})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream: status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("stream content type %q", ct)
	}
	return &squadStream{sc: bufio.NewScanner(resp.Body)}
}

// The console snapshot is the public read: members, the standing order and
// the callouts, without a unit's position or the operator who ordered.
func TestStreamCarriesOnlyConsoleSafeState(t *testing.T) {
	r := newRig(t, true)
	r.hello(t, "alpha", "crew-18", "vanguard")
	r.hello(t, "alpha", "crew-19", "striker")
	r.do(t, "POST", "/v1/squads/alpha/status",
		`{"unit":"crew-18","hp":12.5,"food":7,"weapon":"bow","pos":{"x":1.5,"y":64,"z":-760.25}}`, nil)
	r.do(t, "POST", "/v1/squads/alpha/order", `{"kind":"focus","target":"captain"}`,
		map[string]string{"X-Crew-Request": "1"})
	r.do(t, "POST", "/v1/squads/alpha/callout", `{"unit":"crew-19","kind":"need_heal"}`, nil)

	raw := openSquadStream(t, r).event(t)
	for _, want := range []string{
		`"id":"alpha"`, `"leader":"crew-18"`, `"role":"striker"`,
		`"hp":12.5`, `"food":7`, `"weapon":"bow"`,
		`"kind":"focus"`, `"target":"captain"`,
		`"kind":"need_heal"`, `"from":"crew-19"`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("the snapshot is missing %s: %s", want, raw)
		}
	}
	for _, leak := range []string{`"pos"`, `"by"`, `"owner"`, `-760.25`} {
		if strings.Contains(raw, leak) {
			t.Errorf("the snapshot leaks %s: %s", leak, raw)
		}
	}

	var got consoleList
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("snapshot body: %s", err)
	}
	if len(got.Squads) != 1 || len(got.Squads[0].Members) != 2 {
		t.Fatalf("snapshot squads %+v, want one squad with two members", got.Squads)
	}
	if got.Squads[0].Callouts[0].Kind != "need_heal" {
		t.Errorf("callouts are not newest first: %+v", got.Squads[0].Callouts)
	}
}

// The stream exists whether or not units may post: with no unit token the
// public read routes still answer, as they do for the list route.
func TestStreamExistsWithoutAUnitToken(t *testing.T) {
	r := newRig(t, false)
	openSquadStream(t, r)
}

// A change lands on the next tick, and the rolling log stays short.
func TestStreamFollowsChanges(t *testing.T) {
	r := newRig(t, true)
	r.api.streamEvery = 10 * time.Millisecond
	r.api.streamKeepalive = 20 * time.Millisecond
	r.hello(t, "alpha", "crew-18", "vanguard")
	st := openSquadStream(t, r)
	if raw := st.event(t); !strings.Contains(raw, `"id":"alpha"`) {
		t.Fatalf("first snapshot %s", raw)
	}

	r.do(t, "POST", "/v1/squads/alpha/callout", `{"unit":"crew-18","kind":"boss_phase"}`, nil)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("the callout never reached the stream")
		}
		if raw := st.event(t); strings.Contains(raw, `"boss_phase"`) {
			break
		}
	}

	for i := 0; i < consoleLogDepth+4; i++ {
		r.do(t, "POST", "/v1/squads/alpha/callout", `{"unit":"crew-18","kind":"low_health"}`, nil)
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("the rolling log never settled")
		}
		raw := st.event(t)
		var got consoleList
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("snapshot body: %s", err)
		}
		if len(got.Squads) == 1 && len(got.Squads[0].Callouts) == consoleLogDepth {
			break
		}
	}
}
