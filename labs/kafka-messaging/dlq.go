package kafkasim

import "sync"

// DLQ is a dead letter queue: where messages go after failing to process
// past a retry limit, so one poison message, a message that can never
// succeed, cannot block everything behind it forever. A consumer has to
// process a partition in order, so without a DLQ or something like it, one
// permanently failing message stalls the entire partition indefinitely.
type DLQ struct {
	mu       sync.Mutex
	messages []Message
}

func (d *DLQ) Send(msg Message) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.messages = append(d.messages, msg)
}

func (d *DLQ) Messages() []Message {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Message, len(d.messages))
	copy(out, d.messages)
	return out
}

func (d *DLQ) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.messages)
}
