package main

import (
	"fmt"
	"math/rand"
	"time"

	repl "github.com/Sumit6258/Faultline/labs/replication"
)

const numWrites = 200

// readDelayCeilingMs bounds how long, in this simulation, a client waits
// after writing before it reads its own write back, standing in for normal
// request handling time: page navigation, another API call, anything.
const readDelayCeilingMs = 30

func main() {
	fmt.Println("A client writes to the leader, then reads its own write back from a follower")
	fmt.Println("after a realistic delay, 0 to 30ms, simulating normal request timing.")
	fmt.Println("How often does that read see the fresh value, depending on the follower's replication lag?")
	fmt.Println()

	for _, lag := range []time.Duration{0, 5 * time.Millisecond, 15 * time.Millisecond, 50 * time.Millisecond} {
		runScenario(lag)
	}
}

func runScenario(lag time.Duration) {
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	leader := repl.NewLeader()
	follower := repl.NewFollower(leader, lag)

	// Leader and Follower both expose an injectable clock so this demo runs
	// instantly on a shared fake clock instead of waiting on real time, the
	// same pattern used throughout this repository.
	leader.SetClock(func() time.Time { return fakeNow })
	follower.SetClock(func() time.Time { return fakeNow })

	rnd := rand.New(rand.NewSource(1)) // same seed for every lag value, so only lag differs between runs

	fresh := 0
	for i := 0; i < numWrites; i++ {
		key := fmt.Sprintf("key-%d", i)
		leader.Write(key, "value")

		readDelay := time.Duration(rnd.Intn(readDelayCeilingMs)) * time.Millisecond
		fakeNow = fakeNow.Add(readDelay)

		follower.Sync()
		if _, ok := follower.Read(key); ok {
			fresh++
		}

		fakeNow = fakeNow.Add(time.Millisecond)
	}

	fmt.Printf("follower lag=%-6s  read-your-own-write succeeded %d/%d times (%.0f%%)\n", lag, fresh, numWrites, 100*float64(fresh)/float64(numWrites))
}
