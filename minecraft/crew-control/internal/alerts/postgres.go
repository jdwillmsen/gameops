package alerts

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const alertTable = "minecraft.crew_alert"

// Postgres keeps the alert snapshot in the shared database. It borrows the
// work store's pool, so it neither opens nor closes connections.
type Postgres struct{ pool *pgxpool.Pool }

var _ Store = (*Postgres)(nil)

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

func (p *Postgres) Load(ctx context.Context) ([]Alert, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, rule, unit, name, severity, state, text, since, resolved_at, acked, muted_until
		FROM `+alertTable)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Alert, error) {
		var a Alert
		err := r.Scan(&a.ID, &a.Rule, &a.Unit, &a.Name, &a.Severity, &a.State, &a.Text, &a.Since, &a.ResolvedAt, &a.Acked, &a.MutedUntil)
		return a, err
	})
}

// Save makes the table equal to alerts in one transaction, so a reader never
// sees a half-written list and an alert that left the engine's list leaves
// the table too.
func (p *Postgres) Save(ctx context.Context, alerts []Alert) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ids := make([]string, 0, len(alerts))
	batch := &pgx.Batch{}
	for _, a := range alerts {
		ids = append(ids, a.ID)
		batch.Queue(`INSERT INTO `+alertTable+` (id, rule, unit, name, severity, state, text, since, resolved_at, acked, muted_until, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
			ON CONFLICT (id) DO UPDATE SET rule = EXCLUDED.rule, unit = EXCLUDED.unit, name = EXCLUDED.name,
				severity = EXCLUDED.severity, state = EXCLUDED.state, text = EXCLUDED.text, since = EXCLUDED.since,
				resolved_at = EXCLUDED.resolved_at, acked = EXCLUDED.acked, muted_until = EXCLUDED.muted_until,
				updated_at = now()`,
			a.ID, a.Rule, a.Unit, a.Name, a.Severity, a.State, a.Text, a.Since, a.ResolvedAt, a.Acked, a.MutedUntil)
	}
	batch.Queue(`DELETE FROM `+alertTable+` WHERE NOT (id = ANY($1))`, ids)
	res := tx.SendBatch(ctx, batch)
	for range batch.Len() {
		if _, err := res.Exec(); err != nil {
			_ = res.Close()
			return err
		}
	}
	if err := res.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
