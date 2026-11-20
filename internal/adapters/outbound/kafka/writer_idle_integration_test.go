//go:build integration

// Reproduces the CI wedge in isolation: the relay's topic-less kafka-go writer
// goes idle ~20s (the e2e's fault-poll window), and the next send wedges inside
// kafka-go's partition-metadata path — before any deadline can apply — while the
// Drain transaction sits idle-in-transaction holding its claimed outbox rows.
// If this test times out, the hypothesis is confirmed; the fix under test must
// make it pass reliably.
package kafka_test

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/kafka"
	"github.com/IQVO/asrs-control/internal/application/outbox"
	"github.com/IQVO/asrs-control/internal/testing/kafkatest"
)

func TestWriterSurvivesIdleThenSend(t *testing.T) {
	brokers := kafkatest.Brokers(t)
	kafkatest.CreateTopic(t, "wedge.itest.events")
	kafkatest.CreateTopic(t, "wedge.itest.analytics")

	sink := kafka.NewRelaySink(brokers)
	t.Cleanup(func() { _ = sink.Close() })
	msg := func(topic string) outbox.Message {
		return outbox.Message{Topic: topic, Key: []byte("k"), Value: []byte(`{}`)}
	}

	// Phase 1: a burst of sends, like the scenario's job traffic.
	burst := time.Now()
	for i := 0; i < 60; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := sink.Send(ctx, msg("wedge.itest.events"), msg("wedge.itest.analytics")); err != nil {
			t.Fatalf("burst send %d: %v", i, err)
		}
		cancel()
	}
	t.Logf("burst of 60 sends took %s", time.Since(burst))

	// Phase 2: the idle window (the e2e polls REST for up to 20s here).
	time.Sleep(20 * time.Second)

	// Phase 3: the first send after idleness must complete within the sink's own
	// deadline. On CI this is where WriteMessages never returned.
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := sink.Send(ctx, msg("wedge.itest.events"), msg("wedge.itest.analytics"))
	if err != nil {
		t.Fatalf("send after idle: %v", err)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("send after idle took %s — the wedge reproduced", d)
	}
	t.Logf("send after idle took %s", time.Since(start))
}
