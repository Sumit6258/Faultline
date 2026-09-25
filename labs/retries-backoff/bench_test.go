package retry

import "testing"
import "time"

func BenchmarkFixed_Delay(b *testing.B) {
	f := Fixed{Wait: time.Second}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Delay(0)
	}
}

func BenchmarkFullJitter_Delay(b *testing.B) {
	j := NewFullJitter(10*time.Millisecond, time.Second, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j.Delay(0)
	}
}
