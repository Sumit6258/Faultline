package dedup

import (
	"strconv"
	"testing"
)

func BenchmarkTracker_Claim_NewID(b *testing.B) {
	tr := NewTracker()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.Claim(strconv.Itoa(i))
	}
}

func BenchmarkTracker_Claim_DuplicateID(b *testing.B) {
	tr := NewTracker()
	tr.Claim("same")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.Claim("same")
	}
}
