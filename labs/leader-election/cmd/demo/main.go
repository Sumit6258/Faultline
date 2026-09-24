package main

import (
	"fmt"
	"time"

	le "github.com/Sumit6258/Faultline/labs/leader-election"
)

func main() {
	cluster := le.NewCluster(200 * time.Millisecond)

	fmt.Println("3 nodes start heartbeating. node-1 heartbeats first.")
	term, isLeader := cluster.Heartbeat("node-1")
	fmt.Printf("node-1 heartbeat: term=%d leader=%v\n", term, isLeader)

	_, isLeader = cluster.Heartbeat("node-2")
	fmt.Printf("node-2 heartbeat: leader=%v (refused, node-1 already leads this term)\n", isLeader)

	_, isLeader = cluster.Heartbeat("node-3")
	fmt.Printf("node-3 heartbeat: leader=%v (refused, node-1 already leads this term)\n", isLeader)

	fmt.Println()
	fmt.Println("node-1 keeps heartbeating normally for a while...")
	for i := 0; i < 3; i++ {
		time.Sleep(50 * time.Millisecond)
		term, isLeader = cluster.Heartbeat("node-1")
		fmt.Printf("node-1 heartbeat: term=%d leader=%v\n", term, isLeader)
	}

	fmt.Println()
	fmt.Println("node-1 crashes. it stops heartbeating.")
	fmt.Println("waiting past the 200ms timeout...")
	time.Sleep(250 * time.Millisecond)

	fmt.Println()
	fmt.Println("node-2 and node-3 both notice the silence and heartbeat.")
	term2, isLeader2 := cluster.Heartbeat("node-2")
	fmt.Printf("node-2 heartbeat: term=%d leader=%v\n", term2, isLeader2)
	term3, isLeader3 := cluster.Heartbeat("node-3")
	fmt.Printf("node-3 heartbeat: term=%d leader=%v\n", term3, isLeader3)

	leader, term := cluster.Leader()
	fmt.Printf("\nfinal leader: %s, term %d\n", leader, term)
}
