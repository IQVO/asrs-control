package http

import (
	"fmt"
	"time"

	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

type createJobRequest struct {
	Kind        string `json:"kind"`
	ContainerID string `json:"container_id"`
	PortID      string `json:"port_id,omitempty"`
	Reference   string `json:"reference,omitempty"`
}

func (b createJobRequest) validate() error {
	if b.Kind != string(storagejob.Store) && b.Kind != string(storagejob.Retrieve) {
		return fmt.Errorf("%w: kind must be STORE or RETRIEVE", errInvalidParameter)
	}
	if err := rejectNUL("container_id", b.ContainerID); err != nil {
		return err
	}
	if b.Kind == string(storagejob.Store) || b.PortID != "" {
		if err := rejectNUL("port_id", b.PortID); err != nil {
			return err
		}
	}
	if b.Reference != "" {
		return rejectNUL("reference", b.Reference)
	}
	return nil
}

type pickupRequest struct {
	ContainerID string `json:"container_id"`
}

type jobResponse struct {
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

func toJob(j *storagejob.Job) jobResponse {
	out := jobResponse{StorageJobID: j.ID(), AsrsID: j.AsrsID(), Kind: string(j.Kind()), ContainerID: j.ContainerID(),
		PortID: j.PortID(), Reference: j.Reference(), Status: string(j.Status()), Reason: string(j.Reason()),
		PLCReason: j.PLCReason(), AcceptedAt: j.AcceptedAt().UTC()}
	if !j.EndedAt().IsZero() {
		e := j.EndedAt().UTC()
		out.EndedAt = &e
	}
	return out
}

type pickupResponse struct {
	AsrsID      string `json:"asrs_id"`
	PortID      string `json:"port_id"`
	ContainerID string `json:"container_id"`
}

type aisleResponse struct {
	Aisle          int    `json:"aisle"`
	UnitID         string `json:"unit_id"`
	State          string `json:"state"`
	Busy           bool   `json:"busy"`
	OpenJobs       int    `json:"open_jobs"`
	PositionsTotal int    `json:"positions_total"`
	PositionsFree  int    `json:"positions_free"`
}

type portResponse struct {
	PortID               string  `json:"port_id"`
	Aisle                int     `json:"aisle"`
	Kind                 string  `json:"kind"`
	PresentedContainerID *string `json:"presented_container_id"`
}

type capacityResponse struct {
	UnitsTotal     int `json:"units_total"`
	UnitsAvailable int `json:"units_available"`
	PositionsTotal int `json:"positions_total"`
	PositionsFree  int `json:"positions_free"`
	OpenJobs       int `json:"open_jobs"`
}

type asrsResponse struct {
	AsrsID                 string           `json:"asrs_id"`
	Aisles                 []aisleResponse  `json:"aisles"`
	Ports                  []portResponse   `json:"ports"`
	RecommendedInputPortID *string          `json:"recommended_input_port_id"`
	Capacity               capacityResponse `json:"capacity"`
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toAsrs(v usecases.AsrsView) asrsResponse {
	out := asrsResponse{AsrsID: v.AsrsID, Aisles: []aisleResponse{}, Ports: []portResponse{},
		RecommendedInputPortID: optional(v.RecommendedInputPortID),
		Capacity: capacityResponse{UnitsTotal: v.Capacity.UnitsTotal, UnitsAvailable: v.Capacity.UnitsAvailable,
			PositionsTotal: v.Capacity.PositionsTotal, PositionsFree: v.Capacity.PositionsFree, OpenJobs: v.Capacity.OpenJobs}}
	for _, a := range v.Aisles {
		out.Aisles = append(out.Aisles, aisleResponse{Aisle: a.Aisle, UnitID: a.UnitID, State: string(a.State), Busy: a.Busy,
			OpenJobs: a.OpenJobs, PositionsTotal: a.PositionsTotal, PositionsFree: a.PositionsFree})
	}
	for _, p := range v.Ports {
		out.Ports = append(out.Ports, portResponse{PortID: p.PortID, Aisle: p.Aisle, Kind: string(p.Kind), PresentedContainerID: optional(p.PresentedContainerID)})
	}
	return out
}
