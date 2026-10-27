// Package kafkalag runs the real labs/kafka-messaging code as a live,
// steerable simulation. Nothing here reimplements Kafka's behavior: topics,
// partition assignment, committed offsets and lag all come from the lab. This
// package only decides how many messages are produced and consumed per tick
// and records what the lab reports.
package kafkalag

import (
	"fmt"
	"sync"

	kafkasim "github.com/Sumit6258/Faultline/labs/kafka-messaging"
)

const (
	MaxPartitions  = 12
	MaxConsumers   = 12
	MaxProduceRate = 500
	MaxConsumeRate = 100
	HistoryLen     = 120

	// MaxProduced bounds memory. The lab's partitions are append-only and
	// never trim, because the lab does not model retention. A real broker
	// would delete old segments. Without a bound, a session left running
	// would grow without limit, so the producer halts here and says so in
	// every snapshot until the session is reset.
	MaxProduced = 200_000

	hotKey = "hot-key"
)

// Config is what the browser can steer while the simulation runs.
type Config struct {
	Consumers     int `json:"consumers"`     // consumer instances in the group
	ProduceRate   int `json:"produceRate"`   // messages per tick, whole topic
	ConsumeRate   int `json:"consumeRate"`   // messages per tick, per consumer
	HotKeyPercent int `json:"hotKeyPercent"` // share of messages that use one key
}

// ConfigPatch is a partial update. A nil field leaves that setting alone.
type ConfigPatch struct {
	Consumers     *int `json:"consumers"`
	ProduceRate   *int `json:"produceRate"`
	ConsumeRate   *int `json:"consumeRate"`
	HotKeyPercent *int `json:"hotKeyPercent"`
}

// Snapshot is the state after one tick, exactly as the lab reports it.
type Snapshot struct {
	Epoch          int64   `json:"epoch"` // increments on reset, so a client knows to clear its history
	Tick           int64   `json:"tick"`
	Partitions     int     `json:"partitions"`
	Produced       int64   `json:"produced"`
	Consumed       int64   `json:"consumed"`
	Lag            []int64 `json:"lag"` // per partition
	TotalLag       int64   `json:"totalLag"`
	Owner          []int   `json:"owner"`   // which consumer owns each partition
	Stalled        []bool  `json:"stalled"` // per consumer
	Config         Config  `json:"config"`
	ProducerHalted bool    `json:"producerHalted"`
}

type Session struct {
	mu          sync.Mutex
	epoch       int64
	partitions  int
	cfg         Config
	stalled     []bool
	topic       *kafkasim.Topic
	group       *kafkasim.ConsumerGroup
	tick        int64
	produced    int64
	consumed    int64
	halted      bool
	history     []Snapshot
	maxProduced int64
}

func DefaultConfig() Config {
	return Config{Consumers: 1, ProduceRate: 40, ConsumeRate: 10, HotKeyPercent: 0}
}

func New(partitions int) (*Session, error) {
	return newWithLimit(partitions, MaxProduced)
}

func newWithLimit(partitions int, maxProduced int64) (*Session, error) {
	if err := validatePartitions(partitions); err != nil {
		return nil, err
	}
	s := &Session{maxProduced: maxProduced, cfg: DefaultConfig()}
	s.start(partitions)
	return s, nil
}

func validatePartitions(n int) error {
	if n < 1 || n > MaxPartitions {
		return fmt.Errorf("partitions must be between 1 and %d, got %d", MaxPartitions, n)
	}
	return nil
}

// start builds a fresh topic and group. The caller holds the lock, or is the
// constructor.
func (s *Session) start(partitions int) {
	s.partitions = partitions
	s.topic = kafkasim.NewTopic(partitions)
	s.group = kafkasim.NewConsumerGroup(s.topic, s.cfg.Consumers)
	s.stalled = make([]bool, s.cfg.Consumers)
	s.tick, s.produced, s.consumed = 0, 0, 0
	s.halted = false
	s.history = nil
}

// Step runs one tick: produce, then consume, in the same order as the lab's
// own demo, so the numbers can be compared directly.
func (s *Session) Step() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.produce()
	s.consume()
	s.tick++

	snap := s.snapshotLocked()
	s.history = append(s.history, snap)
	if len(s.history) > HistoryLen {
		s.history = append([]Snapshot(nil), s.history[len(s.history)-HistoryLen:]...)
	}
	return snap
}

func (s *Session) produce() {
	n := s.cfg.ProduceRate
	hot := (n*s.cfg.HotKeyPercent + 50) / 100 // rounded share of this tick's messages
	for i := 0; i < n; i++ {
		if s.produced >= s.maxProduced {
			s.halted = true
			return
		}
		key := fmt.Sprintf("key-%d-%d", s.tick, i) // the lab demo's key scheme
		if i < hot {
			key = hotKey
		}
		s.topic.Produce(key, "payload")
		s.produced++
	}
}

