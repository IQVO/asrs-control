// Package capacity is the AS/RS's capacity fact for material-flow-control's capacity loop
// (message flows, "The capacity loop"): units available, free positions and open jobs.
package capacity

import "time"

// Snapshot is the capacity of one AS/RS.
type Snapshot struct {
	AsrsID                        string
	UnitsTotal, UnitsAvailable    int
	PositionsTotal, PositionsFree int
	OpenJobs                      int
}

// AsrsCapacityChanged is raised whenever the snapshot changes.
type AsrsCapacityChanged struct {
	Snapshot
	At time.Time
}

// Changed returns the event when next differs from prev (or prev is not known yet).
func Changed(prev Snapshot, known bool, next Snapshot, at time.Time) (AsrsCapacityChanged, bool) {
	if known && prev == next {
		return AsrsCapacityChanged{}, false
	}
	return AsrsCapacityChanged{Snapshot: next, At: at}, true
}
