package kafkalag

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func ptr(n int) *int { return &n }

func stepN(s *Session, n int) Snapshot {
	var snap Snapshot
	for i := 0; i < n; i++ {
		snap = s.Step()
	}
	return snap
}

// The lab's own demo (go run ./cmd/demo in labs/kafka-messaging) prints, for
// 4 partitions, 40 messages produced per tick and 10 consumed per consumer
// per tick, the lag after 10 ticks: 300, 200, 2 and 2 for 1, 2, 4 and 8
// consumers. This server drives the same lab code through a different
// harness, so it has to reproduce those exact numbers.
func TestReproducesTheLabDemoNumbers(t *testing.T) {
	want := map[int]int64{1: 300, 2: 200, 4: 2, 8: 2}
	for consumers, lag := range want {
		s, err := New(4)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetConfig(ConfigPatch{Consumers: ptr(consumers)}); err != nil {
			t.Fatal(err)
		}
		got := stepN(s, 10).TotalLag
		if got != lag {
			t.Errorf("%d consumers: lag after 10 ticks = %d, the lab's demo prints %d", consumers, got, lag)
		}
	}
}

func TestConsumersBeyondThePartitionCountAreIdle(t *testing.T) {
	s, _ := New(4)
	s.SetConfig(ConfigPatch{Consumers: ptr(8)})
	snap := stepN(s, 10)

	owners := map[int]bool{}
	for _, o := range snap.Owner {
		if o >= 4 {
			t.Fatalf("partition owned by consumer %d, but only 4 partitions exist so consumers 4 to 7 should own nothing", o)
		}
		owners[o] = true
	}
	if len(owners) != 4 {
		t.Fatalf("expected 4 busy consumers out of 8, got %d", len(owners))
	}
}

func TestRebalanceKeepsCommittedOffsets(t *testing.T) {
	s, _ := New(4)
	before := stepN(s, 5)

	for _, n := range []int{4, 2, 7, 1} {
		if _, err := s.SetConfig(ConfigPatch{Consumers: ptr(n)}); err != nil {
			t.Fatal(err)
		}
		after := s.Snapshot()
		for p := range before.Lag {
			if after.Lag[p] != before.Lag[p] {
				t.Fatalf("changing to %d consumers moved partition %d lag from %d to %d: a rebalance must not lose progress", n, p, before.Lag[p], after.Lag[p])
			}
		}
	}

	s.SetConfig(ConfigPatch{Consumers: ptr(4)})
	next := s.Step()
	if next.Consumed-before.Consumed <= 10 {
		t.Fatalf("with 4 consumers one tick should consume more than the 10 a single consumer manages, consumed %d", next.Consumed-before.Consumed)
	}
}

// Partitions, not consumers, are the unit of parallelism. If a large share of
// messages share one key they all land on one partition, and that partition
// has exactly one owner, so adding consumers cannot help.
func TestHotKeyLagStaysOnOnePartitionWhateverTheConsumerCount(t *testing.T) {
	for _, consumers := range []int{1, 2, 4, 8} {
		s, _ := New(4)
		s.SetConfig(ConfigPatch{Consumers: ptr(consumers), HotKeyPercent: ptr(100)})
		snap := stepN(s, 10)

		backedUp := 0
		for _, l := range snap.Lag {
			if l > 0 {
				backedUp++
			}
		}
		if backedUp != 1 {
			t.Errorf("%d consumers: %d partitions have lag, want exactly 1 (all messages share one key)", consumers, backedUp)
		}
		if want := int64(40*10 - 10*10); snap.TotalLag != want {
			t.Errorf("%d consumers: lag %d, want %d: one owner consumes 10 per tick no matter how many consumers exist", consumers, snap.TotalLag, want)
		}
	}
}

func TestStalledConsumerOnlyBacksUpItsOwnPartitions(t *testing.T) {
	s, _ := New(4)
	s.SetConfig(ConfigPatch{Consumers: ptr(4), ConsumeRate: ptr(100)})
	if got := stepN(s, 3).TotalLag; got != 0 {
		t.Fatalf("with fast consumers the group should keep up, lag %d", got)
	}

	if err := s.SetStalled(1, true); err != nil {
		t.Fatal(err)
	}
	snap := stepN(s, 5)
	if snap.Lag[1] == 0 {
		t.Fatal("partition 1's consumer is stalled, its lag should be growing")
	}
	for _, p := range []int{0, 2, 3} {
		if snap.Lag[p] != 0 {
			t.Errorf("partition %d belongs to a healthy consumer but has lag %d", p, snap.Lag[p])
		}
	}

	s.SetStalled(1, false)
	if got := s.Step().Lag[1]; got != 0 {
		t.Fatalf("once resumed, the consumer should clear its backlog in one tick at this rate, lag %d", got)
	}
}

