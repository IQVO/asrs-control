package events

import (
	"fmt"
	"time"

	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

// Payloads (apis/asyncapi.yaml). *_at is asrs-control's clock; plc_timestamp is the PLC
// telegram's simulated time, omitted when the fact did not come from a telegram.

type containerStored struct {
	StorageJobID string     `json:"storage_job_id"`
	AsrsID       string     `json:"asrs_id"`
	ContainerID  string     `json:"container_id"`
	PortID       string     `json:"port_id"`
	Reference    string     `json:"reference,omitempty"`
	StoredAt     time.Time  `json:"stored_at"`
	PLCTimestamp *time.Time `json:"plc_timestamp,omitempty"`
}

type containerPresented struct {
	StorageJobID       string     `json:"storage_job_id"`
	AsrsID             string     `json:"asrs_id"`
	ContainerID        string     `json:"container_id"`
	PortID             string     `json:"port_id"`
	Reference          string     `json:"reference,omitempty"`
	ScanResult         string     `json:"scan_result"`
	ScannedContainerID string     `json:"scanned_container_id,omitempty"`
	PresentedAt        time.Time  `json:"presented_at"`
	PLCTimestamp       *time.Time `json:"plc_timestamp,omitempty"`
}

type storageJobFailed struct {
	StorageJobID string     `json:"storage_job_id"`
	AsrsID       string     `json:"asrs_id"`
	JobKind      string     `json:"job_kind"`
	ContainerID  string     `json:"container_id"`
	PortID       string     `json:"port_id"`
	Reference    string     `json:"reference,omitempty"`
	Reason       string     `json:"reason"`
	PLCReason    string     `json:"plc_reason,omitempty"`
	FailedAt     time.Time  `json:"failed_at"`
	PLCTimestamp *time.Time `json:"plc_timestamp,omitempty"`
}

type equipmentStateChanged struct {
	AsrsID        string     `json:"asrs_id"`
	UnitID        string     `json:"unit_id"`
	Aisle         int        `json:"aisle"`
	State         string     `json:"state"`
	PreviousState string     `json:"previous_state"`
	ChangedAt     time.Time  `json:"changed_at"`
	PLCTimestamp  *time.Time `json:"plc_timestamp,omitempty"`
}

type asrsCapacityChanged struct {
	AsrsID         string    `json:"asrs_id"`
	UnitsTotal     int       `json:"units_total"`
	UnitsAvailable int       `json:"units_available"`
	PositionsTotal int       `json:"positions_total"`
	PositionsFree  int       `json:"positions_free"`
	OpenJobs       int       `json:"open_jobs"`
	MeasuredAt     time.Time `json:"measured_at"`
}

func plcTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func describe(e any) (occurrence, error) {
	switch ev := e.(type) {
	case storagejob.ContainerStored:
		return occurrence{"storagejob", "ContainerStored", ev.JobID, ev.At, containerStored{ev.JobID, ev.AsrsID, ev.ContainerID,
			ev.PortID, ev.Reference, ev.At.UTC(), plcTime(ev.PLCTime)}}, nil
	case storagejob.ContainerPresentedAtPort:
		return occurrence{"storagejob", "ContainerPresentedAtPort", ev.JobID, ev.At, containerPresented{ev.JobID, ev.AsrsID,
			ev.ContainerID, ev.PortID, ev.Reference, string(ev.Scan), ev.ScannedContainerID, ev.At.UTC(), plcTime(ev.PLCTime)}}, nil
	case storagejob.StorageJobFailed:
		return occurrence{"storagejob", "StorageJobFailed", ev.JobID, ev.At, storageJobFailed{ev.JobID, ev.AsrsID, string(ev.Kind),
			ev.ContainerID, ev.PortID, ev.Reference, string(ev.Reason), ev.PLCReason, ev.At.UTC(), plcTime(ev.PLCTime)}}, nil
	case equipment.EquipmentStateChanged:
		return occurrence{"equipmentunit", "EquipmentStateChanged", ev.AsrsID + "/" + ev.UnitID, ev.At, equipmentStateChanged{
			ev.AsrsID, ev.UnitID, ev.Aisle, string(ev.State), string(ev.Previous), ev.At.UTC(), plcTime(ev.PLCTime)}}, nil
	case capacity.AsrsCapacityChanged:
		return occurrence{"asrs", "AsrsCapacityChanged", ev.AsrsID, ev.At, asrsCapacityChanged{ev.AsrsID, ev.UnitsTotal,
			ev.UnitsAvailable, ev.PositionsTotal, ev.PositionsFree, ev.OpenJobs, ev.At.UTC()}}, nil
	default:
		return occurrence{}, fmt.Errorf("events: no CloudEvents encoder for %T", e)
	}
}
