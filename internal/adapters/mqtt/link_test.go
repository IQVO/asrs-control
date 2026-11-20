package mqtt

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/telegram"
	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/domain/layout"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

type ackCall struct {
	of       string
	accepted bool
	reason   string
}

type fakeUplink struct {
	mu      sync.Mutex
	ups     []telegrams.UpTelegram
	acks    []ackCall
	expired []string
	upErr   error
	ackErr  error
}

func (f *fakeUplink) HandleUp(_ context.Context, _ string, t telegrams.UpTelegram) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ups = append(f.ups, t)
	return "ack-" + t.TelegramID, f.upErr
}

func (f *fakeUplink) HandleAck(_ context.Context, _ string, of string, accepted bool, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acks = append(f.acks, ackCall{of, accepted, reason})
	return f.ackErr
}

func (f *fakeUplink) HandleExpired(_ context.Context, _ string, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expired = append(f.expired, id)
	return nil
}

type rig struct {
	l    *Link
	up   *fakeUplink
	sent []telegram.Telegram
	wall time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{up: &fakeUplink{}, wall: t0}
	r.l = newLink("ASRS1", r.up, func() time.Time { return t0 }, slog.Default())
	r.l.wall = func() time.Time { return r.wall }
	r.l.pub = func(topic string, b []byte) error {
		if topic != "asrs/v1/ASRS1/down" {
			t.Fatalf("published on %s", topic)
		}
		tg, err := telegram.Decode(b)
		if err != nil {
			t.Fatalf("published a malformed telegram: %v", err)
		}
		r.sent = append(r.sent, tg)
		return nil
	}
	return r
}

func (r *rig) raw(b string) { r.l.handle(context.Background(), []byte(b)) }

func (r *rig) deliver(t *testing.T, tg telegram.Telegram) {
	t.Helper()
	tg.Timestamp = t0
	b, err := telegram.Encode(tg)
	if err != nil {
		t.Fatal(err)
	}
	r.l.handle(context.Background(), b)
}

func (r *rig) runQueued() {
	for {
		select {
		case task := <-r.l.inbox.ch:
			task(context.Background())
		default:
			return
		}
	}
}

func TestSendPublishesTheCommandStampedWithAsrsControlsClock(t *testing.T) {
	r := newRig(t)
	to := layout.Position{Aisle: 1, Level: 2, Slot: 3, Depth: 2}
	r.l.Send(context.Background(), telegrams.Command{TelegramID: "t1", Type: telegrams.CmdStore, JobID: "sj-1", ContainerID: "C1", PortID: "IN1", To: &to})
	if len(r.sent) != 1 {
		t.Fatalf("sent %d", len(r.sent))
	}
	s := r.sent[0]
	if s.ID != "t1" || s.Type != telegram.Store || !s.Timestamp.Equal(t0) || s.To == nil || *s.To != (telegram.Position{Aisle: 1, Level: 2, Slot: 3, Depth: 2}) || s.From != nil {
		t.Fatalf("STORE = %+v", s)
	}
}

func TestAnUpTelegramIsAppliedThenAcknowledged(t *testing.T) {
	r := newRig(t)
	r.deliver(t, telegram.Telegram{ID: "u1", Type: telegram.JobFailed, JobID: "sj-1", Reason: "PORT_OCCUPIED"})
	if len(r.up.ups) != 1 || r.up.ups[0].Type != telegrams.UpJobFailed || r.up.ups[0].Reason != "PORT_OCCUPIED" || !r.up.ups[0].Timestamp.Equal(t0) {
		t.Fatalf("uplink got %+v", r.up.ups)
	}
	if len(r.sent) != 1 || r.sent[0].Type != telegram.Ack || r.sent[0].AckOf != "u1" || r.sent[0].ID != "ack-u1" || !*r.sent[0].Accepted {
		t.Fatalf("ACK = %+v", r.sent)
	}
}

func TestAnUpTelegramThatFailsToApplyIsNotAcknowledged(t *testing.T) {
	r := newRig(t)
	r.up.upErr = errors.New("database down")
	r.deliver(t, telegram.Telegram{ID: "u1", Type: telegram.JobDone, JobID: "sj-1"})
	if len(r.sent) != 0 {
		t.Fatalf("acknowledged a telegram that was not applied: %+v", r.sent)
	}
}

func TestMalformedTelegramsAreRefusedWhenTheyHaveAnID(t *testing.T) {
	r := newRig(t)
	r.raw(`{"telegramId":"m1","type":"JOB_DONE","timestamp":"2026-11-20T08:00:00.000Z"}`)
	r.raw(`{"telegramId":"m2","type":"STORE","timestamp":"2026-11-20T08:00:00.000Z","jobId":"j","containerId":"c","portId":"IN1","to":{"aisle":1,"level":1,"slot":1,"depth":1}}`)
	r.raw(`{not json`)
	if len(r.up.ups) != 0 {
		t.Fatal("a malformed telegram reached the application")
	}
	if len(r.sent) != 2 || r.sent[0].AckOf != "m1" || *r.sent[0].Accepted || r.sent[0].Reason != "MALFORMED" || r.sent[1].AckOf != "m2" || r.sent[1].Reason != "MALFORMED" {
		t.Fatalf("NACKs = %+v", r.sent)
	}
}

