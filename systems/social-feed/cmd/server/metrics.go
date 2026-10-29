package main

import (
	"net/http"

	socialfeed "github.com/Sumit6258/Faultline/systems/social-feed"
	"github.com/Sumit6258/Faultline/systems/social-feed/internal/obs"
)

// observe adds a /metrics endpoint and per request metrics around the feed's
// handler. The routes keep their own labels, "/posts" and "/timeline/" and so
// on, so read latency and write latency stay separate.
//
// The number this system exists to expose is the fan out amplification: inbox
// writes per post. It is the rate of socialfeed_inbox_writes_total divided by
// the rate of successful requests to /posts.
func observe(app http.Handler, svc *socialfeed.Service) http.Handler {
	reg := obs.NewRegistry()
	obs.RegisterRuntime(reg, "social-feed")
	reg.NewCounterFunc("socialfeed_inbox_writes_total", "Per follower inbox writes, the cost fan out on write pays for every post.",
		func() float64 { return float64(svc.Stats().InboxWrites) })
	reg.NewGaugeFuncVec("socialfeed_strategy_info", "Always 1. The strategy label says how timelines are built.", []string{"strategy"},
		func() []obs.Sample {
			return []obs.Sample{{Labels: []string{string(svc.Stats().Strategy)}, Value: 1}}
		})
	return obs.NewHTTPMetrics(reg).Wrap(obs.Mount(app, "/metrics", reg.Handler()))
}
