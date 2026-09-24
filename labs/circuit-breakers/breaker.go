package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// State is one of Closed, Open, or HalfOpen. See Breaker for what each
// means.
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ErrCircuitOpen is returned by Call without ever invoking the wrapped
// function, when the circuit is open and its cooldown hasn't elapsed yet.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// Breaker wraps calls to a dependency and stops calling it once it's
// failing too often, so a caller fails fast instead of piling up requests
// against something that's already struggling. Closed means calls go
// through normally. Open means calls fail immediately without touching the
// dependency at all. HalfOpen, entered automatically once the cooldown
// elapses, lets exactly one trial call through to check whether the
// dependency has recovered: success closes the circuit again, failure
// reopens it.
type Breaker struct {
	mu               sync.Mutex
	state            State
	failureThreshold int
	consecutiveFails int
	cooldown         time.Duration
	openedAt         time.Time
	halfOpenInFlight bool
	clock            Clock
}

func NewBreaker(failureThreshold int, cooldown time.Duration) *Breaker {
	return &Breaker{
		failureThreshold: failureThreshold,
		cooldown:         cooldown,
		clock:            realClock,
	}
}

// Call runs fn if the circuit currently allows it, and updates the
// circuit's state based on whether fn succeeded.
func (b *Breaker) Call(fn func() error) error {
	b.mu.Lock()
	switch b.state {
	case Open:
		if b.clock().Sub(b.openedAt) < b.cooldown {
			b.mu.Unlock()
			return ErrCircuitOpen
		}
		b.state = HalfOpen
		b.halfOpenInFlight = true
	case HalfOpen:
		if b.halfOpenInFlight {
			b.mu.Unlock()
			return ErrCircuitOpen
		}
		b.halfOpenInFlight = true
	}
	wasHalfOpen := b.state == HalfOpen
	b.mu.Unlock()

	err := fn()

	b.mu.Lock()
	defer b.mu.Unlock()
	b.halfOpenInFlight = false

	if err != nil {
		b.consecutiveFails++
		if wasHalfOpen || b.consecutiveFails >= b.failureThreshold {
			b.state = Open
			b.openedAt = b.clock()
		}
		return err
	}

	b.consecutiveFails = 0
	b.state = Closed
	return nil
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}
