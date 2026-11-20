package storagejob

import "time"

// ScanResult is what an output port read when a retrieved container arrived.
type ScanResult string

const (
	ScanRead     ScanResult = "READ"
	ScanNoRead   ScanResult = "NO_READ"
	ScanMismatch ScanResult = "MISMATCH"
)

// ContainerStored: a store's JOB_DONE. The rack position stays inside the context. AcceptedAt
// (the job's acceptance) is not on the wire; it times the job (ADR 0004).
type ContainerStored struct {
	JobID, AsrsID, ContainerID, PortID, Reference string
	At, PLCTime, AcceptedAt                       time.Time
}

// ContainerPresentedAtPort: a retrieve's PORT_ARRIVAL at its output port.
type ContainerPresentedAtPort struct {
	JobID, AsrsID, ContainerID, PortID, Reference string
	Scan                                          ScanResult
	ScannedContainerID                            string
	At, PLCTime, AcceptedAt                       time.Time
}

// StorageJobFailed: an external job failed or was cancelled. PLCTime is zero when the failure
// did not come from a telegram.
type StorageJobFailed struct {
	JobID, AsrsID                  string
	Kind                           Kind
	ContainerID, PortID, Reference string
	Reason                         Reason
	PLCReason                      string
	At, PLCTime, AcceptedAt        time.Time
}
