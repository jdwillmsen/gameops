package events

import (
	"errors"
	"os"
	"testing"
	"time"
)

const analyticsMigrationFile = "../../migrations/V12__minecraft_crew_event_analytics.sql"

func analyticBatch() []Event {
	at := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	ms, attempt := 1800, 2
	return []Event{
		{Unit: "crew-11", At: at, Kind: "kit_failed", Severity: SevWarn, ReasonCode: "kit_no_path", SessionID: "ab12cd34", CrewVersion: "1.36.0", Attempt: &attempt},
		{Unit: "crew-11", At: at.Add(time.Minute), Kind: "task_failed", Severity: SevCrit, ReasonCode: "no_pickup", Outcome: OutcomeFailed, DurationMS: &ms, SessionID: "ab12cd34"},
		{Unit: "crew-12", At: at.Add(2 * time.Minute), Kind: "task_done", Severity: SevOK, Outcome: OutcomeOK, DurationMS: &ms},
	}
}

func analyticCases(t *testing.T, s Store) {
	if err := s.Append(ctx, analyticBatch()); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[Query]int{
		{ReasonCode: "no_pickup"}:                1,
		{Outcome: OutcomeOK}:                     1,
		{Outcome: OutcomeFailed}:                 1,
		{SessionID: "ab12cd34"}:                  2,
		{SessionID: "ab12cd34", Unit: "crew-12"}: 0,
	} {
		page, err := s.Query(ctx, q)
		if err != nil {
			t.Fatalf("%+v: %v", q, err)
		}
		if len(page.Events) != want {
			t.Errorf("%+v: %d events, want %d", q, len(page.Events), want)
		}
	}
	page, err := s.Query(ctx, Query{ReasonCode: "no_pickup"})
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("failed task: %v, %v", page, err)
	}
	got := page.Events[0]
	if got.DurationMS == nil || *got.DurationMS != 1800 || got.Outcome != OutcomeFailed || got.SessionID != "ab12cd34" {
		t.Errorf("analytic fields did not round trip: %+v", got)
	}
	// A link the caller sets is stored as given and filterable. The memory
	// store may add its own links on top, so the check is that ours is found.
	kit := mustQuery(t, s, Query{Kind: "kit_failed"})[0]
	failed := got
	failed.ID, failed.CauseRef = 0, &kit.ID
	failed.At = failed.At.Add(time.Second)
	if err := s.Append(ctx, []Event{failed}); err != nil {
		t.Fatal(err)
	}
	linked := mustQuery(t, s, Query{CauseRef: kit.ID})
	found := false
	for _, e := range linked {
		found = found || (e.Kind == "task_failed" && e.At.Equal(failed.At))
	}
	if !found {
		t.Errorf("events explained by the kit failure: %+v", linked)
	}
}

func TestMemoryAnalyticFields(t *testing.T) { analyticCases(t, NewMemory()) }

func TestPostgresAnalyticFields(t *testing.T) { analyticCases(t, pgStore(t)) }

// A deployment can run ahead of V12. Until the columns exist the store must
// keep taking and serving events, refuse an analytic filter rather than
// ignore it, and pick the columns up without a restart once they appear.
func TestPostgresBeforeAndAfterTheAnalyticMigration(t *testing.T) {
	s := pgStore(t)
	if _, err := s.pool.Exec(ctx, `DROP TABLE `+eventTable); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pgStore(t) })
	v11, err := os.ReadFile(migrationFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, string(v11)); err != nil {
		t.Fatal(err)
	}

	if err := s.Append(ctx, analyticBatch()); err != nil {
		t.Fatalf("append on the first schema: %v", err)
	}
	if n := len(mustQuery(t, s, Query{})); n != 3 {
		t.Fatalf("first schema holds %d events, want 3", n)
	}
	if _, err := s.Query(ctx, Query{ReasonCode: "no_pickup"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("analytic filter on the first schema: %v", err)
	}

	v12, err := os.ReadFile(analyticsMigrationFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, string(v12)); err != nil {
		t.Fatal(err)
	}
	s.extMu.Lock()
	s.extChecked = time.Now().Add(-2 * extProbeEvery)
	s.extMu.Unlock()
	if err := s.Append(ctx, analyticBatch()); err != nil {
		t.Fatalf("append after V12: %v", err)
	}
	if n := len(mustQuery(t, s, Query{ReasonCode: "no_pickup"})); n != 1 {
		t.Fatalf("after V12 the filter matches %d, want only the row written after it", n)
	}
	// Rows from before the migration read back with the fields unset.
	old := mustQuery(t, s, Query{Kind: "kit_failed", Asc: true})
	if len(old) != 2 || old[0].ReasonCode != "" || old[1].ReasonCode != "kit_no_path" {
		t.Errorf("kit failures across the migration: %+v", old)
	}
}

// The poller never knows a stored id, so the store links a failed task to
// its cause itself, whichever batch the cause arrived in.
func TestPostgresLinksFailuresToTheirCause(t *testing.T) {
	s := pgStore(t)
	at := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	batch := []Event{
		{Unit: "crew-11", At: at, Kind: "kit_failed", Severity: SevWarn},
		{Unit: "crew-11", At: at.Add(time.Minute), Kind: "task_failed", Severity: SevCrit, Outcome: OutcomeFailed},
		{Unit: "crew-11", At: at.Add(2 * time.Minute), Kind: "task_done", Severity: SevOK, Outcome: OutcomeOK},
		{Unit: "crew-12", At: at.Add(time.Minute), Kind: "task_failed", Severity: SevCrit, Outcome: OutcomeFailed},
	}
	if err := s.Append(ctx, batch); err != nil {
		t.Fatal(err)
	}
	later := []Event{
		{Unit: "crew-11", At: at.Add(3 * time.Minute), Kind: "task_failed", Severity: SevCrit, Outcome: OutcomeFailed},
		{Unit: "crew-11", At: at.Add(time.Hour), Kind: "task_failed", Severity: SevCrit, Outcome: OutcomeFailed},
	}
	if err := s.Append(ctx, later); err != nil {
		t.Fatal(err)
	}
	kit := mustQuery(t, s, Query{Kind: "kit_failed"})[0]
	var linked, unlinked []Event
	for _, e := range mustQuery(t, s, Query{Asc: true}) {
		switch {
		case e.Kind != "task_failed" && e.CauseRef != nil:
			t.Errorf("%s at %s must not be linked", e.Kind, e.At)
		case e.Kind != "task_failed":
		case e.CauseRef != nil:
			linked = append(linked, e)
		default:
			unlinked = append(unlinked, e)
		}
	}
	if len(linked) != 2 || len(unlinked) != 2 {
		t.Fatalf("linked %d and unlinked %d failures, want 2 and 2", len(linked), len(unlinked))
	}
	for _, e := range linked {
		if e.Unit != "crew-11" || *e.CauseRef != kit.ID {
			t.Errorf("failure on %s at %s linked to %d, want crew-11 and %d", e.Unit, e.At, *e.CauseRef, kit.ID)
		}
	}
}
