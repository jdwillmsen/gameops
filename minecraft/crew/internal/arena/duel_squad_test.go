package arena

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
)

const squadToken = "s3cret-token"

// fakeSquads records every duel PUT per squad and can be told to fail.
type fakeSquads struct {
	mu     sync.Mutex
	puts   map[string][]squadDuel
	auth   []string
	status map[string]int
}

func newFakeSquads(t *testing.T) (*fakeSquads, *url.URL) {
	t.Helper()
	f := &fakeSquads{puts: map[string][]squadDuel{}, status: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/squads/{id}/duel", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		id := r.PathValue("id")
		if code := f.status[id]; code != 0 {
			http.Error(w, "refused: "+r.Header.Get("Authorization"), code)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var d squadDuel
		if err := json.Unmarshal(raw, &d); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.puts[id] = append(f.puts[id], d)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return f, u
}

func (f *fakeSquads) states(squad string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, d := range f.puts[squad] {
		out = append(out, d.State)
	}
	return out
}

func (f *fakeSquads) last(squad string) squadDuel {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p := f.puts[squad]; len(p) > 0 {
		return p[len(p)-1]
	}
	return squadDuel{}
}

func newSquadPolicy(t *testing.T, base *url.URL, tags TargetPolicy) *SquadPolicy {
	t.Helper()
	p, err := NewSquadPolicy(base, "lab-red", "lab-blue", squadToken,
		[]string{"Rusty", "Slate"}, []string{"Tinder", "Agate"}, tags)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSquadPolicyTellsEachSquadItsFightersAndEnemies(t *testing.T) {
	f, base := newFakeSquads(t)
	console := &FakeConsole{}
	p := newSquadPolicy(t, base, &ConsolePolicy{Console: console})
	if err := p.Arm(Roster{Red: []string{"Rusty", "Slate"}, Blue: []string{"Tinder", "Agate"}}); err != nil {
		t.Fatal(err)
	}
	red, blue := f.last("lab-red"), f.last("lab-blue")
	if red.State != "armed" || !reflect.DeepEqual(red.Fighters, []string{"Rusty", "Slate"}) || !reflect.DeepEqual(red.Enemies, []string{"Tinder", "Agate"}) {
		t.Errorf("red squad heard %+v", red)
	}
	if blue.State != "armed" || !reflect.DeepEqual(blue.Fighters, []string{"Tinder", "Agate"}) || !reflect.DeepEqual(blue.Enemies, []string{"Rusty", "Slate"}) {
		t.Errorf("blue squad heard %+v", blue)
	}
	if red.Round == "" || red.Round != blue.Round {
		t.Errorf("rounds %q and %q, want one shared id", red.Round, blue.Round)
	}
	if !hasCommand(console.Cmds, `tag "Rusty" add duel_red`) {
		t.Errorf("the world tags were not kept: %v", console.Cmds)
	}
	if err := p.Go(); err != nil {
		t.Fatal(err)
	}
	if f.last("lab-red").State != "go" || f.last("lab-blue").State != "go" {
		t.Error("go did not reach both squads")
	}
	if err := p.Eliminated("Tinder"); err != nil {
		t.Fatal(err)
	}
	for _, sq := range []string{"lab-red", "lab-blue"} {
		if d := f.last(sq); d.State != "go" || !reflect.DeepEqual(d.Eliminated, []string{"Tinder"}) {
			t.Errorf("%s heard %+v after an elimination", sq, d)
		}
	}
	if !hasCommand(console.Cmds, `tag "Tinder" add duel_out`) {
		t.Error("the elimination was not tagged")
	}
	if err := p.Disarm(); err != nil {
		t.Fatal(err)
	}
	for _, sq := range []string{"lab-red", "lab-blue"} {
		if d := f.last(sq); d.State != "over" || len(d.Enemies) != 0 || len(d.Fighters) != 0 {
			t.Errorf("%s heard %+v on disarm", sq, d)
		}
	}
	for _, a := range f.auth {
		if a != "Bearer "+squadToken {
			t.Fatalf("authorization = %q", a)
		}
	}
}

// With --rounds the sides swap but the squads do not: each squad still hunts
// the other's units.
func TestSquadPolicyFollowsTheTeamsAcrossASwap(t *testing.T) {
	f, base := newFakeSquads(t)
	p := newSquadPolicy(t, base, nil)
	if err := p.Arm(Roster{Red: []string{"Tinder", "Agate"}, Blue: []string{"Rusty", "Slate"}}); err != nil {
		t.Fatal(err)
	}
	if d := f.last("lab-red"); !reflect.DeepEqual(d.Fighters, []string{"Rusty", "Slate"}) || !reflect.DeepEqual(d.Enemies, []string{"Tinder", "Agate"}) {
		t.Errorf("after the swap the red squad heard %+v", d)
	}
}

func TestSquadPolicyDisarmIsIdleBeforeGoAndOverAfter(t *testing.T) {
	f, base := newFakeSquads(t)
	p := newSquadPolicy(t, base, nil)
	if err := p.Arm(Roster{Red: []string{"Rusty"}, Blue: []string{"Tinder"}}); err != nil {
		t.Fatal(err)
	}
	if err := p.Disarm(); err != nil {
		t.Fatal(err)
	}
	if got := f.states("lab-red"); !reflect.DeepEqual(got, []string{"armed", "idle"}) {
		t.Errorf("states = %v, want armed then idle", got)
	}
	// Idempotent, and sent even when nothing was ever armed.
	if err := p.Disarm(); err != nil {
		t.Fatal(err)
	}
	fresh := newSquadPolicy(t, base, nil)
	if err := fresh.Disarm(); err != nil || f.last("lab-blue").State != "idle" {
		t.Errorf("a disarm with nothing armed: %v %+v", err, f.last("lab-blue"))
	}
}

func TestSquadPolicyRefusesWhatItCannotSafelySend(t *testing.T) {
	_, base := newFakeSquads(t)
	ru, bu := []string{"Rusty"}, []string{"Tinder"}
	for name, build := range map[string]func() (*SquadPolicy, error){
		"production red squad": func() (*SquadPolicy, error) { return NewSquadPolicy(base, "alpha", "lab-b", "t", ru, bu, nil) },
		"production blue squad": func() (*SquadPolicy, error) {
			return NewSquadPolicy(base, "lab-a", "fwb", "t", ru, bu, nil)
		},
		"a path-shaped id": func() (*SquadPolicy, error) { return NewSquadPolicy(base, "lab-/../fwb", "lab-b", "t", ru, bu, nil) },
		"one squad twice":  func() (*SquadPolicy, error) { return NewSquadPolicy(base, "lab-a", "lab-a", "t", ru, bu, nil) },
		"no token":         func() (*SquadPolicy, error) { return NewSquadPolicy(base, "lab-a", "lab-b", "", ru, bu, nil) },
		"no base":          func() (*SquadPolicy, error) { return NewSquadPolicy(nil, "lab-a", "lab-b", "t", ru, bu, nil) },
		"a file URL": func() (*SquadPolicy, error) {
			u, _ := url.Parse("file:///x")
			return NewSquadPolicy(u, "lab-a", "lab-b", "t", ru, bu, nil)
		},
		"a duelist in both":   func() (*SquadPolicy, error) { return NewSquadPolicy(base, "lab-a", "lab-b", "t", ru, ru, nil) },
		"a squad with nobody": func() (*SquadPolicy, error) { return NewSquadPolicy(base, "lab-a", "lab-b", "t", ru, nil, nil) },
	} {
		if p, err := build(); err == nil || p != nil {
			t.Errorf("%s: built a policy", name)
		}
	}

	p := newSquadPolicy(t, base, nil)
	for name, r := range map[string]Roster{
		"a stranger":           {Red: []string{"Rusty", "Steve"}, Blue: []string{"Tinder"}},
		"a squad on two sides": {Red: []string{"Rusty", "Tinder"}, Blue: []string{"Slate"}},
		"both squads one side": {Red: []string{"Rusty", "Tinder"}},
		"an injected name":     {Red: []string{`Rusty"`}, Blue: []string{"Tinder"}},
	} {
		if err := p.Arm(r); err == nil {
			t.Errorf("%s: armed", name)
		}
	}
}

// A failed PUT is an error the duel stops on, and the error never carries
// the token even when the server echoes the header back.
func TestSquadPolicyFailsClosedAndNeverLeaksTheToken(t *testing.T) {
	f, base := newFakeSquads(t)
	p := newSquadPolicy(t, base, nil)
	r := Roster{Red: []string{"Rusty"}, Blue: []string{"Tinder"}}

	f.mu.Lock()
	f.status["lab-blue"] = http.StatusInternalServerError
	f.mu.Unlock()
	err := p.Arm(r)
	if err == nil {
		t.Fatal("armed with a squad that refused")
	}
	if strings.Contains(err.Error(), squadToken) {
		t.Errorf("the error carries the token: %v", err)
	}
	// Disarm tries both squads whatever the first answers.
	f.mu.Lock()
	f.status["lab-red"] = http.StatusInternalServerError
	f.mu.Unlock()
	if err := p.Disarm(); err == nil || strings.Contains(err.Error(), squadToken) {
		t.Errorf("disarm error = %v", err)
	}
	f.mu.Lock()
	delete(f.status, "lab-red")
	delete(f.status, "lab-blue")
	f.mu.Unlock()
	if err := p.Disarm(); err != nil || f.last("lab-red").State != "idle" {
		t.Errorf("recovery disarm: %v %+v", err, f.last("lab-red"))
	}

	// A squad that is unreachable at all.
	dead, _ := url.Parse("http://127.0.0.1:1")
	q, _ := NewSquadPolicy(dead, "lab-red", "lab-blue", squadToken, []string{"Rusty"}, []string{"Tinder"}, nil)
	if err := q.Arm(r); err == nil || strings.Contains(err.Error(), squadToken) {
		t.Errorf("arm against a dead squad service: %v", err)
	}
}

// hooked lets the duel simulator follow a SquadPolicy the way it follows the
// fake one.
type hooked struct {
	*SquadPolicy
	onGo, onDisarm func()
}

func (h hooked) Go() error {
	h.onGo()
	return h.SquadPolicy.Go()
}

func (h hooked) Disarm() error {
	h.onDisarm()
	return h.SquadPolicy.Disarm()
}

func squadDuelRig(t *testing.T) (*duelRig, *fakeSquads) {
	t.Helper()
	f, base := newFakeSquads(t)
	r := newDuelRig()
	sp := newSquadPolicy(t, base, &ConsolePolicy{Console: r.f})
	r.d.Policy = hooked{sp, func() { r.s.round++; r.s.armed = true; r.s.goAt = r.clock.Now() }, func() { r.s.armed = false }}
	return r, f
}

func TestDuelRunsOverTheSquadPolicy(t *testing.T) {
	r, f := squadDuelRig(t)
	r.d.Rounds = 2
	r.s.die(1, "Tinder", 10*time.Second)
	r.s.die(1, "Agate", 20*time.Second)
	r.s.die(2, "Slate", 10*time.Second)
	r.s.die(2, "Rusty", 20*time.Second)
	r.run(t)
	for sq, want := range map[string][]string{
		"lab-red":  {"armed", "go", "go", "go", "over", "armed", "go", "go", "go", "over"},
		"lab-blue": {"armed", "go", "go", "go", "over", "armed", "go", "go", "go", "over"},
	} {
		if got := f.states(sq); !reflect.DeepEqual(got, want) {
			t.Errorf("%s states = %v, want %v", sq, got, want)
		}
	}
}

// Every exit path withdraws the signal: an interrupt mid-round, a duelist
// that never turned up and a failed start all end with the squads told over
// or idle, on a context the cancelled one cannot cut short.
func TestDuelWithdrawsTheSquadSignalOnEveryExit(t *testing.T) {
	t.Run("interrupt mid round", func(t *testing.T) {
		r, f := squadDuelRig(t)
		ctx, cancel := context.WithCancel(context.Background())
		r.d.Sleep = func(d time.Duration) {
			r.clock.Sleep(d)
			if r.s.armed && r.clock.Now().Sub(r.s.goAt) >= 10*time.Second {
				cancel()
			}
		}
		if _, err := r.d.Run(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		for _, sq := range []string{"lab-red", "lab-blue"} {
			if got := f.last(sq).State; got != "over" {
				t.Errorf("%s ends %q after an interrupt, want over", sq, got)
			}
		}
	})
	t.Run("a squad that refuses the arm", func(t *testing.T) {
		r, f := squadDuelRig(t)
		f.mu.Lock()
		f.status["lab-blue"] = http.StatusServiceUnavailable
		f.mu.Unlock()
		if _, err := r.d.Run(context.Background()); err == nil {
			t.Fatal("the duel ran with a squad that refused")
		}
		if got := f.last("lab-red").State; got != "idle" {
			t.Errorf("the squad that took the arm ends %q, want idle", got)
		}
		for _, c := range r.f.Cmds {
			if strings.Contains(c, "duel_go") && strings.Contains(c, " add ") {
				t.Fatal("the start tag was set although the arm failed")
			}
		}
	})
	t.Run("a missing duelist", func(t *testing.T) {
		r, f := squadDuelRig(t)
		r.s.respawn = false
		r.s.die(1, "Tinder", 5*time.Second)
		_, err := r.d.Run(context.Background())
		if err == nil {
			t.Fatal("no error")
		}
		if got := f.last("lab-blue").State; got != "over" {
			t.Errorf("blue squad ends %q, want over", got)
		}
	})
}
