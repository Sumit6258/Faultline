package analytics

import (
	"sync"
	"testing"
	"time"
)

func TestCollector_CountsClicks(t *testing.T) {
	c := NewCollector(10)
	defer c.Stop()

	c.RecordClick("abc")
	c.RecordClick("abc")
	c.RecordClick("xyz")

	waitForCount(t, c, "abc", 2)
	waitForCount(t, c, "xyz", 1)
}

func TestCollector_RecordClickNeverBlocks(t *testing.T) {
	// A buffer of 1 with nothing draining it yet: the 2nd RecordClick call
	// must still return immediately rather than blocking on a full channel.
	c := &Collector{
		counts: make(map[string]int64),
		events: make(chan string, 1),
		done:   make(chan struct{}),
	}
	// Deliberately not starting run(), to prove RecordClick itself never
	// blocks even if nothing is consuming the channel.

	done := make(chan struct{})
	go func() {
		c.RecordClick("a")
		c.RecordClick("b") // buffer is full now, this must not block
		c.RecordClick("c")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RecordClick blocked instead of dropping when the buffer was full")
	}

	if c.Dropped() != 2 {
		t.Fatalf("expected 2 of the 3 calls to be dropped (buffer size 1), got %d", c.Dropped())
	}
}

func TestCollector_StopDrainsQueuedEvents(t *testing.T) {
	c := NewCollector(100)
	for i := 0; i < 50; i++ {
		c.RecordClick("abc")
	}
	c.Stop()

	if got := c.Count("abc"); got != 50 {
		t.Fatalf("expected all 50 queued clicks to be counted after Stop drains them, got %d", got)
	}
}

func waitForCount(t *testing.T, c *Collector, code string, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if c.Count(code) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to reach count %d, got %d", code, want, c.Count(code))
}

func TestCollector_ConcurrentRecordClickIsSafe(t *testing.T) {
	c := NewCollector(1000)
	defer c.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.RecordClick("shared")
		}()
	}
	wg.Wait()

	waitForCount(t, c, "shared", 200)
}
