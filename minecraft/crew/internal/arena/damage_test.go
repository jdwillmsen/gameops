package arena

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdwillmsen/gameops/minecraft/crew/internal/tactics"
)

func TestRecorderFillsDamageTakenFromTheDifferenceOfTotals(t *testing.T) {
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		setup func(f *FakeDamage)
		fight func(f *FakeDamage)
		want  float64
	}{
		{
			name:  "sums the growth of every warrior, ignoring what they lost earlier",
			setup: func(f *FakeDamage) { f.Set("Rusty", 40); f.Set("Slate", 3.5); f.Set("Tinder", 0) },
			fight: func(f *FakeDamage) { f.Add("Rusty", 6); f.Add("Slate", 2.5) },
			want:  8.5,
		},
		{
			name:  "a unit that restarted mid-fight reads lower and counts nothing",
			setup: func(f *FakeDamage) { f.Set("Rusty", 40); f.Set("Slate", 10) },
			fight: func(f *FakeDamage) { f.Set("Rusty", 5); f.Add("Slate", 4) },
			want:  4,
		},
		{
			name:  "a unit missing at the start is left out",
			setup: func(f *FakeDamage) { f.Set("Slate", 10) },
			fight: func(f *FakeDamage) { f.Add("Rusty", 50); f.Add("Slate", 1) },
			want:  1,
		},
		{
			name:  "a quiet fight is zero",
			setup: func(f *FakeDamage) { f.Set("Rusty", 12) },
			fight: func(*FakeDamage) {},
			want:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &FakeDamage{}
			tt.setup(f)
			r := &Recorder{Meta: recMeta, Damage: f}
			r.FightStart(at)
			tt.fight(f)
			got := r.Finish(tactics.OutcomeWin, 10)
			if got.DamageTaken != tt.want {
				t.Errorf("DamageTaken = %v, want %v", got.DamageTaken, tt.want)
			}
			if got.DamageDealt != 0 {
				t.Errorf("DamageDealt = %v, want 0", got.DamageDealt)
			}
		})
	}
}

func TestRecorderMeasuresEachFightOfAReusedRecorderAlone(t *testing.T) {
	f := &FakeDamage{}
	f.Set("Rusty", 0)
	r := &Recorder{Meta: recMeta, Damage: f}
	r.FightStart(time.Now())
	f.Add("Rusty", 7)
	if got := r.Finish(tactics.OutcomeWin, 1).DamageTaken; got != 7 {
		t.Fatalf("first fight = %v, want 7", got)
	}
	r.FightStart(time.Now())
	f.Add("Rusty", 2)
	if got := r.Finish(tactics.OutcomeWin, 1).DamageTaken; got != 2 {
		t.Errorf("second fight = %v, want 2", got)
	}
}

func TestRecorderScoresWithoutDamageWhenTheSourceFails(t *testing.T) {
	var logged []string
	f := &FakeDamage{}
	f.Set("Rusty", 1)
	r := &Recorder{Meta: recMeta, Damage: f, Logf: func(format string, _ ...any) { logged = append(logged, format) }}
	r.FightStart(time.Now())
	f.Add("Rusty", 9)
	f.Fail(errors.New("down"))
	got := r.Finish(tactics.OutcomeLoss, 1)
	if got.DamageTaken != 0 || len(logged) == 0 {
		t.Errorf("DamageTaken = %v, logged %v; want zero and a logged reason", got.DamageTaken, logged)
	}

	// With no baseline there is nothing to subtract from, so a late success
	// must not be read as the whole lifetime total.
	r.FightStart(time.Now())
	f.Fail(nil)
	f.Add("Rusty", 5)
	if got := r.Finish(tactics.OutcomeLoss, 1).DamageTaken; got != 0 {
		t.Errorf("without a start reading = %v, want 0", got)
	}
}

func TestHTTPDamageReadsTheFightTotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/units/Rusty/v1/state":
			w.Write([]byte(`{"tasks":{"fight":{"total":{"damage_taken":12.5},"current":{"damage_taken":3}}}}`))
		case "/units/Bare/v1/state":
			w.Write([]byte(`{"tasks":{}}`))
		case "/units/Junk/v1/state":
			w.Write([]byte(`not json`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	src, err := NewHTTPDamage(srv.URL + "/units/{name}/v1/state")
	if err != nil {
		t.Fatal(err)
	}
	got, err := src.Taken(context.Background(), []string{"Rusty", "Bare", "Junk", "Gone"})
	if got["Rusty"] != 12.5 || len(got) != 1 {
		t.Errorf("readings = %v, want only Rusty at 12.5", got)
	}
	for _, want := range []string{"Bare: the unit reports no fight stats", "Gone: status 404", "Junk:"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("error %v lacks %q", err, want)
		}
	}
}

func TestHTTPDamageEscapesTheName(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		w.Write([]byte(`{"tasks":{"fight":{"total":{"damage_taken":1}}}}`))
	}))
	defer srv.Close()
	src, _ := NewHTTPDamage(srv.URL + "/units/{name}/v1/state")
	if _, err := src.Taken(context.Background(), []string{"../x y"}); err != nil {
		t.Fatal(err)
	}
	if path != "/units/..%2Fx%20y/v1/state" {
		t.Errorf("path = %q", path)
	}
}

func TestNewHTTPDamageRejectsBadTemplates(t *testing.T) {
	for _, bad := range []string{"", "http://crew/units/v1/state", "ftp://crew/{name}", "/units/{name}"} {
		if _, err := NewHTTPDamage(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
