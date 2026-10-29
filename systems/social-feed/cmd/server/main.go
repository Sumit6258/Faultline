package main

import (
	"flag"
	"log"
	"net/http"

	socialfeed "github.com/Sumit6258/Faultline/systems/social-feed"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	strategy := flag.String("strategy", "hybrid", "timeline strategy: write, read, or hybrid")
	maxInbox := flag.Int("max-inbox", 800, "bound on each precomputed inbox, write and hybrid only")
	threshold := flag.Int("celebrity-threshold", 10000, "followers at which the hybrid stops fanning an author out")
	flag.Parse()

	svc, err := socialfeed.New(socialfeed.Config{
		Strategy:           socialfeed.StrategyKind(*strategy),
		MaxInbox:           *maxInbox,
		CelebrityThreshold: *threshold,
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("social-feed listening on %s, strategy=%s", *addr, *strategy)
	log.Fatal(http.ListenAndServe(*addr, observe(socialfeed.NewHandler(svc), svc)))
}
