package analytics

import "sync"

// Collector counts clicks per code asynchronously: RecordClick never
// blocks the caller on the counting itself, it hands the event to a
// background goroutine over a channel. This exists so a redirect, the hot
// path a real user is waiting on, never pays the cost of updating an
// analytics counter. If the channel's buffer fills, because clicks are
// arriving faster than they can be counted, RecordClick drops the event
// rather than blocking the redirect, an explicit choice: approximate
// analytics are an acceptable cost, a slow redirect is not.
type Collector struct {
	mu      sync.Mutex
	counts  map[string]int64
	events  chan string
	done    chan struct{}
	stopped chan struct{}
	dropped int64
}

func NewCollector(bufferSize int) *Collector {
	c := &Collector{
		counts:  make(map[string]int64),
		events:  make(chan string, bufferSize),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go c.run()
	return c
}

func (c *Collector) run() {
	defer close(c.stopped)
	for {
		select {
		case code := <-c.events:
			c.mu.Lock()
			c.counts[code]++
			c.mu.Unlock()
		case <-c.done:
			// Drain whatever is already queued before actually stopping.
			for {
				select {
				case code := <-c.events:
					c.mu.Lock()
					c.counts[code]++
					c.mu.Unlock()
				default:
					return
				}
			}
		}
	}
}

// RecordClick is fire and forget. It never blocks: if the internal buffer
// is full, the event is dropped and counted as dropped rather than slowing
// down the caller.
func (c *Collector) RecordClick(code string) {
	select {
	case c.events <- code:
	default:
		c.mu.Lock()
		c.dropped++
		c.mu.Unlock()
	}
}

// Stop signals the background goroutine to drain any queued events and
// exit, and waits for it to actually finish before returning. Count and
// Dropped are only guaranteed accurate after Stop returns.
func (c *Collector) Stop() {
	close(c.done)
	<-c.stopped
}

func (c *Collector) Count(code string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[code]
}

func (c *Collector) Dropped() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropped
}
