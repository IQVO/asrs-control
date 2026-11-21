package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/memory"
	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/equipment"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
	"github.com/IQVO/asrs-control/internal/domain/storagemap"
)

func TestADuplicateUpTelegramIsAppliedOnceAndGetsTheSameAck(t *testing.T) {
	h := newHarness(t, tinyLayout())
	j := h.store("A", "IN1")
	h.ackLast()
	plcTime := t0.Add(time.Hour)
	up := telegrams.UpTelegram{TelegramID: "plc-1", Type: telegrams.UpJobDone, JobID: j.ID(), Timestamp: plcTime}
	first, err := h.svc.HandleUp(h.ctx, "ASRS1", up)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.svc.HandleUp(h.ctx, "ASRS1", up)
	if err != nil || first == "" || first != second {
		t.Fatalf("ACK ids %q / %q (%v), want one stable id", first, second, err)
	}
	stored := eventsOf[storagejob.ContainerStored](h)
	if len(stored) != 1 || !stored[0].PLCTime.Equal(plcTime) || !stored[0].At.Equal(t0) {
		t.Fatalf("ContainerStored = %+v, want one, at asrs-control's clock with the PLC time beside it", stored)
	}
}

func TestUpTelegramsThatMatchNothingAreAcknowledgedAndIgnored(t *testing.T) {
	h := newHarness(t, tinyLayout())
	for _, up := range []telegrams.UpTelegram{
		{TelegramID: "x1", Type: telegrams.UpJobDone, JobID: "sj-ghost"},
		{TelegramID: "x2", Type: telegrams.UpUnitState, UnitID: "A7", State: "HELD"},
		{TelegramID: "x3", Type: telegrams.UpPortArrival, PortID: "IN1"},
		{TelegramID: "x4", Type: "SOMETHING_ELSE"},
	} {
		ack, err := h.svc.HandleUp(h.ctx, "ASRS1", up)
		if err != nil || ack == "" {
			t.Fatalf("%s: ack %q, %v", up.Type, ack, err)
		}
		if again, _ := h.svc.HandleUp(h.ctx, "ASRS1", up); again != ack {
			t.Fatalf("%s: a duplicate got ACK %q, want %q", up.Type, again, ack)
		}
	}
	if n := len(h.events.All()); n != 0 {
		t.Fatalf("%d events from telegrams that match nothing", n)
	}
	if _, err := h.svc.HandleUp(h.ctx, "NOPE", telegrams.UpTelegram{TelegramID: "x5", Type: telegrams.UpJobDone}); !errors.Is(err, usecases.ErrUnknownAsrs) {
		t.Fatalf("unknown AS/RS = %v", err)
	}
}

func TestStrayTelegramsForKnownThingsChangeNothing(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.store("A", "IN1")
	q := h.store("B", "IN1")
	h.done(q.ID())                                                                                  // JOB_DONE for a job the PLC never got
	h.up(telegrams.UpTelegram{Type: telegrams.UpJobFailed, JobID: a.ID(), Reason: "PORT_OCCUPIED"}) // fails A (it is open)
	h.up(telegrams.UpTelegram{Type: telegrams.UpJobFailed, JobID: a.ID(), Reason: "PORT_OCCUPIED"}) // a second outcome is ignored
	h.arrive("OUT1", "Z")                                                                           // no retrieve awaits OUT1
	h.up(telegrams.UpTelegram{Type: telegrams.UpUnitState, UnitID: "A1", State: "BROKEN"})          // unknown state
	if got := h.job(q.ID()); got.Status() != storagejob.Dispatched {
		t.Fatalf("B = %s, want DISPATCHED (dispatched after A failed, its stray JOB_DONE ignored)", got.Status())
	}
	if n := len(eventsOf[storagejob.StorageJobFailed](h)); n != 1 {
		t.Fatalf("%d failures, want 1", n)
	}
	if n := len(eventsOf[storagejob.ContainerPresentedAtPort](h)) + len(eventsOf[equipment.EquipmentStateChanged](h)); n != 0 {
		t.Fatalf("%d presentation or state events from stray telegrams", n)
	}
}

