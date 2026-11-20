package storagemap

import "github.com/IQVO/asrs-control/internal/domain/layout"

// CompleteStore records a store's JOB_DONE: the container now sits at its reserved position.
func (m *Map) CompleteStore(jobID, container string, to layout.Position) error {
	if m.reserved[to] != jobID || m.reservedBy[to] != container {
		return ErrNotReserved
	}
	m.unreserve(to)
	m.place(container, to)
	return nil
}

// CompleteMove records a reshuffle's JOB_DONE.
func (m *Map) CompleteMove(jobID, container string, from, to layout.Position) error {
	if m.slots[from] != container {
		return ErrNotAtPosition
	}
	if m.reserved[to] != jobID || m.reservedBy[to] != container {
		return ErrNotReserved
	}
	m.remove(from)
	m.unreserve(to)
	m.place(container, to)
	delete(m.moving, container)
	return nil
}

// CompleteRetrieve records a retrieve's JOB_DONE: the container left the rack and waits on the
// output port. The PLC is the physical truth, so a container still recorded on the port (its
// pickup was never confirmed) is replaced and returned for the caller to log.
func (m *Map) CompleteRetrieve(container string, from layout.Position, port string) (string, error) {
	if m.slots[from] != container {
		return "", ErrNotAtPosition
	}
	prev, ok := m.presented[port]
	if !ok {
		return "", ErrUnknownPort
	}
	m.remove(from)
	delete(m.retrieving, container)
	m.presented[port] = container
	return prev, nil
}

// ReleaseStore frees a store's reservation (the job failed or was cancelled).
func (m *Map) ReleaseStore(jobID string, to layout.Position) {
	if m.reserved[to] == jobID {
		m.unreserve(to)
	}
}

// ReleaseRetrieve ends a retrieve's hold on its container.
func (m *Map) ReleaseRetrieve(jobID, container string) {
	if m.retrieving[container] == jobID {
		delete(m.retrieving, container)
	}
}

// ReleaseMove ends a reshuffle's hold on its container and its target.
func (m *Map) ReleaseMove(jobID, container string, to layout.Position) {
	if m.moving[container] == jobID {
		delete(m.moving, container)
	}
	m.ReleaseStore(jobID, to)
}

// Pickup frees an output port once material-flow-control took the presented container.
func (m *Map) Pickup(port, container string) error {
	cur, ok := m.presented[port]
	if !ok {
		return ErrUnknownPort
	}
	if cur == "" || cur != container {
		return ErrNotPresenting
	}
	m.presented[port] = ""
	return nil
}

func (m *Map) unreserve(p layout.Position) {
	delete(m.reserved, p)
	delete(m.reservedBy, p)
}
