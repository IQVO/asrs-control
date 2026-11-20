package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/memory"
	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/layout"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

func TestNewRefusesBadLayoutsAndMissingDeps(t *testing.T) {
	bad := tinyLayout()
	bad.Aisles = 0
	if _, err := usecases.New(usecases.Deps{Layouts: []layout.Layout{bad}}); !errors.Is(err, layout.ErrInvalidLayout) {
		t.Fatalf("New with an invalid layout = %v", err)
	}
	if _, err := usecases.New(usecases.Deps{Layouts: []layout.Layout{tinyLayout(), tinyLayout()}}); err == nil {
		t.Fatal("New accepted a duplicate AS/RS")
	}
	if _, err := usecases.New(usecases.Deps{}); err == nil {
		t.Fatal("New accepted no layout")
	}
}

func TestAStoreReservesDispatchesAndSendsOneTelegram(t *testing.T) {
	h := newHarness(t, tinyLayout())
	j := h.store("C1", "IN1")
	if j.Status() != storagejob.Dispatched || j.ID() != "sj-id-1" || j.To() != (layout.Position{Aisle: 1, Level: 1, Slot: 2, Depth: 2}) {
		t.Fatalf("job = %+v", j.State())
	}
	cmds := h.down.Sent("ASRS1")
	if len(cmds) != 1 {
		t.Fatalf("sent %d commands, want 1", len(cmds))
	}
	if !isStoreCommandOf(cmds[0], j, "C1", "IN1") {
		t.Fatalf("command = %+v", cmds[0])
	}
	caps := eventsOf[capacity.AsrsCapacityChanged](h)
	if len(caps) != 1 || caps[0].PositionsFree != 3 || caps[0].OpenJobs != 1 || caps[0].UnitsAvailable != 1 || caps[0].PositionsTotal != 4 {
		t.Fatalf("capacity = %+v", caps)
	}
}

func isStoreCommandOf(c telegrams.Command, j *storagejob.Job, container, port string) bool {
	return c.Type == telegrams.CmdStore && c.JobID == j.ID() && c.ContainerID == container && c.PortID == port &&
		c.To != nil && *c.To == j.To() && c.From == nil && c.TelegramID == j.TelegramID()
}

func TestCommandsAreSentOnlyAfterTheOutermostCommit(t *testing.T) {
	h := newHarness(t, tinyLayout())
	outer := memory.NewUnitOfWork(h.db) // stands for the Idempotency-Key middleware's unit of work
	store := func(ctx context.Context) error {
		_, err := h.svc.RequestStore(ctx, usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: "A", PortID: "IN1"})
		if n := len(h.down.Sent("ASRS1")); n != 0 {
			t.Fatalf("%d command(s) sent before the outermost commit", n)
		}
		return err
	}
	boom := errors.New("the caller rolls back")
	err := outer.Do(h.ctx, func(ctx context.Context) error {
		if err := store(ctx); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || len(h.down.Sent("ASRS1")) != 0 {
		t.Fatalf("rolled back: %v, %d command(s) sent, want none", err, len(h.down.Sent("ASRS1")))
	}
	if ok, _ := h.jobs.HasOpen(h.ctx, "ASRS1", "A"); ok {
		t.Fatal("the rolled-back store left a job")
	}
	if err := outer.Do(h.ctx, store); err != nil {
		t.Fatal(err)
	}
	if sent := h.down.Sent("ASRS1"); len(sent) != 1 || sent[0].Type != telegrams.CmdStore {
		t.Fatalf("after the commit: %+v, want one STORE", sent)
	}
}

func TestASecondStoreWaitsForTheFirst(t *testing.T) {
	h := newHarness(t, tinyLayout())
	first := h.store("C1", "IN1")
	second := h.store("C2", "IN1")
	if second.Status() != storagejob.Queued || len(h.down.Sent("ASRS1")) != 1 {
		t.Fatalf("second = %s, sent %d (one job per unit)", second.Status(), len(h.down.Sent("ASRS1")))
	}
	h.ackLast()
	h.done(first.ID())
	if h.job(second.ID()).Status() != storagejob.Dispatched || h.last().JobID != second.ID() {
		t.Fatalf("the second store was not dispatched after the first JOB_DONE: %+v", h.last())
	}
}

func TestStoreRefusals(t *testing.T) {
	h := newHarness(t, tinyLayout())
	h.stored("C1", "IN1")
	cases := []struct {
		name string
		r    usecases.StoreRequest
		want error
	}{
		{"unknown asrs", usecases.StoreRequest{AsrsID: "X", ContainerID: "C9", PortID: "IN1"}, usecases.ErrUnknownAsrs},
		{"unknown port", usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: "C9", PortID: "IN9"}, usecases.ErrUnknownPort},
		{"output port", usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: "C9", PortID: "OUT1"}, usecases.ErrWrongPortKind},
		{"no container", usecases.StoreRequest{AsrsID: "ASRS1", PortID: "IN1"}, usecases.ErrInvalidRequest},
		{"already stored", usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: "C1", PortID: "IN1"}, usecases.ErrContainerAlreadyStored},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := h.svc.RequestStore(h.ctx, c.r); !errors.Is(err, c.want) {
				t.Fatalf("RequestStore = %v, want %v", err, c.want)
			}
		})
	}
	h.store("C2", "IN1")
	if _, err := h.svc.RequestStore(h.ctx, usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: "C2", PortID: "IN1"}); !errors.Is(err, usecases.ErrContainerBusy) {
		t.Fatalf("store of a container with an open store = %v", err)
	}
}

