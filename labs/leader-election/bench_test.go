package leaderelection

import "testing"

func BenchmarkCluster_Heartbeat_SameLeader(b *testing.B) {
	c := NewCluster(1000000) // effectively never times out during the run
	c.Heartbeat("node-1")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Heartbeat("node-1")
	}
}
