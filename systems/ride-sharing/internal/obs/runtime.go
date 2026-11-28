package obs

import (
	"runtime"
	"sync"
	"time"
)

var processStart = time.Now()

// memStats shares one runtime.ReadMemStats between the gauges that read it.
// ReadMemStats briefly stops the world, so it is called at most once a second
// however many metrics use it.
type memStats struct {
	mu   sync.Mutex
	at   time.Time
	last runtime.MemStats
}

func (m *memStats) read() runtime.MemStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Since(m.at) > time.Second {
		runtime.ReadMemStats(&m.last)
		m.at = time.Now()
	}
	return m.last
}

// RegisterRuntime adds the standard Go runtime and process metrics, under
// the names the wider Prometheus ecosystem already uses, so existing
// dashboards work. service identifies the program in faultline_build_info.
func RegisterRuntime(r *Registry, service string) {
	ms := &memStats{}
	r.NewGaugeFunc("go_goroutines", "Number of goroutines that currently exist.", func() float64 { return float64(runtime.NumGoroutine()) })
	r.NewGaugeFunc("go_memstats_alloc_bytes", "Bytes of allocated heap objects.", func() float64 { return float64(ms.read().Alloc) })
	r.NewGaugeFunc("go_memstats_heap_inuse_bytes", "Bytes in in-use heap spans.", func() float64 { return float64(ms.read().HeapInuse) })
	r.NewGaugeFunc("go_memstats_sys_bytes", "Bytes obtained from the system.", func() float64 { return float64(ms.read().Sys) })
	r.NewCounterFunc("go_gc_cycles_total", "Completed garbage collection cycles.", func() float64 { return float64(ms.read().NumGC) })
	r.NewGaugeFunc("process_start_time_seconds", "Start time of the process, in seconds since the Unix epoch.", func() float64 { return float64(processStart.UnixNano()) / 1e9 })
	r.NewGaugeFuncVec("faultline_build_info", "Always 1. The labels identify the service and the Go version.", []string{"service", "go_version"},
		func() []Sample { return []Sample{{Labels: []string{service, runtime.Version()}, Value: 1}} })
}
