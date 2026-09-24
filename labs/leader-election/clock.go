package leaderelection

import "time"

// Clock is time.Now, abstracted so tests can control time deterministically
// instead of relying on real sleeps.
type Clock func() time.Time

func realClock() time.Time {
	return time.Now()
}
