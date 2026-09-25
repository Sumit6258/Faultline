package kafkasim

import "sync"

// ConsumerGroup tracks committed offsets per partition for a group of
// consumers sharing the work of a topic. Partitions are assigned round
// robin across the group's members: each partition is owned by exactly one
// consumer at a time. A group with more consumers than partitions has
// consumers sitting completely idle, since partitions, not consumers, are
// the actual unit of parallelism. See the README's Failure Modes section
// for a measured demonstration.
type ConsumerGroup struct {
	mu           sync.Mutex
	topic        *Topic
	committed    []int64
	assignment   []int
	numConsumers int
}

func NewConsumerGroup(topic *Topic, numConsumers int) *ConsumerGroup {
	g := &ConsumerGroup{
		topic:        topic,
		committed:    make([]int64, topic.NumPartitions()),
		assignment:   make([]int, topic.NumPartitions()),
		numConsumers: numConsumers,
	}
	for p := 0; p < topic.NumPartitions(); p++ {
		g.assignment[p] = p % numConsumers
	}
	return g
}

// OwnerOf returns which consumer index currently owns partition.
func (g *ConsumerGroup) OwnerOf(partition int) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.assignment[partition]
}

// Pending returns messages in partition that this group has produced but
// not yet committed.
func (g *ConsumerGroup) Pending(partition int) []Message {
	g.mu.Lock()
	committed := g.committed[partition]
	g.mu.Unlock()
	return g.topic.Partition(partition).From(committed)
}

// Poll returns every uncommitted message across all partitions assigned to
// consumerIndex.
func (g *ConsumerGroup) Poll(consumerIndex int) []Message {
	var out []Message
	for p := 0; p < g.topic.NumPartitions(); p++ {
		if g.OwnerOf(p) != consumerIndex {
			continue
		}
		out = append(out, g.Pending(p)...)
	}
	return out
}

// Commit advances the committed offset for partition, marking everything up
// to and including offset as processed. Committing an offset lower than
// what's already committed is a no-op, commits only move forward.
func (g *ConsumerGroup) Commit(partition int, offset int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if offset+1 > g.committed[partition] {
		g.committed[partition] = offset + 1
	}
}

// Lag returns, per partition, how many produced messages this group has
// not yet committed.
func (g *ConsumerGroup) Lag() []int64 {
	lag := make([]int64, g.topic.NumPartitions())
	for p := 0; p < g.topic.NumPartitions(); p++ {
		g.mu.Lock()
		committed := g.committed[p]
		g.mu.Unlock()
		lag[p] = g.topic.Partition(p).LatestOffset() - committed
	}
	return lag
}

func (g *ConsumerGroup) TotalLag() int64 {
	var total int64
	for _, l := range g.Lag() {
		total += l
	}
	return total
}
