package ledger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jdwillmsen/gameops/minecraft/crew-control/internal/work"
)

const (
	placeTable = "minecraft.crew_place"
	stockTable = "minecraft.crew_stock"

	placeColumns = "id, name, kind, min_x, min_y, min_z, max_x, max_y, max_z, items, seeded"
)

// A database that hangs rather than refusing connections would otherwise
// hold the request, and a pool slot, forever.
var queryTimeout = 5 * time.Second

// Postgres keeps the ledger in the shared database. It borrows the work
// store's pool, so it neither opens nor closes connections.
type Postgres struct{ pool *pgxpool.Pool }

var _ Ledger = (*Postgres)(nil)

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

// wrap turns a database that cannot be reached into ErrUnavailable, so a
// caller answers "try again" rather than "broken".
func wrap(err error) error {
	if err != nil && !errors.Is(err, ErrUnavailable) && work.IsUnavailable(err) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}

func scanPlace(row pgx.Row) (Place, error) {
	var p Place
	err := row.Scan(&p.ID, &p.Name, &p.Kind,
		&p.Box.Min.X, &p.Box.Min.Y, &p.Box.Min.Z, &p.Box.Max.X, &p.Box.Max.Y, &p.Box.Max.Z,
		&p.Items, &p.Seeded)
	return p, err
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

func (p *Postgres) Places(ctx context.Context) ([]Place, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT `+placeColumns+` FROM `+placeTable+` ORDER BY id`)
	if err != nil {
		return nil, wrap(err)
	}
	ps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Place, error) { return scanPlace(r) })
	return ps, wrap(err)
}

func (p *Postgres) Place(ctx context.Context, id string) (Place, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	pl, err := scanPlace(p.pool.QueryRow(ctx, `SELECT `+placeColumns+` FROM `+placeTable+` WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Place{}, false, nil
	}
	return pl, err == nil, wrap(err)
}

func items(p Place) []string {
	if p.Items == nil {
		return []string{}
	}
	return p.Items
}

const upsertPlace = `INSERT INTO ` + placeTable + ` (id, name, kind, min_x, min_y, min_z, max_x, max_y, max_z, items, seeded)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, kind = EXCLUDED.kind,
		min_x = EXCLUDED.min_x, min_y = EXCLUDED.min_y, min_z = EXCLUDED.min_z,
		max_x = EXCLUDED.max_x, max_y = EXCLUDED.max_y, max_z = EXCLUDED.max_z,
		items = EXCLUDED.items, seeded = EXCLUDED.seeded, updated_at = now()`

func upsert(ctx context.Context, tx pgx.Tx, pl Place, seeded bool) error {
	b := pl.Box
	_, err := tx.Exec(ctx, upsertPlace, pl.ID, pl.Name, pl.Kind,
		b.Min.X, b.Min.Y, b.Min.Z, b.Max.X, b.Max.Y, b.Max.Z, items(pl), seeded)
	return err
}

func (p *Postgres) PutPlace(ctx context.Context, pl Place) error {
	if err := Validate(pl); err != nil {
		return err
	}
	return p.inTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		old, err := scanPlace(tx.QueryRow(ctx, `SELECT `+placeColumns+` FROM `+placeTable+` WHERE id = $1 FOR UPDATE`, pl.ID))
		exists := err == nil
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return err
		case old.Seeded:
			return ErrSeeded
		}
		if err := checkAutoID(pl, old, exists); err != nil {
			return err
		}
		if pl.Kind == KindChest {
			var taken bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+placeTable+`
				WHERE kind = 'chest' AND id <> $1 AND min_x = $2 AND min_y = $3 AND min_z = $4)`,
				pl.ID, pl.Box.Min.X, pl.Box.Min.Y, pl.Box.Min.Z).Scan(&taken); err != nil {
				return err
			}
			if taken {
				return ErrConflict
			}
		}
		if exists && moved(old, pl) {
			if _, err := tx.Exec(ctx, `DELETE FROM `+stockTable+` WHERE place_id = $1`, pl.ID); err != nil {
				return err
			}
		}
		return upsert(ctx, tx, pl, false)
	})
}

func (p *Postgres) DeletePlace(ctx context.Context, id string) error {
	return p.inTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var seeded bool
		err := tx.QueryRow(ctx, `SELECT seeded FROM `+placeTable+` WHERE id = $1 FOR UPDATE`, id).Scan(&seeded)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return err
		case seeded:
			return ErrSeeded
		}
		// Stock goes with the place through ON DELETE CASCADE.
		_, err = tx.Exec(ctx, `DELETE FROM `+placeTable+` WHERE id = $1`, id)
		return err
	})
}