func TestInvalidConfigChangesNothing(t *testing.T) {
	bad := []ConfigPatch{
		{Consumers: ptr(0)},
		{Consumers: ptr(MaxConsumers + 1)},
		{ProduceRate: ptr(-1)},
		{ProduceRate: ptr(MaxProduceRate + 1)},
		{ConsumeRate: ptr(-1)},
		{ConsumeRate: ptr(MaxConsumeRate + 1)},
		{HotKeyPercent: ptr(-1)},
		{HotKeyPercent: ptr(101)},
	}
	for _, p := range bad {
		s, _ := New(4)
		_, err := s.SetConfig(p)
		if err == nil {
			t.Errorf("patch %+v should have been rejected", p)
		}
	}

	// A bad field must not let the good fields in the same request through.
	s, _ := New(4)
	if _, err := s.SetConfig(ConfigPatch{Consumers: ptr(3), ProduceRate: ptr(-5)}); err == nil {
		t.Fatal("expected an error")
	}
	if got := s.Snapshot().Config; got != DefaultConfig() {
		t.Fatalf("a rejected request changed the config to %+v", got)
	}
}

func TestSetStalledValidatesTheConsumerIndex(t *testing.T) {
	s, _ := New(4)
	if err := s.SetStalled(1, true); err == nil {
		t.Error("only consumer 0 exists, stalling consumer 1 should fail")
	}
	if err := s.SetStalled(-1, true); err == nil {
		t.Error("negative index should fail")
	}
	if err := s.SetStalled(0, true); err != nil {
		t.Error(err)
	}
}

func TestProducerHaltsAtTheMessageLimitAndResetClearsIt(t *testing.T) {
	s, _ := newWithLimit(4, 100)
	snap := stepN(s, 3)
	if snap.Produced != 100 || !snap.ProducerHalted {
		t.Fatalf("produced=%d halted=%v, want 100 and halted: the limit must hold", snap.Produced, snap.ProducerHalted)
	}
	if more := stepN(s, 5); more.Produced != 100 {
		t.Fatalf("producer kept going past the limit: %d", more.Produced)
	}

	if err := s.Reset(0); err != nil {
		t.Fatal(err)
	}
	fresh := s.Snapshot()
	if fresh.ProducerHalted || fresh.Produced != 0 || fresh.Tick != 0 || fresh.Epoch != 1 {
		t.Fatalf("reset should clear the session and advance the epoch, got %+v", fresh)
	}
}

func TestResetKeepsTheConfigAndCanChangePartitions(t *testing.T) {
	s, _ := New(4)
	s.SetConfig(ConfigPatch{Consumers: ptr(3), ProduceRate: ptr(80)})
	stepN(s, 4)

	if err := s.Reset(6); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap.Partitions != 6 || snap.Config.Consumers != 3 || snap.Config.ProduceRate != 80 {
		t.Fatalf("got %+v", snap)
	}
	if err := s.Reset(MaxPartitions + 1); err == nil {
		t.Fatal("too many partitions should be rejected")
	}
}

func TestHistoryIsBoundedAndOrdered(t *testing.T) {
	s, _ := New(4)
	stepN(s, 200)
	h := s.History()
	if len(h) != HistoryLen {
		t.Fatalf("history length %d, want %d", len(h), HistoryLen)
	}
	if h[0].Tick != 200-HistoryLen+1 || h[len(h)-1].Tick != 200 {
		t.Fatalf("history spans ticks %d to %d", h[0].Tick, h[len(h)-1].Tick)
	}
}

// Snapshots are handed to other goroutines, so they must not share backing
// arrays with the session's live state.
func TestSnapshotsAreIndependentCopies(t *testing.T) {
	s, _ := New(4)
	snap := stepN(s, 3)
	snap.Lag[0] = -999
	snap.Owner[0] = -999
	if s.Snapshot().Lag[0] == -999 || s.Snapshot().Owner[0] == -999 {
		t.Fatal("mutating a snapshot changed the session")
	}
}

func TestConcurrentUseIsSafe(t *testing.T) {
	s, _ := New(4)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				switch g {
				case 0:
					s.Step()
				case 1:
					s.SetConfig(ConfigPatch{Consumers: ptr(1 + i%8)})
				case 2:
					s.SetStalled(0, i%2 == 0)
				case 3:
					s.Snapshot()
					s.History()
				}
			}
		}(g)
	}
	wg.Wait()
}

// The browser page and this server are separate programs joined only by this
// JSON. The golden file is read by the page's own tests too (see
// src/lib/kafkaLive.test.ts), so renaming a field here fails a test on one
// side or the other instead of breaking the page quietly. After an
// intentional change, regenerate it with: UPDATE_GOLDEN=1 go test ./...
func TestSnapshotJSONMatchesTheGoldenFile(t *testing.T) {
	s, _ := New(4)
	s.SetConfig(ConfigPatch{Consumers: ptr(2), HotKeyPercent: ptr(20)})
	s.SetStalled(1, true)
	got, err := json.MarshalIndent(stepN(s, 3), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	path := filepath.Join("..", "..", "testdata", "snapshot.golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (generate it with UPDATE_GOLDEN=1 go test ./...)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the snapshot JSON changed.\ngot:\n%s\nwant:\n%s\nIf this is intentional, regenerate the golden file and update the page's types and validator to match.", got, want)
	}
}
