package kafkasim

import "testing"

func TestConsumerGroup_AssignsPartitionsRoundRobin(t *testing.T) {
	topic := NewTopic(4)
	g := NewConsumerGroup(topic, 2)

	want := []int{0, 1, 0, 1}
	for p, w := range want {
		if got := g.OwnerOf(p); got != w {
			t.Fatalf("partition %d: expected owner %d, got %d", p, w, got)
		}
	}
}

func TestConsumerGroup_MoreConsumersThanPartitionsLeavesSomeIdle(t *testing.T) {
	topic := NewTopic(2)
	g := NewConsumerGroup(topic, 5)

	owned := make(map[int]bool)
	for p := 0; p < topic.NumPartitions(); p++ {
		owned[g.OwnerOf(p)] = true
	}
	if len(owned) != 2 {
		t.Fatalf("expected only 2 of the 5 consumers to own a partition, got %d owning something", len(owned))
	}
}

func TestConsumerGroup_CommitAdvancesOffsetAndReducesLag(t *testing.T) {
	topic := NewTopic(1)
	topic.Produce("a", "1")
	topic.Produce("b", "2")
	g := NewConsumerGroup(topic, 1)

	if g.TotalLag() != 2 {
		t.Fatalf("expected lag of 2 before any commit, got %d", g.TotalLag())
	}

	g.Commit(0, 0)
	if g.TotalLag() != 1 {
		t.Fatalf("expected lag of 1 after committing offset 0, got %d", g.TotalLag())
	}

	g.Commit(0, 1)
	if g.TotalLag() != 0 {
		t.Fatalf("expected lag of 0 after committing offset 1, got %d", g.TotalLag())
	}
}

func TestConsumerGroup_CommitNeverMovesBackward(t *testing.T) {
	topic := NewTopic(1)
	topic.Produce("a", "1")
	topic.Produce("b", "2")
	g := NewConsumerGroup(topic, 1)

	g.Commit(0, 1) // commit past both messages
	g.Commit(0, 0) // then try to commit backward

	if g.TotalLag() != 0 {
		t.Fatalf("expected a backward commit to be a no-op, lag should stay 0, got %d", g.TotalLag())
	}
}

func TestConsumerGroup_PollOnlyReturnsOwnedPartitions(t *testing.T) {
	topic := NewTopic(2)
	topic.Partition(0).Append("a", "1")
	topic.Partition(1).Append("b", "1")
	g := NewConsumerGroup(topic, 2)

	got0 := g.Poll(0)
	if len(got0) != 1 || got0[0].Key != "a" {
		t.Fatalf("expected consumer 0 to see only partition 0's message, got %v", got0)
	}

	got1 := g.Poll(1)
	if len(got1) != 1 || got1[0].Key != "b" {
		t.Fatalf("expected consumer 1 to see only partition 1's message, got %v", got1)
	}
}
