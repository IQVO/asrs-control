package usecases

import (
	"context"

	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

// Page bounds of ListJobs.
const (
	DefaultJobPageLimit = 20
	MaxJobPageLimit     = 100
)

// ListJobsRequest pages an AS/RS's external jobs (STORE, RETRIEVE; never a reshuffle), newest
// first. Status filters when set; BeforeSeq is the keyset cursor (the previous page's
// NextBeforeSeq, 0 for the first page); Limit 0 means DefaultJobPageLimit.
type ListJobsRequest struct {
	AsrsID    string
	Status    storagejob.Status
	BeforeSeq int64
	Limit     int
}

// JobPage is one page; NextBeforeSeq is 0 when there is no further page.
type JobPage struct {
	Jobs          []*storagejob.Job
	NextBeforeSeq int64
}

// jobStatuses are the statuses ListJobs accepts as a filter.
var jobStatuses = map[storagejob.Status]bool{storagejob.Queued: true, storagejob.Dispatched: true,
	storagejob.Acknowledged: true, storagejob.CancelRequested: true, storagejob.AwaitingArrival: true,
	storagejob.Done: true, storagejob.Failed: true, storagejob.Cancelled: true}

// ListJobs reads one page of the AS/RS's external jobs (a read; it opens no unit of work).
func (s *Service) ListJobs(ctx context.Context, r ListJobsRequest) (JobPage, error) {
	l, err := s.Layout(r.AsrsID)
	if err != nil {
		return JobPage{}, err
	}
	if r.Limit < 0 || r.Limit > MaxJobPageLimit || r.BeforeSeq < 0 || (r.Status != "" && !jobStatuses[r.Status]) {
		return JobPage{}, ErrInvalidRequest
	}
	limit := r.Limit
	if limit == 0 {
		limit = DefaultJobPageLimit
	}
	js, err := s.d.Jobs.ListExternal(ctx, l.ID, r.Status, r.BeforeSeq, limit+1)
	if err != nil {
		return JobPage{}, err
	}
	page := JobPage{Jobs: js[:min(len(js), limit)]}
	if len(js) > limit {
		page.NextBeforeSeq = page.Jobs[limit-1].Seq()
	}
	return page, nil
}

// AsrsIDs lists the configured AS/RS ids in layout order.
func (s *Service) AsrsIDs() []string {
	out := make([]string, 0, len(s.d.Layouts))
	for _, l := range s.d.Layouts {
		out = append(out, l.ID)
	}
	return out
}

// Capacity reads the AS/RS's current capacity snapshot (what AsrsCapacityChanged publishes).
func (s *Service) Capacity(ctx context.Context, asrsID string) (capacity.Snapshot, error) {
	l, err := s.Layout(asrsID)
	if err != nil {
		return capacity.Snapshot{}, err
	}
	return s.capacity(ctx, l)
}
