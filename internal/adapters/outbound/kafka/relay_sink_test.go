package kafka_test

import (
	"context"
	"testing"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/IQVO/asrs-control/internal/adapters/outbound/kafka"
	"github.com/IQVO/asrs-control/internal/application/outbox"
)

type fakeWriter struct{ got []kafkago.Message }

func (f *fakeWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	f.got = append(f.got, msgs...)
	return nil
}

func TestTheSinkRoutesEachMessageToItsOwnTopicWithItsHeaders(t *testing.T) {
	w := &fakeWriter{}
	s := kafka.NewRelaySinkWithWriter(w)
	err := s.Send(context.Background(),
		outbox.Message{Topic: "warehouse.asrs-control.events", Key: []byte("sj-1"), Value: []byte("{}"), Headers: []outbox.Header{{Key: "content-type", Value: "x"}}},
		outbox.Message{Topic: "warehouse.asrs-control.analytics", Key: []byte("sj-1"), Value: []byte("{}")})
	if err != nil || len(w.got) != 2 || w.got[0].Topic != "warehouse.asrs-control.events" || w.got[1].Topic != "warehouse.asrs-control.analytics" ||
		string(w.got[0].Key) != "sj-1" || w.got[0].Headers[0].Key != "content-type" {
		t.Fatalf("Send = %v, %+v", err, w.got)
	}
	if err := s.Send(context.Background(), outbox.Message{EventType: "t"}); err == nil {
		t.Fatal("a message without a topic was sent")
	}
}