func TestJobFailedEndsTheJobWithTheMappedReasonAndFreesTheUnit(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.store("A", "IN1")
	b := h.store("B", "IN1")
	h.ackLast()
	h.up(telegrams.UpTelegram{Type: telegrams.UpJobFailed, JobID: a.ID(), Reason: "CONTAINER_NOT_AT_PORT"})
	got := h.job(a.ID())
	if got.Status() != storagejob.Failed || got.Reason() != storagejob.ReasonContainerNotAtPort || got.PLCReason() != "CONTAINER_NOT_AT_PORT" {
		t.Fatalf("A = %+v", got.State())
	}
	fs := eventsOf[storagejob.StorageJobFailed](h)
	if len(fs) != 1 || fs[0].JobID != a.ID() || fs[0].PLCTime.IsZero() {
		t.Fatalf("failures = %+v", fs)
	}
	if h.job(b.ID()).Status() != storagejob.Dispatched {
		t.Fatal("the next job must run after a JOB_FAILED")
	}
}

func TestAHeldUnitFailsTheQueuedJobsAndKeepsTheOneInThePLC(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.store("A", "IN1")
	b := h.store("B", "IN1")
	h.ackLast()
	h.up(telegrams.UpTelegram{Type: telegrams.UpUnitState, UnitID: "A1", State: "HELD"})
	if h.job(a.ID()).Status() != storagejob.Acknowledged {
		t.Fatal("the job inside the PLC must stay")
	}
	if got := h.job(b.ID()); got.Status() != storagejob.Failed || got.Reason() != storagejob.ReasonEquipmentFault {
		t.Fatalf("queued B = %+v, want FAILED EQUIPMENT_FAULT", got.State())
	}
	changes := eventsOf[equipment.EquipmentStateChanged](h)
	if len(changes) != 1 || changes[0].State != equipment.Held || changes[0].Previous != equipment.Idle || changes[0].UnitID != "A1" || changes[0].Aisle != 1 {
		t.Fatalf("state changes = %+v", changes)
	}
	caps := eventsOf[capacity.AsrsCapacityChanged](h)
	if last := caps[len(caps)-1]; last.UnitsAvailable != 0 || last.UnitsTotal != 1 {
		t.Fatalf("capacity after HELD = %+v", last)
	}
	h.up(telegrams.UpTelegram{Type: telegrams.UpUnitState, UnitID: "A1", State: "IDLE"})
	h.done(a.ID())
	if h.job(a.ID()).Status() != storagejob.Done {
		t.Fatal("the held job did not finish after the fault cleared")
	}
	if n := len(eventsOf[equipment.EquipmentStateChanged](h)); n != 2 {
		t.Fatalf("%d state changes, want 2", n)
	}
}

func TestANoReadArrivalIsPresentedWithItsScanResult(t *testing.T) {
	h := newHarness(t, tinyLayout())
	h.stored("A", "IN1")
	r := h.retrieve("A")
	h.ackLast()
	h.done(r.ID())
	h.arrive("OUT1", "")
	p := eventsOf[storagejob.ContainerPresentedAtPort](h)
	if len(p) != 1 || p[0].Scan != storagejob.ScanNoRead || p[0].ContainerID != "A" || p[0].ScannedContainerID != "" {
		t.Fatalf("presented = %+v", p)
	}
}

func TestAJobDoneTheMapCannotApplyFailsTheJob(t *testing.T) {
	h := newHarness(t, tinyLayout())
	h.stored("A", "IN1")
	r := h.retrieve("A")
	h.ackLast()
	maps := memory.NewStorageMaps(h.db)
	m, err := maps.Get(h.ctx, tinyLayout(), 1)
	if err != nil {
		t.Fatal(err)
	}
	s := m.Snapshot()
	delete(s.Occupied, r.From())
	delete(s.Retrieving, "A")
	broken, err := storagemap.Rehydrate(tinyLayout(), s)
	if err != nil {
		t.Fatal(err)
	}
	_ = maps.Save(h.ctx, "ASRS1", broken)
	h.done(r.ID())
	if got := h.job(r.ID()); got.Status() != storagejob.Failed || got.Reason() != storagejob.ReasonStorageMapMismatch {
		t.Fatalf("retrieve = %+v, want FAILED STORAGE_MAP_MISMATCH", got.State())
	}
}

