package main

import (
	"fmt"
	"time"

	distlock "github.com/Sumit6258/Faultline/labs/distributed-locks"
)

func main() {
	fmt.Println("Scenario: worker-A stalls past its lease, worker-B takes over.")
	fmt.Println()
	runWithFencing()
	fmt.Println()
	runWithoutFencing()
}

func runWithFencing() {
	fmt.Println("With fencing tokens:")
	svc := distlock.NewService()
	resource := distlock.NewFencedResource()

	tokenA, _ := svc.TryAcquire("job-1", "worker-A", 200*time.Millisecond)
	fmt.Printf("  worker-A acquires the lease, token=%d\n", tokenA)

	fmt.Println("  worker-A stalls for 300ms, past its 200ms lease...")
	time.Sleep(300 * time.Millisecond)

	tokenB, ok := svc.TryAcquire("job-1", "worker-B", 200*time.Millisecond)
	fmt.Printf("  worker-B acquires the lease after expiry, ok=%v token=%d\n", ok, tokenB)

	resource.Write(tokenB, "written by B")
	fmt.Println("  worker-B writes to the resource")

	fmt.Println("  worker-A wakes up, unaware it lost the lease, tries to write with its stale token")
	staleOk := resource.Write(tokenA, "written by A, but stale")
	fmt.Printf("  worker-A's write accepted=%v\n", staleOk)
	fmt.Printf("  resource's final value: %q\n", resource.Value())
}

func runWithoutFencing() {
	fmt.Println("Without fencing, same scenario:")
	svc := distlock.NewService()
	resource := distlock.NewNaiveResource()

	svc.TryAcquire("job-1", "worker-A", 200*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	svc.TryAcquire("job-1", "worker-B", 200*time.Millisecond)

	resource.Write("written by B")
	resource.Write("written by A, but stale")

	fmt.Printf("  resource's final value: %q\n", resource.Value())
}
