package kafkasim

import "sync"

// IdempotentConsumer wraps a processing function with deduplication by
// message key: the same key is only actually processed once, no matter how
// many times it's delivered. This is what makes Kafka's at-least-once
// delivery safe to treat as effectively-once at the application level.
// Kafka can and does redeliver a message, after a consumer crashes and
// restarts before committing, for example, and this is how a consumer
// survives that without double-processing.
type IdempotentConsumer struct {
	mu        sync.Mutex
	processed map[string]bool
	process   func(Message) error
}

func NewIdempotentConsumer(process func(Message) error) *IdempotentConsumer {
	return &IdempotentConsumer{processed: make(map[string]bool), process: process}
}

// Process runs the wrapped function for msg, unless a message with the same
// key has already been processed successfully. ranProcess reports whether
// this call actually invoked the wrapped function.
func (c *IdempotentConsumer) Process(msg Message) (ranProcess bool, err error) {
	c.mu.Lock()
	if c.processed[msg.Key] {
		c.mu.Unlock()
		return false, nil
	}
	c.mu.Unlock()

	if err := c.process(msg); err != nil {
		return true, err
	}

	c.mu.Lock()
	c.processed[msg.Key] = true
	c.mu.Unlock()
	return true, nil
}
