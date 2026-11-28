package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	ridesharing "github.com/Sumit6258/Faultline/systems/ride-sharing"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	strategy := flag.String("strategy", "greedy", "matching strategy: greedy or batched")
	index := flag.String("index", "grid", "driver index: grid or brute, brute exists to be compared with")
	tick := flag.Duration("tick", 250*time.Millisecond, "how often offers lapse and matching runs")
	window := flag.Duration("batch-window", 2*time.Second, "how long the batched strategy collects requests before matching")
	offerTTL := flag.Duration("offer-ttl", 10*time.Second, "how long a driver has to answer an offer")
	matchWait := flag.Duration("match-wait", 2*time.Minute, "how long a trip waits for a driver before being given up on")
	flag.Parse()

	m := newMetrics()
	svc, err := ridesharing.New(ridesharing.Config{
		Strategy:     ridesharing.StrategyKind(*strategy),
		Index:        ridesharing.IndexKind(*index),
		BatchWindow:  *window,
		OfferTTL:     *offerTTL,
		MaxMatchWait: *matchWait,
		Observer:     m,
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The service has no goroutines of its own. Everything that happens because time passed, an offer lapsing,
	// a trip being given up on, a batch being decided, happens when this loop calls Tick.
	go func() {
		t := time.NewTicker(*tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				svc.Tick()
			}
		}
	}()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           observe(ridesharing.NewHandler(svc), svc, m),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Printf("ride-sharing listening on %s, strategy=%s index=%s", *addr, *strategy, *index)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
