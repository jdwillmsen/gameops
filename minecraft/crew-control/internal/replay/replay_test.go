package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jdwillmsen/gameops/minecraft/agent/pkg/logging"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/auth"
	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/telemetry"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
)

func track(unit string, n int, step time.Duration) []Sample {
	out := make([]Sample, n)
	for i := range out {
		out[i] = Sample{Unit: unit, At: t0.Add(time.Duration(i) * step), X: float64(i), Y: 64, Z: -float64(i) / 2}
	}
	return out
}

func storeCases(t *testing.T, s Store) {
	if err := s.Append(ctx, append(track("crew-01", 10, 2*time.Second), track("crew-02", 10, 2*time.Second)...)); err != nil {
		t.Fatal(err)
	}
	all, err := s.Query(ctx, Query{Since: t0, Until: t0.Add(time.Hour)})
	if err != nil || len(all.Samples) != 20 || all.Next != nil {
		t.Fatalf("all: %d samples, next %v, %v", len(all.Samples), all.Next, err)
	}
	for i := 1; i < len(all.Samples); i++ {
		if all.Samples[i].At.Before(all.Samples[i-1].At) {
			t.Fatal("not oldest first")
		}
	}

	// Paging with a page size that splits equal timestamps must neither skip
	// nor repeat a sample.
	var got []Sample
	q := Query{Since: t0, Until: t0.Add(time.Hour), Limit: 3}
	for pages := 0; ; pages++ {
		if pages > 20 {
			t.Fatal("paging did not end")
		}
		p, err := s.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, p.Samples...)
		if p.Next == nil {
			break
		}
		q.Cursor = *p.Next
	}
	if len(got) != 20 {
		t.Fatalf("paged %d samples, want 20", len(got))
	}
	seen := map[int64]bool{}
	for _, g := range got {
		if seen[g.ID] {
			t.Fatalf("sample %d repeated", g.ID)
		}
		seen[g.ID] = true
	}

	one, _ := s.Query(ctx, Query{Unit: "crew-02", Since: t0.Add(4 * time.Second), Until: t0.Add(8 * time.Second)})
	if len(one.Samples) != 3 || one.Samples[0].X != 2 || one.Samples[2].X != 4 {
		t.Errorf("unit and range filter: %+v", one.Samples)
	}

	n, err := s.Prune(ctx, t0.Add(10*time.Second))
	if err != nil || n != 10 {
		t.Fatalf("prune: %d, %v", n, err)
	}
	left, _ := s.Query(ctx, Query{})
	if len(left.Samples) != 10 {
		t.Errorf("after prune: %d", len(left.Samples))
	}
}

func TestMemoryStore(t *testing.T) { storeCases(t, NewMemory()) }

func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("CREW_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("CREW_PG_TEST_DSN not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	sql, err := os.ReadFile("../../migrations/V14__minecraft_crew_position.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`CREATE SCHEMA IF NOT EXISTS minecraft`, string(sql), `TRUNCATE ` + table} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	storeCases(t, NewPostgres(pool))
}

type brokenStore struct{}

func (brokenStore) Append(context.Context, []Sample) error { return ErrUnavailable }
func (brokenStore) Query(context.Context, Query) (Page, error) {
	return Page{}, ErrUnavailable
}
func (brokenStore) Prune(context.Context, time.Time) (int, error) { return 0, ErrUnavailable }

func TestFallbackServesFromMemoryWhileTheDatabaseIsAway(t *testing.T) {
	f := NewFallback(logging.New("error"))
	if !f.Degraded() {
		t.Error("a store with no database must report itself degraded")
	}
	if err := f.Append(ctx, track("crew-01", 3, time.Second)); err != nil {
		t.Fatal(err)
	}
	f.SetPrimary(brokenStore{})
	if err := f.Append(ctx, track("crew-01", 3, time.Second)); err != nil {
		t.Fatal(err)
	}
	p, err := f.Query(ctx, Query{})
	if err != nil || len(p.Samples) != 6 {
		t.Fatalf("memory read: %d, %v", len(p.Samples), err)
	}
	f.SetPrimary(NewMemory())
	if err := f.Append(ctx, track("crew-01", 2, time.Second)); err != nil || f.Degraded() {
		t.Errorf("a recovered primary must clear the degraded flag: %v", err)
	}
	p, _ = f.Query(ctx, Query{})
	if len(p.Samples) != 2 {
		t.Errorf("reads must follow the primary once it answers, got %d", len(p.Samples))
	}
}

type fakeSource struct {
	pos    []telemetry.Position
	status []telemetry.Status
}

func (f *fakeSource) Positions() []telemetry.Position { return f.pos }
func (f *fakeSource) Snapshot() []telemetry.Status    { return f.status }

func online(id string) telemetry.Status { return telemetry.Status{ID: id, Online: true} }

func TestRecorderStoresMovementAndKeepaliveOnly(t *testing.T) {
	src := &fakeSource{
		pos:    []telemetry.Position{{ID: "a", X: 10, Y: 64, Z: 5, Known: true}, {ID: "b", X: 1, Y: 64, Z: 1, Known: true}},
		status: []telemetry.Status{online("a"), online("b")},
	}
	r := NewRecorder(NewMemory())
	if got := r.Sample(src, t0); len(got) != 2 {
		t.Fatalf("first reading stores every unit, got %d", len(got))
	}
	src.pos[0].X = 11
	got := r.Sample(src, t0.Add(2*time.Second))
	if len(got) != 1 || got[0].Unit != "a" || got[0].X != 11 {
		t.Fatalf("only the unit that moved: %+v", got)
	}
	if got := r.Sample(src, t0.Add(4*time.Second)); len(got) != 0 {
		t.Fatalf("a standing fleet stores nothing: %+v", got)
	}
	if got := r.Sample(src, t0.Add(Keepalive+3*time.Second)); len(got) != 2 {
		t.Fatalf("a keepalive row per standing unit after %s, got %d", Keepalive, len(got))
	}
}

func TestRecorderForgetsUnitsThatLeave(t *testing.T) {
	src := &fakeSource{
		pos:    []telemetry.Position{{ID: "a", X: 10, Known: true}, {ID: "n", X: 3, Dim: -1, Known: true}, {ID: "u", Known: false}},
		status: []telemetry.Status{online("a"), online("n"), online("u")},
	}
	r := NewRecorder(NewMemory())
	if got := r.Sample(src, t0); len(got) != 1 || got[0].Unit != "a" {
		t.Fatalf("only a known overworld position is stored: %+v", got)
	}
	src.status[0].Dead = true
	r.Sample(src, t0.Add(2*time.Second))
	src.status[0].Dead = false
	if got := r.Sample(src, t0.Add(4*time.Second)); len(got) != 1 {
		t.Errorf("the first reading after a death must be stored even without movement: %+v", got)
	}
}

type gate bool

func (g gate) Operator(*http.Request) (auth.Operator, bool) { return auth.Operator{ID: "o"}, bool(g) }

func serve(t *testing.T, s Store, signedIn bool, query string) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	NewAPI(s).MountOperator(mux, gate(signedIn), func(id string) bool { return id == "crew-01" || id == "crew-02" })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/replay/positions?"+query, nil))
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q", cc)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func rng(extra url.Values) string {
	v := url.Values{"since": {t0.Format(time.RFC3339)}, "until": {t0.Add(time.Hour).Format(time.RFC3339)}}
	for k, x := range extra {
		v[k] = x
	}
	return v.Encode()
}

