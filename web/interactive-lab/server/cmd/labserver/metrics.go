package main

import (
	"net/http"
	"strconv"

	"github.com/Sumit6258/Faultline/web/interactive-lab/server/internal/httpapi"
	"github.com/Sumit6258/Faultline/web/interactive-lab/server/internal/kafkalag"
	"github.com/Sumit6258/Faultline/web/interactive-lab/server/internal/obs"
)

// observe adds a /metrics endpoint and per request metrics around app, and
// publishes what the Kafka lab reports, so the lab's lag can be graphed and
// alerted on like any production consumer group.
//
// The event stream is left out of the latency histogram. A stream's duration
// is how long a browser tab stayed open, which would bury every real latency.
//
// produced and consumed restart from zero when the session is reset, which
// Prometheus handles as an ordinary counter reset.
func observe(app http.Handler, sess *kafkalag.Session, srv *httpapi.Server) http.Handler {
	reg := obs.NewRegistry()
	obs.RegisterRuntime(reg, "lab-server")
	reg.NewGaugeFunc("labserver_stream_subscribers", "Browser tabs connected to the event stream.",
		func() float64 { return float64(srv.Subscribers()) })
	reg.NewGaugeFunc("labserver_kafka_total_lag", "Messages produced but not yet consumed, over all partitions.",
		func() float64 { return float64(sess.Snapshot().TotalLag) })
	reg.NewGaugeFuncVec("labserver_kafka_partition_lag", "Messages produced but not yet consumed, per partition.", []string{"partition"},
		func() []obs.Sample {
			lag := sess.Snapshot().Lag
			out := make([]obs.Sample, len(lag))
			for p, l := range lag {
				out[p] = obs.Sample{Labels: []string{strconv.Itoa(p)}, Value: float64(l)}
			}
			return out
		})
	reg.NewCounterFunc("labserver_kafka_produced_total", "Messages the simulated producer has written.",
		func() float64 { return float64(sess.Snapshot().Produced) })
	reg.NewCounterFunc("labserver_kafka_consumed_total", "Messages the simulated consumers have processed.",
		func() float64 { return float64(sess.Snapshot().Consumed) })
	return obs.NewHTTPMetrics(reg, obs.SkipDuration("GET /api/kafka/stream")).Wrap(obs.Mount(app, "/metrics", reg.Handler()))
}
