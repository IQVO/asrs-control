package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/layout"
)

// UnitRepo is the pgx ports.Units.
type UnitRepo struct{ pool *pgxpool.Pool }

func NewUnitRepo(pool *pgxpool.Pool) *UnitRepo { return &UnitRepo{pool: pool} }

func (r *UnitRepo) Ensure(ctx context.Context, l layout.Layout, at time.Time) error {
	for n := 1; n <= l.Aisles; n++ {
		if _, err := r.pool.Exec(ctx, `INSERT INTO equipment_units (asrs_id, aisle, unit_id, state, since) VALUES ($1, $2, $3, 'IDLE', $4)
			ON CONFLICT (asrs_id, aisle) DO NOTHING`, l.ID, n, l.UnitID(n), at.UTC()); err != nil {
			return fmt.Errorf("ensure unit %s aisle %d: %w", l.ID, n, err)
		}
	}
	return nil
}

const unitColumns = `asrs_id, unit_id, aisle, state, current_job, since`

func scanUnit(row pgx.Row) (*equipment.Unit, error) {
	var s equipment.UnitState
	if err := row.Scan(&s.AsrsID, &s.UnitID, &s.Aisle, &s.State, &s.CurrentJob, &s.Since); err != nil {
		return nil, err
	}
	s.Since = s.Since.UTC()
	return equipment.Rehydrate(s), nil
}

func (r *UnitRepo) Lock(ctx context.Context, asrsID string, aisle int) (*equipment.Unit, error) {
	u, err := scanUnit(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+unitColumns+` FROM equipment_units WHERE asrs_id = $1 AND aisle = $2 FOR UPDATE`, asrsID, aisle))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("unit of %s aisle %d does not exist (boot runs Ensure)", asrsID, aisle)
	}
	return u, err
}

func (r *UnitRepo) List(ctx context.Context, asrsID string) ([]*equipment.Unit, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+unitColumns+` FROM equipment_units WHERE asrs_id = $1 ORDER BY aisle`, asrsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*equipment.Unit
	for rows.Next() {
		u, err := scanUnit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *UnitRepo) Save(ctx context.Context, u *equipment.Unit) error {
	s := u.Snapshot()
	_, err := queryFor(ctx, r.pool).Exec(ctx, `UPDATE equipment_units SET state = $3, current_job = $4, since = $5 WHERE asrs_id = $1 AND aisle = $2`,
		s.AsrsID, s.Aisle, string(s.State), s.CurrentJob, s.Since.UTC())
	return err
}

// CapacityRepo is the pgx ports.Capacities.
type CapacityRepo struct{ pool *pgxpool.Pool }

func NewCapacityRepo(pool *pgxpool.Pool) *CapacityRepo { return &CapacityRepo{pool: pool} }

func (r *CapacityRepo) Lock(ctx context.Context, asrsID string) (capacity.Snapshot, bool, error) {
	s := capacity.Snapshot{AsrsID: asrsID}
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT units_total, units_available, positions_total, positions_free, open_jobs
		FROM asrs_capacity WHERE asrs_id = $1 FOR UPDATE`, asrsID).Scan(&s.UnitsTotal, &s.UnitsAvailable, &s.PositionsTotal, &s.PositionsFree, &s.OpenJobs)
	if errors.Is(err, pgx.ErrNoRows) {
		return capacity.Snapshot{}, false, nil
	}
	return s, err == nil, err
}

func (r *CapacityRepo) Save(ctx context.Context, s capacity.Snapshot) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO asrs_capacity (asrs_id, units_total, units_available, positions_total, positions_free, open_jobs)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (asrs_id) DO UPDATE SET units_total = $2, units_available = $3,
		positions_total = $4, positions_free = $5, open_jobs = $6, updated_at = now()`,
		s.AsrsID, s.UnitsTotal, s.UnitsAvailable, s.PositionsTotal, s.PositionsFree, s.OpenJobs)
	return err
}
