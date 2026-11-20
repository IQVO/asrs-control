package ports

import (
	"context"
	"time"

	"github.com/IQVO/asrs-control/internal/application/telegrams"
)

// Downlink delivers commands to one AS/RS's PLC. The use cases call it only from a
// UnitOfWork.AfterCommit hook, so nothing is sent for a unit of work that rolls back. Delivery
// is the adapter's job (track, publish, retransmit); Send does not fail.
type Downlink interface {
	Send(ctx context.Context, asrsID string, cmds ...telegrams.Command)
}

// ProcessedTelegrams is the up-telegram inbox: Claim records telegramID with the ACK id inside
// the effect's unit of work, or returns the ACK id recorded the first time.
type ProcessedTelegrams interface {
	Claim(ctx context.Context, asrsID, telegramID, ackID string) (string, bool, error)
	Purge(ctx context.Context, before time.Time) (int64, error)
}
