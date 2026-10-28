package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/life"
	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/internal/squad"
)

const testToken = "s3cret-token-value"

// squadServer is the stub of the squad service's profile route: it records
// each PUT and answers with a set status.
type squadServer struct {
	mu   sync.Mutex
	puts map[string][]tactics.Profile
	// served, when set for a squad, is what GET answers instead of the last PUT.
	served map[string]tactics.Profile
	auths  []string
	status int
}

func newSquadServer(t *testing.T) (*squadServer, *url.URL) {
	t.Helper()
	ss := &squadServer{puts: map[string][]tactics.Profile{}, status: http.StatusNoContent}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/squads/{id}/profile", func(w http.ResponseWriter, r *http.Request) {
		ss.mu.Lock()
		defer ss.mu.Unlock()
		ss.auths = append(ss.auths, r.Header.Get("Authorization"))
		var p tactics.Profile
		_ = json.NewDecoder(r.Body).Decode(&p)
		ss.puts[r.PathValue("id")] = append(ss.puts[r.PathValue("id")], p)
		if ss.status != http.StatusNoContent {
			http.Error(w, "refused", ss.status)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v1/squads/{id}/profile", func(w http.ResponseWriter, r *http.Request) {
		ss.mu.Lock()
		defer ss.mu.Unlock()
		id := r.PathValue("id")
		if p, ok := ss.served[id]; ok {
			_ = json.NewEncoder(w).Encode(p)
			return
		}
		if len(ss.puts[id]) == 0 {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(ss.puts[id][len(ss.puts[id])-1])
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base, _ := url.Parse(srv.URL)
	return ss, base
}

func TestSquadSinkPutsToTheSquadRouteWithTheToken(t *testing.T) {
	ss, base := newSquadServer(t)
	s := SquadSink{Base: base, Default: "lab-alpha", Token: testToken}
	cand := tactics.NewProfile("cand")
	cand.Default.FocusRange = 13
	if err := s.Push(context.Background(), []string{"Rusty", "Slate"}, cand); err != nil {
		t.Fatal(err)
	}
	got := ss.puts["lab-alpha"]
	if len(got) != 1 {
		t.Fatalf("PUTs = %d, want one per squad", len(got))
	}
	if got[0].Name != "cand" || got[0].Default.FocusRange != 13 {
		t.Errorf("body = %+v", got[0])
	}
	if ss.auths[0] != "Bearer "+testToken {
		t.Error("the PUT did not carry the bearer token")
	}
}

func TestSquadSinkClearPutsTheShippedDefault(t *testing.T) {
	ss, base := newSquadServer(t)
	s := SquadSink{Base: base, Default: "lab-alpha", Token: testToken}
	if err := s.Clear(context.Background(), []string{"Rusty"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(context.Background(), []string{"Rusty"}); err != nil {
		t.Fatalf("a second clear: %v", err)
	}
	got := ss.puts["lab-alpha"]
	if len(got) != 2 || got[0].Default != tactics.Defaults() || len(got[0].Overrides) != 0 {
		t.Errorf("clear sent %+v, want the default profile", got)
	}
}

func TestSquadSinkSplitsATeamsAcrossSquads(t *testing.T) {
	ss, base := newSquadServer(t)
	s := SquadSink{Base: base, Default: "lab-alpha", Squads: map[string]string{"Cinder": "lab-bravo"}, Token: testToken}
	if err := s.Push(context.Background(), []string{"Rusty", "Cinder"}, tactics.NewProfile("p")); err != nil {
		t.Fatal(err)
	}
	if len(ss.puts["lab-alpha"]) != 1 || len(ss.puts["lab-bravo"]) != 1 {
		t.Errorf("puts = %v", ss.puts)
	}
}

func TestSquadSinkConfirmNeedsTheSquadToServeExactlyThePushedProfile(t *testing.T) {
	ss, base := newSquadServer(t)
	s := SquadSink{Base: base, Default: "lab-alpha", Squads: map[string]string{"Cinder": "lab-bravo"}, Token: testToken}
	cand := candidate("cand")
	units := []string{"Rusty", "Cinder"}

	if err := s.Confirm(context.Background(), units, cand); err == nil {
		t.Error("confirmed with nothing served")
	}
	if err := s.Push(context.Background(), units, cand); err != nil {
		t.Fatal(err)
	}
	if err := s.Confirm(context.Background(), units, cand); err != nil {
		t.Errorf("a squad serving the pushed profile: %v", err)
	}

	other := candidate("cand")
	other.Default.KiteBackoff = 8
	ss.served = map[string]tactics.Profile{"lab-bravo": other}
	err := s.Confirm(context.Background(), units, cand)
	if err == nil || !strings.Contains(err.Error(), "lab-bravo") {
		t.Errorf("a squad serving a different profile under the same name: err = %v", err)
	}

	ss.served = map[string]tactics.Profile{"lab-bravo": tactics.NewProfile("default")}
	if err := s.Confirm(context.Background(), units, cand); err == nil {
		t.Error("confirmed while a squad serves the default")
	}
	if strings.Contains(fmt.Sprint(err), testToken) {
		t.Error("the error carries the token")
	}
}

func TestSquadSinkRefusesANonLabSquadWithoutARequest(t *testing.T) {
	ss, base := newSquadServer(t)
	for _, sq := range []string{"alpha", "alpha-lab-1", "", "lab-/../fwb", "lab-a/b", "lab-", "lab-A", "lab-a\n"} {
		s := SquadSink{Base: base, Default: sq, Token: testToken}
		if err := s.Push(context.Background(), []string{"Rusty"}, tactics.NewProfile("p")); err == nil {
			t.Errorf("squad %q: Push succeeded", sq)
		}
		if err := s.Clear(context.Background(), []string{"Rusty"}); err == nil {
			t.Errorf("squad %q: Clear succeeded", sq)
		}
	}
	if len(ss.auths) != 0 {
		t.Error("a request went out for a non-lab squad")
	}
}

func TestSquadSinkErrorsNeverCarryTheToken(t *testing.T) {
	ss, base := newSquadServer(t)
	ss.status = http.StatusUnauthorized
	s := SquadSink{Base: base, Default: "lab-alpha", Token: testToken}
	err := s.Push(context.Background(), []string{"Rusty"}, tactics.NewProfile("p"))
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want the status", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Error("the error carries the token")
	}

	dead, _ := url.Parse("http://127.0.0.1:1")
	err = SquadSink{Base: dead, Default: "lab-alpha", Token: testToken}.Push(context.Background(), []string{"Rusty"}, tactics.NewProfile("p"))
	if err == nil || strings.Contains(err.Error(), testToken) {
		t.Errorf("transport error = %v", err)
	}
}

func TestNewSinkNeedsALabSquadAndATokenFromTheEnvironment(t *testing.T) {
	t.Setenv("CONTROL_TOKEN", "")
	if _, err := newSink("", false, "http://crew-control:8080", "lab-alpha", "", nil, nil); err == nil {
		t.Error("no token was accepted")
	}
	t.Setenv("CONTROL_TOKEN", testToken)
	cases := []struct {
		name                    string
		dir, url, squad, squadB string
		teamA, teamB            []string
		wantErr                 bool
	}{
		{"files without a dry run", t.TempDir(), "", "", "", nil, nil, true},
		{"neither", "", "", "", "", nil, nil, true},
		{"profile dir with the route", t.TempDir(), "http://c:1", "lab-alpha", "", nil, nil, true},
		{"path-valued squad", "", "http://c:1", "lab-/../fwb", "", nil, nil, true},
		{"upper-case squad", "", "http://c:1", "lab-Alpha", "", nil, nil, true},
		{"squad route", "", "http://c:1", "lab-alpha", "", nil, nil, false},
		{"production squad", "", "http://c:1", "alpha", "", nil, nil, true},
		{"bad url", "", "ftp://c", "lab-alpha", "", nil, nil, true},
		{"duel on one squad", "", "http://c:1", "lab-alpha", "lab-alpha", []string{"A"}, []string{"B"}, true},
		{"duel with b squad", "", "http://c:1", "lab-alpha", "lab-bravo", []string{"A"}, []string{"B"}, false},
		{"duel b not lab", "", "http://c:1", "lab-alpha", "bravo", []string{"A"}, []string{"B"}, true},
	}
	for _, tc := range cases {
		_, err := newSink(tc.dir, false, tc.url, tc.squad, tc.squadB, tc.teamA, tc.teamB)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestNewSinkDryRunIsTheOnlyWayToTheFileSink(t *testing.T) {
	t.Setenv("CONTROL_TOKEN", testToken)
	if sink, err := newSink(t.TempDir(), true, "", "", "", nil, nil); err != nil {
		t.Errorf("dry run with a directory: %v", err)
	} else if _, ok := sink.(FileSink); !ok {
		t.Errorf("dry run sink = %T, want the file sink", sink)
	}
	if _, err := newSink("", true, "", "", "", nil, nil); err == nil {
		t.Error("dry run with nowhere to write was accepted")
	}
	if _, err := newSink(t.TempDir(), true, "http://c:1", "lab-alpha", "", nil, nil); err == nil {
		t.Error("dry run together with the squad route was accepted")
	}
}

type e2eUnits map[string]bool

func (u e2eUnits) Has(id string) bool { return u[id] }

// The sink's PUT body against the squad service's real routes: a trained
// profile, with its tactics and evidence, is stored whole and reads back
// through the contract's own loader.
func TestSquadSinkAgainstTheRealRoutes(t *testing.T) {
	mux := http.NewServeMux()
	squad.Routes(mux, squad.NewStore(), e2eUnits{"crew-18": true}, testToken)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base, _ := url.Parse(srv.URL)

	hello, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/squads/lab-alpha/hello", strings.NewReader(`{"unit":"crew-18","role":"striker"}`))
	hello.Header.Set("Authorization", "Bearer "+testToken)
	if resp, err := http.DefaultClient.Do(hello); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("hello: %v %v", resp, err)
	}

	pr := tactics.NewProfile("champ-9")
	pr.Parent = "champ-8"
	pr.Evidence = "runs/9/cards.jsonl"
	pr.Overrides = map[tactics.Family]tactics.Params{tactics.FamilyCreeper: {KiteBackoff: 9}}
	s := SquadSink{Base: base, Default: "lab-alpha", Token: testToken}
	if err := s.Push(context.Background(), []string{"crew-18"}, pr); err != nil {
		t.Fatal(err)
	}

	get, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/squads/lab-alpha/profile", nil)
	get.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	got, err := tactics.Parse(raw)
	if err != nil || got.Name != "champ-9" || got.Evidence != pr.Evidence || got.For(tactics.FamilyCreeper).KiteBackoff != 9 {
		t.Fatalf("read back %+v, %v from %s", got, err, raw)
	}

	if err := s.Confirm(context.Background(), []string{"crew-18"}, pr); err != nil {
		t.Errorf("the real route's copy does not hash like the pushed profile: %v", err)
	}

	if err := s.Clear(context.Background(), []string{"crew-18"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Confirm(context.Background(), []string{"crew-18"}, pr); err == nil {
		t.Error("confirmed the trained profile after it was cleared")
	}
}

// Every family the contract names must pass the squad service's door, or a
// profile that overrides it would be refused at the PUT.
func TestSquadServiceAcceptsEveryContractFamily(t *testing.T) {
	mux := http.NewServeMux()
	squad.Routes(mux, squad.NewStore(), e2eUnits{"crew-18": true}, testToken)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base, _ := url.Parse(srv.URL)
	hello, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/squads/lab-alpha/hello", strings.NewReader(`{"unit":"crew-18","role":"striker"}`))
	hello.Header.Set("Authorization", "Bearer "+testToken)
	if resp, err := http.DefaultClient.Do(hello); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("hello: %v %v", resp, err)
	}

	pr := tactics.NewProfile("every-family")
	pr.Overrides = map[tactics.Family]tactics.Params{}
	pr.Tactics = map[tactics.Family][]string{}
	for _, f := range []tactics.Family{tactics.FamilyDefault, tactics.FamilyZombie, tactics.FamilySkeleton,
		tactics.FamilyCreeper, tactics.FamilySpider, tactics.FamilyBlaze, tactics.FamilyEnderman,
		tactics.FamilyWitch, tactics.FamilyIllager, tactics.FamilyBrute, tactics.FamilyBoss, tactics.FamilyPlayer} {
		pr.Overrides[f] = tactics.Defaults()
		pr.Tactics[f] = life.TacticNames(f)
	}
	s := SquadSink{Base: base, Default: "lab-alpha", Token: testToken}
	if err := s.Push(context.Background(), []string{"crew-18"}, pr); err != nil {
		t.Fatalf("a profile naming every family and registered tactic was refused: %v", err)
	}
}
