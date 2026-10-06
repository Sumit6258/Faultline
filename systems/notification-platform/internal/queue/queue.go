// Package queue is a priority queue gated by a schedule: an item only
// becomes eligible to be popped once its ScheduledFor time has passed, and
// among eligible items, the highest Priority goes first, ties broken by
// submission order.
//
// It is built from two heaps rather than one, because a single heap can
// only ever be ordered one way, and this queue genuinely needs both: items
// not yet due have to come out in time order so the right ones get
// promoted at the right moment, and items that are due have to come out in
// priority order. A scheduled min-heap, ordered by ScheduledFor, feeds a
// ready max-heap, ordered by Priority, as items become due. This is the
// same two-structure shape a real job scheduler uses, not a simplification
// invented for this lab.
package queue

import (
	"container/heap"
	"sync"
	"time"
)

type Priority int

const (
	Low Priority = iota
	Normal
	High
	Urgent
)

func (p Priority) String() string {
	switch p {
	case Low:
		return "low"
	case Normal:
		return "normal"
	case High:
		return "high"
	case Urgent:
		return "urgent"
	default:
		return "unknown"
	}
}

type Notification struct {
	ID           string
	Channel      string
	Recipient    string
	Body         string
	Priority     Priority
	ScheduledFor time.Time
	Attempts     int
	seq          int64 // submission order, breaks ties within the same priority
}

type Queue struct {
	mu        sync.Mutex
	scheduled scheduledHeap
	ready     readyHeap
	nextSeq   int64
}

func New() *Queue {
	return &Queue{}
}

func (q *Queue) Push(n *Notification) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.nextSeq++
	n.seq = q.nextSeq
	heap.Push(&q.scheduled, n)
}

// Pop returns the highest priority notification that is currently due, or
// ok=false if nothing is due yet, which is not the same as the queue being
// empty: items can exist but all be scheduled for the future.
func (q *Queue) Pop(now time.Time) (n *Notification, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.promoteDue(now)
	if q.ready.Len() == 0 {
		return nil, false
	}
	return heap.Pop(&q.ready).(*Notification), true
}

// promoteDue moves every notification whose ScheduledFor has passed from
// the scheduled heap into the ready heap.
func (q *Queue) promoteDue(now time.Time) {
	for q.scheduled.Len() > 0 && !q.scheduled[0].ScheduledFor.After(now) {
		n := heap.Pop(&q.scheduled).(*Notification)
		heap.Push(&q.ready, n)
	}
}

// Len returns the total count, due or not, currently held.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.scheduled.Len() + q.ready.Len()
}

// ReadyLen reports how many are currently due, without popping anything.
func (q *Queue) ReadyLen(now time.Time) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.promoteDue(now)
	return q.ready.Len()
}

type scheduledHeap []*Notification

func (h scheduledHeap) Len() int { return len(h) }
func (h scheduledHeap) Less(i, j int) bool {
	if !h[i].ScheduledFor.Equal(h[j].ScheduledFor) {
		return h[i].ScheduledFor.Before(h[j].ScheduledFor)
	}
	return h[i].seq < h[j].seq
}
func (h scheduledHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *scheduledHeap) Push(x any)   { *h = append(*h, x.(*Notification)) }
func (h *scheduledHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

type readyHeap []*Notification

func (h readyHeap) Len() int { return len(h) }
func (h readyHeap) Less(i, j int) bool {
	if h[i].Priority != h[j].Priority {
		return h[i].Priority > h[j].Priority // higher priority first
	}
	return h[i].seq < h[j].seq // then FIFO
}
func (h readyHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *readyHeap) Push(x any)   { *h = append(*h, x.(*Notification)) }
func (h *readyHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
