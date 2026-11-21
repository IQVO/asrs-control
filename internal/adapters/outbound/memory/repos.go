package memory

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/IQVO/asrs-control/internal/application/ports"
	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

type StorageMaps struct{ s *Store }

func NewStorageMaps(s *Store) *StorageMaps { return &StorageMaps{s: s} }

func (r *StorageMaps) Ensure(_ context.Context, l layout.Layout) error {
	r.s.with(func(t *tables) {
		for n := 1; n <= l.Aisles; n++ {
			if _, ok := t.maps[aisleKey{l.ID, n}]; !ok {
				t.maps[aisleKey{l.ID, n}] = storagemap.New(l, n).Snapshot()
			}
		}
	})
	return nil
}

func (r *StorageMaps) Lock(ctx context.Context, l layout.Layout, aisle int) (*storagemap.Map, error) {
	return r.Get(ctx, l, aisle)
}

func (r *StorageMaps) Get(_ context.Context, l layout.Layout, aisle int) (*storagemap.Map, error) {
	var snap storagemap.Snapshot
	var ok bool
	r.s.with(func(t *tables) { snap, ok = t.maps[aisleKey{l.ID, aisle}] })
	if !ok {
		return nil, fmt.Errorf("storage map %s aisle %d does not exist", l.ID, aisle)
	}
	return storagemap.Rehydrate(l, cloneSnapshot(snap))
}

func (r *StorageMaps) Save(_ context.Context, asrsID string, m *storagemap.Map) error {
	r.s.with(func(t *tables) { t.maps[aisleKey{asrsID, m.Aisle()}] = m.Snapshot() })
	return nil
}

func (r *StorageMaps) Find(_ context.Context, asrsID, containerID string) (int, bool, error) {
	aisle := 0
	r.s.with(func(t *tables) {
		for k, snap := range t.maps {
			for _, c := range snap.Occupied {
				if k.asrs == asrsID && c == containerID {
					aisle = k.aisle
				}
			}
		}
	})
	return aisle, aisle > 0, nil
}

type StorageJobs struct{ s *Store }

func NewStorageJobs(s *Store) *StorageJobs { return &StorageJobs{s: s} }

func (r *StorageJobs) Insert(_ context.Context, j *storagejob.Job) error {
	err := error(nil)
	r.s.with(func(t *tables) {
		if j.External() {
			for _, o := range t.jobs {
				if o.AsrsID == j.AsrsID() && o.ContainerID == j.ContainerID() && o.Kind != storagejob.Reshuffle && storagejob.Rehydrate(o).Open() {
					err = ports.ErrOpenJobExists
					return
				}
			}
		}
		t.seq++
		j.AssignSeq(t.seq)
		t.jobs[j.ID()] = j.State()
	})
	return err
}

func (r *StorageJobs) Update(_ context.Context, j *storagejob.Job) error {
	err := error(nil)
	r.s.with(func(t *tables) {
		if _, ok := t.jobs[j.ID()]; !ok {
			err = ports.ErrJobNotFound
			return
		}
		t.jobs[j.ID()] = j.State()
	})
	return err
}

func (r *StorageJobs) Get(_ context.Context, jobID string) (*storagejob.Job, error) {
	var s storagejob.State
	var ok bool
	r.s.with(func(t *tables) { s, ok = t.jobs[jobID] })
	if !ok {
		return nil, ports.ErrJobNotFound
	}
	return storagejob.Rehydrate(s), nil
}

