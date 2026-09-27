package idgen

import (
	"sync"
	"testing"
)

func TestCounter_NeverRepeats(t *testing.T) {
	c := &Counter{}
	seen := make(map[string]bool)
	for i := 0; i < 10000; i++ {
		code, err := c.Generate()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if seen[code] {
			t.Fatalf("code %q repeated after %d generations", code, i)
		}
		seen[code] = true
	}
}

func TestCounter_ConcurrentGenerateNeverRepeats(t *testing.T) {
	c := &Counter{}
	var mu sync.Mutex
	seen := make(map[string]bool)
	var wg sync.WaitGroup

	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := c.Generate()
			mu.Lock()
			defer mu.Unlock()
			if seen[code] {
				t.Errorf("code %q generated more than once under concurrent use", code)
			}
			seen[code] = true
		}()
	}
	wg.Wait()
}
