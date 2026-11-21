package mqtt

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/asrs-control/internal/adapters/telegram"
)

// tracerName is the instrumentation scope of the telegram spans (ADR 0004).
const tracerName = "github.com/IQVO/asrs-control/internal/adapters/mqtt"

// Span attributes of a telegram: ids and types only, never a container, a position or any
// other payload field.
const (
	attrAsrsID     = attribute.Key("asrs.id")
	attrTelegramID = attribute.Key("asrs.telegram.id")
	attrType       = attribute.Key("asrs.telegram.type")
	attrRetransmit = attribute.Key("asrs.telegram.retransmit")
)

// RetransmitCounter counts the down telegrams the link resends (telemetry.Metrics implements it).
type RetransmitCounter interface {
	TelegramRetransmitted(ctx context.Context, asrsID, telegramType string)
}

type noRetransmits struct{}

func (noRetransmits) TelegramRetransmitted(context.Context, string, string) {}

// Option customises a Link.
type Option func(*Link)

// WithRetransmitCounter installs the counter of resent telegrams; nil keeps the no-op.
func WithRetransmitCounter(c RetransmitCounter) Option {
	return func(l *Link) {
		if c != nil {
			l.retransmits = c
		}
	}
}

// startPublish opens the producer span of one down telegram: "<down topic> publish", a child of
// ctx (the use case's request or the up telegram the ACK answers).
func (l *Link) startPublish(ctx context.Context, t telegram.Telegram, retransmit bool) (context.Context, trace.Span) {
	topic := telegram.DownTopic(l.asrsID)
	return l.tracer.Start(ctx, topic+" publish", trace.WithSpanKind(trace.SpanKindProducer), trace.WithAttributes(
		semconv.MessagingSystemKey.String("mqtt"), semconv.MessagingDestinationName(topic), semconv.MessagingOperationName("publish"),
		attrAsrsID.String(l.asrsID), attrTelegramID.String(t.ID), attrType.String(string(t.Type)), attrRetransmit.Bool(retransmit)))
}

// startProcess opens the consumer span of one up-topic message: "<up topic> process". Its
// telegram id and type are added once the payload is decoded (tagTelegram).
func (l *Link) startProcess(ctx context.Context) (context.Context, trace.Span) {
	topic := telegram.UpTopic(l.asrsID)
	return l.tracer.Start(ctx, topic+" process", trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(
		semconv.MessagingSystemKey.String("mqtt"), semconv.MessagingDestinationName(topic), semconv.MessagingOperationName("process"),
		attrAsrsID.String(l.asrsID)))
}

func tagTelegram(span trace.Span, t telegram.Telegram) {
	if t.ID != "" {
		span.SetAttributes(attrTelegramID.String(t.ID))
	}
	if t.Type != "" {
		span.SetAttributes(attrType.String(string(t.Type)))
	}
}

func failSpan(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
