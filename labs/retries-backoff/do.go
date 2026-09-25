package retry

import "time"

// Do calls fn, retrying using strategy to compute the delay between
// attempts, until fn succeeds or maxAttempts have been made. sleep is
// injected rather than calling time.Sleep directly so tests can run
// instantly instead of actually waiting; production callers pass
// time.Sleep. Do never sleeps after the final attempt, since there's
// nothing left to wait for.
func Do(fn func() error, strategy Strategy, maxAttempts int, sleep func(time.Duration)) error {
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}
		if attempt < maxAttempts-1 {
			sleep(strategy.Delay(attempt))
		}
	}
	return err
}
