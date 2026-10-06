package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type stats struct {
	Delivered    int `json:"delivered"`
	DeadLettered int `json:"dead_lettered"`
	Queued       int `json:"queued"`
}

type deadLetter struct {
	Notification struct {
		ID       string `json:"ID"`
		Channel  string `json:"Channel"`
		Attempts int    `json:"Attempts"`
	} `json:"Notification"`
	LastError string `json:"LastError"`
}

func submit(baseURL, id, channel, recipient, priority string) bool {
	body, _ := json.Marshal(map[string]string{"id": id, "channel": channel, "recipient": recipient, "body": "hello", "priority": priority})
	resp, err := http.Post(baseURL+"/notifications", "application/json", bytes.NewReader(body))
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var out struct {
		Accepted bool `json:"accepted"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out.Accepted
}

func fetchStats(baseURL string) stats {
	resp, err := http.Get(baseURL + "/stats")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var s stats
	json.NewDecoder(resp.Body).Decode(&s)
	return s
}

func fetchDLQ(baseURL string) []deadLetter {
	resp, err := http.Get(baseURL + "/dlq")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var d []deadLetter
	json.NewDecoder(resp.Body).Decode(&d)
	return d
}

func waitFor(baseURL string, cond func(stats) bool, timeout time.Duration) stats {
	deadline := time.Now().Add(timeout)
	var s stats
	for time.Now().Before(deadline) {
		s = fetchStats(baseURL)
		if cond(s) {
			return s
		}
		time.Sleep(20 * time.Millisecond)
	}
	return s
}

func main() {
	baseURL := "http://localhost:8080"

	fmt.Println("=== deduplication: submitting the same notification id 20 times concurrently ===")
	accepted := 0
	results := make(chan bool, 20)
	for i := 0; i < 20; i++ {
		go func() { results <- submit(baseURL, "dedup-demo", "email", "a@example.com", "normal") }()
	}
	for i := 0; i < 20; i++ {
		if <-results {
			accepted++
		}
	}
	waitFor(baseURL, func(s stats) bool { return s.Queued == 0 }, 2*time.Second)
	fmt.Printf("20 concurrent submissions, same id: accepted=%d (should be 1)\n\n", accepted)

	fmt.Println("=== SMS rate limiting: 30 SMS sent as fast as possible (provider limit: burst 20, refill 5/s) ===")
	before := fetchStats(baseURL)
	for i := 0; i < 30; i++ {
		submit(baseURL, fmt.Sprintf("sms-%d", i), "sms", "+1555", "normal")
	}
	after := waitFor(baseURL, func(s stats) bool { return s.Delivered-before.Delivered+s.DeadLettered-before.DeadLettered >= 30 }, 5*time.Second)
	fmt.Printf("delivered=%d dead_lettered=%d (some SMS should dead-letter: the provider sender always succeeds, but the rate limiter in front of it rejects bursts past capacity, and this run's MaxAttempts is low enough that a sustained burst exhausts retries)\n\n", after.Delivered-before.Delivered, after.DeadLettered-before.DeadLettered)

	fmt.Println("=== unroutable channel: straight to DLQ, no retries wasted ===")
	beforeDLQ := fetchStats(baseURL).DeadLettered
	submit(baseURL, "pigeon-demo", "carrier-pigeon", "loft-7", "normal")
	waitFor(baseURL, func(s stats) bool { return s.DeadLettered > beforeDLQ }, 2*time.Second)
	dlq := fetchDLQ(baseURL)
	found := false
	for _, d := range dlq {
		if d.Notification.ID == "pigeon-demo" {
			found = true
			fmt.Printf("pigeon-demo in DLQ after %d attempt(s): %s\n\n", d.Notification.Attempts, d.LastError)
		}
	}
	if !found {
		fmt.Println("pigeon-demo not found in DLQ (unexpected)")
	}

	final := fetchStats(baseURL)
	fmt.Printf("=== final stats: delivered=%d dead_lettered=%d queued=%d ===\n", final.Delivered, final.DeadLettered, final.Queued)
}
