// Package mqtt connects asrs-control to the broker: one link per AS/RS speaking telegram
// protocol v1 (ADR 0002).
package mqtt

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// InboxSize is how many tasks one link buffers while its worker is busy.
const InboxSize = 1024

// clientOptions: OrderMatters is on, so paho calls the handler one message at a time in
// arrival order on its own goroutine; the handler must never block (it only enqueues).
func clientOptions(brokerURL, clientID string, onConnect func(pahomqtt.Client)) *pahomqtt.ClientOptions {
	return pahomqtt.NewClientOptions().AddBroker(brokerURL).SetClientID(clientID).
		SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(2 * time.Second).
		SetOrderMatters(true).SetOnConnectHandler(onConnect)
}

// connect opens a client; onConnect runs after every (re)connection, so subscriptions of the
// clean session are made again there.
func connect(brokerURL, clientID string, onConnect func(pahomqtt.Client)) (pahomqtt.Client, error) {
	c := pahomqtt.NewClient(clientOptions(brokerURL, clientID, onConnect))
	tok := c.Connect()
	if !tok.WaitTimeout(15*time.Second) || tok.Error() != nil {
		c.Disconnect(0)
		return nil, fmt.Errorf("mqtt connect %s as %s: %v", brokerURL, clientID, tok.Error())
	}
	return c, nil
}

func publish(c pahomqtt.Client, topic string, payload []byte) error {
	tok := c.Publish(topic, 1, false, payload)
	if !tok.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("mqtt publish %s: timeout", topic)
	}
	return tok.Error()
}

func subscribe(c pahomqtt.Client, topic string, h pahomqtt.MessageHandler) error {
	tok := c.Subscribe(topic, 1, h)
	if !tok.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("mqtt subscribe %s: timeout", topic)
	}
	return tok.Error()
}

// task is one unit of work for a link's worker.
type task func(ctx context.Context)

// inbox is the queue between paho (and the retransmit ticker) and the link's single worker.
type inbox struct {
	ch   chan task
	log  *slog.Logger
	link string
}

func newInbox(size int, link string, log *slog.Logger) *inbox {
	return &inbox{ch: make(chan task, size), log: log, link: link}
}

// offer enqueues without blocking. A full inbox drops the task: an up telegram is retransmitted
// by the PLC, an expiry is recomputed by nothing, so the drop is logged at ERROR.
func (b *inbox) offer(t task) {
	select {
	case b.ch <- t:
	default:
		b.log.Error("inbox full; task dropped", "link", b.link, "capacity", cap(b.ch))
	}
}

// drain runs queued tasks in order until ctx ends.
func (b *inbox) drain(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-b.ch:
			t(ctx)
		}
	}
}
