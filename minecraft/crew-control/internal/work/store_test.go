package work

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	ctx  = context.Background()
	tpl  = json.RawMessage(`{"kind":"goto","x":1}`)
	stop = json.RawMessage(`{"name":"stop"}`)
)

func TestMemoryStore(t *testing.T) {
	suite(t, func(t *testing.T) Store { return NewMemory() },
		// The memory store takes its clock from Next's argument.
		func(_ *testing.T, _ Store, _ uuid.UUID, d time.Duration) time.Time { return time.Now().Add(d) })
}

func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("CREW_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("CREW_PG_TEST_DSN not set")
	}
	suite(t, func(t *testing.T) Store {
		p, err := OpenPostgres(ctx, dsn, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		// Every case uses its own unit names, but start from nothing so a
		// previous run cannot leak in.
		if _, err := p.Prune(ctx, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		return p
	}, func(t *testing.T, s Store, id uuid.UUID, d time.Duration) time.Time {
		// The database keeps its own clock, so age the row instead.
		_, err := s.(*Postgres).pool.Exec(ctx,
			`UPDATE `+workTable+` SET delivered_at = delivered_at - make_interval(secs => $2) WHERE id = $1`,
			id, d.Seconds())
		if err != nil {
			t.Fatal(err)
		}
		return time.Now()
	})
}

// age makes a delivered item d older and returns the now to hand Next.
func suite(t *testing.T, mk func(*testing.T) Store, age func(*testing.T, Store, uuid.UUID, time.Duration) time.Time) {
	cases := map[string]func(*testing.T, Store){
		"commands before tasks, FIFO within a kind": func(t *testing.T, s Store) {
			t1 := must(t, s, "crew-01", KindTask, tpl)
			t2 := must(t, s, "crew-01", KindTask, tpl)
			c1 := must(t, s, "crew-01", KindCommand, stop)
			now := time.Now()
			for i, want := range []uuid.UUID{c1.ID, t1.ID, t2.ID} {
				it, ok, _, err := s.Next(ctx, "crew-01", now)
				if err != nil || !ok || it.ID != want {
					t.Fatalf("pull %d: got %v ok=%v err=%v, want %v", i, it.ID, ok, err, want)
				}
				if it.State != Delivered {
					t.Fatalf("state %q, want delivered", it.State)
				}
			}
			if _, ok, _, _ := s.Next(ctx, "crew-01", now); ok {
				t.Fatal("queue should be drained")
			}
		},
		"units do not see each other's work": func(t *testing.T, s Store) {
			must(t, s, "crew-02", KindTask, tpl)
			if _, ok, _, _ := s.Next(ctx, "crew-03", time.Now()); ok {
				t.Fatal("crew-03 received crew-02 work")
			}
		},
		"redelivery after 30 seconds": func(t *testing.T, s Store) {
			it := must(t, s, "crew-01", KindTask, tpl)
			if _, ok, _, _ := s.Next(ctx, "crew-01", time.Now()); !ok {
				t.Fatal("first delivery missing")
			}
			now := age(t, s, it.ID, 20*time.Second)
			_, ok, due, _ := s.Next(ctx, "crew-01", now)
			if ok {
				t.Fatal("redelivered too early")
			}
			if due < 5*time.Second || due > 11*time.Second {
				t.Fatalf("redeliverIn %v, want about 10s", due)
			}
			now = age(t, s, it.ID, 31*time.Second)
			got, ok, _, err := s.Next(ctx, "crew-01", now)
			if err != nil || !ok || got.ID != it.ID {
				t.Fatalf("no redelivery: %v %v %v", got.ID, ok, err)
			}
		},
		"running items are not redelivered": func(t *testing.T, s Store) {
			it := must(t, s, "crew-01", KindTask, tpl)
			now := time.Now()
			s.Next(ctx, "crew-01", now)
			if _, err := s.Apply(ctx, "crew-01", it.ID, Running, 40, ""); err != nil {
				t.Fatal(err)
			}
			if _, ok, _, _ := s.Next(ctx, "crew-01", age(t, s, it.ID, time.Hour)); ok {
				t.Fatal("running item came back")
			}
		},
		"apply updates and terminal states are sticky": func(t *testing.T, s Store) {
			it := must(t, s, "crew-01", KindTask, tpl)
			got, err := s.Apply(ctx, "crew-01", it.ID, Running, 40, "")
			if err != nil || got.State != Running || got.Progress != 40 {
				t.Fatalf("running: %+v %v", got, err)
			}
			got, _ = s.Apply(ctx, "crew-01", it.ID, Failed, 40, "blocked")
			if got.State != Failed || got.Reason != "blocked" {
				t.Fatalf("failed: %+v", got)
			}
			got, err = s.Apply(ctx, "crew-01", it.ID, Running, 90, "")
			if err != nil || got.State != Failed || got.Progress != 40 {
				t.Fatalf("terminal changed: %+v %v", got, err)
			}
		},
		"apply refusals": func(t *testing.T, s Store) {
			it := must(t, s, "crew-01", KindTask, tpl)
			if _, err := s.Apply(ctx, "crew-02", it.ID, Running, 0, ""); !errors.Is(err, ErrWrongUnit) {
				t.Fatalf("wrong unit: %v", err)
			}
			if _, err := s.Apply(ctx, "crew-01", uuid.New(), Running, 0, ""); !errors.Is(err, ErrNotFound) {
				t.Fatalf("unknown id: %v", err)
			}
			if _, err := s.Apply(ctx, "crew-01", it.ID, "queued", 0, ""); !errors.Is(err, ErrInvalid) {
				t.Fatalf("bad state: %v", err)
			}
		},
		"enqueue refuses bodies a unit cannot decode": func(t *testing.T, s Store) {
			for _, c := range []struct {
				kind string
				body string
			}{
				{KindTask, `[1]`}, {KindTask, `"x"`}, {KindTask, `null`}, {KindTask, `{`},
				{KindCommand, `{"name":"restart"}`}, {KindCommand, `{}`},
				{KindCommand, `{"name":"stop","x":1}`}, {KindCommand, `[]`},
				{"other", `{}`},
			} {
				if _, err := s.Enqueue(ctx, "crew-01", c.kind, json.RawMessage(c.body), "op"); !errors.Is(err, ErrInvalid) {
					t.Errorf("%s %s: err %v, want ErrInvalid", c.kind, c.body, err)
				}
			}
		},
		"cancel open": func(t *testing.T, s Store) {
			a := must(t, s, "crew-01", KindTask, tpl)
			b := must(t, s, "crew-01", KindTask, tpl)
			other := must(t, s, "crew-02", KindTask, tpl)
			s.Apply(ctx, "crew-01", b.ID, Done, 100, "")
			got, err := s.CancelOpen(ctx, "crew-01")
			if err != nil || len(got) != 1 || got[0].ID != a.ID || got[0].State != Cancelled {
				t.Fatalf("cancelled %+v %v", got, err)
			}
			recent, _ := s.Recent(ctx, "crew-02", 5)
			if len(recent) != 1 || recent[0].ID != other.ID || recent[0].State != Queued {
				t.Fatalf("other unit touched: %+v", recent)
			}
			if _, ok, _, _ := s.Next(ctx, "crew-01", time.Now()); ok {
				t.Fatal("cancelled item delivered")
			}
		},
		"recent is newest first and limited": func(t *testing.T, s Store) {
			var ids []uuid.UUID
			for range 4 {
				ids = append(ids, must(t, s, "crew-01", KindTask, tpl).ID)
				time.Sleep(2 * time.Millisecond)
			}
			got, err := s.Recent(ctx, "crew-01", 3)
			if err != nil || len(got) != 3 {
				t.Fatalf("len %d err %v", len(got), err)
			}
			for i, it := range got {
				if it.ID != ids[3-i] {
					t.Fatalf("position %d: %v, want %v", i, it.ID, ids[3-i])
				}
			}
		},
		"prune": func(t *testing.T, s Store) {
			must(t, s, "crew-01", KindTask, tpl)
			if n, err := s.Prune(ctx, time.Now().Add(-time.Hour)); err != nil || n != 0 {
				t.Fatalf("pruned fresh rows: %d %v", n, err)
			}
			if n, err := s.Prune(ctx, time.Now().Add(time.Hour)); err != nil || n != 1 {
				t.Fatalf("prune: %d %v", n, err)
			}
			if got, _ := s.Recent(ctx, "crew-01", 5); len(got) != 0 {
				t.Fatalf("still there: %+v", got)
			}
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) { fn(t, mk(t)) })
	}
}

func must(t *testing.T, s Store, unit, kind string, body json.RawMessage) Item {
	t.Helper()
	it, err := s.Enqueue(ctx, unit, kind, body, "op")
	if err != nil {
		t.Fatal(err)
	}
	return it
}
