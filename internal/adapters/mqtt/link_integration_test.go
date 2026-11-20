//go:build integration

package mqtt

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/IQVO/asrs-control/internal/adapters/telegram"
	"github.com/IQVO/asrs-control/internal/application/telegrams"
)

func mosquitto(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true,
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "eclipse-mosquitto:2.0.20", ExposedPorts: []string{"1883/tcp"},
			Cmd:        []string{"mosquitto", "-c", "/mosquitto-no-auth.conf"},
			WaitingFor: wait.ForListeningPort("1883/tcp"),
		}})
	if err != nil {
		t.Fatalf("start mosquitto: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	ep, err := c.PortEndpoint(ctx, "1883/tcp", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	return ep
}

func TestUpTelegramsAreHandledInArrivalOrderAndEachIsAcknowledged(t *testing.T) {
	broker := mosquitto(t)
	up := &fakeUplink{}
	link, err := NewLink(broker, "asrs-control-it", "ASRS1", up, time.Now, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	link.Start(ctx)

	var mu sync.Mutex
	var down []telegram.Telegram
	plc, err := connect(broker, "plc-it", func(c pahomqtt.Client) {
		_ = subscribe(c, telegram.DownTopic("ASRS1"), func(_ pahomqtt.Client, m pahomqtt.Message) {
			tg, _ := telegram.Decode(m.Payload())
			mu.Lock()
			down = append(down, tg)
			mu.Unlock()
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plc.Disconnect(250)
	time.Sleep(300 * time.Millisecond) // both subscriptions are in place

	const n = 50
	for i := 0; i < n; i++ {
		b, _ := telegram.Encode(telegram.Telegram{ID: fmt.Sprintf("u%02d", i), Type: telegram.JobDone, JobID: "sj", Timestamp: time.Now()})
		if err := publish(plc, telegram.UpTopic("ASRS1"), b); err != nil {
			t.Fatal(err)
		}
	}
	link.Send(ctx, telegrams.Command{TelegramID: "s1", Type: telegrams.CmdStateRequest})

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(down)
		mu.Unlock()
		if got >= n+1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if len(up.ups) != n {
		t.Fatalf("handled %d up telegrams, want %d", len(up.ups), n)
	}
	for i, u := range up.ups {
		if u.TelegramID != fmt.Sprintf("u%02d", i) {
			t.Fatalf("up telegram %d is %s: arrival order lost", i, u.TelegramID)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	acks, state := 0, 0
	for _, d := range down {
		switch d.Type {
		case telegram.Ack:
			acks++
		case telegram.StateRequest:
			state++
		}
	}
	if acks != n || state != 1 {
		t.Fatalf("down topic carried %d ACKs and %d STATE_REQUESTs, want %d and 1", acks, state, n)
	}
	if !link.Connected() || (Links{"ASRS1": link}).Connected() != nil {
		t.Fatal("the link reports disconnected")
	}
}
