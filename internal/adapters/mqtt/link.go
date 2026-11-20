package mqtt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/asrs-control/internal/adapters/telegram"
	"github.com/IQVO/asrs-control/internal/application/telegrams"
	"github.com/IQVO/asrs-control/internal/domain/layout"
)

const (
	RetransmitAfter = 2 * time.Second
	MaxRetransmits  = 3
	retransmitTick  = 250 * time.Millisecond
	reasonNotReady  = "NOT_READY"
	reasonMalformed = "MALFORMED"
)

// Uplink is the application side of a link (usecases.Service).
type Uplink interface {
	HandleUp(ctx context.Context, asrsID string, t telegrams.UpTelegram) (string, error)
	HandleAck(ctx context.Context, asrsID, ackOf string, accepted bool, reason string) error
	HandleExpired(ctx context.Context, asrsID, telegramID string) error
}

// Link is the WCS side of one AS/RS's telegram protocol. Two clocks: now (asrs-control's
// clock) stamps what it publishes; wall drives the 2 s retransmit timer, a property of the wire.
type Link struct {
	asrsID string
	up     Uplink
	retx   *telegram.Retransmitter
	inbox  *inbox
	now    func() time.Time
	wall   func() time.Time
	newID  func() string
	log    *slog.Logger
	pub    func(topic string, payload []byte) error
	client pahomqtt.Client
	start  sync.Once
	// tracer starts one span per telegram (link_tracing.go); retransmits counts resends.
	tracer      trace.Tracer
	retransmits RetransmitCounter
}

// NewLink connects the link's client and subscribes the up topic on every (re)connection.
// Telegrams that arrive before Start wait in the inbox.
func NewLink(brokerURL, clientID, asrsID string, up Uplink, now func() time.Time, log *slog.Logger, opts ...Option) (*Link, error) {
	l := newLink(asrsID, up, now, log, opts...)
	c, err := connect(brokerURL, clientID, func(c pahomqtt.Client) {
		if err := subscribe(c, telegram.UpTopic(asrsID), l.handler()); err != nil {
			log.Warn("mqtt subscribe failed; retried on the next reconnect", "asrs", asrsID, "error", err)
		}
	})
	if err != nil {
		return nil, err
	}
	l.client = c
	l.pub = func(topic string, b []byte) error {
		if !c.IsConnectionOpen() {
			return errors.New("mqtt client disconnected") // tracked already: the retransmitter and expiry still run
		}
		return publish(c, topic, b)
	}
	return l, nil
}

func newLink(asrsID string, up Uplink, now func() time.Time, log *slog.Logger, opts ...Option) *Link {
	l := &Link{asrsID: asrsID, up: up, retx: telegram.NewRetransmitter(RetransmitAfter, MaxRetransmits),
		inbox: newInbox(InboxSize, "asrs "+asrsID, log), now: now, wall: time.Now, newID: uuid.NewString, log: log,
		tracer: otel.Tracer(tracerName), retransmits: noRetransmits{}}
	for _, o := range opts {
		o(l)
	}
	return l
}

// handler is paho's message handler: it only enqueues.
func (l *Link) handler() pahomqtt.MessageHandler {
	return func(_ pahomqtt.Client, m pahomqtt.Message) {
		payload := m.Payload()
		l.inbox.offer(func(ctx context.Context) { l.handle(ctx, payload) })
	}
}

// Start runs the worker and the retransmit ticker until ctx ends.
func (l *Link) Start(ctx context.Context) {
	l.start.Do(func() { l.startWorkers(ctx) })
}

func (l *Link) startWorkers(ctx context.Context) {
	go l.inbox.drain(ctx)
	go l.runRetransmit(ctx)
}

// Send publishes commands after their unit of work committed. Each is tracked BEFORE it is
// published, since its ACK can be handled before Publish returns. Two commands of one aisle
// never race: the aisle has one job in flight. A CANCEL committed in the same instant as its
// job's dispatch may overtake it; the PLC then refuses it UNKNOWN_JOB and the job runs on.
// The send carries ctx (the use case's, after commit) so each telegram's span joins its trace.
func (l *Link) Send(ctx context.Context, cmds ...telegrams.Command) {
	for _, c := range cmds {
		l.send(ctx, toTelegram(c, l.now()), true)
	}
}

func (l *Link) send(ctx context.Context, t telegram.Telegram, track bool) {
	l.publish(ctx, t, track, false)
}

// resend publishes a telegram the PLC did not acknowledge in time, counted as a retransmit.
func (l *Link) resend(ctx context.Context, t telegram.Telegram) {
	l.retransmits.TelegramRetransmitted(ctx, l.asrsID, string(t.Type))
	l.publish(ctx, t, false, true)
}

func (l *Link) publish(ctx context.Context, t telegram.Telegram, track, retransmit bool) {
	_, span := l.startPublish(ctx, t, retransmit)
	defer span.End()
	b, err := telegram.Encode(t)
	if err != nil {
		failSpan(span, err)
		l.log.ErrorContext(ctx, "cannot encode telegram", "asrs", l.asrsID, "type", t.Type, "error", err)
		return
	}
	if track {
		l.retx.Track(t, l.wall())
	}
	if err := l.pub(telegram.DownTopic(l.asrsID), b); err != nil {
		failSpan(span, err)
		l.log.WarnContext(ctx, "mqtt publish failed; the retransmitter retries", "telegramId", t.ID, "error", err)
	}
}