// where returns the matching jobs in acceptance (Seq) order.
func (r *StorageJobs) where(keep func(s storagejob.State) bool) []*storagejob.Job {
	var out []*storagejob.Job
	r.s.with(func(t *tables) {
		for _, s := range t.jobs {
			if keep(s) {
				out = append(out, storagejob.Rehydrate(s))
			}
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Seq() < out[j].Seq() })
	return out
}

func (r *StorageJobs) first(keep func(s storagejob.State) bool) (*storagejob.Job, error) {
	if js := r.where(keep); len(js) > 0 {
		return js[0], nil
	}
	return nil, ports.ErrJobNotFound
}

func open(s storagejob.State) bool { return storagejob.Rehydrate(s).Open() }

func (r *StorageJobs) ByTelegram(_ context.Context, asrsID, id string) (*storagejob.Job, error) {
	return r.first(func(s storagejob.State) bool {
		return s.AsrsID == asrsID && id != "" && (s.TelegramID == id || s.CancelTelegramID == id)
	})
}

func (r *StorageJobs) Open(_ context.Context, asrsID string, aisle int) ([]*storagejob.Job, error) {
	return r.where(func(s storagejob.State) bool { return s.AsrsID == asrsID && s.Aisle == aisle && open(s) }), nil
}

func (r *StorageJobs) OldestAwaiting(_ context.Context, asrsID, portID string) (*storagejob.Job, error) {
	return r.first(func(s storagejob.State) bool {
		return s.AsrsID == asrsID && s.PortID == portID && s.Status == storagejob.AwaitingArrival
	})
}

func (r *StorageJobs) HasOpen(_ context.Context, asrsID, containerID string) (bool, error) {
	return len(r.where(func(s storagejob.State) bool {
		return s.AsrsID == asrsID && s.ContainerID == containerID && s.Kind != storagejob.Reshuffle && open(s)
	})) > 0, nil
}

func (r *StorageJobs) CountOpen(_ context.Context, asrsID string) (int, error) {
	return len(r.where(func(s storagejob.State) bool {
		return s.AsrsID == asrsID && s.Kind != storagejob.Reshuffle && open(s)
	})), nil
}

func (r *StorageJobs) CountRetrievals(_ context.Context, asrsID, containerID string, since time.Time) (int, error) {
	return len(r.where(func(s storagejob.State) bool {
		return s.AsrsID == asrsID && s.ContainerID == containerID && s.Kind == storagejob.Retrieve &&
			s.Status == storagejob.Done && !s.EndedAt.Before(since)
	})), nil
}

func (r *StorageJobs) InFlightCommands(_ context.Context, asrsID string) ([]*storagejob.Job, error) {
	return r.where(func(s storagejob.State) bool {
		return s.AsrsID == asrsID && (s.Status == storagejob.Dispatched || s.Status == storagejob.CancelRequested)
	}), nil
}

func (r *StorageJobs) ListExternal(_ context.Context, asrsID string, status storagejob.Status, beforeSeq int64, limit int) ([]*storagejob.Job, error) {
	js := r.where(func(s storagejob.State) bool {
		return s.AsrsID == asrsID && s.Kind != storagejob.Reshuffle && (status == "" || s.Status == status) &&
			(beforeSeq == 0 || s.Seq < beforeSeq)
	})
	slices.Reverse(js)
	return js[:min(len(js), limit)], nil
}

type Units struct{ s *Store }

func NewUnits(s *Store) *Units { return &Units{s: s} }

func (r *Units) Ensure(_ context.Context, l layout.Layout, at time.Time) error {
	r.s.with(func(t *tables) {
		for n := 1; n <= l.Aisles; n++ {
			if _, ok := t.units[aisleKey{l.ID, n}]; !ok {
				t.units[aisleKey{l.ID, n}] = equipment.New(l.ID, l.UnitID(n), n, at).Snapshot()
			}
		}
	})
	return nil
}

func (r *Units) Lock(_ context.Context, asrsID string, aisle int) (*equipment.Unit, error) {
	var s equipment.UnitState
	var ok bool
	r.s.with(func(t *tables) { s, ok = t.units[aisleKey{asrsID, aisle}] })
	if !ok {
		return nil, fmt.Errorf("unit of %s aisle %d does not exist", asrsID, aisle)
	}
	return equipment.Rehydrate(s), nil
}

func (r *Units) List(_ context.Context, asrsID string) ([]*equipment.Unit, error) {
	var out []*equipment.Unit
	r.s.with(func(t *tables) {
		for k, s := range t.units {
			if k.asrs == asrsID {
				out = append(out, equipment.Rehydrate(s))
			}
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Aisle() < out[j].Aisle() })
	return out, nil
}

func (r *Units) Save(_ context.Context, u *equipment.Unit) error {
	r.s.with(func(t *tables) { t.units[aisleKey{u.AsrsID(), u.Aisle()}] = u.Snapshot() })
	return nil
}

type Capacities struct{ s *Store }

func NewCapacities(s *Store) *Capacities { return &Capacities{s: s} }

func (r *Capacities) Lock(_ context.Context, asrsID string) (capacity.Snapshot, bool, error) {
	var c capacity.Snapshot
	var ok bool
	r.s.with(func(t *tables) { c, ok = t.capacity[asrsID] })
	return c, ok, nil
}

func (r *Capacities) Save(_ context.Context, c capacity.Snapshot) error {
	r.s.with(func(t *tables) { t.capacity[c.AsrsID] = c })
	return nil
}
