package main

import (
	"fmt"
	"sync"
	"time"

	cache "github.com/Sumit6258/faultline/labs/caching"
)

func main() {
	fmt.Println("Cache stampede demo: 100 concurrent requests for the same missing key.")
	fmt.Println()

	// Naive: no coalescing, every concurrent miss goes straight to the source.
	naiveSource := cache.NewSlowStore(30 * time.Millisecond)
	naiveSource.Seed("hot-key", "value")
	naiveCache := cache.NewLRU(100, time.Minute)

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := naiveCache.Get("hot-key"); !ok {
				if v, found := naiveSource.Get("hot-key"); found {
					naiveCache.Set("hot-key", v)
				}
			}
		}()
	}
	wg.Wait()
	naiveElapsed := time.Since(start)
	fmt.Printf("naive, no coalescing:     source hit %d times, wall clock %s\n", naiveSource.Calls(), naiveElapsed)

	// Coalesced: cache-aside with request coalescing, only one goroutine
	// actually reaches the source, everyone else waits for its result.
	coalescedSource := cache.NewSlowStore(30 * time.Millisecond)
	coalescedSource.Seed("hot-key", "value")
	coalescedCache := cache.NewCacheAsideStore(cache.NewLRU(100, time.Minute), coalescedSource)

	start = time.Now()
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			coalescedCache.Get("hot-key")
		}()
	}
	wg.Wait()
	coalescedElapsed := time.Since(start)
	fmt.Printf("coalesced, cache-aside:   source hit %d times, wall clock %s\n", coalescedSource.Calls(), coalescedElapsed)
}
