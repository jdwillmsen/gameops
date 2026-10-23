package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

const (
	eventTable = "minecraft.crew_event"

	eventColumns = "id, unit, at, kind, severity, task_id, job_id, x, y, z, payload"
)

// A database that hangs rather than refusing connections would otherwise
// hold a request, and a pool slot, forever instead of answering
// "unavailable".
var queryTimeout = 5 * time.Second

// Postgres keeps the event stream in the shared database. It borrows the
// work store's pool, so it neither opens nor closes connections.
type Postgres struct{ pool *pgxpool.Pool }

var _ Store = (*Postgres)(nil)

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

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
		rows = append(rows, []any{e.Unit, e.At, e.Kind, e.Severity, e.TaskID, e.JobID,
			pos[0], pos[1], pos[2], payload})
	}
	_, err := p.pool.CopyFrom(ctx, pgx.Identifier{"minecraft", "crew_event"},
		[]string{"unit", "at", "kind", "severity", "task_id", "job_id", "x", "y", "z", "payload"}, pgx.CopyFromRows(rows))
	return wrap(err)
}

func scan(row pgx.Row) (Event, error) {
	var (
		e   Event
		x   *float64
		y   *float64
		z   *float64
		err = row.Scan(&e.ID, &e.Unit, &e.At, &e.Kind, &e.Severity, &e.TaskID, &e.JobID,
			&x, &y, &z, &e.Payload)
	)
	if err == nil && x != nil && y != nil && z != nil {
		e.Pos = &Pos{X: *x, Y: *y, Z: *z}
	}
	return e, err
}

func (p *Postgres) Query(ctx context.Context, q Query) (Page, error) {
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
	sql := `SELECT ` + eventColumns + ` FROM ` + eventTable
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
	events, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) { return scan(r) })
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
		`SELECT `+eventColumns+` FROM `+eventTable+` WHERE at < $1 ORDER BY at, id LIMIT $2`, before, sample)
	if err != nil {
		return PruneReport{}, wrap(err)
	}
	rep.Would, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Event, error) { return scan(r) })
	return rep, wrap(err)
}
