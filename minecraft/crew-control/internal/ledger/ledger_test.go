package ledger

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

var ctx = context.Background()

func TestMemoryLedger(t *testing.T) {
	suite(t, func(t *testing.T) Ledger { return NewMemory() })
}

func TestPostgresLedger(t *testing.T) {
	dsn := os.Getenv("CREW_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("CREW_PG_TEST_DSN not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	suite(t, func(t *testing.T) Ledger {
		if _, err := pool.Exec(ctx, `DELETE FROM minecraft.crew_place`); err != nil {
			t.Fatal(err)
		}
		return NewPostgres(pool)
	})
}

func chest(id string, x, y, z int) Place {
	p := Pos{x, y, z}
	return Place{ID: id, Name: "Chest " + id, Kind: KindChest, Box: Box{p, p}}
}

func area(id string, min, max Pos, items ...string) Place {
	return Place{ID: id, Name: "Area " + id, Kind: KindArea, Box: Box{min, max}, Items: items}
}

func ids(ps []Place) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

func suite(t *testing.T, mk func(*testing.T) Ledger) {
	t0 := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	cases := map[string]func(*testing.T, Ledger){
		"seed upserts and drops seeded rows no longer listed": func(t *testing.T, l Ledger) {
			must(t, l.Seed(ctx, []Place{chest("home-chest", 1, 64, 2), area("forest", Pos{0, 64, 0}, Pos{9, 70, 9}, "oak_log")}))
			must(t, l.PutPlace(ctx, chest("mine", 5, 64, 5)))
			must(t, l.Seed(ctx, []Place{chest("home-chest", 1, 64, 3)}))
			got, err := l.Places(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if g := ids(got); len(g) != 2 || g[0] != "home-chest" || g[1] != "mine" {
				t.Fatalf("places %v, want [home-chest mine]", g)
			}
			if !got[0].Seeded || got[0].Box.Min.Z != 3 || got[1].Seeded {
				t.Errorf("seeded=%v z=%d mine seeded=%v", got[0].Seeded, got[0].Box.Min.Z, got[1].Seeded)
			}
			p, ok, err := l.Place(ctx, "forest")
			if err != nil || ok {
				t.Errorf("dropped place still there: %v %v %v", p, ok, err)
			}
		},
		"seeding drops the stock of a removed place": func(t *testing.T, l Ledger) {
			must(t, l.Seed(ctx, []Place{chest("home-chest", 1, 64, 2)}))
			must(t, l.Observe(ctx, "crew-01", t0, Pos{1, 64, 2}, map[string]int{"cobblestone": 3}))
			must(t, l.Seed(ctx, nil))
			must(t, l.Seed(ctx, []Place{chest("home-chest", 1, 64, 2)}))
			st, err := l.Stock(ctx, "home-chest")
			if err != nil || len(st) != 0 {
				t.Fatalf("stock %v err=%v", st, err)
			}
		},
		"a seeded place refuses edit and delete": func(t *testing.T, l Ledger) {
			must(t, l.Seed(ctx, []Place{chest("home-chest", 1, 64, 2)}))
			if err := l.PutPlace(ctx, chest("home-chest", 9, 64, 9)); !errors.Is(err, ErrSeeded) {
				t.Errorf("put: %v", err)
			}
			if err := l.DeletePlace(ctx, "home-chest"); !errors.Is(err, ErrSeeded) {
				t.Errorf("delete: %v", err)
			}
			if _, ok, _ := l.Place(ctx, "home-chest"); !ok {
				t.Error("seeded place was lost")
			}
		},
		"an unseeded place can be edited and deleted": func(t *testing.T, l Ledger) {
			must(t, l.PutPlace(ctx, chest("mine", 5, 64, 5)))
			edited := chest("mine", 6, 64, 6)
			edited.Name = "Renamed"
			must(t, l.PutPlace(ctx, edited))
			p, ok, err := l.Place(ctx, "mine")
			if err != nil || !ok || p.Name != "Renamed" || p.Box.Min.X != 6 {
				t.Fatalf("got %+v ok=%v err=%v", p, ok, err)
			}
			must(t, l.DeletePlace(ctx, "mine"))
			if _, ok, _ := l.Place(ctx, "mine"); ok {
				t.Error("still there")
			}
			if err := l.DeletePlace(ctx, "mine"); !errors.Is(err, ErrNotFound) {
				t.Errorf("second delete: %v", err)
			}
		},
		"invalid places are refused": func(t *testing.T, l Ledger) {
			bad := map[string]Place{
				"id":           chest("Bad_ID", 0, 64, 0),
				"x range":      chest("c1", 500, 64, 0),
				"y range":      chest("c1", 0, 321, 0),
				"z range":      chest("c1", 0, 64, -501),
				"chest box":    {ID: "c1", Name: "n", Kind: KindChest, Box: Box{Pos{0, 64, 0}, Pos{1, 64, 0}}},
				"area wide":    area("a1", Pos{0, 64, 0}, Pos{64, 64, 0}),
				"min over max": area("a1", Pos{5, 64, 0}, Pos{4, 64, 0}),
				"kind":         {ID: "c1", Name: "n", Kind: "pit", Box: Box{}},
				"name":         {ID: "c1", Kind: KindChest, Box: Box{Pos{0, 64, 0}, Pos{0, 64, 0}}},
				"item":         area("a1", Pos{0, 64, 0}, Pos{1, 64, 0}, "Oak Log"),
			}
			for name, p := range bad {
				if err := l.PutPlace(ctx, p); !errors.Is(err, ErrInvalid) {
					t.Errorf("%s: %v", name, err)
				}
				if err := l.Seed(ctx, []Place{p}); !errors.Is(err, ErrInvalid) {
					t.Errorf("seed %s: %v", name, err)
				}
			}
			ok := area("a1", Pos{0, 64, 0}, Pos{63, 64, 0})
			must(t, l.PutPlace(ctx, ok))
		},
		"observe at a known chest records stock": func(t *testing.T, l Ledger) {
			must(t, l.Seed(ctx, []Place{chest("home-chest", 1, 64, 2)}))
			must(t, l.Observe(ctx, "crew-01", t0, Pos{1, 64, 2}, map[string]int{"cobblestone": 12, "oak_log": 3}))
			st, err := l.Stock(ctx, "home-chest")
			if err != nil || len(st) != 2 {
				t.Fatalf("stock %v err=%v", st, err)
			}
			if st[0].Item != "cobblestone" || st[0].Count != 12 || !st[0].SeenAt.Equal(t0) || st[0].Unit != "crew-01" {
				t.Errorf("stock %+v", st[0])
			}
			all, _ := l.Places(ctx)
			if len(all) != 1 {
				t.Errorf("a known chest must not add a place: %v", ids(all))
			}
		},
		"observe at an unknown position creates a chest": func(t *testing.T, l Ledger) {
			must(t, l.Observe(ctx, "crew-02", t0, Pos{-12, 100, 5}, map[string]int{"coal": 2}))
			p, ok, err := l.Place(ctx, "chest-m12-100-5")
			if err != nil || !ok {
				t.Fatalf("place missing: ok=%v err=%v", ok, err)
			}
			if p.Name != "Unnamed chest" || p.Kind != KindChest || p.Seeded || p.Box.Min != (Pos{-12, 100, 5}) || p.Box.Max != p.Box.Min {
				t.Errorf("place %+v", p)
			}
			at, ok, err := l.ChestAt(ctx, Pos{-12, 100, 5})
			if err != nil || !ok || at.ID != p.ID {
				t.Errorf("ChestAt %+v ok=%v err=%v", at, ok, err)
			}
			if _, ok, _ := l.ChestAt(ctx, Pos{0, 0, 0}); ok {
				t.Error("ChestAt found a chest where there is none")
			}
		},
		"observe replaces the old items": func(t *testing.T, l Ledger) {
			must(t, l.Seed(ctx, []Place{chest("home-chest", 1, 64, 2)}))
			must(t, l.Observe(ctx, "crew-01", t0, Pos{1, 64, 2}, map[string]int{"cobblestone": 12, "oak_log": 3}))
			t1 := t0.Add(time.Minute)
			must(t, l.Observe(ctx, "crew-02", t1, Pos{1, 64, 2}, map[string]int{"oak_log": 1, "coal": 4}))
			st, _ := l.Stock(ctx, "home-chest")
			got := map[string]int{}
			for _, s := range st {
				got[s.Item] = s.Count
				if !s.SeenAt.Equal(t1) || s.Unit != "crew-02" {
					t.Errorf("%s not restamped: %+v", s.Item, s)
				}
			}
			if len(got) != 3 || got["cobblestone"] != 0 || got["oak_log"] != 1 || got["coal"] != 4 {
				t.Errorf("stock %v", got)
			}
		},
		"an older observation is ignored": func(t *testing.T, l Ledger) {
			must(t, l.Seed(ctx, []Place{chest("home-chest", 1, 64, 2)}))
			must(t, l.Observe(ctx, "crew-01", t0, Pos{1, 64, 2}, map[string]int{"coal": 9}))
			must(t, l.Observe(ctx, "crew-02", t0.Add(-time.Minute), Pos{1, 64, 2}, map[string]int{"coal": 1}))
			st, _ := l.Stock(ctx, "home-chest")
			if len(st) != 1 || st[0].Count != 9 || st[0].Unit != "crew-01" {
				t.Errorf("stock %+v", st)
			}
		},
		"observe drops bad item rows and refuses an impossible position": func(t *testing.T, l Ledger) {
			must(t, l.Observe(ctx, "crew-01", t0, Pos{3, 64, 3}, map[string]int{"coal": 2, "Bad Name": 1, "iron_ingot": -4}))
			st, _ := l.Stock(ctx, "chest-3-64-3")
			if len(st) != 1 || st[0].Item != "coal" {
				t.Errorf("stock %+v", st)
			}
			if err := l.Observe(ctx, "crew-01", t0, Pos{9999, 64, 3}, nil); !errors.Is(err, ErrInvalid) {
				t.Errorf("out of world: %v", err)
			}
		},
		"stock of an unknown place is empty": func(t *testing.T, l Ledger) {
			st, err := l.Stock(ctx, "nowhere")
			if err != nil || len(st) != 0 {
				t.Errorf("%v %v", st, err)
			}
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { c(t, mk(t)) })
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseSeed(t *testing.T) {
	if ps, err := ParseSeed(""); err != nil || len(ps) != 0 {
		t.Errorf("empty: %v %v", ps, err)
	}
	if ps, err := ParseSeed("  \n"); err != nil || len(ps) != 0 {
		t.Errorf("blank: %v %v", ps, err)
	}
	raw := `[{"id":"home-chest","name":"Home","kind":"chest","box":{"min":{"x":1,"y":64,"z":2},"max":{"x":1,"y":64,"z":2}}},
	  {"id":"forest","name":"Forest","kind":"area","box":{"min":{"x":0,"y":64,"z":0},"max":{"x":9,"y":70,"z":9}},"items":["oak_log"]}]`
	ps, err := ParseSeed(raw)
	if err != nil || len(ps) != 2 {
		t.Fatalf("%v %v", ps, err)
	}
	if !ps[0].Seeded || ps[1].Box.Max != (Pos{9, 70, 9}) || ps[1].Items[0] != "oak_log" {
		t.Errorf("%+v", ps)
	}
	for name, bad := range map[string]string{
		"json":      `[`,
		"unknown":   `[{"id":"a1","name":"n","kind":"chest","box":{},"colour":"red"}]`,
		"duplicate": `[{"id":"a1","name":"n","kind":"chest","box":{"min":{"x":0,"y":0,"z":0},"max":{"x":0,"y":0,"z":0}}},{"id":"a1","name":"n","kind":"chest","box":{"min":{"x":0,"y":0,"z":0},"max":{"x":0,"y":0,"z":0}}}]`,
		"invalid":   `[{"id":"A","name":"n","kind":"chest","box":{}}]`,
	} {
		if _, err := ParseSeed(bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestChestID(t *testing.T) {
	for p, want := range map[Pos]string{
		{-12, 100, 5}:  "chest-m12-100-5",
		{0, -64, -1}:   "chest-0-m64-m1",
		{499, 320, 31}: "chest-499-320-31",
	} {
		if got := chestID(p); got != want || !idRE.MatchString(got) {
			t.Errorf("%v: %q, want %q", p, got, want)
		}
	}
	// The widest id the world allows must still fit the 32-character limit.
	if id := chestID(Pos{-500, -64, -500}); !idRE.MatchString(id) {
		t.Errorf("%q does not match", id)
	}
}

func item(state string) work.Item { return work.Item{ID: uuid.New(), State: state} }

func TestFree(t *testing.T) {
	cases := []struct {
		name string
		u    UnitStatus
		want bool
	}{
		{"idle", UnitStatus{Online: true}, true},
		{"offline", UnitStatus{}, false},
		{"dead", UnitStatus{Online: true, Dead: true}, false},
		{"running", UnitStatus{Online: true, Running: 1}, false},
		{"delivered counts as busy", UnitStatus{Online: true, Delivered: 1}, false},
		{"one queued", UnitStatus{Online: true, Queued: 1}, true},
		{"two queued", UnitStatus{Online: true, Queued: 2}, false},
		{"one queued and running", UnitStatus{Online: true, Queued: 1, Running: 1}, false},
	}
	for _, c := range cases {
		if got := c.u.Free(); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStatuses(t *testing.T) {
	hp := float32(20)
	seen := time.Now()
	units := []telemetry.Status{
		{ID: "crew-01", Name: "One", Online: true, Health: &hp, Seen: seen},
		{ID: "crew-02", Name: "Two", Online: true},
		{ID: "crew-03", Name: "Three"},
	}
	open := map[string][]work.Item{
		"crew-01": {item(work.Queued), item(work.Delivered), item(work.Done)},
		"crew-02": {item(work.Running), item(work.Queued)},
	}
	got := Statuses(units, open)
	if len(got) != 3 {
		t.Fatalf("%d statuses", len(got))
	}
	a := got[0]
	if a.ID != "crew-01" || a.Name != "One" || a.Health != &hp || !a.Seen.Equal(seen) ||
		a.Queued != 1 || a.Delivered != 1 || a.Running != 0 || a.Free() {
		t.Errorf("crew-01 %+v", a)
	}
	if b := got[1]; b.Running != 1 || b.Queued != 1 || b.Free() {
		t.Errorf("crew-02 %+v", b)
	}
	if c := got[2]; c.Free() || c.Queued != 0 {
		t.Errorf("crew-03 %+v", c)
	}
}
