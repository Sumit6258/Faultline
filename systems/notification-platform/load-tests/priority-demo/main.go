// A standalone demo, not an HTTP load test: submits a burst of mixed
// priority notifications directly against a Service with exactly one
// worker, so delivery order is deterministic and the priority ordering is
// unambiguous to read, not blurred by several workers pulling at once.
package main

import (
	"fmt"
	"time"

	notification "github.com/Sumit6258/Faultline/systems/notification-platform"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/queue"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/senders"
)

func main() {
	email := senders.NewLocalSender("email", senders.AlwaysSucceed)
	svc := notification.New(map[string]senders.Sender{"email": email}, 3, func(int) time.Duration { return 0 })

	order := []struct {
		id string
		p  queue.Priority
	}{
		{"low-1", queue.Low}, {"normal-1", queue.Normal}, {"low-2", queue.Low},
		{"urgent-1", queue.Urgent}, {"normal-2", queue.Normal}, {"high-1", queue.High},
		{"low-3", queue.Low}, {"urgent-2", queue.Urgent}, {"normal-3", queue.Normal},
	}
	fmt.Println("Submission order:")
	for _, n := range order {
		fmt.Printf("  %s (%s)\n", n.id, n.p)
		svc.Submit(notification.SubmitRequest{ID: n.id, Channel: "email", Recipient: n.id, Priority: n.p})
	}

	stop := make(chan struct{})
	svc.RunWorkers(1, time.Millisecond, stop) // exactly one worker, for a deterministic order
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && svc.Pool.Delivered() < int64(len(order)) {
		time.Sleep(time.Millisecond)
	}
	close(stop)

	fmt.Println("\nDelivery order:")
	for _, m := range email.Sent() {
		fmt.Printf("  %s\n", m.Recipient)
	}
}
