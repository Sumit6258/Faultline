package cache

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoalescer_ConcurrentCallsForSameKeyRunOnce(t *testing.T) {
	c := NewCoalescer()
	var calls atomic.Int32
	var wg sync.WaitGroup

	fn := func() (string, bool) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return "result", true
	}

	results := make([]string, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			v, _ := c.Do("shared-key", fn)
			results[idx] = v
		}(i)
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected fn to run exactly once for 50 concurrent callers on the same key, ran %d times", got)
	}
	for i, v := range results {
		if v != "result" {
			t.Fatalf("caller %d got %q, expected all callers to get the coalesced result", i, v)
		}
	}
}

func TestCoalescer_DifferentKeysRunIndependently(t *testing.T) {
	c := NewCoalescer()
	var calls atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c.Do(string(rune('a'+idx)), func() (string, bool) {
				calls.Add(1)
				return "x", true
			})
		}(i)
	}
	wg.Wait()

	if got := calls.Load(); got != 5 {
		t.Fatalf("expected 5 distinct keys to each run fn once, ran %d times total", got)
	}
}
