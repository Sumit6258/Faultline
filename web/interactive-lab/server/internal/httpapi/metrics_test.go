package httpapi

import (
	"testing"
	"time"
)

func TestSubscribersCountsConnectedStreams(t *testing.T) {
	f := newFixture(t)
	if got := f.srv.Subscribers(); got != 0 {
		t.Fatalf("no one is connected yet, got %d", got)
	}
	f.openStream()
	f.openStream()
	waitFor(t, func() bool { return f.srv.Subscribers() == 2 }, "two subscribers")
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
