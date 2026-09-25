package main

import (
	"fmt"
	"time"

	retry "github.com/Sumit6258/Faultline/labs/retries-backoff"
)

const numClients = 200
const bucketSize = 20 * time.Millisecond

func main() {
	fmt.Println("200 clients all fail at once, a shared dependency blips. How do their retries land?")
	fmt.Println()

	fixed := retry.Fixed{Wait: 200 * time.Millisecond}
	report("fixed delay, no jitter", computeDelays(numClients, fixed))

	fmt.Println()

	jittered := retry.NewFullJitter(200*time.Millisecond, 400*time.Millisecond, 42)
	report("exponential backoff with full jitter", computeDelays(numClients, jittered))
}

func computeDelays(n int, strategy retry.Strategy) []time.Duration {
	out := make([]time.Duration, n)
	for i := 0; i < n; i++ {
		out[i] = strategy.Delay(0)
	}
	return out
}

func report(label string, delays []time.Duration) {
	buckets := make(map[time.Duration]int)
	peak := 0
	for _, d := range delays {
		bucket := (d / bucketSize) * bucketSize
		buckets[bucket]++
		if buckets[bucket] > peak {
			peak = buckets[bucket]
		}
	}
	fmt.Printf("%s:\n", label)
	fmt.Printf("  %d clients, %d distinct %s buckets used, busiest bucket has %d simultaneous retries\n", len(delays), len(buckets), bucketSize, peak)
}