func (s *Session) consume() {
	for c := 0; c < s.cfg.Consumers; c++ {
		if s.stalled[c] {
			continue
		}
		budget := s.cfg.ConsumeRate
		for p := 0; p < s.partitions && budget > 0; p++ {
			if s.group.OwnerOf(p) != c {
				continue
			}
			for _, m := range s.group.Pending(p) {
				if budget == 0 {
					break
				}
				s.group.Commit(p, m.Offset)
				budget--
				s.consumed++
			}
		}
	}
}

func (s *Session) snapshotLocked() Snapshot {
	lag := s.group.Lag()
	var total int64
	for _, l := range lag {
		total += l
	}
	owner := make([]int, s.partitions)
	for p := range owner {
		owner[p] = s.group.OwnerOf(p)
	}
	return Snapshot{
		Epoch:          s.epoch,
		Tick:           s.tick,
		Partitions:     s.partitions,
		Produced:       s.produced,
		Consumed:       s.consumed,
		Lag:            lag,
		TotalLag:       total,
		Owner:          owner,
		Stalled:        append([]bool(nil), s.stalled...),
		Config:         s.cfg,
		ProducerHalted: s.halted,
	}
}

// Snapshot returns the current state without advancing the simulation.
func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// History returns up to HistoryLen recent snapshots, oldest first.
func (s *Session) History() []Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Snapshot(nil), s.history...)
}

// SetConfig applies a partial update. Every provided field is validated
// before any is applied, so a bad request changes nothing.
func (s *Session) SetConfig(p ConfigPatch) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.cfg
	if p.Consumers != nil {
		next.Consumers = *p.Consumers
	}
	if p.ProduceRate != nil {
		next.ProduceRate = *p.ProduceRate
	}
	if p.ConsumeRate != nil {
		next.ConsumeRate = *p.ConsumeRate
	}
	if p.HotKeyPercent != nil {
		next.HotKeyPercent = *p.HotKeyPercent
	}
	if err := validateConfig(next); err != nil {
		return s.cfg, err
	}

	if next.Consumers != s.cfg.Consumers {
		s.rebalance(next.Consumers)
	}
	s.cfg = next
	return s.cfg, nil
}

func validateConfig(c Config) error {
	switch {
	case c.Consumers < 1 || c.Consumers > MaxConsumers:
		return fmt.Errorf("consumers must be between 1 and %d, got %d", MaxConsumers, c.Consumers)
	case c.ProduceRate < 0 || c.ProduceRate > MaxProduceRate:
		return fmt.Errorf("produceRate must be between 0 and %d, got %d", MaxProduceRate, c.ProduceRate)
	case c.ConsumeRate < 0 || c.ConsumeRate > MaxConsumeRate:
		return fmt.Errorf("consumeRate must be between 0 and %d, got %d", MaxConsumeRate, c.ConsumeRate)
	case c.HotKeyPercent < 0 || c.HotKeyPercent > 100:
		return fmt.Errorf("hotKeyPercent must be between 0 and 100, got %d", c.HotKeyPercent)
	}
	return nil
}

// rebalance changes the number of consumers. The lab's ConsumerGroup assigns
// partitions once, at construction, and has no rebalance of its own, so this
// builds a new group and carries the committed offsets across through the
// lab's public API. That is the property a real rebalance preserves:
// consumers join or leave, and the group's progress does not reset. The
// caller holds the lock.
func (s *Session) rebalance(consumers int) {
	lag := s.group.Lag()
	g := kafkasim.NewConsumerGroup(s.topic, consumers)
	for p := range lag {
		committed := s.topic.Partition(p).LatestOffset() - lag[p]
		if committed > 0 {
			g.Commit(p, committed-1) // Commit(offset) marks offset+1 as committed
		}
	}
	s.group = g

	stalled := make([]bool, consumers)
	copy(stalled, s.stalled)
	s.stalled = stalled
}

// SetStalled stops one consumer from processing anything, which is what a
// crashed or paused instance looks like to the rest of the group, before any
// rebalance would happen.
func (s *Session) SetStalled(consumer int, stalled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if consumer < 0 || consumer >= s.cfg.Consumers {
		return fmt.Errorf("consumer must be between 0 and %d, got %d", s.cfg.Consumers-1, consumer)
	}
	s.stalled[consumer] = stalled
	return nil
}

// Reset starts over with an empty topic. Pass 0 to keep the current
// partition count. The current config is kept.
func (s *Session) Reset(partitions int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if partitions == 0 {
		partitions = s.partitions
	}
	if err := validatePartitions(partitions); err != nil {
		return err
	}
	s.epoch++
	s.start(partitions)
	return nil
}
