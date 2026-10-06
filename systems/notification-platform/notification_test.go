package notification

import (
	"testing"
	"time"

	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/queue"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/senders"
)

func noBackoff(int) time.Duration { return 0 }

func TestService_SubmitAccepted(t *testing.T) {
	svc := New(map[string]senders.Sender{}, 3, noBackoff)
	if !svc.Submit(SubmitRequest{ID: "1", Channel: "email", Priority: queue.Normal}) {
		t.Fatal("expected the first submission to be accepted")
	}
	if svc.Queue.Len() != 1 {
		t.Fatalf("expected 1 item queued, got %d", svc.Queue.Len())
	}
}

func TestService_DuplicateSubmitIsRejectedNotQueuedTwice(t *testing.T) {
	svc := New(map[string]senders.Sender{}, 3, noBackoff)
	svc.Submit(SubmitRequest{ID: "1", Channel: "email", Priority: queue.Normal})
	accepted := svc.Submit(SubmitRequest{ID: "1", Channel: "email", Priority: queue.Normal})

	if accepted {
		t.Fatal("expected the duplicate submission to be rejected")
	}
	if svc.Queue.Len() != 1 {
		t.Fatalf("expected still only 1 item queued after the duplicate, got %d", svc.Queue.Len())
	}
}

func TestService_RunWorkersDeliversEndToEnd(t *testing.T) {
	email := senders.NewLocalSender("email", senders.AlwaysSucceed)
	svc := New(map[string]senders.Sender{"email": email}, 3, noBackoff)

	svc.Submit(SubmitRequest{ID: "1", Channel: "email", Recipient: "a@example.com", Priority: queue.Normal})
	svc.Submit(SubmitRequest{ID: "2", Channel: "email", Recipient: "b@example.com", Priority: queue.Urgent})

	stop := make(chan struct{})
	svc.RunWorkers(2, time.Millisecond, stop)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && svc.Pool.Delivered() < 2 {
		time.Sleep(time.Millisecond)
	}
	close(stop)

	if got := svc.Pool.Delivered(); got != 2 {
		t.Fatalf("expected both notifications delivered, got %d", got)
	}
}
