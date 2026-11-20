package mqtt

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/asrs-control/internal/adapters/telegram"
	"github.com/IQVO/asrs-control/internal/application/telegrams"
)

type retransmitCall struct{ asrsID, telegramType string }

type fakeRetransmits struct {
	mu    sync.Mutex
	calls []retransmitCall
}

func (f *fakeRetransmits) TelegramRetransmitted(_ context.Context, asrsID, telegramType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, retransmitCall{asrsID, telegramType})
}

// traced gives the rig a recording tracer: every span the link starts lands in the recorder.
func traced(r *rig) *tracetest.SpanRecorder {
	rec := tracetest.NewSpanRecorder()
	r.l.tracer = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer("test")
	return rec
}

func attrs(s sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	out := map[attribute.Key]attribute.Value{}
	for _, kv := range s.Attributes() {
		out[kv.Key] = kv.Value
	}
	return out
}

func spanNamed(t *testing.T, rec *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	t.Helper()
	var out []sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out
}

func TestEveryDownTelegramIsAProducerSpanCarryingItsIDAndType(t *testing.T) {
	r := newRig(t)
	rec := traced(r)
	r.l.Send(context.Background(), telegrams.Command{TelegramID: "t1", Type: telegrams.CmdCancel, JobID: "sj-1"})
	spans := spanNamed(t, rec, "asrs/v1/ASRS1/down publish")
	if len(spans) != 1 {
		t.Fatalf("publish spans = %d, want 1 (all: %d)", len(spans), len(rec.Ended()))
	}
	s := spans[0]
	a := attrs(s)
	if s.SpanKind() != trace.SpanKindProducer || a["asrs.telegram.id"].AsString() != "t1" || a["asrs.telegram.type"].AsString() != "CANCEL" ||
		a["asrs.id"].AsString() != "ASRS1" || a["messaging.system"].AsString() != "mqtt" || a["asrs.telegram.retransmit"].AsBool() {
		t.Fatalf("publish span kind %v attributes %v", s.SpanKind(), a)
	}
	for _, k := range []attribute.Key{"asrs.container.id", "container_id"} {
		if _, ok := a[k]; ok {
			t.Errorf("span carries %s: telegram spans carry ids and types only", k)
		}
	}
}

func TestEveryUpTelegramIsAConsumerSpanAndItsAckIsAChildOfIt(t *testing.T) {
	r := newRig(t)
	rec := traced(r)
	r.deliver(t, telegram.Telegram{ID: "u1", Type: telegram.JobFailed, JobID: "sj-1", Reason: "PORT_OCCUPIED"})
	process := spanNamed(t, rec, "asrs/v1/ASRS1/up process")
	if len(process) != 1 {
		t.Fatalf("process spans = %d, want 1", len(process))
	}
	p := process[0]
	a := attrs(p)
	if p.SpanKind() != trace.SpanKindConsumer || a["asrs.telegram.id"].AsString() != "u1" || a["asrs.telegram.type"].AsString() != "JOB_FAILED" {
		t.Fatalf("process span kind %v attributes %v", p.SpanKind(), a)
	}
	if p.Status().Code == codes.Error {
		t.Fatalf("an applied telegram's span is an error: %v", p.Status())
	}
	acks := spanNamed(t, rec, "asrs/v1/ASRS1/down publish")
	if len(acks) != 1 || acks[0].Parent().SpanID() != p.SpanContext().SpanID() {
		t.Fatalf("the ACK publish span must be a child of the telegram's process span: %d spans", len(acks))
	}
	if attrs(acks[0])["asrs.telegram.type"].AsString() != "ACK" {
		t.Fatalf("ack span attributes %v", attrs(acks[0]))
	}
}

func TestAnUpTelegramThatFailsMarksItsSpanAsAnError(t *testing.T) {
	r := newRig(t)
	rec := traced(r)
	r.raw(`{"telegramId":"m1","type":"JOB_DONE","timestamp":"2026-11-20T08:00:00.000Z"}`)
	r.up.upErr = context.DeadlineExceeded
	r.deliver(t, telegram.Telegram{ID: "u2", Type: telegram.JobDone, JobID: "sj-1"})
	process := spanNamed(t, rec, "asrs/v1/ASRS1/up process")
	if len(process) != 2 {
		t.Fatalf("process spans = %d, want 2", len(process))
	}
	for _, p := range process {
		if p.Status().Code != codes.Error {
			t.Errorf("span of %v: status %v, want Error", attrs(p)["asrs.telegram.id"], p.Status())
		}
	}
}

func TestARetransmitIsCountedAndSpannedAsARetransmit(t *testing.T) {
	r := newRig(t)
	rec := traced(r)
	retx := &fakeRetransmits{}
	r.l.retransmits = retx
	r.l.Send(context.Background(), telegrams.Command{TelegramID: "t1", Type: telegrams.CmdCancel, JobID: "sj-1"})
	r.wall = t0.Add(2 * time.Second)
	r.l.retransmitDue(context.Background())
	if len(retx.calls) != 1 || retx.calls[0] != (retransmitCall{"ASRS1", "CANCEL"}) {
		t.Fatalf("retransmits = %+v, want one CANCEL of ASRS1", retx.calls)
	}
	spans := spanNamed(t, rec, "asrs/v1/ASRS1/down publish")
	if len(spans) != 2 || attrs(spans[0])["asrs.telegram.retransmit"].AsBool() || !attrs(spans[1])["asrs.telegram.retransmit"].AsBool() {
		t.Fatalf("want a first send and a retransmit span, got %d", len(spans))
	}
}

func TestWithRetransmitCounterInstallsTheCounter(t *testing.T) {
	retx := &fakeRetransmits{}
	l := newLink("ASRS1", &fakeUplink{}, func() time.Time { return t0 }, nil, WithRetransmitCounter(retx))
	if l.retransmits != retx {
		t.Fatal("WithRetransmitCounter did not install the counter")
	}
	if newLink("ASRS1", &fakeUplink{}, func() time.Time { return t0 }, nil).retransmits == nil {
		t.Fatal("a link without the option must still have a no-op counter")
	}
}
