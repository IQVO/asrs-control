package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/asrs-control/internal/application/idempotency"
)

// TelegramInbox is the pgx ports.ProcessedTelegrams.
type TelegramInbox struct{ pool *pgxpool.Pool }

func NewTelegramInbox(pool *pgxpool.Pool) *TelegramInbox { return &TelegramInbox{pool: pool} }

// Claim records the telegram with ackID, or returns the ACK id recorded first. Inside a unit
// of work the row commits or rolls back with the telegram's effect.
func (r *TelegramInbox) Claim(ctx context.Context, asrsID, telegramID, ackID string) (string, bool, error) {
	q := queryFor(ctx, r.pool)
	tag, err := q.Exec(ctx, `INSERT INTO processed_telegrams (asrs_id, telegram_id, ack_telegram_id) VALUES ($1, $2, $3)
		ON CONFLICT (asrs_id, telegram_id) DO NOTHING`, asrsID, telegramID, ackID)
	if err != nil {
		return "", false, fmt.Errorf("claim telegram: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return ackID, true, nil
	}
	var first string
	if err := q.QueryRow(ctx, `SELECT ack_telegram_id FROM processed_telegrams WHERE asrs_id = $1 AND telegram_id = $2`,
		asrsID, telegramID).Scan(&first); err != nil {
		return "", false, fmt.Errorf("read claimed telegram: %w", err)
	}
	return first, false, nil
}

func (r *TelegramInbox) Purge(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM processed_telegrams WHERE processed_at < $1`, before.UTC())
	return tag.RowsAffected(), err
}

// IdempotencyStore is the pgx ports.IdempotencyStore (24-hour window enforced by notBefore).
type IdempotencyStore struct{ pool *pgxpool.Pool }

func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore { return &IdempotencyStore{pool: pool} }

func (s *IdempotencyStore) Lookup(ctx context.Context, key string, notBefore time.Time) (idempotency.IdempotencyRecord, bool, error) {
	rec := idempotency.IdempotencyRecord{Key: key}
	err := queryFor(ctx, s.pool).QueryRow(ctx, `SELECT fingerprint, status, content_type, body, created_at
		FROM idempotency_keys WHERE key = $1 AND created_at >= $2`, key, notBefore.UTC()).
		Scan(&rec.Fingerprint, &rec.Status, &rec.ContentType, &rec.Body, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return idempotency.IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return idempotency.IdempotencyRecord{}, false, fmt.Errorf("lookup idempotency key: %w", err)
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return rec, true, nil
}

// Save keeps the first writer of a key; a row older than notBefore is replaced.
func (s *IdempotencyStore) Save(ctx context.Context, rec idempotency.IdempotencyRecord, notBefore time.Time) error {
	body := rec.Body
	if body == nil {
		body = []byte{}
	}
	_, err := queryFor(ctx, s.pool).Exec(ctx, `INSERT INTO idempotency_keys (key, fingerprint, status, content_type, body, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (key) DO UPDATE SET fingerprint = EXCLUDED.fingerprint, status = EXCLUDED.status,
			content_type = EXCLUDED.content_type, body = EXCLUDED.body, created_at = EXCLUDED.created_at
		WHERE idempotency_keys.created_at < $7`,
		rec.Key, rec.Fingerprint, rec.Status, rec.ContentType, body, rec.CreatedAt.UTC(), notBefore.UTC())
	if err != nil {
		return fmt.Errorf("save idempotency key: %w", err)
	}
	return nil
}

func (s *IdempotencyStore) Purge(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE created_at < $1`, before.UTC())
	return tag.RowsAffected(), err
}
