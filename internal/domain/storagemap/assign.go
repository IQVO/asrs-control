package storagemap

import (
	"sort"

	"github.com/IQVO/asrs-control/internal/domain/layout"
)

// assignable applies invariant 2 and the depth rules D1 and D2 (ADR 0002).
func (m *Map) assignable(p layout.Position) bool {
	if m.slots[p] != "" || m.reserved[p] != "" {
		return false
	}
	if p.Depth == 2 { // D1: the front must stay clear until the back is filled
		f := p.Front()
		return m.slots[f] == "" && m.reserved[f] == ""
	}
	if !m.l.DoubleDeep {
		return true
	}
	b := p.Back()
	if m.reserved[b] != "" { // D2: a store is on its way to the back
		return false
	}
	c := m.slots[b]
	return c == "" || (m.retrieving[c] == "" && m.moving[c] == "") // D2: never block a leaving container
}

// Assign picks the best assignable position for velocity v, skipping positions exclude rejects.
func (m *Map) Assign(v Velocity, exclude func(layout.Position) bool) (layout.Position, error) {
	var cands []layout.Position
	for _, p := range m.l.AislePositions(m.aisle) {
		if m.assignable(p) && (exclude == nil || !exclude(p)) {
			cands = append(cands, p)
		}
	}
	if len(cands) == 0 {
		return layout.Position{}, ErrNoFreePosition
	}
	sort.SliceStable(cands, func(i, j int) bool { return m.better(v, cands[i], cands[j]) })
	return cands[0], nil
}

// better ranks a before b: the preferred depth first (FAST the front, SLOW the back of a
// double-deep rack), then travel cost (FAST cheapest, SLOW dearest). Assign sorts stably over
// AislePositions (level by level, slot by slot, front first), so a cost tie keeps the lower
// level, then the lower slot, and the choice is deterministic. The tie-break is the sort's
// stability, not more comparisons: compared only after `ca != cb` / `a.Level != b.Level`,
// `<` and `<=` are the same answer, so explicit tie-break keys only bred equivalent mutants.
func (m *Map) better(v Velocity, a, b layout.Position) bool {
	pref := 1
	if v == Slow && m.l.DoubleDeep {
		pref = 2
	}
	if (a.Depth == pref) != (b.Depth == pref) {
		return a.Depth == pref
	}
	if v == Fast {
		return m.l.CostOf(a) < m.l.CostOf(b)
	}
	return m.l.CostOf(a) > m.l.CostOf(b)
}

// ReserveStore reserves the position a store job will fill.
func (m *Map) ReserveStore(jobID, container string, v Velocity) (layout.Position, error) {
	if _, in := m.where[container]; in {
		return layout.Position{}, ErrContainerInRack
	}
	if m.hasPendingStore(container) || m.onPort(container) {
		return layout.Position{}, ErrContainerBusy
	}
	p, err := m.Assign(v, nil)
	if err != nil {
		return layout.Position{}, err
	}
	m.reserved[p], m.reservedBy[p] = jobID, container
	return p, nil
}

// ReserveRetrieve marks a stored container as leaving with jobID.
func (m *Map) ReserveRetrieve(jobID, container string) error {
	if _, in := m.where[container]; !in {
		return ErrContainerNotInRack
	}
	if m.retrieving[container] != "" || m.moving[container] != "" {
		return ErrContainerBusy
	}
	m.retrieving[container] = jobID
	return nil
}

// PlanReshuffle reserves a position in another slot for the container blocking p and marks
// it moving with jobID.
func (m *Map) PlanReshuffle(jobID string, blocked layout.Position, v Velocity) (string, layout.Position, layout.Position, error) {
	none := layout.Position{}
	c, ok := m.Blocker(blocked)
	if !ok {
		return "", none, none, ErrNotBlocked
	}
	if m.moving[c] != "" || m.retrieving[c] != "" {
		return "", none, none, ErrContainerBusy
	}
	sameSlot := func(q layout.Position) bool { return q.Level == blocked.Level && q.Slot == blocked.Slot }
	to, err := m.Assign(v, sameSlot)
	if err != nil {
		return "", none, none, err
	}
	m.reserved[to], m.reservedBy[to] = jobID, c
	m.moving[c] = jobID
	return c, blocked.Front(), to, nil
}

// hasPendingStore reports whether a reservation is already held for container.
func (m *Map) hasPendingStore(container string) bool {
	for _, c := range m.reservedBy {
		if c == container {
			return true
		}
	}
	return false
}

func (m *Map) onPort(container string) bool {
	for _, c := range m.presented {
		if c == container {
			return true
		}
	}
	return false
}
