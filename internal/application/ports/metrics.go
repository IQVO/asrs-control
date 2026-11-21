package ports

import (
	"context"
	"time"
)

// JobMetrics is the business-metrics port of the storage jobs (ADR 0004): every external job
// (STORE or RETRIEVE; never an internal reshuffle) that was accepted, and every one that ended.
// The use cases call it only after the outermost unit of work committed, so a rolled-back
// request or a refusal counts nothing. kind is the job kind (STORE, RETRIEVE); outcome is
// "completed" (a store's JOB_DONE, a retrieve's PORT_ARRIVAL) or "failed"; reason is the
// StorageJobFailed reason (a bounded catalogue: EQUIPMENT_FAULT, CANCELLED, ...), empty when
// completed; took is acceptance to end on asrs-control's clock. A nil port records nothing.
type JobMetrics interface {
	JobAccepted(ctx context.Context, kind string)
	JobEnded(ctx context.Context, kind, outcome, reason string, took time.Duration)
}
