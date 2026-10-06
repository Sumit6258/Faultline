package worker

import (
	"sync"
	"time"

	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/queue"
)

type DeadLetter struct {
	Notification *queue.Notification
	LastError    string
	FailedAt     time.Time
}

// DLQ holds notifications that exhausted every retry attempt, so one
// permanently failing notification, a bad phone number, a provider that's
// genuinely down, cannot occupy a worker forever. See labs/kafka-messaging
// for the same pattern applied to a message broker instead of a
// notification queue.
type DLQ struct {
	mu    sync.Mutex
	items []DeadLetter
}

func (d *DLQ) Add(n *queue.Notification, err error, at time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.items = append(d.items, DeadLetter{Notification: n, LastError: err.Error(), FailedAt: at})
}

func (d *DLQ) Items() []DeadLetter {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]DeadLetter, len(d.items))
	copy(out, d.items)
	return out
}

func (d *DLQ) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.items)
}
