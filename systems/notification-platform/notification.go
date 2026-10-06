// Package notification ties the queue, deduplication, and worker pool
// together into the two operations that matter: Submit and the background
// delivery loop started by RunWorkers.
package notification

import (
	"time"

	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/dedup"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/queue"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/senders"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/worker"
)

type SubmitRequest struct {
	ID           string
	Channel      string
	Recipient    string
	Body         string
	Priority     queue.Priority
	ScheduledFor time.Time // zero value means immediately
}

type Service struct {
	Queue *queue.Queue
	Dedup *dedup.Tracker
	Pool  *worker.Pool
}

func New(snd map[string]senders.Sender, maxAttempts int, backoff func(int) time.Duration) *Service {
	q := queue.New()
	return &Service{
		Queue: q,
		Dedup: dedup.NewTracker(),
		Pool:  worker.NewPool(q, snd, maxAttempts, backoff),
	}
}

// Submit enqueues a notification unless its ID has already been submitted,
// in which case it's a no-op: accepted reports which happened. This is
// what makes it safe for a caller to retry a submission it isn't sure
// succeeded, the same idempotency-by-ID shape as systems/payment-platform,
// applied here to notifications instead of charges.
func (s *Service) Submit(req SubmitRequest) (accepted bool) {
	if !s.Dedup.Claim(req.ID) {
		return false
	}
	scheduledFor := req.ScheduledFor
	if scheduledFor.IsZero() {
		scheduledFor = time.Now()
	}
	s.Queue.Push(&queue.Notification{
		ID:           req.ID,
		Channel:      req.Channel,
		Recipient:    req.Recipient,
		Body:         req.Body,
		Priority:     req.Priority,
		ScheduledFor: scheduledFor,
	})
	return true
}

// RunWorkers starts n goroutines continuously draining the queue until
// stop is closed. Each one calls Pool.ProcessOne in a loop, sleeping
// briefly whenever nothing is due, which is what lets many workers share
// one queue safely, queue.Pop is already synchronized.
func (s *Service) RunWorkers(n int, idleSleep time.Duration, stop <-chan struct{}) {
	for i := 0; i < n; i++ {
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if !s.Pool.ProcessOne() {
					time.Sleep(idleSleep)
				}
			}
		}()
	}
}
