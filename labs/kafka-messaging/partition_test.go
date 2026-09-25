package kafkasim

import "testing"

func TestPartition_AppendAssignsIncreasingOffsets(t *testing.T) {
	p := &Partition{}
	o1 := p.Append("a", "1")
	o2 := p.Append("b", "2")
	if o1 != 0 || o2 != 1 {
		t.Fatalf("expected offsets 0 then 1, got %d then %d", o1, o2)
	}
}

func TestPartition_FromReturnsOnlyAtOrAfterOffset(t *testing.T) {
	p := &Partition{}
	p.Append("a", "1")
	p.Append("b", "2")
	p.Append("c", "3")

	got := p.From(1)
	if len(got) != 2 || got[0].Key != "b" || got[1].Key != "c" {
		t.Fatalf("expected b and c from offset 1, got %v", got)
	}
}

func TestPartition_FromEmptyPastTheEnd(t *testing.T) {
	p := &Partition{}
	p.Append("a", "1")
	if got := p.From(5); got != nil {
		t.Fatalf("expected nil for an offset past the end, got %v", got)
	}
}

func TestPartition_LatestOffset(t *testing.T) {
	p := &Partition{}
	if p.LatestOffset() != 0 {
		t.Fatal("expected 0 on an empty partition")
	}
	p.Append("a", "1")
	p.Append("b", "2")
	if p.LatestOffset() != 2 {
		t.Fatalf("expected latest offset 2, got %d", p.LatestOffset())
	}
}