// handle runs on the worker: one up-topic message, inside its process span.
func (l *Link) handle(ctx context.Context, payload []byte) {
	ctx, span := l.startProcess(ctx)
	defer span.End()
	t, err := telegram.Decode(payload)
	tagTelegram(span, t)
	switch {
	case err != nil:
		err = l.refuseMalformed(ctx, t, err)
	case t.Type == telegram.Ack:
		err = l.onAck(ctx, t)
	case !telegram.IsUp(t.Type):
		err = l.refuseMalformed(ctx, t, fmt.Errorf("MALFORMED: %s is not an up telegram", t.Type))
	default:
		err = l.onUp(ctx, t)
	}
	if err != nil {
		failSpan(span, err)
	}
}

func (l *Link) refuseMalformed(ctx context.Context, t telegram.Telegram, err error) error {
	l.log.WarnContext(ctx, "malformed up telegram", "asrs", l.asrsID, "telegramId", t.ID, "error", err)
	if t.ID != "" {
		l.send(ctx, telegram.NewAck(t, errors.New(reasonMalformed), l.newID(), l.now()), false)
	}
	return err
}

func (l *Link) onUp(ctx context.Context, t telegram.Telegram) error {
	ackID, err := l.up.HandleUp(ctx, l.asrsID, toUp(t))
	if err != nil {
		l.log.ErrorContext(ctx, "up telegram not applied; not acknowledged, the PLC retransmits it", "telegramId", t.ID, "type", t.Type, "error", err)
		return err
	}
	l.send(ctx, telegram.NewAck(t, nil, ackID, l.now()), false)
	return nil
}

func (l *Link) onAck(ctx context.Context, t telegram.Telegram) error {
	orig, tracked := l.retx.Ack(t.AckOf)
	if !tracked {
		return nil
	}
	accepted := *t.Accepted
	if !accepted && t.Reason == reasonNotReady {
		l.retx.Track(orig, l.wall())
		return nil
	}
	if orig.Type == telegram.StateRequest {
		return nil
	}
	if err := l.up.HandleAck(ctx, l.asrsID, t.AckOf, accepted, t.Reason); err != nil {
		l.log.ErrorContext(ctx, "ACK not applied; the command is resent so the PLC answers again", "telegramId", t.AckOf, "error", err)
		l.retx.Track(orig, l.wall())
		return err
	}
	return nil
}

func (l *Link) runRetransmit(ctx context.Context) {
	tick := time.NewTicker(retransmitTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			l.retransmitDue(ctx)
		}
	}
}

// retransmitDue resends due telegrams and queues each expiry for the worker. The ticker is not a
// paho callback, so it may wait for room: an expiry is already marked in the retransmitter and
// would never be reported again if it were dropped.
func (l *Link) retransmitDue(ctx context.Context) {
	resend, expired := l.retx.Due(l.wall())
	for _, t := range resend {
		l.resend(ctx, t)
	}
	for _, t := range expired {
		l.inbox.put(ctx, func(ctx context.Context) { l.expire(ctx, t) })
	}
}

func (l *Link) expire(ctx context.Context, t telegram.Telegram) {
	if !l.retx.Forget(t.ID) {
		return // acknowledged in the meantime
	}
	l.log.Warn("telegram never acknowledged", "asrs", l.asrsID, "telegramId", t.ID, "type", t.Type)
	if t.Type == telegram.StateRequest {
		return
	}
	if err := l.up.HandleExpired(ctx, l.asrsID, t.ID); err != nil {
		l.log.Error("expiry not applied", "telegramId", t.ID, "error", err)
	}
}

// Connected reports whether the client is connected.
func (l *Link) Connected() bool { return l.client != nil && l.client.IsConnectionOpen() }

// Close disconnects.
func (l *Link) Close() {
	if l.client != nil {
		l.client.Disconnect(250)
	}
}

func toTelegram(c telegrams.Command, at time.Time) telegram.Telegram {
	return telegram.Telegram{ID: c.TelegramID, Type: telegram.Type(c.Type), Timestamp: at, JobID: c.JobID,
		ContainerID: c.ContainerID, PortID: c.PortID, From: position(c.From), To: position(c.To)}
}

func position(p *layout.Position) *telegram.Position {
	if p == nil {
		return nil
	}
	return &telegram.Position{Aisle: p.Aisle, Level: p.Level, Slot: p.Slot, Depth: p.Depth}
}

func toUp(t telegram.Telegram) telegrams.UpTelegram {
	return telegrams.UpTelegram{TelegramID: t.ID, Type: string(t.Type), JobID: t.JobID, ContainerID: t.ContainerID, PortID: t.PortID,
		Reason: t.Reason, UnitID: t.UnitID, State: t.State, Timestamp: t.Timestamp}
}

// Links is every AS/RS's link; it is the ports.Downlink.
type Links map[string]*Link

func (ls Links) Send(ctx context.Context, asrsID string, cmds ...telegrams.Command) {
	l, ok := ls[asrsID]
	if !ok {
		slog.ErrorContext(ctx, "no MQTT link for AS/RS; commands dropped", "asrs", asrsID, "commands", len(cmds))
		return
	}
	l.Send(ctx, cmds...)
}

// Connected is the /readyz check: every link must be connected.
func (ls Links) Connected() error {
	for id, l := range ls {
		if !l.Connected() {
			return fmt.Errorf("mqtt link %s is not connected", id)
		}
	}
	return nil
}
