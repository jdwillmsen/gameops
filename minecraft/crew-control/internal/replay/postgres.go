package replay

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

const table = "minecraft.crew_position"

// A database that hangs would otherwise hold a request, and a pool slot,
// forever instead of answering "unavailable".
var queryTimeout = 5 * time.Second

// pruneChunk bounds one delete so a first pass after a long outage does not
// hold locks or write WAL for the whole backlog.
const pruneChunk = 5000

// Postgres keeps positions in the shared database, borrowing the work
// store's pool.
type Postgres struct{ pool *pgxpool.Pool }

var _ Store = (*Postgres)(nil)

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

func wrap(err error) error {
	if err != nil && !errors.Is(err, work.ErrUnavailable) && work.IsUnavailable(err) {
		return fmt.Errorf("%w: %v", work.ErrUnavailable, err)
	}
	return err
}

func (p *Postgres) Append(ctx context.Context, s []Sample) error {
	if len(s) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows := make([][]any, len(s))
	for i, x := range s {
		rows[i] = []any{x.Unit, x.At, x.X, x.Y, x.Z}
	}
	_, err := p.pool.CopyFrom(ctx, pgx.Identifier{"minecraft", "crew_position"},
		[]string{"unit", "at", "x", "y", "z"}, pgx.CopyFromRows(rows))
	return wrap(err)
}

func (p *Postgres) Query(ctx context.Context, q Query) (Page, error) {
	limit := q.limit()
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
	if !q.Since.IsZero() {
		add("at >= $%d", q.Since)
	}
	if !q.Until.IsZero() {
		add("at <= $%d", q.Until)
	}
	if q.paged() {
		args = append(args, q.Cursor.At, q.Cursor.ID)
		where = append(where, fmt.Sprintf("(at, id) > ($%d, $%d)", len(args)-1, len(args)))
	}
	sql := `SELECT id, unit, at, x, y, z FROM ` + table
	if len(where) > 0 {
		sql += ` WHERE ` + strings.Join(where, " AND ")
	}
	sql += fmt.Sprintf(` ORDER BY at, id LIMIT %d`, limit+1)

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := p.pool.Query(ctx, sql, args...)
	if err != nil {
		return Page{}, wrap(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Sample, error) {
		var s Sample
		return s, r.Scan(&s.ID, &s.Unit, &s.At, &s.X, &s.Y, &s.Z)
	})
	if err != nil {
		return Page{}, wrap(err)
	}
	if len(out) <= limit {
		return Page{Samples: out}, nil
	}
	last := out[limit-1]
	return Page{Samples: out[:limit], Next: &Cursor{At: last.At, ID: last.ID}}, nil
}

func (p *Postgres) Prune(ctx context.Context, before time.Time) (int, error) {
	total := 0
	for {
		n, err := p.pruneOnce(ctx, before)
		total += n
		if err != nil || n < pruneChunk {
			return total, err
		}
	}
}

func (p *Postgres) pruneOnce(ctx context.Context, before time.Time) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	tag, err := p.pool.Exec(ctx, `DELETE FROM `+table+` WHERE id IN (
		SELECT id FROM `+table+` WHERE at < $1 ORDER BY id LIMIT $2)`, before, pruneChunk)
	return int(tag.RowsAffected()), wrap(err)
}
