package telegram_test

import (
	"testing"
	"time"

	"github.com/IQVO/asrs-control/internal/adapters/telegram"
)

func TestRetransmitEveryIntervalThreeTimesThenExpireOnce(t *testing.T) {
	r := telegram.NewRetransmitter(2*time.Second, 3)
	tg := telegram.Telegram{ID: "t1", Type: telegram.Store}
	r.Track(tg, t0)
	if resend, expired := r.Due(t0.Add(1999 * time.Millisecond)); len(resend)+len(expired) != 0 {
		t.Fatal("resent before 2 s")
	}
	for i := 1; i <= 3; i++ {
		resend, expired := r.Due(t0.Add(time.Duration(2*i) * time.Second))
		if len(resend) != 1 || resend[0].ID != "t1" || len(expired) != 0 {
			t.Fatalf("attempt %d: resend %v expired %v", i, resend, expired)
		}
	}
	resend, expired := r.Due(t0.Add(8 * time.Second))
	if len(resend) != 0 || len(expired) != 1 || expired[0].ID != "t1" {
		t.Fatalf("after 3 resends: resend %v expired %v", resend, expired)
	}
	if resend, expired := r.Due(t0.Add(time.Hour)); len(resend)+len(expired) != 0 {
		t.Fatal("an expired telegram was reported twice")
	}
	if !r.Forget("t1") || r.Forget("t1") {
		t.Fatal("Forget must remove the expired telegram exactly once")
	}
}

func TestAckStopsRetransmissionAndReturnsTheTelegram(t *testing.T) {
	r := telegram.NewRetransmitter(2*time.Second, 3)
	r.Track(telegram.Telegram{ID: "t1", Type: telegram.Cancel, JobID: "j"}, t0)
	got, ok := r.Ack("t1")
	if !ok || got.JobID != "j" {
		t.Fatalf("Ack = %+v, %v", got, ok)
	}
	if _, ok := r.Ack("t1"); ok {
		t.Fatal("a second ACK found the telegram")
	}
	if resend, expired := r.Due(t0.Add(time.Minute)); len(resend)+len(expired) != 0 {
		t.Fatal("an acknowledged telegram was resent")
	}
	r.Track(telegram.Telegram{ID: "t2"}, t0)
	r.Track(telegram.Telegram{ID: "t2"}, t0.Add(5*time.Second)) // re-tracking restarts the count
	if resend, _ := r.Due(t0.Add(6 * time.Second)); len(resend) != 0 {
		t.Fatal("a re-tracked telegram kept its old deadline")
	}
}

func TestForgetRemovesOnlyAnExpiredTelegram(t *testing.T) {
	r := telegram.NewRetransmitter(2*time.Second, 3)
	r.Track(telegram.Telegram{ID: "t1"}, t0)
	if r.Forget("t1") {
		t.Fatal("Forget removed a telegram that never expired")
	}
	if _, ok := r.Ack("t1"); !ok {
		t.Fatal("the live telegram was lost")
	}
}
