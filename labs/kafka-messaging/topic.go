package kafkasim

import "hash/fnv"

// Topic is a set of partitions. A message with a given key always lands on
// the same partition, hash(key) mod partition count, which is what lets a
// consumer group process every message for a given key in order while
// still parallelizing across different keys, up to the partition count.
type Topic struct {
	partitions []*Partition
}

func NewTopic(numPartitions int) *Topic {
	t := &Topic{partitions: make([]*Partition, numPartitions)}
	for i := range t.partitions {
		t.partitions[i] = &Partition{}
	}
	return t
}

// Produce appends value to the partition determined by hashing key, and
// returns which partition it landed on and at what offset.
func (t *Topic) Produce(key, value string) (partition int, offset int64) {
	p := t.partitionFor(key)
	offset = t.partitions[p].Append(key, value)
	return p, offset
}

func (t *Topic) partitionFor(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() % uint32(len(t.partitions)))
}

func (t *Topic) NumPartitions() int {
	return len(t.partitions)
}

func (t *Topic) Partition(i int) *Partition {
	return t.partitions[i]
}
