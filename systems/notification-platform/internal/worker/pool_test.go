package worker

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/queue"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/senders"
)

func noBackoff(attempt int) time.Duration { return 0 }

func TestPool_DeliversASuccessfulNotification(t *testing.T) {
	q := queue.New()
	email := senders.NewLocalSender("email", senders.AlwaysSucceed)
	p := NewPool(q, map[string]senders.Sender{"email": email}, 3, noBackoff)

	q.Push(&queue.Notification{ID: "1", Channel: "email", Recipient: "a@example.com", Priority: queue.Normal})
	if !p.ProcessOne() {
		t.Fatal("expected a due notification to be processed")
	}
	if p.Delivered() != 1 {
		t.Fatalf("expected 1 delivered, got %d", p.Delivered())
	}
	if len(email.Sent()) != 1 {
		t.Fatalf("expected the provider to have recorded 1 send, got %d", len(email.Sent()))
	}
}

func TestPool_RetriesThenSucceeds(t *testing.T) {
	q := queue.New()
	sms := senders.NewLocalSender("sms", senders.FailTimes(2))
	p := NewPool(q, map[string]senders.Sender{"sms": sms}, 5, noBackoff)

	q.Push(&queue.Notification{ID: "1", Channel: "sms", Recipient: "+1555", Priority: queue.Normal})

	// Each failed attempt gets rescheduled for "now" (zero backoff), so
	// draining the queue a few times is enough to walk it through 2
	// failures and a success.
	for i := 0; i < 3; i++ {
		p.ProcessOne()
	}

	if p.Delivered() != 1 {
		t.Fatalf("expected exactly 1 delivery after 2 failures, got %d", p.Delivered())
	}
	if p.DLQ.Len() != 0 {
		t.Fatalf("expected nothing in the DLQ, it recovered within MaxAttempts, got %d", p.DLQ.Len())
	}
}

func TestPool_ExhaustsRetriesIntoDLQ(t *testing.T) {
	q := queue.New()
	sms := senders.NewLocalSender("sms", senders.NeverSucceed)
	p := NewPool(q, map[string]senders.Sender{"sms": sms}, 3, noBackoff)

	q.Push(&queue.Notification{ID: "1", Channel: "sms", Recipient: "+1555", Priority: queue.Normal})

	for i := 0; i < 3; i++ {
		p.ProcessOne()
	}

	if p.Delivered() != 0 {
		t.Fatalf("expected 0 delivered, got %d", p.Delivered())
	}
	if p.DLQ.Len() != 1 {
		t.Fatalf("expected exactly 1 in the DLQ after exhausting 3 attempts, got %d", p.DLQ.Len())
	}
	dead := p.DLQ.Items()[0]
	if dead.Notification.Attempts != 3 {
		t.Fatalf("expected the dead letter to record 3 attempts, got %d", dead.Notification.Attempts)
	}
}

func TestPool_UnknownChannelGoesStraightToDLQ(t *testing.T) {
	q := queue.New()
	p := NewPool(q, map[string]senders.Sender{}, 3, noBackoff)
	q.Push(&queue.Notification{ID: "1", Channel: "carrier-pigeon", Priority: queue.Normal})

	p.ProcessOne()

	if p.DLQ.Len() != 1 {
		t.Fatalf("expected an unroutable notification to go straight to the DLQ, got len=%d", p.DLQ.Len())
	}
}

func TestPool_FailedAttemptIsRescheduledWithBackoff(t *testing.T) {
	q := queue.New()
	sms := senders.NewLocalSender("sms", senders.FailTimes(1))
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	backoff := func(attempt int) time.Duration { return 500 * time.Millisecond }
	p := NewPool(q, map[string]senders.Sender{"sms": sms}, 3, backoff)
	p.Clock = func() time.Time { return fakeNow }

	q.Push(&queue.Notification{ID: "1", Channel: "sms", Priority: queue.Normal})
	p.ProcessOne() // fails, rescheduled 500ms out

	if _, ok := q.Pop(fakeNow); ok {
		t.Fatal("the retry should not be due yet")
	}
	fakeNow = fakeNow.Add(500 * time.Millisecond)
	if _, ok := q.Pop(fakeNow); !ok {
		t.Fatal("the retry should be due once the backoff has elapsed")
	}
}

func TestFullJitter_StaysWithinCapAndVaries(t *testing.T) {
	backoff := FullJitter(10*time.Millisecond, time.Second, rand.New(rand.NewSource(1)))
	seen := map[time.Duration]bool{}
	for i := 0; i < 50; i++ {
		d := backoff(3)
		if d < 0 || d > 80*time.Millisecond {
			t.Fatalf("delay %s outside the expected cap for attempt 3 (base 10ms * 2^3 = 80ms)", d)
		}
		seen[d] = true
	}
	if len(seen) < 30 {
		t.Fatalf("expected real variation across 50 draws, only got %d distinct values", len(seen))
	}
}

func TestPool_ConcurrentWorkersNeverDoubleDeliverOrLoseAnItem(t *testing.T) {
	q := queue.New()
	email := senders.NewLocalSender("email", senders.AlwaysSucceed)
	p := NewPool(q, map[string]senders.Sender{"email": email}, 3, noBackoff)

	const n = 500
	for i := 0; i < n; i++ {
		q.Push(&queue.Notification{ID: string(rune(i)), Channel: "email", Priority: queue.Normal})
	}

	var wg sync.WaitGroup
	var processed atomic.Int64
	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if p.ProcessOne() {
					processed.Add(1)
				} else {
					return
				}
			}
		}()
	}
	wg.Wait()

	if processed.Load() != n {
		t.Fatalf("expected exactly %d processed across 10 concurrent workers, got %d", n, processed.Load())
	}
	if int64(len(email.Sent())) != n {
		t.Fatalf("expected exactly %d actually delivered, no duplicates, no losses, got %d", n, len(email.Sent()))
	}
}
