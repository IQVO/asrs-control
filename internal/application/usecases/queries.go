package usecases

import (
	"context"

	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

// AisleView is one aisle's unit and occupancy. No rack position is exposed (D11).
type AisleView struct {
	Aisle                                   int
	UnitID                                  string
	State                                   equipment.State
	Busy                                    bool
	OpenJobs, PositionsTotal, PositionsFree int
}

// PortView is one port; an output port shows the container waiting on it.
type PortView struct {
	PortID               string
	Aisle                int
	Kind                 layout.PortKind
	PresentedContainerID string
}

// AsrsView is the AS/RS state material-flow-control reads.
type AsrsView struct {
	AsrsID                 string
	Aisles                 []AisleView
	Ports                  []PortView
	RecommendedInputPortID string
	Capacity               capacity.Snapshot
}

// Job reads an external job of asrsID.
func (s *Service) Job(ctx context.Context, asrsID, jobID string) (*storagejob.Job, error) {
	l, err := s.Layout(asrsID)
	if err != nil {
		return nil, err
	}
	return s.externalJob(ctx, l.ID, jobID)
}

// Asrs reads the whole AS/RS.
func (s *Service) Asrs(ctx context.Context, asrsID string) (AsrsView, error) {
	l, err := s.Layout(asrsID)
	if err != nil {
		return AsrsView{}, err
	}
	v := AsrsView{AsrsID: l.ID}
	maps, err := s.aisleViews(ctx, l, &v)
	if err != nil {
		return AsrsView{}, err
	}
	for _, p := range l.Ports {
		pv := PortView{PortID: p.ID, Aisle: p.Aisle, Kind: p.Kind}
		if p.Kind == layout.PortOutput {
			pv.PresentedContainerID = maps[p.Aisle].Presented(p.ID)
		}
		v.Ports = append(v.Ports, pv)
	}
	v.RecommendedInputPortID = recommendInput(l, v.Aisles)
	v.Capacity, err = s.capacity(ctx, l)
	return v, err
}

func (s *Service) aisleViews(ctx context.Context, l layout.Layout, v *AsrsView) (map[int]*storagemap.Map, error) {
	units, err := s.d.Units.List(ctx, l.ID)
	if err != nil {
		return nil, err
	}
	maps := map[int]*storagemap.Map{}
	for _, u := range units {
		m, err := s.d.Maps.Get(ctx, l, u.Aisle())
		if err != nil {
			return nil, err
		}
		open, err := s.d.Jobs.Open(ctx, l.ID, u.Aisle())
		if err != nil {
			return nil, err
		}
		external := 0
		for _, j := range open {
			if j.External() {
				external++
			}
		}
		maps[u.Aisle()] = m
		v.Aisles = append(v.Aisles, AisleView{Aisle: u.Aisle(), UnitID: u.ID(), State: u.State(), Busy: u.Busy(),
			OpenJobs: external, PositionsTotal: l.PositionsPerAisle(), PositionsFree: m.Free()})
	}
	return maps, nil
}

// recommendInput is the input port of the least-loaded available aisle with room.
func recommendInput(l layout.Layout, aisles []AisleView) string {
	best := -1
	for i, a := range aisles {
		if a.State == equipment.Held || a.PositionsFree == 0 {
			continue
		}
		if best < 0 || a.OpenJobs < aisles[best].OpenJobs ||
			(a.OpenJobs == aisles[best].OpenJobs && a.PositionsFree > aisles[best].PositionsFree) {
			best = i
		}
	}
	if best < 0 {
		return ""
	}
	return l.InputPorts(aisles[best].Aisle)[0].ID
}
