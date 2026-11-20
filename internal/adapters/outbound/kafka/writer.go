// Package kafka holds the outbound Kafka adapters. The outbox relay publishes
// through the Writer built here; the topic is chosen per message, never on the
// Writer, so one Writer serves the events and analytics topics.
package kafka

import (
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// NewWriter builds the shared Kafka writer. RequiredAcks is All and the key
// balancer is Hash, so every event of one aggregate lands on one partition in
// order (the aggregate id is the message key). AllowAutoTopicCreation matches
// the broker, which creates business topics on first write. WriteTimeout and
// ReadTimeout bound a stalled broker connection: without them one half-open
// TCP connection made WriteMessages block forever while the outbox relay held
// its row locks — passes went silent and events rotted unpublished (CI e2e,
// 2026-11-20). A timed-out write returns an error, the row's failure is
// recorded and the next pass re-dials. MaxAttempts 2 bounds a dead route:
// kafka-go retries one batch internally up to ten times with the full request
// deadline each, so a broken topic route could hold a single send (and, in id
// order, every row behind it) for 100+ seconds with no error surfaced.
func NewWriter(brokers string) *kafkago.Writer {
	return &kafkago.Writer{
		Addr:                   kafkago.TCP(splitBrokers(brokers)...),
		RequiredAcks:           kafkago.RequireAll,
		Balancer:               &kafkago.Hash{},
		AllowAutoTopicCreation: true,
		WriteTimeout:           10 * time.Second,
		ReadTimeout:            10 * time.Second,
		MaxAttempts:            2,
	}
}

func splitBrokers(s string) []string {
	var out []string
	for _, b := range strings.Split(s, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}
