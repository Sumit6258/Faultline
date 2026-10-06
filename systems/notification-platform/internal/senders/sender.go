// Package senders provides the Sender interface every channel (email, SMS,
// push) implements, and a local, in-memory implementation standing in for
// a real provider. No real network is reachable from the sandbox this was
// built in, so LocalSender never actually sends anything, it records what
// it would have sent, and can be configured to fail on demand, which is
// what makes retry and DLQ behavior something this system can actually
// demonstrate rather than only describe.
package senders

import (
	"fmt"
	"sync"
	"time"
)

type Sender interface {
	Send(recipient, body string) error
}

type SentMessage struct {
	Recipient string
	Body      string
	SentAt    time.Time
}

type LocalSender struct {
	mu         sync.Mutex
	channel    string
	sent       []SentMessage
	shouldFail func() bool
	clock      func() time.Time
}

// NewLocalSender builds a sender for channel. shouldFail is consulted on
// every Send call; pass a closure with its own state to simulate a
// provider that fails for a while and then recovers, see the tests and
// load test for examples.
func NewLocalSender(channel string, shouldFail func() bool) *LocalSender {
	return &LocalSender{channel: channel, shouldFail: shouldFail, clock: time.Now}
}

func (s *LocalSender) Send(recipient, body string) error {
	if s.shouldFail() {
		return fmt.Errorf("%s provider: delivery failed", s.channel)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, SentMessage{Recipient: recipient, Body: body, SentAt: s.clock()})
	return nil
}

func (s *LocalSender) Sent() []SentMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SentMessage, len(s.sent))
	copy(out, s.sent)
	return out
}

// AlwaysSucceed and NeverSucceed are the two trivial shouldFail functions
// most tests need; FailTimes builds the "fails N times then recovers"
// function the retry and DLQ demonstrations need.
func AlwaysSucceed() bool { return false }
func NeverSucceed() bool  { return true }

func FailTimes(n int) func() bool {
	var calls int
	var mu sync.Mutex
	return func() bool {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return calls <= n
	}
}
