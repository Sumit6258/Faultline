package main

import (
	"encoding/json"
	"flag"
	"log"
	"math/rand"
	"net/http"
	"time"

	notification "github.com/Sumit6258/Faultline/systems/notification-platform"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/queue"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/senders"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/worker"
)

var priorityNames = map[string]queue.Priority{
	"low": queue.Low, "normal": queue.Normal, "high": queue.High, "urgent": queue.Urgent,
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	numWorkers := flag.Int("workers", 4, "number of delivery workers")
	flag.Parse()

	snd := map[string]senders.Sender{
		"email": senders.NewLocalSender("email", senders.AlwaysSucceed),
		"sms":   senders.NewRateLimited(senders.NewLocalSender("sms", senders.AlwaysSucceed), 20, 5),
		"push":  senders.NewLocalSender("push", senders.AlwaysSucceed),
	}
	backoff := worker.FullJitter(100*time.Millisecond, 5*time.Second, rand.New(rand.NewSource(time.Now().UnixNano())))
	svc := notification.New(snd, 5, backoff)

	stop := make(chan struct{})
	svc.RunWorkers(*numWorkers, 5*time.Millisecond, stop)

	mux := http.NewServeMux()

	mux.HandleFunc("/notifications", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			ID        string `json:"id"`
			Channel   string `json:"channel"`
			Recipient string `json:"recipient"`
			Body      string `json:"body"`
			Priority  string `json:"priority"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		pr, ok := priorityNames[body.Priority]
		if !ok {
			pr = queue.Normal
		}
		accepted := svc.Submit(notification.SubmitRequest{
			ID: body.ID, Channel: body.Channel, Recipient: body.Recipient, Body: body.Body, Priority: pr,
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"accepted": accepted})
	})

	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"delivered":     svc.Pool.Delivered(),
			"dead_lettered": svc.Pool.DLQ.Len(),
			"queued":        svc.Queue.Len(),
		})
	})

	mux.HandleFunc("/dlq", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(svc.Pool.DLQ.Items())
	})

	log.Printf("notification-platform listening on %s with %d workers", *addr, *numWorkers)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
