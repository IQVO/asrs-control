package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/asrs-control/internal/application/ports"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

// StorageJobRepo is the pgx ports.StorageJobs.
type StorageJobRepo struct{ pool *pgxpool.Pool }

func NewStorageJobRepo(pool *pgxpool.Pool) *StorageJobRepo { return &StorageJobRepo{pool: pool} }

const jobColumns = `job_id, seq, asrs_id, aisle, kind, container_id, port_id, reference, parent_job_id, from_pos, to_pos,
	status, reason, plc_reason, telegram_id, cancel_telegram_id, acked, accepted_at, ended_at`

// openExternalIndex is the partial unique index that allows one open external job per container
// of an AS/RS (see the migration); its violation is ports.ErrOpenJobExists.
const openExternalIndex = "uq_storage_jobs_open_external"

const openStatus = `status NOT IN ('DONE', 'FAILED', 'CANCELLED')`

// posArray: positions are bounded by the validated layout (small positive ints,
// revalidated by storagemap.Rehydrate), and the explicit bound check makes the
// narrowing safe even if that invariant ever changes (CodeQL go/incorrect-
// integer-conversion; layout coords cannot approach MaxInt32).
func posArray(p layout.Position) []int32 {
	if p.IsZero() {
		return nil
	}
	for _, v := range []int{p.Aisle, p.Level, p.Slot, p.Depth} {
		if v < 0 || v > math.MaxInt32 {
			return nil // an out-of-range coordinate is not a position; stored as NULL
		}
	}
	return []int32{int32(p.Aisle), int32(p.Level), int32(p.Slot), int32(p.Depth)} //nolint:gosec // bounded above
}

func posFrom(a []int32) layout.Position {
	if len(a) != 4 {
		return layout.Position{}
	}
	return layout.Position{Aisle: int(a[0]), Level: int(a[1]), Slot: int(a[2]), Depth: int(a[3])}
}

func endedAt(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func scanJob(row pgx.Row) (*storagejob.Job, error) {
	var s storagejob.State
	var from, to []int32
	var ended *time.Time
	if err := row.Scan(&s.ID, &s.Seq, &s.AsrsID, &s.Aisle, &s.Kind, &s.ContainerID, &s.PortID, &s.Reference, &s.ParentID,
		&from, &to, &s.Status, &s.Reason, &s.PLCReason, &s.TelegramID, &s.CancelTelegramID, &s.Acked, &s.AcceptedAt, &ended); err != nil {
		return nil, err
	}
	s.From, s.To, s.AcceptedAt = posFrom(from), posFrom(to), s.AcceptedAt.UTC()
	if ended != nil {
		s.EndedAt = ended.UTC()
	}
	return storagejob.Rehydrate(s), nil
}

func (r *StorageJobRepo) Insert(ctx context.Context, j *storagejob.Job) error {
	s := j.State()
	var seq int64
	err := queryFor(ctx, r.pool).QueryRow(ctx, `INSERT INTO storage_jobs (job_id, asrs_id, aisle, kind, container_id, port_id,
		reference, parent_job_id, from_pos, to_pos, status, reason, plc_reason, telegram_id, cancel_telegram_id, acked, accepted_at, ended_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18) RETURNING seq`,
		s.ID, s.AsrsID, s.Aisle, string(s.Kind), s.ContainerID, s.PortID, s.Reference, s.ParentID, posArray(s.From), posArray(s.To),
		string(s.Status), string(s.Reason), s.PLCReason, s.TelegramID, s.CancelTelegramID, s.Acked, s.AcceptedAt.UTC(), endedAt(s.EndedAt)).Scan(&seq)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == openExternalIndex {
		return ports.ErrOpenJobExists
	}
	if err != nil {
		return fmt.Errorf("insert storage job: %w", err)
	}
	j.AssignSeq(seq)
	return nil
}

func (r *StorageJobRepo) Update(ctx context.Context, j *storagejob.Job) error {
	s := j.State()
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `UPDATE storage_jobs SET from_pos = $2, to_pos = $3, status = $4, reason = $5,
		plc_reason = $6, telegram_id = $7, cancel_telegram_id = $8, acked = $9, ended_at = $10 WHERE job_id = $1`,
		s.ID, posArray(s.From), posArray(s.To), string(s.Status), string(s.Reason), s.PLCReason, s.TelegramID, s.CancelTelegramID, s.Acked, endedAt(s.EndedAt))
	if err != nil {
		return fmt.Errorf("update storage job: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ports.ErrJobNotFound
	}
	return nil
}

func (r *StorageJobRepo) one(ctx context.Context, where string, args ...any) (*storagejob.Job, error) {
	j, err := scanJob(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+jobColumns+` FROM storage_jobs WHERE `+where+` ORDER BY seq LIMIT 1`, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrJobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read storage job: %w", err)
	}
	return j, nil
}

func (r *StorageJobRepo) many(ctx context.Context, where string, args ...any) ([]*storagejob.Job, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+jobColumns+` FROM storage_jobs WHERE `+where+` ORDER BY seq`, args...)
	if err != nil {
		return nil, fmt.Errorf("list storage jobs: %w", err)
	}
	defer rows.Close()
	var out []*storagejob.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan storage job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (r *StorageJobRepo) count(ctx context.Context, where string, args ...any) (int, error) {
	var n int
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT count(*) FROM storage_jobs WHERE `+where, args...).Scan(&n)
	return n, err
}

func (r *StorageJobRepo) Get(ctx context.Context, jobID string) (*storagejob.Job, error) {
	return r.one(ctx, `job_id = $1`, jobID)
}

func (r *StorageJobRepo) ByTelegram(ctx context.Context, asrsID, id string) (*storagejob.Job, error) {
	return r.one(ctx, `asrs_id = $1 AND $2 <> '' AND (telegram_id = $2 OR cancel_telegram_id = $2)`, asrsID, id)
}

func (r *StorageJobRepo) Open(ctx context.Context, asrsID string, aisle int) ([]*storagejob.Job, error) {
	return r.many(ctx, `asrs_id = $1 AND aisle = $2 AND `+openStatus, asrsID, aisle)
}

func (r *StorageJobRepo) OldestAwaiting(ctx context.Context, asrsID, portID string) (*storagejob.Job, error) {
	return r.one(ctx, `asrs_id = $1 AND port_id = $2 AND status = 'AWAITING_ARRIVAL'`, asrsID, portID)
}

func (r *StorageJobRepo) HasOpen(ctx context.Context, asrsID, containerID string) (bool, error) {
	n, err := r.count(ctx, `asrs_id = $1 AND container_id = $2 AND kind <> 'RESHUFFLE' AND `+openStatus, asrsID, containerID)
	return n > 0, err
}

func (r *StorageJobRepo) CountOpen(ctx context.Context, asrsID string) (int, error) {
	return r.count(ctx, `asrs_id = $1 AND kind <> 'RESHUFFLE' AND `+openStatus, asrsID)
}

func (r *StorageJobRepo) CountRetrievals(ctx context.Context, asrsID, containerID string, since time.Time) (int, error) {
	return r.count(ctx, `asrs_id = $1 AND container_id = $2 AND kind = 'RETRIEVE' AND status = 'DONE' AND ended_at >= $3`,
		asrsID, containerID, since.UTC())
}

func (r *StorageJobRepo) InFlightCommands(ctx context.Context, asrsID string) ([]*storagejob.Job, error) {
	return r.many(ctx, `asrs_id = $1 AND status IN ('DISPATCHED', 'CANCEL_REQUESTED')`, asrsID)
}
