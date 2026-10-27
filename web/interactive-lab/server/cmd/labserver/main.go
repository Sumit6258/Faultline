// Command labserver runs the real Faultline lab code behind a small HTTP API
// so the interactive lab in the browser can show it live. Today that is the
// Kafka consumer lag simulation from labs/kafka-messaging.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sumit6258/Faultline/web/interactive-lab/server/internal/httpapi"
	"github.com/Sumit6258/Faultline/web/interactive-lab/server/internal/kafkalag"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8081", "listen address. There is no authentication, so keep it on loopback")
	tick := flag.Duration("tick", 250*time.Millisecond, "wall clock time per simulated tick")
	partitions := flag.Int("partitions", 4, "partitions in the Kafka topic")
	flag.Parse()

	sess, err := kafkalag.New(*partitions)
	if err != nil {
		log.Fatal(err)
	}
	srv := httpapi.New(sess)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.Run(ctx, *tick)

	httpSrv := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close() // end open event streams, or Shutdown would wait on them
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	log.Printf("lab server listening on http://%s (kafka lag simulation: %d partitions, one tick every %s)", *addr, *partitions, *tick)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