func TestARefusedCommandFailsTheJobWithItsReason(t *testing.T) {
	h := newHarness(t, tinyLayout())
	j := h.store("A", "IN1")
	if err := h.svc.HandleAck(h.ctx, "ASRS1", j.TelegramID(), false, "INVALID_POSITION"); err != nil {
		t.Fatal(err)
	}
	got := h.job(j.ID())
	if got.Status() != storagejob.Failed || got.Reason() != storagejob.ReasonCommandRefused || got.PLCReason() != "INVALID_POSITION" {
		t.Fatalf("refused job = %+v", got.State())
	}
	if k := h.store("B", "IN1"); k.Status() != storagejob.Dispatched {
		t.Fatal("the unit is free after a refusal")
	}
	if err := h.svc.HandleAck(h.ctx, "ASRS1", "unknown-telegram", true, ""); err != nil {
		t.Fatalf("ACK of an unknown telegram = %v", err)
	}
	if err := h.svc.HandleAck(h.ctx, "ASRS1", j.TelegramID(), true, ""); err != nil || h.job(j.ID()).Status() != storagejob.Failed {
		t.Fatalf("a late ACK changed the failed job: %v", err)
	}
	if err := h.svc.HandleAck(h.ctx, "NOPE", "x", true, ""); !errors.Is(err, usecases.ErrUnknownAsrs) {
		t.Fatalf("unknown AS/RS = %v", err)
	}
}

func TestAnAcceptedCancelEndsTheJobAndARefusedOneLetsItRun(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.store("A", "IN1")
	h.ackLast()
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", a.ID()); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.HandleAck(h.ctx, "ASRS1", h.last().TelegramID, false, "JOB_RUNNING"); err != nil {
		t.Fatal(err)
	}
	if got := h.job(a.ID()); got.Status() != storagejob.Acknowledged || got.CancelTelegramID() != "" {
		t.Fatalf("A after a refused CANCEL = %+v", got.State())
	}
	h.done(a.ID())
	b := h.store("B", "IN1")
	h.ackLast()
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", b.ID()); err != nil {
		t.Fatal(err)
	}
	h.ackLast()
	if got := h.job(b.ID()); got.Status() != storagejob.Cancelled {
		t.Fatalf("B after an accepted CANCEL = %s", got.Status())
	}
	fs := eventsOf[storagejob.StorageJobFailed](h)
	if len(fs) != 1 || fs[0].JobID != b.ID() || fs[0].Reason != storagejob.ReasonCancelled {
		t.Fatalf("failures = %+v", fs)
	}
	if c := h.store("C", "IN1"); c.Status() != storagejob.Dispatched {
		t.Fatal("the unit is free after a cancellation")
	}
}

func TestExpiredTelegrams(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.store("A", "IN1")
	if err := h.svc.HandleExpired(h.ctx, "ASRS1", a.TelegramID()); err != nil {
		t.Fatal(err)
	}
	if got := h.job(a.ID()); got.Status() != storagejob.Failed || got.Reason() != storagejob.ReasonEquipmentUnreachable {
		t.Fatalf("A = %+v, want FAILED EQUIPMENT_UNREACHABLE", got.State())
	}
	b := h.store("B", "IN1")
	h.ackLast()
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", b.ID()); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.HandleExpired(h.ctx, "ASRS1", h.last().TelegramID); err != nil {
		t.Fatal(err)
	}
	if got := h.job(b.ID()); got.Status() != storagejob.Acknowledged {
		t.Fatalf("B after an expired CANCEL = %s, want ACKNOWLEDGED", got.Status())
	}
	if err := h.svc.HandleExpired(h.ctx, "ASRS1", b.TelegramID()); err != nil || h.job(b.ID()).Status() != storagejob.Acknowledged {
		t.Fatalf("expiry of an acknowledged command changed B: %v", err)
	}
	if err := h.svc.HandleExpired(h.ctx, "ASRS1", "never-sent"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.HandleExpired(h.ctx, "NOPE", "x"); !errors.Is(err, usecases.ErrUnknownAsrs) {
		t.Fatalf("unknown AS/RS = %v", err)
	}
}

func TestCancellingAnUnacknowledgedCommandNeverWedgesTheAisle(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.store("A", "IN1") // dispatched, its STORE not acknowledged yet
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", a.ID()); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.HandleAck(h.ctx, "ASRS1", h.last().TelegramID, false, "UNKNOWN_JOB"); err != nil {
		t.Fatal(err)
	}
	if got := h.job(a.ID()); got.Status() != storagejob.Dispatched || got.Acked() {
		t.Fatalf("A after a refused CANCEL = %+v, want DISPATCHED (its STORE is still unanswered)", got.State())
	}
	if err := h.svc.HandleExpired(h.ctx, "ASRS1", a.TelegramID()); err != nil {
		t.Fatal(err)
	}
	if got := h.job(a.ID()); got.Status() != storagejob.Failed || got.Reason() != storagejob.ReasonEquipmentUnreachable {
		t.Fatalf("A after its STORE expired = %+v, want FAILED EQUIPMENT_UNREACHABLE", got.State())
	}
	if b := h.store("B", "IN1"); b.Status() != storagejob.Dispatched {
		t.Fatal("the aisle is wedged: the next store was not dispatched")
	}
}

