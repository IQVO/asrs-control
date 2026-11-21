package storagejob

import (
	"time"

	"github.com/IQVO/asrs-control/internal/domain/layout"
)

// Reason is why a job failed.
type Reason string

const (
	ReasonCancelled            Reason = "CANCELLED"
	ReasonEquipmentFault       Reason = "EQUIPMENT_FAULT"
	ReasonEquipmentUnreachable Reason = "EQUIPMENT_UNREACHABLE"
	ReasonCommandRefused       Reason = "COMMAND_REFUSED"
	ReasonContainerNotAtPort   Reason = "CONTAINER_NOT_AT_PORT"
	ReasonStorageMapMismatch   Reason = "STORAGE_MAP_MISMATCH"
	ReasonPortOccupied         Reason = "PORT_OCCUPIED"
	ReasonReshuffleFailed      Reason = "RESHUFFLE_FAILED"
	ReasonNoReshufflePosition  Reason = "NO_RESHUFFLE_POSITION"
)

// ReasonForPLC maps a JOB_FAILED reason code of telegram protocol v1 to a job reason.
func ReasonForPLC(code string) Reason {
	switch code {
	case "CONTAINER_NOT_AT_PORT":
		return ReasonContainerNotAtPort
	case "CONTAINER_NOT_AT_POSITION", "POSITION_OCCUPIED", "POSITION_BLOCKED":
		return ReasonStorageMapMismatch
	case "PORT_OCCUPIED":
		return ReasonPortOccupied
	default:
		return ReasonEquipmentFault
	}
}

// Failure is how a job fails.
type Failure struct {
	Reason      Reason
	PLCReason   string
	At, PLCTime time.Time
}

// Locate sets the rack position a queued retrieve takes its container from.
func (j *Job) Locate(from layout.Position) error {
	if j.s.Kind != Retrieve || j.s.Status != Queued {
		return ErrInvalidTransition
	}
	j.s.From = from
	return nil
}

// Dispatch records that the job's command telegram goes to the PLC.
func (j *Job) Dispatch(telegramID string) error {
	if j.s.Status != Queued || telegramID == "" || (j.s.Kind == Retrieve && j.s.From.IsZero()) {
		return ErrInvalidTransition
	}
	j.s.Status, j.s.TelegramID = Dispatched, telegramID
	return nil
}

// Acknowledge records the PLC's accepting ACK of the command; during a cancel request it only
// records the ACK, so a refused CANCEL can return to the right status.
func (j *Job) Acknowledge() error {
	switch j.s.Status {
	case Dispatched:
		j.s.Status, j.s.Acked = Acknowledged, true
		return nil
	case Acknowledged, CancelRequested:
		j.s.Acked = true
		return nil
	default:
		return ErrInvalidTransition
	}
}

// CompleteMotion records JOB_DONE. A store ends DONE with ContainerStored, a reshuffle ends DONE
// silently, a retrieve waits for its PORT_ARRIVAL.
func (j *Job) CompleteMotion(at, plcTime time.Time) ([]any, error) {
	if !j.InFlight() {
		return nil, ErrInvalidTransition
	}
	switch j.s.Kind {
	case Retrieve:
		j.s.Status = AwaitingArrival
		return nil, nil
	case Store:
		j.end(Done, "", "", at)
		return []any{ContainerStored{JobID: j.s.ID, AsrsID: j.s.AsrsID, ContainerID: j.s.ContainerID, PortID: j.s.PortID,
			Reference: j.s.Reference, At: at, PLCTime: plcTime, AcceptedAt: j.s.AcceptedAt}}, nil
	default:
		j.end(Done, "", "", at)
		return nil, nil
	}
}

// Present records the PORT_ARRIVAL of a retrieve; scanned is what the port read ("" = no-read).
func (j *Job) Present(scanned string, at, plcTime time.Time) ([]any, error) {
	if j.s.Status != AwaitingArrival {
		return nil, ErrInvalidTransition
	}
	j.end(Done, "", "", at)
	return []any{ContainerPresentedAtPort{JobID: j.s.ID, AsrsID: j.s.AsrsID, ContainerID: j.s.ContainerID, PortID: j.s.PortID,
		Reference: j.s.Reference, Scan: j.scanOf(scanned), ScannedContainerID: scanned, At: at, PLCTime: plcTime, AcceptedAt: j.s.AcceptedAt}}, nil
}

// scanOf classifies what the output port read. An if-chain, not a tagless switch: Go's cover
// profile does not instrument `case` expressions, so gremlins would report them NOT COVERED.
func (j *Job) scanOf(scanned string) ScanResult {
	if scanned == "" {
		return ScanNoRead
	}
	if scanned != j.s.ContainerID {
		return ScanMismatch
	}
	return ScanRead
}

// Fail ends an open job with f.
func (j *Job) Fail(f Failure) ([]any, error) {
	if !j.Open() {
		return nil, ErrJobEnded
	}
	j.end(Failed, f.Reason, f.PLCReason, f.At)
	return j.failedEvent(f.PLCTime), nil
}

// RequestCancel cancels a queued job at once (immediate) or asks the PLC with cancelTelegramID.
// A repeated request while one is pending keeps the first.
func (j *Job) RequestCancel(cancelTelegramID string, at time.Time) (bool, []any, error) {
	switch j.s.Status {
	case Queued:
		j.end(Cancelled, ReasonCancelled, "", at)
		return true, j.failedEvent(time.Time{}), nil
	case Dispatched, Acknowledged:
		if cancelTelegramID == "" {
			return false, nil, ErrInvalidTransition
		}
		j.s.Status, j.s.CancelTelegramID = CancelRequested, cancelTelegramID
		return false, nil, nil
	case CancelRequested:
		return false, nil, nil
	case AwaitingArrival:
		return false, nil, ErrNotCancellable
	default:
		return false, nil, ErrJobEnded
	}
}

// ConfirmCancel records the PLC's accepting ACK of the CANCEL.
func (j *Job) ConfirmCancel(at time.Time) ([]any, error) {
	if j.s.Status != CancelRequested {
		return nil, ErrInvalidTransition
	}
	j.end(Cancelled, ReasonCancelled, "", at)
	return j.failedEvent(time.Time{}), nil
}

// RevertCancel records a refused or unanswered CANCEL: the job goes on, ACKNOWLEDGED when its
// command was acknowledged, else back to DISPATCHED so that command's own ACK or expiry still
// decides it (a cancel must never leave a never-acknowledged command without an outcome).
func (j *Job) RevertCancel() error {
	if j.s.Status != CancelRequested {
		return ErrInvalidTransition
	}
	j.s.Status, j.s.CancelTelegramID = Dispatched, ""
	if j.s.Acked {
		j.s.Status = Acknowledged
	}
	return nil
}

func (j *Job) end(s Status, r Reason, plcReason string, at time.Time) {
	j.s.Status, j.s.Reason, j.s.PLCReason, j.s.EndedAt = s, r, plcReason, at
}

func (j *Job) failedEvent(plcTime time.Time) []any {
	if !j.External() {
		return nil
	}
	return []any{StorageJobFailed{JobID: j.s.ID, AsrsID: j.s.AsrsID, Kind: j.s.Kind, ContainerID: j.s.ContainerID,
		PortID: j.s.PortID, Reference: j.s.Reference, Reason: j.s.Reason, PLCReason: j.s.PLCReason, At: j.s.EndedAt, PLCTime: plcTime,
		AcceptedAt: j.s.AcceptedAt}}
}