func TestAFullAisleRefusesAndWritesNothing(t *testing.T) {
	h := newHarness(t, tinyLayout())
	h.store("A", "IN1") // back S2
	h.store("B", "IN1") // back S1; both fronts now wait for their backs (D2)
	before := len(h.events.All())
	if _, err := h.svc.RequestStore(h.ctx, usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: "C", PortID: "IN1"}); !errors.Is(err, usecases.ErrStorageFull) {
		t.Fatalf("third store = %v, want ErrStorageFull", err)
	}
	if len(h.events.All()) != before {
		t.Fatal("a refused store wrote events")
	}
	if ok, _ := h.jobs.HasOpen(h.ctx, "ASRS1", "C"); ok {
		t.Fatal("a refused store left a job")
	}
}

func TestAHeldAisleRefusesNewJobs(t *testing.T) {
	h := newHarness(t, tinyLayout())
	h.stored("A", "IN1")
	h.up(telegrams.UpTelegram{Type: telegrams.UpUnitState, UnitID: "A1", State: string(equipment.Held)})
	if _, err := h.svc.RequestStore(h.ctx, usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: "B", PortID: "IN1"}); !errors.Is(err, usecases.ErrAisleUnavailable) {
		t.Fatalf("store on a held aisle = %v", err)
	}
	if _, err := h.svc.RequestRetrieve(h.ctx, usecases.RetrieveRequest{AsrsID: "ASRS1", ContainerID: "A"}); !errors.Is(err, usecases.ErrAisleUnavailable) {
		t.Fatalf("retrieve on a held aisle = %v", err)
	}
}

func TestSlowAndFastContainersGoToTheirZones(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.stored("A", "IN1")
	if a.To() != (layout.Position{Aisle: 1, Level: 1, Slot: 2, Depth: 2}) {
		t.Fatalf("a new (SLOW) container went to %s, want the dearest back A1-L1-S2-D2", a.To())
	}
	h.retrieved("A")
	h.stored("A", "IN1")
	h.retrieved("A") // two retrievals within the window: FAST
	f := h.stored("A", "IN1")
	if f.To() != (layout.Position{Aisle: 1, Level: 1, Slot: 1, Depth: 1}) {
		t.Fatalf("a FAST container went to %s, want the cheapest front A1-L1-S1-D1", f.To())
	}
	h.retrieved("A")
	h.clock.Advance(25 * time.Hour) // the window has passed: SLOW again
	s := h.stored("A", "IN1")
	if s.To().Depth != 2 {
		t.Fatalf("after the window the container went to %s, want a back position", s.To())
	}
}

func TestARetrieveDefaultsToItsAislesOutputPort(t *testing.T) {
	h := newHarness(t, twoAisles())
	h.stored("A", "IN2")
	j := h.retrieve("A")
	if j.PortID() != "OUT2" || j.Aisle() != 2 || j.Status() != storagejob.Dispatched || j.From().Aisle != 2 {
		t.Fatalf("retrieve = %+v", j.State())
	}
	c := h.last()
	if c.Type != telegrams.CmdRetrieve || c.PortID != "OUT2" || c.From == nil || *c.From != j.From() || c.To != nil {
		t.Fatalf("command = %+v", c)
	}
}

func TestRetrieveRefusals(t *testing.T) {
	h := newHarness(t, twoAisles())
	h.stored("A", "IN1")
	cases := []struct {
		name string
		r    usecases.RetrieveRequest
		want error
	}{
		{"unknown asrs", usecases.RetrieveRequest{AsrsID: "X", ContainerID: "A"}, usecases.ErrUnknownAsrs},
		{"no container", usecases.RetrieveRequest{AsrsID: "ASRS1"}, usecases.ErrInvalidRequest},
		{"unknown container", usecases.RetrieveRequest{AsrsID: "ASRS1", ContainerID: "Z"}, usecases.ErrUnknownContainer},
		{"unknown port", usecases.RetrieveRequest{AsrsID: "ASRS1", ContainerID: "A", PortID: "OUT9"}, usecases.ErrUnknownPort},
		{"input port", usecases.RetrieveRequest{AsrsID: "ASRS1", ContainerID: "A", PortID: "IN1"}, usecases.ErrWrongPortKind},
		{"another aisle's port", usecases.RetrieveRequest{AsrsID: "ASRS1", ContainerID: "A", PortID: "OUT2"}, usecases.ErrPortUnreachable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := h.svc.RequestRetrieve(h.ctx, c.r); !errors.Is(err, c.want) {
				t.Fatalf("RequestRetrieve = %v, want %v", err, c.want)
			}
		})
	}
	h.retrieve("A")
	if _, err := h.svc.RequestRetrieve(h.ctx, usecases.RetrieveRequest{AsrsID: "ASRS1", ContainerID: "A"}); !errors.Is(err, usecases.ErrContainerBusy) {
		t.Fatalf("a second retrieve = %v", err)
	}
}

