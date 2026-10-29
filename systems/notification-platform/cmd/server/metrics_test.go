package main

import (
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	notification "github.com/Sumit6258/Faultline/systems/notification-platform"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/queue"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/senders"
	"github.com/Sumit6258/Faultline/systems/notification-platform/internal/worker"
)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}

func TestMetricsFollowTheNotificationPipeline(t *testing.T) {
	snd := map[string]senders.Sender{"email": senders.NewLocalSender("email", senders.AlwaysSucceed)}
	backoff := worker.FullJitter(time.Millisecond, time.Millisecond, rand.New(rand.NewSource(1)))
	svc := notification.New(snd, 3, backoff)
	h := observe(http.NewServeMux(), svc)

	svc.Submit(notification.SubmitRequest{ID: "a", Channel: "email", Recipient: "x", Body: "1", Priority: queue.Normal})
	svc.Submit(notification.SubmitRequest{ID: "b", Channel: "email", Recipient: "x", Body: "2", Priority: queue.Urgent})
	svc.Submit(notification.SubmitRequest{ID: "c", Channel: "carrier-pigeon", Recipient: "x", Body: "3", Priority: queue.Low}) // no such channel

	before := scrape(t, h)
	if !strings.Contains(before, "notification_queue_depth 3\n") {
		t.Errorf("three notifications are waiting:\n%s", before)
	}

	for svc.Pool.ProcessOne() {
	}

	after := scrape(t, h)
	for _, want := range []string{
		"notification_delivered_total 2\n",
		"notification_dead_letter_queue_size 1\n",
		"notification_queue_depth 0\n",
		"# TYPE notification_delivered_total counter",
		"# TYPE notification_dead_letter_queue_size gauge",
	} {
		if !strings.Contains(after, want) {
			t.Errorf("missing %q in\n%s", want, after)
		}
	}
}
