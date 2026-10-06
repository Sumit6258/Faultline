package queue

import (
	"strconv"
	"testing"
	"time"
)

func BenchmarkQueue_PushPop(b *testing.B) {
	q := New()
	now := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Push(&Notification{ID: strconv.Itoa(i), Priority: Normal, ScheduledFor: now})
		q.Pop(now)
	}
}
