package queue

import (
	"testing"
	"time"
)

func mk(id string, p Priority, at time.Time) *Notification {
	return &Notification{ID: id, Priority: p, ScheduledFor: at}
}

func TestQueue_HigherPriorityComesFirstAmongReady(t *testing.T) {
	q := New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q.Push(mk("a", Low, now))
	q.Push(mk("b", Urgent, now))
	q.Push(mk("c", Normal, now))

	n, ok := q.Pop(now)
	if !ok || n.ID != "b" {
		t.Fatalf("expected urgent (b) first, got %v ok=%v", n, ok)
	}
	n, ok = q.Pop(now)
	if !ok || n.ID != "c" {
		t.Fatalf("expected normal (c) second, got %v ok=%v", n, ok)
	}
	n, ok = q.Pop(now)
	if !ok || n.ID != "a" {
		t.Fatalf("expected low (a) last, got %v ok=%v", n, ok)
	}
}

func TestQueue_SamePriorityIsFIFO(t *testing.T) {
	q := New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q.Push(mk("first", Normal, now))
	q.Push(mk("second", Normal, now))
	q.Push(mk("third", Normal, now))

	for _, want := range []string{"first", "second", "third"} {
		n, ok := q.Pop(now)
		if !ok || n.ID != want {
			t.Fatalf("expected %s, got %v ok=%v", want, n, ok)
		}
	}
}

func TestQueue_FutureScheduledItemsAreNotPoppedEarly(t *testing.T) {
	q := New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q.Push(mk("future", Urgent, now.Add(time.Hour)))

	_, ok := q.Pop(now)
	if ok {
		t.Fatal("expected nothing due yet, even though the queue isn't empty")
	}
	if q.Len() != 1 {
		t.Fatalf("expected the item to still be held, Len()=%d", q.Len())
	}
}

func TestQueue_ReadyLowerPriorityBeatsNotYetDueHigherPriority(t *testing.T) {
	// The real reason this queue is two heaps and not one: a high priority
	// item scheduled for later must not block a lower priority item that's
	// already due.
	q := New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q.Push(mk("later-urgent", Urgent, now.Add(time.Hour)))
	q.Push(mk("due-now-low", Low, now))

	n, ok := q.Pop(now)
	if !ok || n.ID != "due-now-low" {
		t.Fatalf("expected the ready low priority item, not the not-yet-due urgent one, got %v ok=%v", n, ok)
	}
}

func TestQueue_ItemBecomesReadyWhenTimePasses(t *testing.T) {
	q := New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q.Push(mk("soon", Normal, now.Add(time.Minute)))

	if _, ok := q.Pop(now); ok {
		t.Fatal("should not be ready yet")
	}
	later := now.Add(2 * time.Minute)
	n, ok := q.Pop(later)
	if !ok || n.ID != "soon" {
		t.Fatalf("expected it to be ready once its schedule passed, got %v ok=%v", n, ok)
	}
}

func TestQueue_PopOnEmptyQueueReturnsFalse(t *testing.T) {
	q := New()
	if _, ok := q.Pop(time.Now()); ok {
		t.Fatal("expected false on an empty queue")
	}
}

func TestQueue_ReadyLenDoesNotRemoveItems(t *testing.T) {
	q := New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q.Push(mk("a", Normal, now))
	q.Push(mk("b", Normal, now))

	if got := q.ReadyLen(now); got != 2 {
		t.Fatalf("expected ReadyLen 2, got %d", got)
	}
	if got := q.Len(); got != 2 {
		t.Fatalf("expected ReadyLen to not remove anything, Len()=%d", got)
	}
}
