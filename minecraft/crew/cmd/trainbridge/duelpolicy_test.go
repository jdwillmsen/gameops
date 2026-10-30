package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/arena"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

// duelPuts records the duel bodies PUT to each squad.
type duelPuts struct {
	mu   sync.Mutex
	puts map[string][]map[string]any
}

func newDuelPuts(t *testing.T) (*duelPuts, *url.URL) {
	t.Helper()
	d := &duelPuts{puts: map[string][]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/squads/{id}/duel", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		d.mu.Lock()
		d.puts[r.PathValue("id")] = append(d.puts[r.PathValue("id")], body)
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return d, u
}

func (d *duelPuts) states(squad string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, b := range d.puts[squad] {
		out = append(out, b["state"].(string))
	}
	return out
}

func (d *duelPuts) last(squad string) map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p := d.puts[squad]; len(p) > 0 {
		return p[len(p)-1]
	}
	return nil
}

func teamSink(base *url.URL) SquadSink {
	return SquadSink{Base: base, Default: "lab-alpha", Token: "tok",
		Squads: map[string]string{"Tinder": "lab-bravo", "Agate": "lab-bravo"}}
}

func names(v any) []string {
	var out []string
	for _, n := range v.([]any) {
		out = append(out, n.(string))
	}
	return out
}

// The bridge hands the duel to the squad that holds each team's profile, and
// when the candidate starts on blue the squads still follow their teams.
func TestDuelStartSignalReachesTheTeamsSquads(t *testing.T) {
	wantFighters := []string{"Rusty", "Slate"}
	for _, side := range []string{"a", "b"} {
		t.Run("side "+side, func(t *testing.T) {
			puts, base := newDuelPuts(t)
			r := newRig(t)
			sink := teamSink(base)
			r.b.DuelPolicy = func(red, blue []string) (arena.TargetPolicy, error) {
				return sink.duelPolicy(r.b.Console, red, blue)
			}
			r.sim.dieAfter = map[string]time.Duration{"Tinder": 20 * time.Second, "Agate": 30 * time.Second}
			if _, err := r.b.Run(context.Background(), duelRequest(side)); err != nil {
				t.Fatalf("Run: %v\n%s", err, r.out)
			}
			if got := puts.states("lab-alpha"); got[0] != "armed" || got[1] != "go" || got[len(got)-1] != "over" {
				t.Errorf("alpha states = %v", got)
			}
			if got := puts.states("lab-bravo"); got[0] != "armed" || got[len(got)-1] != "over" {
				t.Errorf("bravo states = %v", got)
			}
			// The last PUT before over is the go that names who is out.
			p := puts.puts["lab-alpha"]
			goBody := p[len(p)-2]
			if !reflect.DeepEqual(names(goBody["fighters"]), wantFighters) ||
				!reflect.DeepEqual(names(goBody["enemies"]), []string{"Tinder", "Agate"}) {
				t.Errorf("alpha heard %v, want its own team fighting team B", goBody)
			}
			if !reflect.DeepEqual(names(goBody["eliminated"]), []string{"Tinder", "Agate"}) {
				t.Errorf("eliminated = %v", goBody["eliminated"])
			}
		})
	}
}

func TestDuelWithAPolicyThatCannotBeBuiltDoesNotFight(t *testing.T) {
	r := newRig(t)
	r.b.DuelPolicy = func(red, blue []string) (arena.TargetPolicy, error) { return nil, errors.New("no squads") }
	cards, err := r.b.Run(context.Background(), duelRequest("a"))
	if !errors.As(err, new(refusal)) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if len(cards) != 1 || cards[0].Outcome != tactics.OutcomeAborted {
		t.Errorf("cards = %+v, want one aborted card", cards)
	}
	if len(r.sink.cleared) != 1 {
		t.Errorf("cleared = %v, want the profiles withdrawn", r.sink.cleared)
	}
	for _, c := range r.b.Console.(*arena.FakeConsole).Cmds {
		if strings.Contains(c, "duel_go") {
			t.Fatal("the duel went on")
		}
	}
}

func TestDuelPolicyRefusesASideSplitAcrossSquads(t *testing.T) {
	_, base := newDuelPuts(t)
	sink := teamSink(base)
	sink.Squads["Agate"] = "lab-charlie"
	if _, err := sink.duelPolicy(&arena.FakeConsole{}, []string{"Rusty"}, []string{"Tinder", "Agate"}); err == nil {
		t.Error("built a policy for a side in two squads")
	}
	if _, err := sink.duelPolicy(&arena.FakeConsole{}, []string{"Rusty"}, []string{"Slate"}); err == nil {
		t.Error("built a policy for two sides in one squad")
	}
	sink.Default = "alpha"
	if _, err := sink.duelPolicy(&arena.FakeConsole{}, []string{"Rusty"}, []string{"Tinder"}); err == nil {
		t.Error("built a policy for a production squad")
	}
}

func TestOnlyASquadSinkGetsADuelPolicy(t *testing.T) {
	t.Setenv("CONTROL_TOKEN", "tok")
	args := func(extra ...string) []string {
		return append([]string{"--lab", "crew-lab", "--layout", "../../lab/colosseum/layout.json", "--team-a", "Rusty", "--team-b", "Tinder"}, extra...)
	}
	b, err := parse(args("--squad-url", "http://control.lab:8080", "--squad", "lab-alpha", "--squad-b", "lab-bravo"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if b.DuelPolicy == nil {
		t.Fatal("a squad sink got no duel policy")
	}
	pol, err := b.DuelPolicy([]string{"Rusty"}, []string{"Tinder"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pol.(*arena.SquadPolicy); !ok {
		t.Errorf("policy = %T", pol)
	}
	d, err := parse(args("--dry-run", "--profile-dir", t.TempDir()), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if d.DuelPolicy != nil {
		t.Error("a dry run got a duel policy")
	}
}
