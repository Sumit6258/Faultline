package main

import (
	"errors"
	"fmt"
	"time"

	cb "github.com/Sumit6258/Faultline/labs/circuit-breakers"
)

func main() {
	breaker := cb.NewBreaker(3, 200*time.Millisecond)

	// A dependency that fails on calls 3 through 5, then recovers.
	callCount := 0
	dependency := func() error {
		callCount++
		if callCount >= 3 && callCount <= 5 {
			return errors.New("dependency unavailable")
		}
		return nil
	}

	fmt.Println("Calling a dependency that fails on calls 3 through 5, then recovers.")
	fmt.Println()

	for i := 1; i <= 6; i++ {
		err := breaker.Call(dependency)
		fmt.Printf("call %d: state=%-9s err=%v\n", i, breaker.State(), err)
	}

	fmt.Println()
	fmt.Println("circuit is open, further calls fail fast without touching the dependency")
	before := callCount
	err := breaker.Call(dependency)
	fmt.Printf("call 7: state=%-9s err=%v (dependency actually called: %v)\n", breaker.State(), err, callCount != before)

	fmt.Println()
	fmt.Printf("waiting for the %s cooldown...\n", 200*time.Millisecond)
	time.Sleep(220 * time.Millisecond)
	fmt.Printf("state right after the wait, before any call is attempted: %s\n", breaker.State())
	fmt.Println("(the breaker checks the cooldown lazily, on the next call, not on a timer)")

	fmt.Println()
	fmt.Println("cooldown elapsed, next call is a half-open trial against the now-recovered dependency")
	for i := 8; i <= 10; i++ {
		err := breaker.Call(dependency)
		fmt.Printf("call %d: state=%-9s err=%v\n", i, breaker.State(), err)
	}
}
