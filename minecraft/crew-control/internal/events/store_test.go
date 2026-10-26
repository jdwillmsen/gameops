package events

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ctx = context.Background()

// batch is a small fixture: three units, three kinds, one shared timestamp
// in the middle so the (at, id) tie-break is exercised by every walk.
func batch(t time.Time) []Event {
	shared := time.Date(2026, 10, 22, 12, 0, 1, 0, time.UTC)
	task := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	job := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	pos := &Pos{X: 12.5, Y: 64, Z: -3}
	return []Event{
		{Unit: "crew-01", At: shared.Add(-2 * time.Hour), Kind: "online", Severity: SevOK},
		{Unit: "crew-01", At: shared.Add(-time.Hour), Kind: "task_started", Severity: SevInfo, TaskID: &task, Pos: pos,
			Payload: []byte(`{"text":"started gather_and_deposit"}`)},
		{Unit: "crew-02", At: shared, Kind: "died", Severity: SevCrit, Pos: pos},
		{Unit: "crew-03", At: shared, Kind: "stuck", Severity: SevWarn, Payload: []byte(`{"text":"gather has not progressed"}`)},
		{Unit: "crew-02", At: shared, Kind: "respawned", Severity: SevInfo},
		{Unit: "crew-01", At: shared.Add(time.Hour), Kind: "task_done", Severity: SevOK, TaskID: &task, JobID: &job},
	}
}

func suite(t *testing.T, mk func(*testing.T) Store) {
	t.Helper()
	seed := func(t *testing.T, s Store) {
		if err := s.Append(ctx, batch(time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]func(*testing.T, Store){
		"query is newest first": func(t *testing.T, s Store) {
			seed(t, s)
			page, err := s.Query(ctx, Query{})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Events) != 6 {
				t.Fatalf("page holds %d, want 6", len(page.Events))
			}
			want := []string{"task_done", "respawned", "stuck", "died", "task_started", "online"}
			for i, w := range want {
				if page.Events[i].Kind != w {
					t.Fatalf("order %d = %s, want %s", i, page.Events[i].Kind, w)
				}
			}
			// The shared timestamp pages three rows by id, never randomly.
			if !page.Events[1].At.Equal(page.Events[2].At) || !page.Events[2].At.Equal(page.Events[3].At) {
				t.Fatal("the shared timestamp is not the middle three")
			}
			if page.Events[1].ID <= page.Events[2].ID || page.Events[2].ID <= page.Events[3].ID {
				t.Fatal("equal timestamps are not ordered by id")
			}
			if page.Next != nil {
				t.Fatal("a short page promised a next one")
			}
		},
		"filters narrow by unit, kind and severity": func(t *testing.T, s Store) {
			seed(t, s)
			for q, wantLen := range map[Query]int{
				{Unit: "crew-01"}:               3,
				{Kind: "died"}:                  1,
				{Severity: SevCrit}:             1,
				{Unit: "crew-02", Kind: "died"}: 1,
				{Unit: "crew-01", Kind: "died"}: 0,
			} {
				page, err := s.Query(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Events) != wantLen {
					t.Fatalf("query %+v: %d events, want %d", q, len(page.Events), wantLen)
				}
			}
		},
		"filters bound by time": func(t *testing.T, s Store) {
			seed(t, s)
			shared := time.Date(2026, 10, 22, 12, 0, 1, 0, time.UTC)
			since, until := Query{}, Query{}
			since.Since, until.Until = shared, shared
			for q, want := range map[Query]int{since: 4, until: 5} {
				page, err := s.Query(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Events) != want {
					t.Fatalf("query %+v: %d events, want %d", q, len(page.Events), want)
				}
			}
		},
		"a walk pages everything, in both directions": func(t *testing.T, s Store) {
			seed(t, s)
			for _, asc := range []bool{false, true} {
				var (
					seen  []Event
					cur   Query
					pages int
				)
				cur.Asc, cur.Limit = asc, 2
				for {
					page, err := s.Query(ctx, cur)
					if err != nil {
						t.Fatal(err)
					}
					seen = append(seen, page.Events...)
					pages++
					if page.Next == nil {
						break
					}
					cur.Cursor = *page.Next
					if pages > 10 {
						t.Fatal("pagination does not converge")
					}
				}
				if len(seen) != 6 || pages != 3 {
					t.Fatalf("asc=%v: %d events over %d pages, want 6 over 3", asc, len(seen), pages)
				}
				for i := 1; i < len(seen); i++ {
					a, b := seen[i-1], seen[i]
					if asc && !b.At.After(a.At) && (!b.At.Equal(a.At) || b.ID <= a.ID) {
						t.Fatalf("asc walk out of order at %d", i)
					}
					if !asc && !b.At.Before(a.At) && (!b.At.Equal(a.At) || b.ID >= a.ID) {
						t.Fatalf("desc walk out of order at %d", i)
					}
				}
			}
		},
		"a late post lands where it happened": func(t *testing.T, s Store) {
			seed(t, s)
			late := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
			if err := s.Append(ctx, []Event{{Unit: "crew-01", At: late, Kind: "online", Severity: SevOK}}); err != nil {
				t.Fatal(err)
			}
			page, err := s.Query(ctx, Query{})
			if err != nil {
				t.Fatal(err)
			}
			if last := page.Events[len(page.Events)-1]; !last.At.Equal(late) {
				t.Fatalf("oldest is %v, want the late post at %v", last.At, late)
			}
		},
		"a round trip keeps task, job, position and payload": func(t *testing.T, s Store) {
			seed(t, s)
			task := uuid.MustParse("11111111-1111-1111-1111-111111111111")
			job := uuid.MustParse("22222222-2222-2222-2222-222222222222")
			page, err := s.Query(ctx, Query{Kind: "task_done"})
			if err != nil || len(page.Events) != 1 {
				t.Fatalf("task_done: %d events, err %v", len(page.Events), err)
			}
			e := page.Events[0]
			if e.TaskID == nil || *e.TaskID != task || e.JobID == nil || *e.JobID != job {
				t.Fatalf("ids: %v %v", e.TaskID, e.JobID)
			}
			if e.Pos != nil {
				t.Fatalf("task_done carries a position: %+v", e.Pos)
			}
			page, err = s.Query(ctx, Query{Kind: "task_started"})
			if err != nil || len(page.Events) != 1 {
				t.Fatalf("task_started: %d events, err %v", len(page.Events), err)
			}
			if page.Events[0].Pos == nil || *page.Events[0].Pos != (Pos{12.5, 64, -3}) {
				t.Fatalf("pos: %+v", page.Events[0].Pos)
			}
			page, err = s.Query(ctx, Query{Kind: "stuck"})
			if err != nil || len(page.Events) != 1 {
				t.Fatalf("stuck: %d events, err %v", len(page.Events), err)
			}
			// jsonb reformats what it stores, so the content is compared, not the bytes.
			var got map[string]string
			if err := json.Unmarshal(page.Events[0].Payload, &got); err != nil || got["text"] != "gather has not progressed" || len(got) != 1 {
				t.Fatalf("payload: %s (%v)", page.Events[0].Payload, err)
			}
		},
		"prune keeps recent rows and reports old ones": func(t *testing.T, s Store) {
			seed(t, s)
			shared := time.Date(2026, 10, 22, 12, 0, 1, 0, time.UTC)
			before := shared.Add(-time.Minute)

			rep, err := s.Prune(ctx, before, 2, true)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Pruned != 0 || rep.Total != 2 || len(rep.Would) != 2 {
				t.Fatalf("dry run: %+v", rep)
			}
			if rep.Would[0].Kind != "online" || rep.Would[1].Kind != "task_started" {
				t.Fatalf("dry run sample is not oldest first: %+v", rep.Would)
			}
			if n := len(mustQuery(t, s, Query{})); n != 6 {
				t.Fatalf("dry run pruned anyway: %d rows", n)
			}
			rep, err = s.Prune(ctx, before, 1, false)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Pruned != 2 || rep.Total != 2 || len(rep.Would) != 0 {
				t.Fatalf("prune: %+v", rep)
			}
			kept := mustQuery(t, s, Query{})
			if len(kept) != 4 || kept[len(kept)-1].Kind != "died" {
				t.Fatalf("kept rows: %+v", kept)
			}
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			fn(t, mk(t))
		})
	}
}

