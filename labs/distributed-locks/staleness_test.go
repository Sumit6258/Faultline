package distlock

import "testing"
import "time"

// TestStaleHolderWriteIsRejectedByFencing is the scenario this whole lab
// exists to demonstrate. Worker A holds a lease, stalls past it (a long
// pause, a slow network, anything), and worker B takes over. When A wakes
// up and, unaware it lost the lease, tries to write anyway, the fencing
// token stops it from corrupting what B already wrote.
func TestStaleHolderWriteIsRejectedByFencing(t *testing.T) {
	svc := NewService()
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.clock = func() time.Time { return fakeNow }
	resource := NewFencedResource()

	tokenA, ok := svc.TryAcquire("job-1", "worker-A", 5*time.Second)
	if !ok {
		t.Fatal("worker-A should acquire the lock, nobody holds it yet")
	}

	// worker-A stalls past its lease: a GC pause, a slow network, a
	// descheduled goroutine, anything.
	fakeNow = fakeNow.Add(10 * time.Second)

	tokenB, ok := svc.TryAcquire("job-1", "worker-B", 5*time.Second)
	if !ok {
		t.Fatal("worker-B should acquire the lock, worker-A's lease expired")
	}
	if tokenB <= tokenA {
		t.Fatalf("expected worker-B's token %d to be higher than worker-A's %d", tokenB, tokenA)
	}

	if !resource.Write(tokenB, "written by B") {
		t.Fatal("worker-B's write should succeed, it holds the current lease")
	}

	// worker-A wakes up, unaware it lost the lease, and tries to write
	// using its now stale token.
	if resource.Write(tokenA, "written by A, but stale") {
		t.Fatal("worker-A's stale write should have been rejected by fencing")
	}

	if got := resource.Value(); got != "written by B" {
		t.Fatalf("expected the resource to still hold B's value, got %q, fencing failed to protect it", got)
	}
}

func TestNaiveResourceAllowsStaleWriteToCorruptData(t *testing.T) {
	svc := NewService()
	fakeNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.clock = func() time.Time { return fakeNow }
	resource := NewNaiveResource()

	svc.TryAcquire("job-1", "worker-A", 5*time.Second)
	fakeNow = fakeNow.Add(10 * time.Second)
	svc.TryAcquire("job-1", "worker-B", 5*time.Second)

	resource.Write("written by B")
	// worker-A wakes up and writes with no fencing to stop it.
	resource.Write("written by A, but stale")

	if got := resource.Value(); got != "written by A, but stale" {
		t.Fatalf("expected this demonstration to end with A's stale write winning, got %q", got)
	}
}
