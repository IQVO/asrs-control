package mcp

import (
	"time"

	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

// The tool results carry the REST response shapes of apis/openapi.yaml (snake_case fields), so
// one vocabulary serves both surfaces. They are this adapter's own types: the REST adapter is a
// sibling and is never imported. No rack position appears anywhere (D11).

type asrsList struct {
	AsrsIDs []string `json:"asrs_ids"`
}

type jobPayload struct {
	StorageJobID string     `json:"storage_job_id"`
	AsrsID       string     `json:"asrs_id"`
	Kind         string     `json:"kind"`
	ContainerID  string     `json:"container_id"`
	PortID       string     `json:"port_id"`
	Reference    string     `json:"reference,omitempty"`
	Status       string     `json:"status"`
	Reason       string     `json:"reason,omitempty"`
	PLCReason    string     `json:"plc_reason,omitempty"`
	AcceptedAt   time.Time  `json:"accepted_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
}

type jobPagePayload struct {
	Items      []jobPayload `json:"items"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

type aislePayload struct {
	Aisle          int    `json:"aisle"`
	UnitID         string `json:"unit_id"`
	State          string `json:"state"`
	Busy           bool   `json:"busy"`
	OpenJobs       int    `json:"open_jobs"`
	PositionsTotal int    `json:"positions_total"`
	PositionsFree  int    `json:"positions_free"`
}

type portPayload struct {
	PortID               string `json:"port_id"`
	Aisle                int    `json:"aisle"`
	Kind                 string `json:"kind"`
	PresentedContainerID string `json:"presented_container_id,omitempty"`
}

type capacityPayload struct {
	AsrsID         string `json:"asrs_id"`
	UnitsTotal     int    `json:"units_total"`
	UnitsAvailable int    `json:"units_available"`
	PositionsTotal int    `json:"positions_total"`
	PositionsFree  int    `json:"positions_free"`
	OpenJobs       int    `json:"open_jobs"`
}

type asrsPayload struct {
	AsrsID                 string          `json:"asrs_id"`
	Aisles                 []aislePayload  `json:"aisles"`
	Ports                  []portPayload   `json:"ports"`
	RecommendedInputPortID string          `json:"recommended_input_port_id,omitempty"`
	Capacity               capacityPayload `json:"capacity"`
}

func toJob(j *storagejob.Job) jobPayload {
	out := jobPayload{StorageJobID: j.ID(), AsrsID: j.AsrsID(), Kind: string(j.Kind()), ContainerID: j.ContainerID(),
		PortID: j.PortID(), Reference: j.Reference(), Status: string(j.Status()), Reason: string(j.Reason()),
		PLCReason: j.PLCReason(), AcceptedAt: j.AcceptedAt().UTC()}
	if !j.EndedAt().IsZero() {
		e := j.EndedAt().UTC()
		out.EndedAt = &e
	}
	return out
}

func toCapacity(c capacity.Snapshot) capacityPayload {
	return capacityPayload{AsrsID: c.AsrsID, UnitsTotal: c.UnitsTotal, UnitsAvailable: c.UnitsAvailable,
		PositionsTotal: c.PositionsTotal, PositionsFree: c.PositionsFree, OpenJobs: c.OpenJobs}
}

func toAsrs(v usecases.AsrsView) asrsPayload {
	out := asrsPayload{AsrsID: v.AsrsID, Aisles: []aislePayload{}, Ports: []portPayload{},
		RecommendedInputPortID: v.RecommendedInputPortID, Capacity: toCapacity(v.Capacity)}
	out.Capacity.AsrsID = v.AsrsID
	for _, a := range v.Aisles {
		out.Aisles = append(out.Aisles, aislePayload{Aisle: a.Aisle, UnitID: a.UnitID, State: string(a.State), Busy: a.Busy,
			OpenJobs: a.OpenJobs, PositionsTotal: a.PositionsTotal, PositionsFree: a.PositionsFree})
	}
	for _, p := range v.Ports {
		out.Ports = append(out.Ports, portPayload{PortID: p.PortID, Aisle: p.Aisle, Kind: string(p.Kind),
			PresentedContainerID: p.PresentedContainerID})
	}
	return out
}