func TestTheCommandExpiryDecidesAJobWhoseCancelIsStillPending(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.store("A", "IN1")
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", a.ID()); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.HandleExpired(h.ctx, "ASRS1", a.TelegramID()); err != nil {
		t.Fatal(err)
	}
	if got := h.job(a.ID()); got.Status() != storagejob.Failed || got.Reason() != storagejob.ReasonEquipmentUnreachable {
		t.Fatalf("A = %+v, want FAILED EQUIPMENT_UNREACHABLE", got.State())
	}
}

func TestACommandACKDuringAPendingCancelIsRemembered(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.store("A", "IN1")
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", a.ID()); err != nil {
		t.Fatal(err)
	}
	cancelTID := h.last().TelegramID
	if err := h.svc.HandleAck(h.ctx, "ASRS1", a.TelegramID(), true, ""); err != nil {
		t.Fatal(err)
	}
	if got := h.job(a.ID()); got.Status() != storagejob.CancelRequested || !got.Acked() {
		t.Fatalf("A = %+v, want CANCEL_REQUESTED with its ACK recorded", got.State())
	}
	if err := h.svc.HandleAck(h.ctx, "ASRS1", cancelTID, false, "JOB_RUNNING"); err != nil {
		t.Fatal(err)
	}
	if got := h.job(a.ID()); got.Status() != storagejob.Acknowledged {
		t.Fatalf("A after the refused CANCEL = %s, want ACKNOWLEDGED", got.Status())
	}
	if err := h.svc.HandleExpired(h.ctx, "ASRS1", a.TelegramID()); err != nil || h.job(a.ID()).Status() != storagejob.Acknowledged {
		t.Fatalf("a stale expiry of an acknowledged command changed A: %v", err)
	}
}

// settled is what must not move when a duplicate or late telegram arrives.
type settled struct{ events, sent int }

func (h *harness) settled() settled { return settled{len(h.events.All()), len(h.down.Sent("ASRS1"))} }

func TestADuplicateJobDoneAfterDoneIsAnIdempotentNoOp(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.stored("A", "IN1")
	before := h.settled()
	ack, err := h.svc.HandleUp(h.ctx, "ASRS1", telegrams.UpTelegram{TelegramID: "dup-1", Type: telegrams.UpJobDone, JobID: a.ID(), Timestamp: t0})
	if err != nil || ack == "" {
		t.Fatalf("duplicate JOB_DONE: ack %q, %v (want ACK, no error)", ack, err)
	}
	if got := h.settled(); got != before {
		t.Fatalf("duplicate JOB_DONE changed events/commands: %+v -> %+v", before, got)
	}
	if h.job(a.ID()).Status() != storagejob.Done {
		t.Fatalf("job = %s, want DONE", h.job(a.ID()).Status())
	}
}

func TestALateJobDoneAfterACancelledJobIsAnIdempotentNoOp(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.store("A", "IN1")
	h.ackLast()
	if _, err := h.svc.Cancel(h.ctx, "ASRS1", a.ID()); err != nil {
		t.Fatal(err)
	}
	h.ackLast() // the PLC accepts the CANCEL
	if h.job(a.ID()).Status() != storagejob.Cancelled {
		t.Fatalf("job = %s, want CANCELLED", h.job(a.ID()).Status())
	}
	before := h.settled()
	ack, err := h.svc.HandleUp(h.ctx, "ASRS1", telegrams.UpTelegram{TelegramID: "late-1", Type: telegrams.UpJobDone, JobID: a.ID(), Timestamp: t0})
	if err != nil || ack == "" {
		t.Fatalf("late JOB_DONE: ack %q, %v (want ACK, no error)", ack, err)
	}
	if got := h.settled(); got != before {
		t.Fatalf("late JOB_DONE changed events/commands: %+v -> %+v", before, got)
	}
	if h.job(a.ID()).Status() != storagejob.Cancelled {
		t.Fatalf("job = %s, want CANCELLED", h.job(a.ID()).Status())
	}
}

