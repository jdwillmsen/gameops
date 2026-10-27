package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
	"github.com/jdwillmsen/gameops/minecraft/internal/squad"
)

const testToken = "s3cret-token-value"

// squadServer is the stub of the squad service's profile route: it records
// each PUT and answers with a set status.
type squadServer struct {
	mu     sync.Mutex
	puts   map[string][]tactics.Profile
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

func TestSquadSinkRefusesANonLabSquadWithoutARequest(t *testing.T) {
	ss, base := newSquadServer(t)
	for _, sq := range []string{"alpha", "alpha-lab-1", ""} {
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
	if _, err := newSink("", "http://crew-control:8080", "lab-alpha", "", nil, nil); err == nil {
		t.Error("no token was accepted")
	}
	t.Setenv("CONTROL_TOKEN", testToken)
	cases := []struct {
		name                    string
		dir, url, squad, squadB string
		teamA, teamB            []string
		wantErr                 bool
	}{
		{"files", t.TempDir(), "", "", "", nil, nil, false},
		{"neither", "", "", "", "", nil, nil, true},
		{"both", t.TempDir(), "http://c:1", "lab-alpha", "", nil, nil, true},
		{"squad route", "", "http://c:1", "lab-alpha", "", nil, nil, false},
		{"production squad", "", "http://c:1", "alpha", "", nil, nil, true},
		{"bad url", "", "ftp://c", "lab-alpha", "", nil, nil, true},
		{"duel on one squad", "", "http://c:1", "lab-alpha", "lab-alpha", []string{"A"}, []string{"B"}, true},
		{"duel with b squad", "", "http://c:1", "lab-alpha", "lab-bravo", []string{"A"}, []string{"B"}, false},
		{"duel b not lab", "", "http://c:1", "lab-alpha", "bravo", []string{"A"}, []string{"B"}, true},
	}
	for _, tc := range cases {
		_, err := newSink(tc.dir, tc.url, tc.squad, tc.squadB, tc.teamA, tc.teamB)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
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

	if err := s.Clear(context.Background(), []string{"crew-18"}); err != nil {
		t.Fatal(err)
	}
}
