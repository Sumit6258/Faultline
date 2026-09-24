package circuitbreaker

import "testing"

func BenchmarkBreaker_Call_Closed_Success(b *testing.B) {
	br := NewBreaker(3, 0)
	fn := func() error { return nil }
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		br.Call(fn)
	}
}

func BenchmarkBreaker_Call_Open_FastReject(b *testing.B) {
	br := NewBreaker(1, 1_000_000_000_000) // cooldown effectively never elapses
	br.Call(func() error { return errDependencyDown })
	fn := func() error { return nil }
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		br.Call(fn)
	}
}
