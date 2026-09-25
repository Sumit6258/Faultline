package kafkasim

import (
	"strconv"
	"testing"
)

func BenchmarkTopic_Produce(b *testing.B) {
	topic := NewTopic(8)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		topic.Produce("key-"+strconv.Itoa(i%1000), "value")
	}
}

func BenchmarkConsumerGroup_Commit(b *testing.B) {
	topic := NewTopic(1)
	g := NewConsumerGroup(topic, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Commit(0, int64(i))
	}
}