func (p *Postgres) Seed(ctx context.Context, ps []Place) error {
	keep := make([]string, 0, len(ps))
	for _, pl := range ps {
		if err := validSeed(pl); err != nil {
			return err
		}
		keep = append(keep, pl.ID)
	}
	if err := positionsFree(ps); err != nil {
		return err
	}
	return p.inTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM `+placeTable+` WHERE seeded AND NOT (id = ANY($1))`, keep); err != nil {
			return err
		}
		for _, pl := range ps {
			old, err := scanPlace(tx.QueryRow(ctx, `SELECT `+placeColumns+` FROM `+placeTable+` WHERE id = $1 FOR UPDATE`, pl.ID))
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				return err
			case moved(old, pl):
				if _, err := tx.Exec(ctx, `DELETE FROM `+stockTable+` WHERE place_id = $1`, pl.ID); err != nil {
					return err
				}
			}
			if err := upsert(ctx, tx, pl, true); err != nil {
				return err
			}
			if pl.Kind == KindChest {
				if err := fold(ctx, tx, pl); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// fold merges unseeded chests at a seeded chest's position into it: the
// stock moves over when it is newer than what the seeded chest holds.
func fold(ctx context.Context, tx pgx.Tx, seeded Place) error {
	b := seeded.Box.Min
	rows, err := tx.Query(ctx, `SELECT id FROM `+placeTable+`
		WHERE kind = 'chest' AND NOT seeded AND id <> $1 AND min_x = $2 AND min_y = $3 AND min_z = $4`,
		seeded.ID, b.X, b.Y, b.Z)
	if err != nil {
		return err
	}
	dups, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range dups {
		var newer bool
		if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT max(seen_at) FROM `+stockTable+` WHERE place_id = $1), '-infinity')
			> COALESCE((SELECT max(seen_at) FROM `+stockTable+` WHERE place_id = $2), '-infinity')`,
			id, seeded.ID).Scan(&newer); err != nil {
			return err
		}
		if newer {
			if _, err := tx.Exec(ctx, `DELETE FROM `+stockTable+` WHERE place_id = $1`, seeded.ID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE `+stockTable+` SET place_id = $2 WHERE place_id = $1`, id, seeded.ID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM `+placeTable+` WHERE id = $1`, id); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) chestAt(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, pos Pos) (Place, bool, error) {
	pl, err := scanPlace(q.QueryRow(ctx, `SELECT `+placeColumns+` FROM `+placeTable+`
		WHERE kind = 'chest' AND min_x = $1 AND min_y = $2 AND min_z = $3 ORDER BY seeded DESC, id LIMIT 1`,
		pos.X, pos.Y, pos.Z))
	if errors.Is(err, pgx.ErrNoRows) {
		return Place{}, false, nil
	}
	return pl, err == nil, wrap(err)
}

func (p *Postgres) ChestAt(ctx context.Context, pos Pos) (Place, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	return p.chestAt(ctx, p.pool, pos)
}

func (p *Postgres) Observe(ctx context.Context, unit string, at time.Time, chest Pos, observed map[string]int, partial bool) error {
	if !validPos(chest) {
		return invalid("chest is outside the world")
	}
	observed = cleanObservation(observed)
	if now := time.Now(); at.After(now) {
		at = now
	}
	names := make([]string, 0, len(observed))
	counts := make([]int32, 0, len(observed))
	for k, v := range observed {
		names = append(names, k)
		counts = append(counts, int32(min(v, 1<<30)))
	}
	return p.inTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		pl, ok, err := p.chestAt(ctx, tx, chest)
		if err != nil {
			return err
		}
		if !ok {
			pl = autoChest(chest)
			// Two units can find the same new chest at once; the loser's
			// insert is a no-op and both then write stock to the same row.
			if _, err := tx.Exec(ctx, `INSERT INTO `+placeTable+` (id, name, kind, min_x, min_y, min_z, max_x, max_y, max_z)
				VALUES ($1, $2, $3, $4, $5, $6, $4, $5, $6) ON CONFLICT (id) DO NOTHING`,
				pl.ID, pl.Name, pl.Kind, chest.X, chest.Y, chest.Z); err != nil {
				return err
			}
		}
		// The place row serialises observations of one chest, so the
		// newer-than check below cannot race another writer.
		var locked int
		switch err := tx.QueryRow(ctx, `SELECT 1 FROM `+placeTable+` WHERE id = $1 FOR UPDATE`, pl.ID).Scan(&locked); {
		case errors.Is(err, pgx.ErrNoRows):
			// Deleted since the lookup: nothing left to record against.
			return nil
		case err != nil:
			return err
		}
		var newer bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM `+stockTable+` WHERE place_id = $1 AND seen_at > $2)`,
			pl.ID, at).Scan(&newer); err != nil {
			return err
		}
		if newer {
			return nil
		}
		// Rows for items no longer in the chest stay, at zero, so a planner
		// can tell "none left" from "never looked".
		if !partial {
			if _, err := tx.Exec(ctx, `UPDATE `+stockTable+` SET count = 0, seen_at = $3, unit = $4
				WHERE place_id = $1 AND NOT (item = ANY($2))`, pl.ID, names, at, unit); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO `+stockTable+` (place_id, item, count, seen_at, unit)
			SELECT $1, n, c, $4, $5 FROM unnest($2::text[], $3::int[]) AS t(n, c)
			ON CONFLICT (place_id, item) DO UPDATE SET count = EXCLUDED.count, seen_at = EXCLUDED.seen_at, unit = EXCLUDED.unit`,
			pl.ID, names, counts, at, unit)
		return err
	})
}

func (p *Postgres) Stock(ctx context.Context, placeID string) ([]Stock, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := p.pool.Query(ctx,
		`SELECT item, count, seen_at, unit FROM `+stockTable+` WHERE place_id = $1 ORDER BY item`, placeID)
	if err != nil {
		return nil, wrap(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Stock, error) {
		var s Stock
		err := r.Scan(&s.Item, &s.Count, &s.SeenAt, &s.Unit)
		return s, err
	})
	return out, wrap(err)
}
