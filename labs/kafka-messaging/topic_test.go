package kafkasim

import (
	"fmt"
	"testing"
)

func TestTopic_SameKeyAlwaysSamePartition(t *testing.T) {
	topic := NewTopic(8)
	first, _ := topic.Produce("stable-key", "1")
	for i := 0; i < 20; i++ {
		p, _ := topic.Produce("stable-key", fmt.Sprintf("value-%d", i))
		if p != first {
			t.Fatalf("expected the same key to always land on partition %d, got %d", first, p)
		}
	}
}

func TestTopic_ProduceReturnsCorrectOffset(t *testing.T) {
	topic := NewTopic(4)
	_, o1 := topic.Produce("same-key", "1")
	_, o2 := topic.Produce("same-key", "2")
	if o1 != 0 || o2 != 1 {
		t.Fatalf("expected offsets 0 then 1 for the same key, got %d then %d", o1, o2)
	}
}

func TestTopic_DifferentKeysSpreadAcrossPartitions(t *testing.T) {
	topic := NewTopic(4)
	counts := make(map[int]int)
	for i := 0; i < 4000; i++ {
		p, _ := topic.Produce(fmt.Sprintf("key-%d", i), "value")
		counts[p]++
	}
	if len(counts) != 4 {
		t.Fatalf("expected all 4 partitions to receive at least one message across 4000 distinct keys, only %d did", len(counts))
	}
	for p, c := range counts {
		if c < 700 || c > 1300 {
			t.Fatalf("partition %d got %d of 4000 messages, expected roughly even distribution around 1000", p, c)
		}
	}
}