func mustQuery(t *testing.T, s Store, q Query) []Event {
	t.Helper()
	page, err := s.Query(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	return page.Events
}

func TestMemoryStore(t *testing.T) {
	suite(t, func(*testing.T) Store { return NewMemory() })
}

// pgStore connects to the test database, applies the shipped migration to
// it, and starts from an empty table so a previous run cannot leak in. The
// test is skipped when no database is configured, like the work queue's.
func pgStore(t *testing.T) *Postgres {
	t.Helper()
	dsn := os.Getenv("CREW_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("CREW_PG_TEST_DSN not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// The migration owns the schema in production; the test makes sure the
	// schema exists, then runs the very file that ships.
	sql, err := os.ReadFile(migrationFile)
	if err != nil {
		t.Fatal(err)
	}
	ext, err := os.ReadFile(analyticsMigrationFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`CREATE SCHEMA IF NOT EXISTS minecraft`, string(sql), string(ext)} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("migration: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `TRUNCATE `+eventTable); err != nil {
		t.Fatal(err)
	}
	return NewPostgres(pool)
}

func TestPostgresStore(t *testing.T) {
	pgStore(t)
	suite(t, func(t *testing.T) Store { return pgStore(t) })
}

// The retention delete goes a chunk at a time; a backlog bigger than one
// chunk must still be cleared whole, and what is newer left alone.
func TestPostgresPruneClearsMoreThanOneChunk(t *testing.T) {
	s := pgStore(t)
	old := time.Now().Add(-MinRetention - time.Hour)
	rows := make([]Event, 0, pruneChunk*2+17)
	for i := 0; i < cap(rows); i++ {
		rows = append(rows, Event{Unit: "crew-01", At: old.Add(time.Duration(i) * time.Millisecond), Kind: "died", Severity: SevCrit})
	}
	rows = append(rows, Event{Unit: "crew-01", At: time.Now(), Kind: "online", Severity: SevOK})
	if err := s.Append(ctx, rows); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Prune(ctx, time.Now().Add(-MinRetention), 3, true)
	if err != nil || rep.Total != pruneChunk*2+17 || len(rep.Would) != 3 || rep.Pruned != 0 {
		t.Fatalf("dry run: %+v, %v", rep, err)
	}
	rep, err = s.Prune(ctx, time.Now().Add(-MinRetention), 0, false)
	if err != nil || rep.Pruned != pruneChunk*2+17 {
		t.Fatalf("prune: %+v, %v", rep, err)
	}
	if left := mustQuery(t, s, Query{}); len(left) != 1 || left[0].Kind != "online" {
		t.Fatalf("left: %+v", left)
	}
}

// The ticket's bar is a unit's last 1,000 events in well under 100 ms. On a
// table big enough that a scan would show, the plan for each console query
// shape must walk an index and never sort: the keyset order is the index's.
func TestPostgresQueryPlansUseTheIndexes(t *testing.T) {
	s := pgStore(t)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO `+eventTable+` (unit, at, kind, severity, payload)
		SELECT 'crew-' || lpad((g % 20)::text, 2, '0'),
		       now() - make_interval(secs => g),
		       (ARRAY['task_started','task_done','task_failed','died'])[1 + g % 4],
		       (ARRAY['info','ok','crit','crit'])[1 + g % 4],
		       jsonb_build_object('text', 'event ' || g)
		FROM generate_series(1, 60000) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `ANALYZE `+eventTable); err != nil {
		t.Fatal(err)
	}
	// A sampled ANALYZE makes the planner's pick between a bitmap scan plus
	// sort and an ordered index scan a near coin flip for a unit holding a
	// twentieth of the table. What is asserted is that the index can serve
	// the order with no sort, so the alternatives are taken off the table.
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for _, set := range []string{`SET enable_bitmapscan = off`, `SET enable_seqscan = off`} {
		if _, err := conn.Exec(ctx, set); err != nil {
			t.Fatal(err)
		}
	}
	cur := `(now(), 9223372036854775807)`
	for name, where := range map[string]string{
		"unit":          `unit = 'crew-07'`,
		"unit and time": `unit = 'crew-07' AND at >= now() - interval '1 day'`,
		"kind":          `kind = 'died'`,
		"severity":      `severity = 'crit'`,
		"unfiltered":    `true`,
		"unit paged":    `unit = 'crew-07' AND (at, id) < ` + cur,
	} {
		for _, dir := range []string{"DESC", "ASC"} {
			plan := explain(t, conn, `SELECT `+eventColumns+` FROM `+eventTable+` WHERE `+where+
				` ORDER BY at `+dir+`, id `+dir+` LIMIT 1001`)
			if strings.Contains(plan, "Seq Scan") || strings.Contains(plan, "Sort") {
				t.Errorf("%s %s: plan scans or sorts:\n%s", name, dir, plan)
			}
		}
	}
	start := time.Now()
	page, err := s.Query(ctx, Query{Unit: "crew-07", Limit: maxLimit})
	if err != nil || len(page.Events) != maxLimit || page.Next == nil {
		t.Fatalf("page: %d events, next %v, %v", len(page.Events), page.Next, err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("a unit's page took %v", d)
	}
}

func explain(t *testing.T, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, query string) string {
	t.Helper()
	rows, err := q.Query(ctx, `EXPLAIN `+query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	return plan.String()
}

// A Lazy with no store behind it answers unavailable, the same contract the
// work queue's has.
func TestLazyAnswersUnavailableUntilSet(t *testing.T) {
	l := &Lazy{}
	if _, err := l.Query(ctx, Query{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("empty lazy: %v", err)
	}
	if err := l.Append(ctx, batch(time.Now())); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("empty lazy append: %v", err)
	}
	l.Set(NewMemory())
	if err := l.Append(ctx, batch(time.Now())); err != nil {
		t.Fatal(err)
	}
	if n := len(mustQuery(t, l, Query{})); n != 6 {
		t.Fatalf("after set: %d rows, want 6", n)
	}
}

// A fake store for the writer's tests: it records batches and can be made to
// answer unavailable.
type fakeStore struct {
	mu     sync.Mutex
	events []Event
	err    error
}

func (f *fakeStore) Append(_ context.Context, ev []Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, ev...)
	return nil
}

func (f *fakeStore) Query(context.Context, Query) (Page, error) { return Page{}, nil }

func (f *fakeStore) Prune(context.Context, time.Time, int, bool) (PruneReport, error) {
	return PruneReport{}, nil
}
