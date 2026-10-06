package worker

import (
	"math/rand"
	"time"
)

// FullJitter is the same formula as labs/retries-backoff, a fresh
// implementation here rather than a shared import. The actual delay is
// drawn uniformly between 0 and the exponential cap, which is what spreads
// many notifications that failed at the same moment, a provider outage,
// out over time instead of all retrying in the same instant.
func FullJitter(base, max time.Duration, rng *rand.Rand) func(attempt int) time.Duration {
	return func(attempt int) time.Duration {
		cap := base * time.Duration(int64(1)<<uint(min(attempt, 20)))
		if cap <= 0 || cap > max {
			cap = max
		}
		return time.Duration(rng.Int63n(int64(cap) + 1))
	}
}
