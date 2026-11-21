package a2a

import (
	"fmt"
	"time"

	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

// problem is the RFC 7807-shaped answer an agent client gets instead of a result: rejections and
// request-shape failures are business answers inside the envelope, not transport errors. Types
// are the REST adapter's slugs where the outcome is shared; the rest belong to the A2A request.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

func (p *problem) summary() string { return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail) }

func problemMalformedRequest(detail string) *problem {
	return &problem{Type: "malformed-skill-request", Title: "Malformed skill request", Detail: detail, Status: 400}
}

func problemUnknownSkill(skill string) *problem {
	return &problem{Type: "unknown-skill", Title: "Unknown skill", Status: 400,
		Detail: fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill)}
}

func problemInvalidArgument(detail string) *problem {
	return &problem{Type: "invalid-argument", Title: "Invalid skill argument", Detail: detail, Status: 400}
}

// envelope is every answer's structured payload: exactly one of Data or Problem, plus the skill.
type envelope struct {
	Skill   string    `json:"skill"`
	Data    *skillDTO `json:"data,omitempty"`
	Problem *problem  `json:"problem,omitempty"`
}

// skillDTO is the union of the skill results; each skill fills its own field.
type skillDTO struct {
	Job      *jobDTO      `json:"storageJob,omitempty"`
	Capacity *capacityDTO `json:"capacity,omitempty"`
	summary  string
}

// jobDTO is a storage job on the A2A wire (camelCase like the arguments). No rack position (D11).
type jobDTO struct {
	StorageJobID string     `json:"storageJobId"`
	AsrsID       string     `json:"asrsId"`
	Kind         string     `json:"kind"`
	ContainerID  string     `json:"containerId"`
	PortID       string     `json:"portId"`
	Reference    string     `json:"reference,omitempty"`
	Status       string     `json:"status"`
	Reason       string     `json:"reason,omitempty"`
	PLCReason    string     `json:"plcReason,omitempty"`
	AcceptedAt   time.Time  `json:"acceptedAt"`
	EndedAt      *time.Time `json:"endedAt,omitempty"`
}

type capacityDTO struct {
	AsrsID         string `json:"asrsId"`
	UnitsTotal     int    `json:"unitsTotal"`
	UnitsAvailable int    `json:"unitsAvailable"`
	PositionsTotal int    `json:"positionsTotal"`
	PositionsFree  int    `json:"positionsFree"`
	OpenJobs       int    `json:"openJobs"`
}

func toJobDTO(j *storagejob.Job) jobDTO {
	out := jobDTO{StorageJobID: j.ID(), AsrsID: j.AsrsID(), Kind: string(j.Kind()), ContainerID: j.ContainerID(), PortID: j.PortID(),
		Reference: j.Reference(), Status: string(j.Status()), Reason: string(j.Reason()), PLCReason: j.PLCReason(), AcceptedAt: j.AcceptedAt().UTC()}
	if !j.EndedAt().IsZero() {
		e := j.EndedAt().UTC()
		out.EndedAt = &e
	}
	return out
}

func toCapacityDTO(c capacity.Snapshot) capacityDTO {
	return capacityDTO{AsrsID: c.AsrsID, UnitsTotal: c.UnitsTotal, UnitsAvailable: c.UnitsAvailable, PositionsTotal: c.PositionsTotal,
		PositionsFree: c.PositionsFree, OpenJobs: c.OpenJobs}
}

func summarizeJob(j jobDTO) string {
	s := fmt.Sprintf("%s: %s of container %s at port %s is %s", j.StorageJobID, j.Kind, j.ContainerID, j.PortID, j.Status)
	if j.Reason != "" {
		s += " (" + j.Reason + ")"
	}
	return s
}

func summarizeCapacity(c capacityDTO) string {
	return fmt.Sprintf("%s: %d/%d units available, %d/%d positions free, %d open job(s)", c.AsrsID, c.UnitsAvailable, c.UnitsTotal,
		c.PositionsFree, c.PositionsTotal, c.OpenJobs)
}
