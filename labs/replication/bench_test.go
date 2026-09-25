package replication

import "testing"

func BenchmarkLeader_Write(b *testing.B) {
	l := NewLeader()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Write("key", "value")
	}
}

func BenchmarkFollower_Sync_NoNewEntries(b *testing.B) {
	l := NewLeader()
	l.Write("key", "value")
	f := NewFollower(l, 0)
	f.Sync()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Sync()
	}
}
