package retry

import (
	"math/rand"
	"time"
)

// Strategy computes how long to wait before the next retry attempt, given
// how many attempts have already been made. attempt is 0 for the delay
// before the first retry (that is, after the first failure).
type Strategy interface {
	Delay(attempt int) time.Duration
}

// Fixed always waits the same amount of time between attempts. It's the
// simplest strategy, and the one most likely to cause a retry storm: every
// client that failed at the same moment retries at the same moment again,
// with nothing to spread them apart. See the README.
type Fixed struct {
	Wait time.Duration
}

func (f Fixed) Delay(attempt int) time.Duration {
	return f.Wait
}

// ExponentialBackoff doubles the delay on every attempt, up to Max. It
// spreads out repeated failures from a single client over time, but does
// nothing to spread out many different clients that all failed at once,
// since they all compute the same delay for the same attempt number.
type ExponentialBackoff struct {
	Base time.Duration
	Max  time.Duration
}

func (e ExponentialBackoff) Delay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 62 { // guard against overflow from the shift below
		return e.Max
	}
	d := e.Base * time.Duration(int64(1)<<uint(attempt))
	if d <= 0 || d > e.Max {
		return e.Max
	}
	return d
}

// FullJitter is exponential backoff where the actual delay is chosen
// uniformly at random between 0 and the exponential cap, following the
// "full jitter" approach described in AWS's Architecture Blog post on
// backoff and jitter. Two clients hitting the same attempt number get
// different delays, which is what spreads retries out over time instead of
// letting them land in the same instant.
type FullJitter struct {
	Base time.Duration
	Max  time.Duration
	Rand *rand.Rand
}

// NewFullJitter builds a FullJitter strategy with its own seeded random
// source, so results are reproducible given the same seed.
func NewFullJitter(base, max time.Duration, seed int64) *FullJitter {
	return &FullJitter{Base: base, Max: max, Rand: rand.New(rand.NewSource(seed))}
}

func (j *FullJitter) Delay(attempt int) time.Duration {
	cap := ExponentialBackoff{Base: j.Base, Max: j.Max}.Delay(attempt)
	if cap <= 0 {
		return 0
	}
	return time.Duration(j.Rand.Int63n(int64(cap)))
}
