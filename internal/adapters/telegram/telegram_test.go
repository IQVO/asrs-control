package telegram_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/telegram"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 123_000_000, time.UTC)

func TestTopicsFollowTheProtocol(t *testing.T) {
	if telegram.DownTopic("ASRS1") != "asrs/v1/ASRS1/down" || telegram.UpTopic("ASRS1") != "asrs/v1/ASRS1/up" {
		t.Fatal("topics differ from docs/telegram-protocol.md")
	}
}

func TestAStoreEncodesAsOneFlatObjectWithMillisecondUTC(t *testing.T) {
	b, err := telegram.Encode(telegram.Telegram{ID: "t1", Type: telegram.Store, Timestamp: t0.In(time.FixedZone("X", 3600)),
		JobID: "sj-1", ContainerID: "C1", PortID: "IN1", To: &telegram.Position{Aisle: 1, Level: 2, Slot: 3, Depth: 2}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	want := map[string]any{"telegramId": "t1", "type": "STORE", "timestamp": "2026-11-20T08:00:00.123Z", "jobId": "sj-1",
		"containerId": "C1", "portId": "IN1", "to": map[string]any{"aisle": 1.0, "level": 2.0, "slot": 3.0, "depth": 2.0}}
	if len(got) != len(want) {
		t.Fatalf("fields = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if gb, _ := json.Marshal(got[k]); string(gb) != mustJSON(v) {
			t.Errorf("%s = %s, want %s", k, gb, mustJSON(v))
		}
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestRoundTripOfEveryType(t *testing.T) {
	yes := true
	p := &telegram.Position{Aisle: 1, Level: 1, Slot: 1, Depth: 1}
	for _, tg := range []telegram.Telegram{
		{ID: "a", Type: telegram.Store, JobID: "j", ContainerID: "c", PortID: "IN1", To: p},
		{ID: "b", Type: telegram.Retrieve, JobID: "j", ContainerID: "c", PortID: "OUT1", From: p},
		{ID: "c", Type: telegram.Move, JobID: "j", ContainerID: "c", From: p, To: p},
		{ID: "d", Type: telegram.Cancel, JobID: "j"},
		{ID: "e", Type: telegram.StateRequest},
		{ID: "f", Type: telegram.Ack, AckOf: "a", Accepted: &yes},
		{ID: "g", Type: telegram.JobDone, JobID: "j"},
		{ID: "h", Type: telegram.JobFailed, JobID: "j", Reason: "PORT_OCCUPIED"},
		{ID: "i", Type: telegram.UnitState, UnitID: "A1", State: "HELD"},
		{ID: "k", Type: telegram.PortArrival, PortID: "OUT1"},
	} {
		tg.Timestamp = t0
		b, err := telegram.Encode(tg)
		if err != nil {
			t.Fatalf("%s: %v", tg.Type, err)
		}
		back, err := telegram.Decode(b)
		if err != nil || back.ID != tg.ID || back.Type != tg.Type || !back.Timestamp.Equal(t0) {
			t.Fatalf("%s: %+v, %v", tg.Type, back, err)
		}
	}
}

func TestDecodeRefusesMalformedTelegramsButKeepsTheirID(t *testing.T) {
	cases := map[string]string{
		"missing id":             `{"type":"JOB_DONE","timestamp":"2026-11-20T08:00:00.000Z","jobId":"j"}`,
		"unknown type":           `{"telegramId":"x","type":"HELLO","timestamp":"2026-11-20T08:00:00.000Z"}`,
		"job done without job":   `{"telegramId":"x","type":"JOB_DONE","timestamp":"2026-11-20T08:00:00.000Z"}`,
		"failure without reason": `{"telegramId":"x","type":"JOB_FAILED","timestamp":"2026-11-20T08:00:00.000Z","jobId":"j"}`,
		"ack without accepted":   `{"telegramId":"x","type":"ACK","timestamp":"2026-11-20T08:00:00.000Z","ackTelegramId":"a"}`,
		"state without unit":     `{"telegramId":"x","type":"UNIT_STATE","timestamp":"2026-11-20T08:00:00.000Z","state":"IDLE"}`,
		"arrival without port":   `{"telegramId":"x","type":"PORT_ARRIVAL","timestamp":"2026-11-20T08:00:00.000Z"}`,
		"bad timestamp":          `{"telegramId":"x","type":"JOB_DONE","timestamp":"yesterday","jobId":"j"}`,
		"wrong field type":       `{"telegramId":"x","type":"JOB_DONE","timestamp":"2026-11-20T08:00:00.000Z","jobId":7}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			tg, err := telegram.Decode([]byte(raw))
			if err == nil || !strings.HasPrefix(err.Error(), "MALFORMED") {
				t.Fatalf("Decode = %v, want a MALFORMED error", err)
			}
			if name != "missing id" && tg.ID != "x" {
				t.Errorf("the id was lost (%q): the receiver cannot NACK it", tg.ID)
			}
		})
	}
	if tg, err := telegram.Decode([]byte(`{`)); err == nil || tg.ID != "" {
		t.Fatal("broken JSON must decode to no telegram")
	}
	if _, err := telegram.Encode(telegram.Telegram{Type: telegram.JobDone}); err == nil {
		t.Fatal("Encode accepted a telegram without id")
	}
}

func TestNewAckCarriesTheBareReasonCode(t *testing.T) {
	of := telegram.Telegram{ID: "up-1", Type: telegram.JobDone, JobID: "j"}
	ok := telegram.NewAck(of, nil, "ack-1", t0)
	if ok.Type != telegram.Ack || ok.AckOf != "up-1" || ok.ID != "ack-1" || ok.Accepted == nil || !*ok.Accepted || ok.Reason != "" {
		t.Fatalf("accepting ACK = %+v", ok)
	}
	no := telegram.NewAck(of, errors.New("MALFORMED: jobId: bad\nmore"), "ack-2", t0)
	if *no.Accepted || no.Reason != "MALFORMED" {
		t.Fatalf("refusing ACK = %+v", no)
	}
}

func TestIsUpNamesThePLCsTelegrams(t *testing.T) {
	for _, ty := range []telegram.Type{telegram.JobDone, telegram.JobFailed, telegram.UnitState, telegram.PortArrival} {
		if !telegram.IsUp(ty) {
			t.Errorf("%s is an up telegram", ty)
		}
	}
	for _, ty := range []telegram.Type{telegram.Store, telegram.Retrieve, telegram.Move, telegram.Cancel, telegram.StateRequest, telegram.Ack} {
		if telegram.IsUp(ty) {
			t.Errorf("%s is not an up telegram", ty)
		}
	}
}
