package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

const (
	jobTable  = "minecraft.crew_job"
	stepTable = "minecraft.crew_job_step"

	jobColumns  = "id, kind, spec, state, reason, operator, round, deadline, created_at, updated_at"
	stepColumns = "id, key, unit, template, depends_on, state, attempts, work_id, reason"
)

// A database that hangs rather than refusing connections would otherwise
// hold the scheduler, and a pool slot, forever.
var queryTimeout = 5 * time.Second

// Postgres keeps jobs in the shared database. It borrows the work store's
// pool, so it neither opens nor closes connections.
type Postgres struct{ pool *pgxpool.Pool }

var _ Store = (*Postgres)(nil)

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

// wrap turns a database that cannot be reached into work.ErrUnavailable, so
// the scheduler waits instead of reporting a broken job.
func wrap(err error) error {
	if err != nil && !errors.Is(err, work.ErrUnavailable) && work.IsUnavailable(err) {
		return fmt.Errorf("%w: %v", work.ErrUnavailable, err)
	}
	return err
}

func (p *Postgres) inTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return wrap(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx); err != nil {
		return wrap(err)
	}
	return wrap(tx.Commit(ctx))
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Kind, &j.Spec, &j.State, &j.Reason, &j.Operator, &j.Round, &j.Deadline, &j.Created, &j.Updated)
	return j, err
}

func loadSteps(ctx context.Context, q querier, job uuid.UUID) ([]Step, error) {
	// The table has no sequence, so key order is the only stable one; nothing
	// depends on the order steps were given in.
	rows, err := q.Query(ctx, `SELECT `+stepColumns+` FROM `+stepTable+` WHERE job_id = $1 ORDER BY key`, job)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Step, error) {
		var (
			s    Step
			unit *string
		)
		err := r.Scan(&s.ID, &s.Key, &unit, &s.Template, &s.DependsOn, &s.State, &s.Attempts, &s.WorkID, &s.Reason)
		if unit != nil {
			s.Unit = *unit
		}
		return s, err
	})
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (p *Postgres) Create(ctx context.Context, j Job) (Job, error) {
	j, err := prepare(j)
	if err != nil {
		return Job{}, err
	}
	err = p.inTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `INSERT INTO `+jobTable+` (id, kind, spec, state, reason, operator, round, deadline)
			VALUES ($1, $2, $3, $4, '', $5, 0, $6) RETURNING created_at, updated_at`,
			j.ID, j.Kind, j.Spec, j.State, j.Operator, j.Deadline)
		if err := row.Scan(&j.Created, &j.Updated); err != nil {
			return err
		}
		for _, s := range j.Steps {
			if err := insertStep(ctx, tx, j.ID, s); err != nil {
				return err
			}
		}
		return nil
	})
	return j, err
}

func insertStep(ctx context.Context, tx pgx.Tx, job uuid.UUID, s Step) error {
	_, err := tx.Exec(ctx, `INSERT INTO `+stepTable+` (id, job_id, key, unit, template, depends_on, state, attempts, work_id, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		s.ID, job, s.Key, nilIfEmpty(s.Unit), s.Template, nonNil(s.DependsOn), s.State, s.Attempts, s.WorkID, s.Reason)
	return err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (p *Postgres) Running(ctx context.Context) ([]uuid.UUID, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT id FROM `+jobTable+` WHERE state = 'running' ORDER BY created_at, id`)
	if err != nil {
		return nil, wrap(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	return ids, wrap(err)
}

func (p *Postgres) Lock(ctx context.Context, id uuid.UUID, fn func(*Job) error) (bool, error) {
	ok := false
	err := p.inTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		j, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM `+jobTable+` WHERE id = $1 FOR UPDATE SKIP LOCKED`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			// Skipped rows and missing rows look alike; tell them apart.
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+jobTable+` WHERE id = $1)`, id).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
			return nil
		}
		if err != nil {
			return err
		}
		ok = true
		if j.Steps, err = loadSteps(ctx, tx, id); err != nil {
			return err
		}
		known := map[uuid.UUID]bool{}
		for _, s := range j.Steps {
			known[s.ID] = true
		}
		if err := fn(&j); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE `+jobTable+` SET state = $2, reason = $3, round = $4, updated_at = now() WHERE id = $1`,
			id, j.State, j.Reason, j.Round); err != nil {
			return err
		}
		for _, s := range j.Steps {
			if !known[s.ID] {
				if s.ID == uuid.Nil {
					return errors.New("jobs: appended step has no id")
				}
				if err := insertStep(ctx, tx, id, s); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE `+stepTable+` SET state = $2, attempts = $3, work_id = $4, reason = $5 WHERE id = $1`,
				s.ID, s.State, s.Attempts, s.WorkID, s.Reason); err != nil {
				return err
			}
		}
		return nil
	})
	return ok, err
}

func (p *Postgres) Get(ctx context.Context, id uuid.UUID) (Job, error) {
	var j Job
	err := p.inTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		j, err = scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM `+jobTable+` WHERE id = $1`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		j.Steps, err = loadSteps(ctx, tx, id)
		return err
	})
	return j, err
}

func (p *Postgres) List(ctx context.Context, n int) ([]Job, error) {
	var out []Job
	err := p.inTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+jobColumns+` FROM `+jobTable+` ORDER BY created_at DESC, id LIMIT $1`, n)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Job, error) { return scanJob(r) })
		if err != nil {
			return err
		}
		for i := range out {
			if out[i].Steps, err = loadSteps(ctx, tx, out[i].ID); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}
