package main

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sumit6258/Faultline/web/interactive-lab/server/internal/httpapi"
	"github.com/Sumit6258/Faultline/web/interactive-lab/server/internal/kafkalag"
)

func TestTheLabsLagIsPublishedAndTheStreamIsNotTimed(t *testing.T) {
	sess, err := kafkalag.New(4)
	if err != nil {
		t.Fatal(err)
	}
	srv := httpapi.New(sess)
	h := observe(srv.Handler(), sess, srv)

	for i := 0; i < 10; i++ {
		sess.Step() // one consumer, 40 in and 10 out per tick: lag climbs by 30
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/kafka/state", nil))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	b, _ := io.ReadAll(rec.Body)
	out := string(b)

	for _, want := range []string{
		"labserver_kafka_total_lag 300\n", // the lab's own demo prints 300 for one consumer
		"labserver_kafka_produced_total 400\n",
		"labserver_kafka_consumed_total 100\n",
		"labserver_stream_subscribers 0\n",
		`labserver_kafka_partition_lag{partition="0"} `,
		`http_requests_total{method="GET",route="GET /api/kafka/state",status="200"} 1` + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Count(out, "labserver_kafka_partition_lag{") != 4 {
		t.Errorf("expected one series per partition:\n%s", out)
	}
}

func TestStreamRequestsAreCountedButExcludedFromTheLatencyHistogram(t *testing.T) {
	sess, _ := kafkalag.New(4)
	srv := httpapi.New(sess)
	srv.Close() // the stream handler returns at once, as if the browser left
	h := observe(srv.Handler(), sess, srv)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/kafka/stream", nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	out := rec.Body.String()

	if !strings.Contains(out, `http_requests_total{method="GET",route="GET /api/kafka/stream",status="200"} 1`+"\n") {
		t.Errorf("the stream request should be counted:\n%s", out)
	}
	if strings.Contains(out, `http_request_duration_seconds_count{method="GET",route="GET /api/kafka/stream"}`) {
		t.Errorf("the stream must stay out of the latency histogram:\n%s", out)
	}
}
