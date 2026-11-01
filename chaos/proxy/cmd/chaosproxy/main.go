// Command chaosproxy runs a fault injecting reverse proxy. See the proxy package.
//
//	chaosproxy -listen 127.0.0.1:9201 -upstream http://127.0.0.1:9105 -control 127.0.0.1:9202
//
//	curl -X PUT localhost:9202/fault -d '{"mode":"drop-response","path_prefix":"/payments"}'
//	curl -X DELETE localhost:9202/fault
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sumit6258/Faultline/chaos/proxy"
	"github.com/Sumit6258/Faultline/chaos/proxy/internal/obs"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:9201", "address clients connect to")
	upstream := flag.String("upstream", "", "the service to proxy to, such as http://127.0.0.1:9105")
	control := flag.String("control", "127.0.0.1:9202", "address of the control API and /metrics. There is no authentication, so keep it on loopback")
	flag.Parse()

	target, err := url.Parse(*upstream)
	if err != nil || target.Scheme == "" || target.Host == "" {
		log.Fatalf("-upstream must be a URL such as http://127.0.0.1:9105, got %q", *upstream)
	}

	reg := obs.NewRegistry()
	obs.RegisterRuntime(reg, "chaos-proxy")
	p := proxy.New(target, reg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	servers := []*http.Server{
		{Addr: *listen, Handler: p, ReadHeaderTimeout: 5 * time.Second},
		{Addr: *control, Handler: observe(p.ControlHandler(), reg), ReadHeaderTimeout: 5 * time.Second},
	}
	for _, s := range servers {
		s := s
		go func() {
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatal(err)
			}
		}()
	}
	log.Printf("chaos proxy: %s -> %s, control and metrics on %s", *listen, target, *control)

	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shutdown)
	}
}
