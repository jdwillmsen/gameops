package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	workTable  = "minecraft.crew_work"
	eventTable = "minecraft.crew_work_event"

	columns = "id, unit, kind, body, state, progress, reason, operator, created_at, updated_at"
)

// Postgres keeps the queue in the shared database, so work survives a restart
// of crew-control.
type Postgres struct {
	pool   *pgxpool.Pool
	closed atomic.Bool
}

var _ Store = (*Postgres)(nil)

// OpenPostgres connects and pings before returning, so a wrong setting shows
// at start rather than at the first poll.
func OpenPostgres(ctx context.Context, dsn string, connectTimeout time.Duration) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("work: parse dsn: %w", err)
	}
	// Each long poll holds no connection while it waits, only while it reads.
	cfg.MaxConns = 5
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("work: open pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("work: ping: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() {
	p.closed.Store(true)
	p.pool.Close()
}

// unavailable reports errors that mean the database cannot be reached right
// now, as opposed to a query it rejected: those are the ones a caller should
// answer with "try again", not "broken".
func (p *Postgres) unavailable(err error) bool {
	var (
		netErr  net.Error
		connErr *pgconn.ConnectError
		pgErr   *pgconn.PgError
	)
	switch {
	case p.closed.Load(),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
		errors.As(err, &connErr),
		errors.As(err, &netErr),
		pgconn.SafeToRetry(err):
		return true
	case errors.As(err, &pgErr):
		// Connection exceptions, and the server shutting down or crashing.
		return strings.HasPrefix(pgErr.Code, "08") || strings.HasPrefix(pgErr.Code, "57P")
	}
	return false
}

func (p *Postgres) wrap(err error) error {
	if err != nil && !errors.Is(err, ErrUnavailable) && p.unavailable(err) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}

func scan(row pgx.Row) (Item, error) {
	var it Item
	err := row.Scan(&it.ID, &it.Unit, &it.Kind, &it.Body, &it.State, &it.Progress,
		&it.Reason, &it.Operator, &it.Created, &it.Updated)
	return it, err
}

// inTx commits only if fn succeeds, which is what keeps a state change and
// its event row from being recorded apart.
func (p *Postgres) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return p.wrap(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return p.wrap(err)
	}
	return p.wrap(tx.Commit(ctx))
}

