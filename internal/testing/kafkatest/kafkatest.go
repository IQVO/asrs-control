//go:build integration

// Package kafkatest boots ONE Kafka broker per test binary (testcontainers; topics auto-create).
package kafkatest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
)

var (
	once     sync.Once
	brokers  []string
	errStart error
)

// Brokers starts the broker once and returns its addresses.
func Brokers(t *testing.T) []string {
	t.Helper()
	once.Do(func() {
		ctx := context.Background()
		c, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1", tckafka.WithClusterID("asrs-control-itest"))
		if err != nil {
			errStart = err
			return
		}
		brokers, errStart = c.Brokers(ctx)
	})
	if errStart != nil {
		t.Fatalf("start kafka: %v", errStart)
	}
	return brokers
}

// Message is one record read back.
type Message struct {
	Key     string
	Value   []byte
	Headers map[string]string
}

// ReadAll reads topic partition 0 from the start until until(read) is true or timeout passes.
func ReadAll(t *testing.T, topic string, until func([]Message) bool, timeout time.Duration) []Message {
	t.Helper()
	r := kafkago.NewReader(kafkago.ReaderConfig{Brokers: Brokers(t), Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20})
	defer func() { _ = r.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var out []Message
	for !until(out) {
		m, err := r.ReadMessage(ctx)
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("read %s: %d messages before the timeout, the condition never held", topic, len(out))
		}
		if err != nil {
			time.Sleep(200 * time.Millisecond) // the topic may not exist yet
			continue
		}
		h := map[string]string{}
		for _, x := range m.Headers {
			h[x.Key] = string(x.Value)
		}
		out = append(out, Message{Key: string(m.Key), Value: m.Value, Headers: h})
	}
	return out
}
