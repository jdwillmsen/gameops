package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

const (
	eventTable = "minecraft.crew_event"

	eventColumns = "id, unit, at, kind, severity, task_id, job_id, x, y, z, payload"
	// analyticColumns exist from the V12 migration on.
	analyticColumns = ", reason_code, duration_ms, attempt, session_id, crew_version, cause_ref, outcome"

	// A table without the analytic columns is rechecked this often, so
	// applying the migration takes effect without a restart.
	extProbeEvery = time.Minute
)

// A database that hangs rather than refusing connections would otherwise
// hold a request, and a pool slot, forever instead of answering
// "unavailable".
var queryTimeout = 5 * time.Second

// Postgres keeps the event stream in the shared database. It borrows the
// work store's pool, so it neither opens nor closes connections.
type Postgres struct {
	pool *pgxpool.Pool

	// The analytic columns arrive with a later migration than the table, and
	// a deployment can run ahead of it: reading or writing a column that is
	// not there would fail every append and every query, not just the
	// analytic ones.
	extMu      sync.Mutex
	extKnown   bool
	extChecked time.Time
}

var _ Store = (*Postgres)(nil)

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

// extended reports whether the analytic columns exist. A probe that fails
// counts as "no": the store then behaves as the first schema did, and the
// next call after extProbeEvery looks again.
func (p *Postgres) extended(ctx context.Context) bool {
	p.extMu.Lock()
	defer p.extMu.Unlock()
	if p.extKnown || (!p.extChecked.IsZero() && time.Since(p.extChecked) < extProbeEvery) {
		return p.extKnown
	}
	p.extChecked = time.Now()
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	err := p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema = 'minecraft' AND table_name = 'crew_event' AND column_name = 'cause_ref')`).Scan(&p.extKnown)
	if err != nil {
		p.extKnown = false
	}
	return p.extKnown
}

func wrap(err error) error {
	if err != nil && !errors.Is(err, work.ErrUnavailable) && work.IsUnavailable(err) {
		return fmt.Errorf("%w: %v", work.ErrUnavailable, err)
	}
	return err
}

func (p *Postgres) Append(ctx context.Context, ev []Event) error {
	if len(ev) == 0 {
		return nil
	}
	ext := p.extended(ctx)
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	// One insert for the whole batch: the poller hands over a tick's worth
	// at a time and a unit posts a list, and a row-per-exec round trip per
	// event would turn either into a burst of connections held for no reason.
	rows := make([][]any, 0, len(ev))
	for _, e := range ev {
		payload := []byte(e.Payload)
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		var pos [3]*float64
		if e.Pos != nil {
			// A position is three nullable columns, not one, so an event
			// without a place reads back without one however it was written.
			pos = [3]*float64{&e.Pos.X, &e.Pos.Y, &e.Pos.Z}
		}
		row := []any{e.Unit, e.At, e.Kind, e.Severity, e.TaskID, e.JobID,
			pos[0], pos[1], pos[2], payload}
		if ext {
			row = append(row, nullable(e.ReasonCode), e.DurationMS, e.Attempt, nullable(e.SessionID),
				nullable(e.CrewVersion), e.CauseRef, nullable(e.Outcome))
		}
		rows = append(rows, row)
	}
	cols := []string{"unit", "at", "kind", "severity", "task_id", "job_id", "x", "y", "z", "payload"}
	if ext {
		cols = append(cols, "reason_code", "duration_ms", "attempt", "session_id", "crew_version", "cause_ref", "outcome")
	}
	_, err := p.pool.CopyFrom(ctx, pgx.Identifier{"minecraft", "crew_event"}, cols, pgx.CopyFromRows(rows))
	return wrap(err)
}

// nullable stores an unset string as NULL, so "no reason" is one value in the
// column and an exact filter never has to wonder about the empty string.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func scan(row pgx.Row, ext bool) (Event, error) {
	var (
		e               Event
		x               *float64
		y               *float64
		z               *float64
		dest            = []any{&e.ID, &e.Unit, &e.At, &e.Kind, &e.Severity, &e.TaskID, &e.JobID, &x, &y, &z, &e.Payload}
		rc, sid, cv, oc *string
	)
	if ext {
		dest = append(dest, &rc, &e.DurationMS, &e.Attempt, &sid, &cv, &e.CauseRef, &oc)
	}
	err := row.Scan(dest...)
	if err == nil && ext {
		e.ReasonCode, e.SessionID, e.CrewVersion, e.Outcome = deref(rc), deref(sid), deref(cv), deref(oc)
	}
	if err == nil && x != nil && y != nil && z != nil {
		e.Pos = &Pos{X: *x, Y: *y, Z: *z}
	}
	return e, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (p *Postgres) Query(ctx context.Context, q Query) (Page, error) {
	ext := p.extended(ctx)
	// Without the analytic columns a filter on one cannot be answered;
	// ignoring it would return unfiltered history that reads as a match.
	if !ext && (q.ReasonCode != "" || q.Outcome != "" || q.SessionID != "" || q.CauseRef != 0) {
		return Page{}, fmt.Errorf("%w: analytic filters need the extended schema", ErrInvalid)
	}
	limit := defaultLimit
	if q.Limit > 0 {
		limit = min(q.Limit, maxLimit)
	}

	var (
		where []string
		args  []any
	)
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if q.Unit != "" {
		add("unit = $%d", q.Unit)
	}
	if q.Kind != "" {
		add("kind = $%d", q.Kind)
	}
	if q.Severity != "" {
		add("severity = $%d", q.Severity)
	}
	if q.ReasonCode != "" {
		add("reason_code = $%d", q.ReasonCode)
	}
	if q.Outcome != "" {
		add("outcome = $%d", q.Outcome)
	}
	if q.SessionID != "" {
		add("session_id = $%d", q.SessionID)
	}
	if q.CauseRef != 0 {
		add("cause_ref = $%d", q.CauseRef)
	}
	if q.JobID != nil {
		add("job_id = $%d", *q.JobID)
	}
	if !q.Since.IsZero() {
		add("at >= $%d", q.Since)
	}
	if !q.Until.IsZero() {
		add("at <= $%d", q.Until)
	}
	if !q.Cursor.At.IsZero() || q.Cursor.ID != 0 {
		// The row comparison is the whole cursor: both columns compare at
		// once, so equal timestamps do not let the same row page twice.
		args = append(args, q.Cursor.At, q.Cursor.ID)
		where = append(where, fmt.Sprintf("(at, id) %s ($%d, $%d)", bound(q.Asc), len(args)-1, len(args)))
	}
	ord := "DESC, id DESC"
	if q.Asc {
		ord = "ASC, id ASC"
	}
	sql := `SELECT ` + columns(ext) + ` FROM ` + eventTable
	if len(where) > 0 {
		sql += ` WHERE ` + strings.Join(where, " AND ")
	}
	sql += fmt.Sprintf(` ORDER BY at %s LIMIT %d`, ord, limit+1)

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := p.pool.Query(ctx, sql, args...)
	if err != nil {
		return Page{}, wrap(err)
	}
	events, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) { return scan(r, ext) })
	if err != nil {
		return Page{}, wrap(err)
	}
	var next *Cursor
	if len(events) > limit {
		last := events[limit-1]
		next = &Cursor{At: last.At, ID: last.ID}
		events = events[:limit]
	}
	return Page{Events: events, Next: next}, nil
}

func columns(ext bool) string {
	if ext {
		return eventColumns + analyticColumns
	}
	return eventColumns
}

// bound is the strict half-open comparison a keyset cursor walks: after the
// cursor ascending, before it descending.
func bound(asc bool) string {
	if asc {
		return ">"
	}
	return "<"
}

// pruneChunk bounds one delete statement: a month of history is deleted a
// slice at a time so no statement holds row locks or writes WAL for the whole
// backlog, which matters most on the first pass after a long outage.
const pruneChunk = 5000

// Prune deletes events older than before in chunks, or in a dry run counts
// them and lists the oldest sample of them without touching anything. A real
// pass reports only the count: listing every deleted row would hold them all
// in memory for a report nobody reads.
func (p *Postgres) Prune(ctx context.Context, before time.Time, sample int, dryRun bool) (PruneReport, error) {
	if dryRun {
		return p.dryRun(ctx, before, sample)
	}
	var rep PruneReport
	for {
		n, err := p.pruneOnce(ctx, before)
		rep.Pruned += n
		rep.Total = rep.Pruned
		if err != nil {
			return rep, err
		}
		if n < pruneChunk {
			return rep, nil
		}
	}
}

func (p *Postgres) pruneOnce(ctx context.Context, before time.Time) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	// Ordered by id so the chunk is the oldest rows and the primary key
	// answers it without a sort.
	tag, err := p.pool.Exec(ctx, `DELETE FROM `+eventTable+` WHERE id IN (
		SELECT id FROM `+eventTable+` WHERE at < $1 ORDER BY id LIMIT $2)`, before, pruneChunk)
	return int(tag.RowsAffected()), wrap(err)
}

func (p *Postgres) dryRun(ctx context.Context, before time.Time, sample int) (PruneReport, error) {
	ext := p.extended(ctx)
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	var rep PruneReport
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM `+eventTable+` WHERE at < $1`, before).Scan(&rep.Total); err != nil {
		return PruneReport{}, wrap(err)
	}
	if sample <= 0 || rep.Total == 0 {
		return rep, nil
	}
	rows, err := p.pool.Query(ctx,
		`SELECT `+columns(ext)+` FROM `+eventTable+` WHERE at < $1 ORDER BY at, id LIMIT $2`, before, sample)
	if err != nil {
		return PruneReport{}, wrap(err)
	}
	rep.Would, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) { return scan(r, ext) })
	return rep, wrap(err)
}