func TestAPIIsOperatorOnly(t *testing.T) {
	s := NewMemory()
	_ = s.Append(ctx, track("crew-01", 3, time.Second))
	code, body := serve(t, s, false, rng(nil))
	if code != 401 || body["samples"] != nil {
		t.Errorf("signed out: %d %v", code, body)
	}
}

func TestAPIRejectsBadRanges(t *testing.T) {
	s := NewMemory()
	for name, q := range map[string]string{
		"no range":      "",
		"inverted":      url.Values{"since": {t0.Add(time.Hour).Format(time.RFC3339)}, "until": {t0.Format(time.RFC3339)}}.Encode(),
		"too long":      url.Values{"since": {t0.Format(time.RFC3339)}, "until": {t0.Add(25 * time.Hour).Format(time.RFC3339)}}.Encode(),
		"bad limit":     rng(url.Values{"limit": {"0"}}),
		"huge limit":    rng(url.Values{"limit": {fmt.Sprint(MaxLimit + 1)}}),
		"bad cursor":    rng(url.Values{"cursor": {"nope"}}),
		"bad timestamp": rng(url.Values{"since": {"yesterday"}}),
	} {
		if code, _ := serve(t, s, true, q); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	if code, _ := serve(t, s, true, rng(url.Values{"unit": {"crew-99"}})); code != 404 {
		t.Errorf("unknown unit: %d, want 404", code)
	}
}

// What an operator replays is what the unit reported: the recorded rows, read
// back through the route, match field for field and in order.
func TestAPIReturnsTheRecordedRowsInOrderAndPages(t *testing.T) {
	s := NewMemory()
	rows := append(track("crew-01", 300, 2*time.Second), track("crew-02", 300, 2*time.Second)...)
	rows[7].X, rows[7].Z = 12.3456, -9.8765
	_ = s.Append(ctx, rows)

	var got []map[string]any
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		extra := url.Values{"limit": {"250"}}
		if cursor != "" {
			extra.Set("cursor", cursor)
		}
		code, body := serve(t, s, true, rng(extra))
		if code != 200 {
			t.Fatalf("status %d", code)
		}
		for _, x := range body["samples"].([]any) {
			got = append(got, x.(map[string]any))
		}
		next, _ := body["next"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(got) != 600 {
		t.Fatalf("%d samples across pages, want 600", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i]["t"].(float64) < got[i-1]["t"].(float64) {
			t.Fatal("samples out of order")
		}
	}
	for _, g := range got {
		if g["u"] == "crew-01" && g["t"].(float64) == float64(t0.Add(14*time.Second).UnixMilli()) {
			if g["x"] != 12.35 || g["z"] != -9.88 || g["y"] != 64.0 {
				t.Errorf("sample did not match the stored row: %v", g)
			}
			return
		}
	}
	t.Error("the edited sample was not returned")
}

func TestAPIStoreErrors(t *testing.T) {
	if code, _ := serve(t, brokenStore{}, true, rng(nil)); code != 503 {
		t.Errorf("unavailable store: %d, want 503", code)
	}
}