func TestLateAcksAndExpiriesOnAnEndedJobAreNoOps(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.stored("A", "IN1")
	before := h.settled()
	if err := h.svc.HandleAck(h.ctx, "ASRS1", a.TelegramID(), false, "X"); err != nil {
		t.Fatalf("late refusal: %v", err)
	}
	if err := h.svc.HandleExpired(h.ctx, "ASRS1", a.TelegramID()); err != nil {
		t.Fatalf("late expiry: %v", err)
	}
	if h.settled() != before || h.job(a.ID()).Status() != storagejob.Done {
		t.Fatal("a late ACK or expiry changed an ended job")
	}
}

func TestCapacityIsComputedFromMapsUnitsAndOpenJobs(t *testing.T) {
	h := newHarness(t, twoAisles())
	h.store("A", "IN1") // one position reserved in aisle 1, one open job
	h.up(telegrams.UpTelegram{Type: telegrams.UpUnitState, UnitID: "A2", State: string(equipment.Held)})
	caps := eventsOf[capacity.AsrsCapacityChanged](h)
	last := caps[len(caps)-1]
	want := capacity.Snapshot{AsrsID: "ASRS1", UnitsTotal: 2, UnitsAvailable: 1, PositionsTotal: 8, PositionsFree: 7, OpenJobs: 1}
	if last.Snapshot != want {
		t.Fatalf("capacity = %+v, want %+v", last.Snapshot, want)
	}
}

func TestAStrayJobFailedOnAQueuedJobIsIgnored(t *testing.T) {
	h := newHarness(t, tinyLayout())
	h.store("A", "IN1")
	q := h.store("B", "IN1")
	before := h.settled()
	h.up(telegrams.UpTelegram{Type: telegrams.UpJobFailed, JobID: q.ID(), Reason: "PORT_OCCUPIED"})
	if got := h.job(q.ID()); got.Status() != storagejob.Queued {
		t.Fatalf("B = %s, want QUEUED", got.Status())
	}
	if h.settled() != before {
		t.Fatal("a stray JOB_FAILED changed events or commands")
	}
}

func TestALateJobFailedAfterJobDoneDoesNotDropThePortArrival(t *testing.T) {
	h := newHarness(t, tinyLayout())
	h.stored("A", "IN1")
	r := h.retrieve("A")
	h.ackLast()
	h.done(r.ID())
	h.up(telegrams.UpTelegram{Type: telegrams.UpJobFailed, JobID: r.ID(), Reason: "CONTAINER_NOT_AT_PORT"})
	if got := h.job(r.ID()); got.Status() != storagejob.AwaitingArrival {
		t.Fatalf("retrieve = %s, want AWAITING_ARRIVAL", got.Status())
	}
	h.arrive("OUT1", "A")
	if got := h.job(r.ID()); got.Status() != storagejob.Done {
		t.Fatalf("retrieve = %s, want DONE", got.Status())
	}
	if n := len(eventsOf[storagejob.StorageJobFailed](h)); n != 0 {
		t.Fatalf("%d failures, want 0", n)
	}
	if p := eventsOf[storagejob.ContainerPresentedAtPort](h); len(p) != 1 || p[0].Scan != storagejob.ScanRead {
		t.Fatalf("presented = %+v", p)
	}
}

func TestJobDoneBeforeItsAckThenTheLateAckAndExpiryGiveOneOutcome(t *testing.T) {
	h := newHarness(t, tinyLayout())
	a := h.store("A", "IN1")
	h.done(a.ID()) // the PLC's JOB_DONE overtakes the command's ACK
	if h.job(a.ID()).Status() != storagejob.Done {
		t.Fatalf("A = %s, want DONE", h.job(a.ID()).Status())
	}
	before := h.settled()
	if err := h.svc.HandleAck(h.ctx, "ASRS1", a.TelegramID(), true, ""); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.HandleExpired(h.ctx, "ASRS1", a.TelegramID()); err != nil {
		t.Fatal(err)
	}
	if h.settled() != before || h.job(a.ID()).Status() != storagejob.Done {
		t.Fatal("a late ACK or expiry changed the DONE job")
	}
	if n := len(eventsOf[storagejob.ContainerStored](h)); n != 1 {
		t.Fatalf("%d ContainerStored, want 1", n)
	}
	if n := len(eventsOf[storagejob.StorageJobFailed](h)); n != 0 {
		t.Fatalf("%d failures, want 0", n)
	}
}
