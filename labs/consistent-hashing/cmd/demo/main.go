package main

import (
	"fmt"
	"strconv"

	ch "github.com/Sumit6258/faultline/labs/consistent-hashing"
)

const numKeys = 10000

func main() {
	keys := make([]string, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = "key-" + strconv.Itoa(i)
	}

	fmt.Println("Distribution evenness: 5 nodes, 10000 keys")
	fmt.Println()
	demoDistribution(keys)

	fmt.Println()
	fmt.Println("Keys remapped when removing 1 of 5 nodes, 10000 keys")
	fmt.Println()
	demoRemapping(keys)
}

func demoDistribution(keys []string) {
	nodes := []string{"node-a", "node-b", "node-c", "node-d", "node-e"}

	naive := ch.NewNaiveModN(nodes...)
	printSpread("naive mod-N", countPerNode(keys, naive.Get))

	oneReplica := ch.NewRing(1)
	for _, n := range nodes {
		oneReplica.AddNode(n)
	}
	printSpread("consistent hash, 1 virtual node per real node", countPerNode(keys, oneReplica.Get))

	manyReplicas := ch.NewRing(150)
	for _, n := range nodes {
		manyReplicas.AddNode(n)
	}
	printSpread("consistent hash, 150 virtual nodes per real node", countPerNode(keys, manyReplicas.Get))
}

func demoRemapping(keys []string) {
	nodes := []string{"node-a", "node-b", "node-c", "node-d", "node-e"}

	naiveBefore := ch.NewNaiveModN(nodes...)
	beforeNaive := make(map[string]string, len(keys))
	for _, k := range keys {
		beforeNaive[k] = naiveBefore.Get(k)
	}
	naiveAfter := ch.NewNaiveModN("node-a", "node-b", "node-c", "node-d")
	movedNaive := 0
	for _, k := range keys {
		if naiveAfter.Get(k) != beforeNaive[k] {
			movedNaive++
		}
	}

	ring := ch.NewRing(150)
	for _, n := range nodes {
		ring.AddNode(n)
	}
	beforeRing := make(map[string]string, len(keys))
	for _, k := range keys {
		beforeRing[k] = ring.Get(k)
	}
	ring.RemoveNode("node-e")
	movedRing := 0
	for _, k := range keys {
		if ring.Get(k) != beforeRing[k] {
			movedRing++
		}
	}

	fmt.Printf("naive mod-N:        %d of %d keys moved (%.1f%%)\n", movedNaive, len(keys), 100*float64(movedNaive)/float64(len(keys)))
	fmt.Printf("consistent hashing: %d of %d keys moved (%.1f%%)\n", movedRing, len(keys), 100*float64(movedRing)/float64(len(keys)))
	fmt.Printf("theoretical minimum for consistent hashing when removing 1 of 5 nodes: about %.1f%%\n", 100.0/5.0)
}

func countPerNode(keys []string, get func(string) string) map[string]int {
	counts := make(map[string]int)
	for _, k := range keys {
		counts[get(k)]++
	}
	return counts
}

func printSpread(label string, counts map[string]int) {
	min, max, total := -1, -1, 0
	for _, c := range counts {
		if min == -1 || c < min {
			min = c
		}
		if c > max {
			max = c
		}
		total += c
	}
	avg := float64(total) / float64(len(counts))
	fmt.Printf("%-48s min=%d max=%d avg=%.0f spread=%.1f%%\n", label, min, max, avg, 100*float64(max-min)/avg)
}
