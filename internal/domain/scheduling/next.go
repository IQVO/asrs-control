// Package scheduling decides what one aisle's unit does next (ADR 0002). It is a pure
// function of the aisle's StorageMap, its EquipmentUnit and its open jobs.
package scheduling

import (
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

type Action string

const (
	None      Action = ""
	Dispatch  Action = "DISPATCH"
	Reshuffle Action = "RESHUFFLE"
	Fail      Action = "FAIL"
)

// Decision is the scheduler's answer. Blocked is set for Reshuffle, Reason for Fail.
type Decision struct {
	Action  Action
	Job     *storagejob.Job
	Blocked layout.Position
	Reason  storagejob.Reason
}

// Next picks the aisle's next action. open holds the aisle's open jobs in acceptance order.
func Next(m *storagemap.Map, u *equipment.Unit, open []*storagejob.Job) Decision {
	if u.Busy() || !u.Available() {
		return Decision{}
	}
	for _, j := range open {
		if j.Status() == storagejob.Queued && j.Kind() == storagejob.Reshuffle {
			return Decision{Action: Dispatch, Job: j}
		}
	}
	held := map[string]bool{} // output ports a retrieve AWAITING_ARRIVAL still holds (invariant 4)
	for _, j := range open {
		if j.Status() == storagejob.AwaitingArrival {
			held[j.PortID()] = true
		}
	}
	for _, j := range open {
		if j.Status() != storagejob.Queued {
			continue
		}
		if d, ok := consider(m, j, held); ok {
			return d
		}
	}
	return Decision{}
}

func consider(m *storagemap.Map, j *storagejob.Job, held map[string]bool) (Decision, bool) {
	if j.Kind() != storagejob.Retrieve {
		return Decision{Action: Dispatch, Job: j}, true
	}
	port := j.PortID()
	if held[port] || !m.PortFree(port) { // a later retrieve to this port sees the same answer
		return Decision{}, false
	}
	pos, ok := m.Position(j.ContainerID())
	if !ok {
		return Decision{Action: Fail, Job: j, Reason: storagejob.ReasonStorageMapMismatch}, true
	}
	if _, blocked := m.Blocker(pos); blocked {
		return Decision{Action: Reshuffle, Job: j, Blocked: pos}, true
	}
	return Decision{Action: Dispatch, Job: j}, true
}