func addEvent(ctx context.Context, tx pgx.Tx, it Item) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO `+eventTable+` (work_id, state, progress, reason) VALUES ($1, $2, $3, $4)`,
		it.ID, it.State, it.Progress, it.Reason)
	return err
}

func (p *Postgres) Enqueue(ctx context.Context, unit, kind string, body json.RawMessage, operator string) (Item, error) {
	if err := validateBody(kind, body); err != nil {
		return Item{}, err
	}
	var it Item
	err := p.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		it, err = scan(tx.QueryRow(ctx,
			`INSERT INTO `+workTable+` (id, unit, kind, body, state, operator)
			 VALUES ($1, $2, $3, $4, 'queued', $5) RETURNING `+columns,
			uuid.New(), unit, kind, []byte(body), operator))
		if err != nil {
			return err
		}
		return addEvent(ctx, tx, it)
	})
	return it, err
}

func (p *Postgres) Next(ctx context.Context, unit, kinds string, _ time.Time) (Item, bool, time.Duration, error) {
	var (
		it    Item
		found bool
		due   time.Duration
	)
	err := p.inTx(ctx, func(tx pgx.Tx) error {
		// SKIP LOCKED keeps two controller replicas from handing one item to
		// the same unit twice.
		var err error
		it, err = scan(tx.QueryRow(ctx,
			`SELECT `+columns+` FROM `+workTable+`
				 WHERE unit = $1 AND ($3 = '' OR kind = 'command') AND (state = 'queued'
			       OR (state = 'delivered' AND delivered_at <= now() - make_interval(secs => $2))
			       OR (state = 'running' AND updated_at <= now() - make_interval(secs => $2)))
			 ORDER BY (kind = 'command') DESC, created_at, id
			 LIMIT 1 FOR UPDATE SKIP LOCKED`,
			unit, RedeliverAfter.Seconds(), kinds))
		if errors.Is(err, pgx.ErrNoRows) {
			var secs *float64
			if err := tx.QueryRow(ctx,
				`SELECT extract(epoch FROM min(CASE state WHEN 'delivered' THEN delivered_at ELSE updated_at END)
				        + make_interval(secs => $2) - now())::float8
				 FROM `+workTable+` WHERE unit = $1 AND ($3 = '' OR kind = 'command') AND state IN ('delivered', 'running')`,
				unit, RedeliverAfter.Seconds(), kinds).Scan(&secs); err != nil {
				return err
			}
			if secs != nil {
				// A row locked by another replica can read as already due;
				// a short wait beats a spin.
				due = max(time.Duration(*secs*float64(time.Second)), 50*time.Millisecond)
			}
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		it, err = scan(tx.QueryRow(ctx,
			`UPDATE `+workTable+` SET state = 'delivered', delivered_at = now(), updated_at = now()
			 WHERE id = $1 RETURNING `+columns, it.ID))
		if err != nil {
			return err
		}
		return addEvent(ctx, tx, it)
	})
	return it, found && err == nil, due, err
}

func (p *Postgres) Apply(ctx context.Context, unit string, id uuid.UUID, state string, progress int, reason string) (Item, error) {
	if err := validateReport(state); err != nil {
		return Item{}, err
	}
	progress, reason = cleanReport(progress, reason)
	var it Item
	err := p.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		it, err = scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM `+workTable+` WHERE id = $1 FOR UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if it.Unit != unit {
			return ErrWrongUnit
		}
		if terminal(it.State) {
			return nil
		}
		it, err = scan(tx.QueryRow(ctx,
			`UPDATE `+workTable+` SET state = $2, progress = $3, reason = $4, updated_at = now()
			 WHERE id = $1 RETURNING `+columns, id, state, progress, reason))
		if err != nil {
			return err
		}
		return addEvent(ctx, tx, it)
	})
	return it, err
}

func (p *Postgres) CancelQueued(ctx context.Context, unit string, id uuid.UUID) (Item, bool, error) {
	var (
		it        Item
		cancelled bool
	)
	err := p.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		it, err = scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM `+workTable+` WHERE id = $1 FOR UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if it.Unit != unit {
			return ErrWrongUnit
		}
		if it.State != Queued {
			return nil
		}
		it, err = scan(tx.QueryRow(ctx,
			`UPDATE `+workTable+` SET state = 'cancelled', reason = 'cancelled by operator', updated_at = now()
			 WHERE id = $1 AND state = 'queued' RETURNING `+columns, id))
		if err != nil {
			return err
		}
		cancelled = true
		return addEvent(ctx, tx, it)
	})
	return it, cancelled, err
}

func (p *Postgres) StopUnit(ctx context.Context, unit, operator string, onlyIfOpen *uuid.UUID) (cancelled []Item, stop Item, target Item, stopped bool, err error) {
	body := json.RawMessage(`{"name":"stop"}`)
	if err := validateBody(KindCommand, body); err != nil {
		return nil, Item{}, Item{}, false, err
	}
	err = p.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, unit); err != nil {
			return err
		}
		if onlyIfOpen != nil {
			target, err = scan(tx.QueryRow(ctx,
				`SELECT `+columns+` FROM `+workTable+` WHERE id = $1 AND unit = $2 FOR UPDATE`, *onlyIfOpen, unit))
			if errors.Is(err, pgx.ErrNoRows) {
				var exists bool
				if lookupErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+workTable+` WHERE id = $1)`, *onlyIfOpen).Scan(&exists); lookupErr != nil {
					return lookupErr
				}
				if exists {
					return ErrWrongUnit
				}
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			if terminal(target.State) {
				return nil
			}
		}
		rows, err := tx.Query(ctx,
			`UPDATE `+workTable+` SET state = 'cancelled', updated_at = now()
			 WHERE unit = $1 AND state NOT IN ('done', 'failed', 'cancelled')
			 RETURNING `+columns, unit)
		if err != nil {
			return err
		}
		cancelled, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Item, error) { return scan(r) })
		if err != nil {
			return err
		}
		for _, it := range cancelled {
			if err := addEvent(ctx, tx, it); err != nil {
				return err
			}
			if onlyIfOpen != nil && it.ID == *onlyIfOpen {
				target = it
			}
		}
		stop, err = scan(tx.QueryRow(ctx,
			`INSERT INTO `+workTable+` (id, unit, kind, body, state, operator)
			 VALUES ($1, $2, 'command', $3, 'queued', $4) RETURNING `+columns,
			uuid.New(), unit, []byte(body), operator))
		if err != nil {
			return err
		}
		if err := addEvent(ctx, tx, stop); err != nil {
			return err
		}
		stopped = true
		return nil
	})
	return cancelled, stop, target, stopped, err
}

func (p *Postgres) Recent(ctx context.Context, unit string, n int) ([]Item, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT `+columns+` FROM `+workTable+` WHERE unit = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, unit, n)
	if err != nil {
		return nil, p.wrap(err)
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Item, error) { return scan(r) })
	return items, p.wrap(err)
}

// Prune relies on the event table's ON DELETE CASCADE to drop the trail.
func (p *Postgres) Prune(ctx context.Context, before time.Time) (int, error) {
	tag, err := p.pool.Exec(ctx, `DELETE FROM `+workTable+` WHERE created_at < $1`, before)
	return int(tag.RowsAffected()), p.wrap(err)
}
