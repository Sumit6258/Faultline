// Package worker drains a queue.Queue, delivering each notification
// through the sender registered for its channel, retrying with backoff on
// failure, and giving up to the DLQ after MaxAttempts.
package worker

import (
	"sync/atomic"
	"time"

	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/queue"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/senders"
)

type Pool struct {
	Queue       *queue.Queue
	Senders     map[string]senders.Sender
	MaxAttempts int
	Backoff     func(attempt int) time.Duration
	DLQ         *DLQ
	Clock       func() time.Time

	delivered atomic.Int64
	skipped   atomic.Int64 // unknown channel, never attempted
}

func NewPool(q *queue.Queue, snd map[string]senders.Sender, maxAttempts int, backoff func(int) time.Duration) *Pool {
	return &Pool{
		Queue:       q,
		Senders:     snd,
		MaxAttempts: maxAttempts,
		Backoff:     backoff,
		DLQ:         &DLQ{},
		Clock:       time.Now,
	}
}

// ProcessOne pops and handles a single due notification, if one exists. It
// returns false only when nothing was due, which is how a worker's run
// loop knows to wait rather than spin.
func (p *Pool) ProcessOne() bool {
	now := p.Clock()
	n, ok := p.Queue.Pop(now)
	if !ok {
		return false
	}

	sender, exists := p.Senders[n.Channel]
	if !exists {
		p.DLQ.Add(n, errUnknownChannel(n.Channel), now)
		p.skipped.Add(1)
		return true
	}

	err := sender.Send(n.Recipient, n.Body)
	if err == nil {
		p.delivered.Add(1)
		return true
	}

	n.Attempts++
	if n.Attempts >= p.MaxAttempts {
		p.DLQ.Add(n, err, now)
		return true
	}

	n.ScheduledFor = now.Add(p.Backoff(n.Attempts))
	p.Queue.Push(n)
	return true
}

func (p *Pool) Delivered() int64 { return p.delivered.Load() }

type channelError string

func (e channelError) Error() string { return string(e) }
func errUnknownChannel(channel string) error {
	return channelError("worker: no sender registered for channel " + channel)
}
