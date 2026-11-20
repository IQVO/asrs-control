package storagejob_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func at(level, slot, depth int) layout.Position {
	return layout.Position{Aisle: 1, Level: level, Slot: slot, Depth: depth}
}

func storeSpec() storagejob.Spec {
	return storagejob.Spec{ID: "sj-1", AsrsID: "ASRS1", Aisle: 1, ContainerID: "C1", PortID: "IN1", Reference: "leg-9", To: at(1, 2, 2), At: t0}
}

func retrieveSpec() storagejob.Spec {
	return storagejob.Spec{ID: "sj-2", AsrsID: "ASRS1", Aisle: 1, ContainerID: "C1", PortID: "OUT1", At: t0}
}

func reshuffleSpec() storagejob.Spec {
	return storagejob.Spec{ID: "sj-3", AsrsID: "ASRS1", Aisle: 1, ContainerID: "C9", ParentID: "sj-2", From: at(1, 2, 1), To: at(1, 1, 1), At: t0}
}

func mustStore(t *testing.T) *storagejob.Job {
	t.Helper()
	j, err := storagejob.NewStore(storeSpec())
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func mustRetrieve(t *testing.T) *storagejob.Job {
	t.Helper()
	j, err := storagejob.NewRetrieve(retrieveSpec())
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func mustReshuffle(t *testing.T) *storagejob.Job {
	t.Helper()
	j, err := storagejob.NewReshuffle(reshuffleSpec())
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestNewJobsStartQueuedWithTheirSpec(t *testing.T) {
	s := mustStore(t)
	if s.Kind() != storagejob.Store || s.Status() != storagejob.Queued || s.ID() != "sj-1" || s.AsrsID() != "ASRS1" ||
		s.Aisle() != 1 || s.ContainerID() != "C1" {
		t.Fatalf("store = %+v", s.State())
	}
	if s.PortID() != "IN1" || s.Reference() != "leg-9" || s.To() != at(1, 2, 2) || !s.From().IsZero() {
		t.Fatalf("store = %+v", s.State())
	}
	if !s.AcceptedAt().Equal(t0) || !s.EndedAt().IsZero() {
		t.Fatalf("store = %+v", s.State())
	}
}

func TestAQueuedStoreIsExternalOpenAndNotInFlight(t *testing.T) {
	s := mustStore(t)
	if !s.External() || !s.Open() || s.InFlight() {
		t.Error("a queued store is external, open and not in flight")
	}
}

func TestNewRetrieveAndReshuffleStartQueued(t *testing.T) {
	r := mustRetrieve(t)
	if r.Kind() != storagejob.Retrieve || !r.From().IsZero() {
		t.Fatalf("retrieve = %+v", r.State())
	}
	m := mustReshuffle(t)
	if m.Kind() != storagejob.Reshuffle || m.External() || m.ParentID() != "sj-2" {
		t.Fatalf("reshuffle = %+v", m.State())
	}
}

func TestConstructorsRefuseIncompleteSpecs(t *testing.T) {
	type ctor func(storagejob.Spec) (*storagejob.Job, error)
	cases := []struct {
		name string
		new  ctor
		spec func() storagejob.Spec
		bad  func(*storagejob.Spec)
	}{
		{"store without id", storagejob.NewStore, storeSpec, func(s *storagejob.Spec) { s.ID = "" }},
		{"store without asrs", storagejob.NewStore, storeSpec, func(s *storagejob.Spec) { s.AsrsID = "" }},
		{"store without container", storagejob.NewStore, storeSpec, func(s *storagejob.Spec) { s.ContainerID = "" }},
		{"store in aisle zero", storagejob.NewStore, storeSpec, func(s *storagejob.Spec) { s.Aisle = 0 }},
		{"store without time", storagejob.NewStore, storeSpec, func(s *storagejob.Spec) { s.At = time.Time{} }},
		{"store without port", storagejob.NewStore, storeSpec, func(s *storagejob.Spec) { s.PortID = "" }},
		{"store without target", storagejob.NewStore, storeSpec, func(s *storagejob.Spec) { s.To = layout.Position{} }},
		{"store into another aisle", storagejob.NewStore, storeSpec, func(s *storagejob.Spec) { s.To.Aisle = 2 }},
		{"retrieve without port", storagejob.NewRetrieve, retrieveSpec, func(s *storagejob.Spec) { s.PortID = "" }},
		{"retrieve with a source", storagejob.NewRetrieve, retrieveSpec, func(s *storagejob.Spec) { s.From = at(1, 1, 1) }},
		{"retrieve with a target", storagejob.NewRetrieve, retrieveSpec, func(s *storagejob.Spec) { s.To = at(1, 1, 1) }},
		{"reshuffle without parent", storagejob.NewReshuffle, reshuffleSpec, func(s *storagejob.Spec) { s.ParentID = "" }},
		{"reshuffle without source", storagejob.NewReshuffle, reshuffleSpec, func(s *storagejob.Spec) { s.From = layout.Position{} }},
		{"reshuffle without target", storagejob.NewReshuffle, reshuffleSpec, func(s *storagejob.Spec) { s.To = layout.Position{} }},
		{"reshuffle across aisles", storagejob.NewReshuffle, reshuffleSpec, func(s *storagejob.Spec) { s.To.Aisle = 2 }},
		{"reshuffle source in another aisle", storagejob.NewReshuffle, reshuffleSpec, func(s *storagejob.Spec) { s.From.Aisle = 2 }},
		{"reshuffle at a port", storagejob.NewReshuffle, reshuffleSpec, func(s *storagejob.Spec) { s.PortID = "OUT1" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.spec()
			c.bad(&s)
			if _, err := c.new(s); !errors.Is(err, storagejob.ErrInvalidJob) {
				t.Fatalf("err = %v, want ErrInvalidJob", err)
			}
		})
	}
}

func TestStateRoundTripsAndSeqIsAssignedOnce(t *testing.T) {
	j := mustStore(t)
	j.AssignSeq(7)
	j.AssignSeq(9)
	if j.Seq() != 7 {
		t.Fatalf("Seq = %d, want 7 (assigned once)", j.Seq())
	}
	_ = j.Dispatch("tg-1")
	back := storagejob.Rehydrate(j.State())
	if back.State() != j.State() || back.TelegramID() != "tg-1" {
		t.Fatalf("round trip: %+v != %+v", back.State(), j.State())
	}
}

func TestReasonForPLCMapsTheProtocolCodes(t *testing.T) {
	cases := map[string]storagejob.Reason{
		"CONTAINER_NOT_AT_PORT":     storagejob.ReasonContainerNotAtPort,
		"CONTAINER_NOT_AT_POSITION": storagejob.ReasonStorageMapMismatch,
		"POSITION_OCCUPIED":         storagejob.ReasonStorageMapMismatch,
		"POSITION_BLOCKED":          storagejob.ReasonStorageMapMismatch,
		"PORT_OCCUPIED":             storagejob.ReasonPortOccupied,
		"SOMETHING_NEW":             storagejob.ReasonEquipmentFault,
	}
	for code, want := range cases {
		if got := storagejob.ReasonForPLC(code); got != want {
			t.Errorf("ReasonForPLC(%s) = %s, want %s", code, got, want)
		}
	}
}
