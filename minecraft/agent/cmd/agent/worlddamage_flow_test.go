package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/agent/internal/audit"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/bus"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/chat"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/mapclient"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/plugin"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/plugins"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/roster"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/store"
	"github.com/jdwillmsen/gameops/minecraft/agent/internal/worlddamage"
	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
)

const (
	damageOperatorXUID = "2535400000000001"
	damageToken        = "internal-token-0123456789"
)

// fakeWorldMap is the map service's world endpoints with the rule that
// matters to the agent: an acknowledgement is accepted only for the count
// currently held, and clears the loss. Its state outlives any agent talking
// to it, as the real ledger does.
type fakeWorldMap struct {
	mu     sync.Mutex
	at     time.Time
	lost   int
	sample []mapclient.Chunk
	acked  []time.Time
	srv    *httptest.Server
}

func newFakeWorldMap(t *testing.T, lost int) *fakeWorldMap {
	t.Helper()
	m := &fakeWorldMap{
		at: time.Date(2026, 10, 3, 4, 0, 0, 123456789, time.UTC), lost: lost,
		sample: []mapclient.Chunk{{Dimension: "overworld", X: 2560, Z: -16}, {Dimension: "nether", X: 2576, Z: 32}},
	}
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+damageToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /internal/v1/world", auth(func(w http.ResponseWriter, _ *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		out := map[string]any{"checked": true, "checkedAt": m.at, "lost": map[string]int{"overworld": m.lost}, "lostTotal": m.lost}
		if m.lost > 0 {
			out["lostSample"] = m.sample
		}
		json.NewEncoder(w).Encode(out)
	}))
	mux.HandleFunc("POST /internal/v1/world/acknowledge", auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			CheckedAt time.Time `json:"checkedAt"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.CheckedAt.IsZero() {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if !req.CheckedAt.Equal(m.at) {
			http.Error(w, "a newer chunk count has replaced the one acknowledged", http.StatusConflict)
			return
		}
		m.acked = append(m.acked, req.CheckedAt)
		m.lost, m.sample = 0, nil
		w.WriteHeader(http.StatusNoContent)
	}))
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

// recount is the map taking a newer count that holds more loss.
func (m *fakeWorldMap) recount(lost int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.at, m.lost = m.at.Add(15*time.Minute), lost
}

func (m *fakeWorldMap) acknowledgements() []time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]time.Time(nil), m.acked...)
}

// damageRig is an agent as main assembles it for a map: the watch, the
// plugin that is both command and notice, and a drain that delivers it.
type damageRig struct {
	registry *plugin.Registry
	pctx     *plugin.Context
	voice    *recordingVoice
	drain    *plugins.AnnounceDrain
	watch    *worlddamage.Watch
}

func newDamageRig(t *testing.T, m *fakeWorldMap) *damageRig {
	t.Helper()
	log := logging.New("error")
	permResolver := fakePermResolver(t, map[string]string{damageOperatorXUID: "operator", playerXUID: "visitor"})
	watch := worlddamage.New(mapclient.New(m.srv.URL, damageToken, time.Second), log)
	warning := plugins.NewWorldDamage(watch, permResolver.Resolve)

	registry := plugin.NewRegistry()
	for _, p := range []plugin.Plugin{plugins.NewCore(), warning} {
		if err := registry.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	voice := &recordingVoice{}
	return &damageRig{
		registry: registry,
		pctx:     &plugin.Context{Voice: voice, Directory: registry},
		voice:    voice,
		watch:    watch,
		drain:    plugins.NewAnnounceDrain(t.Context(), nil, 0, log, plugins.WithJoinNotice(warning)),
	}
}

func (r *damageRig) join(t *testing.T, xuid string) {
	t.Helper()
	ev := roster.JoinEvent{Entry: roster.Entry{XUID: xuid, Username: "Someone"}}
	if err := r.drain.HandleEvent(context.Background(), r.pctx, ev); err != nil {
		t.Fatal(err)
	}
}

// heardBy waits for want whispers to xuid, then a little longer for any that
// should not have come. A test that expects silence passes want 0 and pays
// only the grace.
func (r *damageRig) heardBy(xuid string, want int) []string {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(r.whispersTo(xuid)) < want {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	return r.whispersTo(xuid)
}

func (r *damageRig) whispersTo(xuid string) []string {
	var out []string
	for _, line := range r.voice.output() {
		if rest, ok := strings.CutPrefix(line, "tell "+xuid+": "); ok {
			out = append(out, rest)
		}
	}
	return out
}

func (r *damageRig) typed(t *testing.T, xuid, line string, auditor audit.Store) {
	t.Helper()
	permResolver := fakePermResolver(t, map[string]string{damageOperatorXUID: "operator", playerXUID: "visitor"})
	handleCommand(context.Background(), xuid, chat.ParseTrigger(line), false, logging.New("error"),
		r.registry, r.pctx, unlimitedRateLimit(), permResolver, auditor, roster.New())
}

func TestWorldDamageFlow_AJoiningPlayerIsWarnedUntilAnOperatorClearsIt(t *testing.T) {
	m := newFakeWorldMap(t, 6460)
	rig := newDamageRig(t, m)
	trail := &recordingAudit{}

	rig.join(t, playerXUID)
	player := rig.heardBy(playerXUID, 1)
	if len(player) != 1 || !strings.Contains(player[0], "damaged") || strings.Contains(player[0], "6460") {
		t.Fatalf("player heard %q, want the plain warning and no figures", player)
	}

	rig.join(t, damageOperatorXUID)
	op := rig.heardBy(damageOperatorXUID, 3)
	joined := strings.Join(op, "\n")
	for _, want := range []string{"6460", "2560 -16", "!worlddamage clear"} {
		if !strings.Contains(joined, want) {
			t.Errorf("operator heard %q, missing %q", joined, want)
		}
	}

	rig.typed(t, damageOperatorXUID, "!worlddamage clear", trail)
	if acks := m.acknowledgements(); len(acks) != 1 || !acks[0].Equal(m.at) {
		t.Fatalf("map acknowledgements = %v, want the one count players were told about", acks)
	}

	records := trail.all()
	if len(records) != 1 {
		t.Fatalf("audit records = %+v, want exactly the clear", records)
	}
	if got := records[0]; got.Command != "worlddamage" || got.Args != "clear" || got.Permission != "operator" || got.Outcome != audit.OutcomeOK || got.XUID != damageOperatorXUID {
		t.Errorf("audit record = %+v", got)
	}

	before := len(rig.whispersTo(playerXUID))
	rig.join(t, playerXUID)
	if after := rig.heardBy(playerXUID, 0); len(after) != before {
		t.Fatalf("player heard %q after the clear, want no further notice", after[before:])
	}
}

func TestWorldDamageFlow_AVisitorCannotClearIt(t *testing.T) {
	m := newFakeWorldMap(t, 6460)
	rig := newDamageRig(t, m)
	trail := &recordingAudit{}

	rig.typed(t, playerXUID, "!worlddamage clear", trail)

	if acks := m.acknowledgements(); len(acks) != 0 {
		t.Fatalf("map acknowledgements = %v, want none from a visitor", acks)
	}
	records := trail.all()
	if len(records) != 1 || records[0].Outcome != audit.OutcomeDenied || records[0].Args != "clear" {
		t.Fatalf("audit records = %+v, want the refused attempt recorded as denied", records)
	}
	rig.join(t, playerXUID)
	if got := rig.heardBy(playerXUID, 2); len(got) < 1 || !strings.Contains(got[len(got)-1], "damaged") {
		t.Fatalf("player heard %q, want the notice still on", got)
	}
}

// The console is the operator's other hand: `send-command say` reaches the
// agent as a trusted operator, so the clear works from outside the game.
func TestWorldDamageFlow_TheConsoleCanClearIt(t *testing.T) {
	m := newFakeWorldMap(t, 6460)
	rig := newDamageRig(t, m)
	registry, pctx, voice, eventBus := rig.registry, rig.pctx, rig.voice, bus.New()

	handlePacket(context.Background(), consoleSayPacket("!worlddamage clear"), selfXUID, nil, logging.New("error"), registry, pctx, eventBus,
		unlimitedRateLimit(), roster.New(), fakePermResolver(t, nil), testAnswering(), store.Nop{}, &recordingAudit{}, newJoinTimes())

	if acks := m.acknowledgements(); len(acks) != 1 {
		t.Fatalf("map acknowledgements = %v, want the console's clear to go through", acks)
	}
	if out := voice.output(); len(out) != 1 || !strings.HasPrefix(out[0], "say: World damage cleared") {
		t.Fatalf("voice output = %v", out)
	}
}

// A newer count landed after the operator read the last one. The clear is
// refused and says so, and the world stays flagged.
func TestWorldDamageFlow_AStaleClearIsRefusedAndTheNoticeStays(t *testing.T) {
	m := newFakeWorldMap(t, 6460)
	rig := newDamageRig(t, m)
	if err := rig.watch.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.recount(6500)

	rig.typed(t, damageOperatorXUID, "!worlddamage clear", &recordingAudit{})

	said := rig.whispersTo(damageOperatorXUID)
	if len(said) != 1 || !strings.Contains(said[0], "Not cleared") || !strings.Contains(said[0], "6500") {
		t.Fatalf("operator heard %q, want the refusal naming the newer count", said)
	}
	if acks := m.acknowledgements(); len(acks) != 0 {
		t.Fatalf("map acknowledgements = %v, want none", acks)
	}

	rig.typed(t, damageOperatorXUID, "!worlddamage clear", &recordingAudit{})
	if acks := m.acknowledgements(); len(acks) != 1 || !acks[0].Equal(m.at) {
		t.Fatalf("map acknowledgements = %v, want the retry to name the newer count", acks)
	}
}

// What an agent restart must not change: the condition is the map's, so a
// new process warns the first player without having polled, and does not
// bring back a loss that was already accepted.
func TestWorldDamageFlow_ARestartedAgentStillWarnsAndStillStaysCleared(t *testing.T) {
	m := newFakeWorldMap(t, 6460)

	restarted := newDamageRig(t, m)
	restarted.join(t, playerXUID)
	if got := restarted.heardBy(playerXUID, 1); len(got) != 1 || !strings.Contains(got[0], "damaged") {
		t.Fatalf("player heard %q from a freshly started agent, want the warning", got)
	}

	restarted.typed(t, damageOperatorXUID, "!worlddamage clear", &recordingAudit{})

	again := newDamageRig(t, m)
	again.join(t, playerXUID)
	if got := again.heardBy(playerXUID, 0); len(got) != 0 {
		t.Fatalf("player heard %q from an agent started after the clear, want silence", got)
	}
}

// The wiring in registerPlugins is the one thing the rig above builds for
// itself, and a dropped option there compiles and leaves the rest green.
// Waits out the production drain delay, so it runs beside the other slow
// tests rather than ahead of them.
func TestWorldDamageFlow_TheDrainRegisteredForProductionCarriesTheNotice(t *testing.T) {
	t.Parallel()
	m := newFakeWorldMap(t, 6460)
	registry := plugin.NewRegistry()
	log := logging.New("error")
	permResolver := fakePermResolver(t, nil)
	notice := plugins.NewWorldDamage(worlddamage.New(mapclient.New(m.srv.URL, damageToken, time.Second), log), permResolver.Resolve)
	if err := registerPlugins(t.Context(), registry, nil, newJoinTimes(), notice, nil, log, notice); err != nil {
		t.Fatal(err)
	}
	var drain plugin.EventHandler
	for _, p := range registry.Plugins() {
		if p.Name() == "announce-drain" {
			drain = p.(plugin.EventHandler)
		}
	}
	voice := &recordingVoice{}
	ev := roster.JoinEvent{Entry: roster.Entry{XUID: playerXUID, Username: "Steve"}}
	if err := drain.HandleEvent(context.Background(), &plugin.Context{Voice: voice}, ev); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(announceDrainDelay + 10*time.Second)
	for time.Now().Before(deadline) {
		if out := voice.output(); len(out) == 1 && strings.Contains(out[0], "damaged") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("voice output = %v, want the world warning from the registered drain", voice.output())
}