func TestABlockedRetrieveReshufflesSilentlyFirst(t *testing.T) {
	h := newHarness(t, tinyLayout())
	h.stored("A", "IN1") // S2-D2
	h.stored("B", "IN1") // S1-D2
	h.stored("C", "IN1") // S2-D1 blocks A
	h.down.Reset()
	before := len(h.events.All())
	r := h.retrieve("A")
	move := h.last()
	if move.Type != telegrams.CmdMove || move.ContainerID != "C" || *move.From != (layout.Position{Aisle: 1, Level: 1, Slot: 2, Depth: 1}) ||
		*move.To != (layout.Position{Aisle: 1, Level: 1, Slot: 1, Depth: 1}) || move.PortID != "" {
		t.Fatalf("reshuffle command = %+v", move)
	}
	if h.job(r.ID()).Status() != storagejob.Queued {
		t.Fatal("the retrieve must wait for its reshuffle")
	}
	h.ackLast()
	h.done(move.JobID)
	if c := h.last(); c.Type != telegrams.CmdRetrieve || c.JobID != r.ID() {
		t.Fatalf("after the reshuffle the retrieve must be dispatched, got %+v", c)
	}
	for _, e := range h.events.All()[before:] {
		if _, ok := e.(capacity.AsrsCapacityChanged); !ok {
			t.Fatalf("a reshuffle published %T (only capacity may change)", e)
		}
	}
	if j := h.job(move.JobID); j.Status() != storagejob.Done || j.External() {
		t.Fatalf("reshuffle job = %+v", j.State())
	}
}

func TestARetrieveWithoutRoomToReshuffleFails(t *testing.T) {
	l := tinyLayout()
	l.Slots = 1
	h := newHarness(t, l)
	h.stored("A", "IN1") // S1-D2
	h.stored("B", "IN1") // S1-D1 blocks A, no other slot
	r := h.retrieve("A")
	if got := h.job(r.ID()); got.Status() != storagejob.Failed || got.Reason() != storagejob.ReasonNoReshufflePosition {
		t.Fatalf("retrieve = %+v", got.State())
	}
	if fs := eventsOf[storagejob.StorageJobFailed](h); len(fs) != 1 || fs[0].Reason != storagejob.ReasonNoReshufflePosition {
		t.Fatalf("failures = %+v", fs)
	}
	if _, err := h.svc.RequestRetrieve(h.ctx, usecases.RetrieveRequest{AsrsID: "ASRS1", ContainerID: "A"}); err != nil {
		t.Fatalf("A is free again after the failure: %v", err)
	}
}

func TestTwoStoresOfOneContainerViaDifferentAislesAdmitOnlyOne(t *testing.T) {
	h := newHarness(t, twoAisles())
	h.store("A", "IN1")
	_, err := h.svc.RequestStore(h.ctx, usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: "A", PortID: "IN2"})
	if !errors.Is(err, usecases.ErrContainerBusy) {
		t.Fatalf("second store of A via another aisle = %v, want ErrContainerBusy", err)
	}
	if n := len(h.down.Sent("ASRS1")); n != 1 {
		t.Fatalf("%d commands sent, want exactly one STORE", n)
	}
	// the refused unit of work left no reservation in aisle 2: its 4 positions are all free.
	j := h.store("B", "IN2")
	if j.Status() != storagejob.Dispatched || j.To().Aisle != 2 {
		t.Fatalf("B = %+v", j.State())
	}
	if caps := eventsOf[capacity.AsrsCapacityChanged](h); caps[len(caps)-1].PositionsFree != 6 {
		t.Fatalf("positions free = %d, want 6 (two reservations only)", caps[len(caps)-1].PositionsFree)
	}
}

func TestAStoreOfAContainerStillPresentedOnAPortIsRefusedWithAUsecaseError(t *testing.T) {
	h := newHarness(t, tinyLayout())
	h.stored("A", "IN1")
	r := h.retrieve("A")
	h.ackLast()
	h.done(r.ID())
	h.arrive("OUT1", "A") // job DONE, pickup not confirmed
	if h.job(r.ID()).Status() != storagejob.Done {
		t.Fatal("retrieve not DONE")
	}
	_, err := h.svc.RequestStore(h.ctx, usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: "A", PortID: "IN1"})
	if !errors.Is(err, usecases.ErrContainerBusy) && !errors.Is(err, usecases.ErrContainerAlreadyStored) {
		t.Fatalf("RequestStore = %v, want a usecases refusal", err)
	}
}
