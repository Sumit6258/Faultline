package main

import (
	"net/http"

	notification "github.com/Sumit6258/Faultline/systems/notification-platform"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/obs"
)

// observe adds a /metrics endpoint and per request metrics around app, and
// publishes the pipeline's state by reading it when Prometheus scrapes.
func observe(app http.Handler, svc *notification.Service) http.Handler {
	reg := obs.NewRegistry()
	obs.RegisterRuntime(reg, "notification-platform")
	reg.NewCounterFunc("notification_delivered_total", "Notifications delivered to a sender.",
		func() float64 { return float64(svc.Pool.Delivered()) })
	reg.NewGaugeFunc("notification_dead_letter_queue_size", "Notifications that gave up and sit in the dead letter queue.",
		func() float64 { return float64(svc.Pool.DLQ.Len()) })
	reg.NewGaugeFunc("notification_queue_depth", "Notifications waiting to be delivered, ready or scheduled for later.",
		func() float64 { return float64(svc.Queue.Len()) })
	return obs.NewHTTPMetrics(reg).Wrap(obs.Mount(app, "/metrics", reg.Handler()))
}
