package kafkasim

import "sync"

// Message is one record in a partition.
type Message struct {
	Offset int64
	Key    string
	Value  string
}

// Partition is an append-only, ordered log, the basic unit of parallelism
// in this simulation, the same role a real Kafka partition plays. Messages
// are never removed once appended here. A real broker applies a retention
// policy, deleting or compacting old segments, which this lab doesn't
// model. See the README for why this lab simulates Kafka rather than
// running a real broker: neither a Kafka broker nor a Go client library for
// one is reachable from this sandbox.
type Partition struct {
	mu       sync.Mutex
	messages []Message
}

func (p *Partition) Append(key, value string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	offset := int64(len(p.messages))
	p.messages = append(p.messages, Message{Offset: offset, Key: key, Value: value})
	return offset
}

// From returns every message at or after offset, in order.
func (p *Partition) From(offset int64) []Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if offset < 0 {
		offset = 0
	}
	if offset >= int64(len(p.messages)) {
		return nil
	}
	out := make([]Message, len(p.messages)-int(offset))
	copy(out, p.messages[offset:])
	return out
}

func (p *Partition) LatestOffset() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return int64(len(p.messages))
}