func TestAnAckOfACommandReachesTheApplicationOnce(t *testing.T) {
	r := newRig(t)
	r.l.Send(context.Background(), telegrams.Command{TelegramID: "t1", Type: telegrams.CmdCancel, JobID: "sj-1"})
	no := false
	r.deliver(t, telegram.Telegram{ID: "a1", Type: telegram.Ack, AckOf: "t1", Accepted: &no, Reason: "JOB_RUNNING"})
	r.deliver(t, telegram.Telegram{ID: "a2", Type: telegram.Ack, AckOf: "t1", Accepted: &no, Reason: "JOB_RUNNING"})
	if len(r.up.acks) != 1 || r.up.acks[0] != (ackCall{"t1", false, "JOB_RUNNING"}) {
		t.Fatalf("acks = %+v", r.up.acks)
	}
	if len(r.sent) != 1 {
		t.Fatal("an ACK was acknowledged")
	}
}

func TestNotReadyIsResentNeverAnOutcome(t *testing.T) {
	r := newRig(t)
	r.l.Send(context.Background(), telegrams.Command{TelegramID: "t1", Type: telegrams.CmdStateRequest})
	r.l.Send(context.Background(), telegrams.Command{TelegramID: "t2", Type: telegrams.CmdCancel, JobID: "sj-1"})
	no := false
	r.deliver(t, telegram.Telegram{ID: "a1", Type: telegram.Ack, AckOf: "t2", Accepted: &no, Reason: "NOT_READY"})
	yes := true
	r.deliver(t, telegram.Telegram{ID: "a2", Type: telegram.Ack, AckOf: "t1", Accepted: &yes})
	if len(r.up.acks) != 0 {
		t.Fatalf("NOT_READY or a STATE_REQUEST ACK reached the application: %+v", r.up.acks)
	}
	r.wall = t0.Add(2 * time.Second)
	r.l.retransmitDue()
	if len(r.sent) != 3 || r.sent[2].ID != "t2" {
		t.Fatalf("the NOT_READY telegram was not resent: %+v", r.sent)
	}
}

func TestAFailedAckIsResentSoThePLCAnswersAgain(t *testing.T) {
	r := newRig(t)
	r.up.ackErr = errors.New("database down")
	r.l.Send(context.Background(), telegrams.Command{TelegramID: "t1", Type: telegrams.CmdCancel, JobID: "sj-1"})
	yes := true
	r.deliver(t, telegram.Telegram{ID: "a1", Type: telegram.Ack, AckOf: "t1", Accepted: &yes})
	r.wall = t0.Add(2 * time.Second)
	r.l.retransmitDue()
	if len(r.sent) != 2 || r.sent[1].ID != "t1" {
		t.Fatalf("the command was not tracked again: %+v", r.sent)
	}
}

func TestAnExpiryRunsOnTheWorkerAndLosesToAnEarlierAck(t *testing.T) {
	r := newRig(t)
	r.l.Send(context.Background(), telegrams.Command{TelegramID: "t1", Type: telegrams.CmdCancel, JobID: "sj-1"})
	r.l.Send(context.Background(), telegrams.Command{TelegramID: "t2", Type: telegrams.CmdCancel, JobID: "sj-2"})
	for i := 1; i <= 4; i++ {
		r.wall = t0.Add(time.Duration(2*i) * time.Second)
		r.l.retransmitDue()
	}
	if len(r.sent) != 8 || len(r.up.expired) != 0 {
		t.Fatalf("sent %d (want 2 + 3x2 resends), expired %v before the worker ran", len(r.sent), r.up.expired)
	}
	yes := true
	r.deliver(t, telegram.Telegram{ID: "a1", Type: telegram.Ack, AckOf: "t1", Accepted: &yes}) // arrives before the worker runs the expiry
	r.runQueued()
	if len(r.up.expired) != 1 || r.up.expired[0] != "t2" || len(r.up.acks) != 1 {
		t.Fatalf("expired %v acks %+v, want only t2 expired and t1 acknowledged", r.up.expired, r.up.acks)
	}
}

func TestLinksRouteByAsrsAndReportConnection(t *testing.T) {
	r := newRig(t)
	ls := Links{"ASRS1": r.l}
	ls.Send(context.Background(), "ASRS1", telegrams.Command{TelegramID: "t1", Type: telegrams.CmdStateRequest})
	ls.Send(context.Background(), "NOPE", telegrams.Command{TelegramID: "t2", Type: telegrams.CmdStateRequest})
	if len(r.sent) != 1 {
		t.Fatalf("sent %d", len(r.sent))
	}
	if err := ls.Connected(); err == nil {
		t.Fatal("a link without a client reports connected")
	}
}

func TestASendWhileDisconnectedIsStillTrackedAndExpires(t *testing.T) {
	r := newRig(t)
	r.l.pub = func(string, []byte) error { return errors.New("disconnected") }
	r.l.Send(context.Background(), telegrams.Command{TelegramID: "t1", Type: telegrams.CmdCancel, JobID: "sj-1"})
	for i := 1; i <= MaxRetransmits+1; i++ {
		r.wall = t0.Add(time.Duration(i) * RetransmitAfter)
		r.l.retransmitDue()
	}
	r.runQueued()
	if len(r.up.expired) != 1 || r.up.expired[0] != "t1" {
		t.Fatalf("expired = %v, want t1 once", r.up.expired)
	}
	r.wall = t0.Add(time.Hour)
	r.l.retransmitDue()
	r.runQueued()
	if len(r.up.expired) != 1 {
		t.Fatalf("expired twice: %v", r.up.expired)
	}
}
