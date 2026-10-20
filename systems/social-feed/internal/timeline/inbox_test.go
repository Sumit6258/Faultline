package timeline

import (
	"sync"
	"testing"
)

func TestInbox_ReadIsNewestFirst(t *testing.T) {
	in := newInboxes(10)
	for i := int64(1); i <= 3; i++ {
		in.push("u", mkPost(i, int(i)))
	}
	got := seqs(in.read("u", 10))
	if want := []int64{3, 2, 1}; !equal(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestInbox_ReadRespectsLimit(t *testing.T) {
	in := newInboxes(10)
	for i := int64(1); i <= 5; i++ {
		in.push("u", mkPost(i, int(i)))
	}
	if got := in.read("u", 2); len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
}

func TestInbox_NeverReturnsMoreThanItsBoundAndKeepsTheNewest(t *testing.T) {
	const max = 10
	in := newInboxes(max)
	// Far past 2*max, forcing several physical trims.
	for i := int64(1); i <= 95; i++ {
		in.push("u", mkPost(i, int(i)))
	}
	got := seqs(in.read("u", 1000))
	if len(got) != max {
		t.Fatalf("expected exactly the bound %d, got %d", max, len(got))
	}
	if got[0] != 95 || got[max-1] != 86 {
		t.Fatalf("expected the newest %d posts, 95 down to 86, got %v", max, got)
	}
}

func TestInbox_UsersAreIndependent(t *testing.T) {
	in := newInboxes(10)
	in.push("a", mkPost(1, 1))
	in.push("b", mkPost(2, 2))
	if got := seqs(in.read("a", 10)); !equal(got, []int64{1}) {
		t.Fatalf("expected a to see only its own, got %v", got)
	}
}

func TestInbox_UnknownUserIsEmpty(t *testing.T) {
	if got := newInboxes(10).read("nobody", 10); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestInbox_ConcurrentPushesAreSafe(t *testing.T) {
	in := newInboxes(1000)
	var wg sync.WaitGroup
	for w := 0; w < 20; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				in.push("shared", mkPost(int64(w*20+i), w*20+i))
			}
		}(w)
	}
	wg.Wait()
	if got := in.read("shared", 1000); len(got) != 400 {
		t.Fatalf("expected all 400 concurrent pushes retained, got %d", len(got))
	}
}
