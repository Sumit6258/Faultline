package ridesharing

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/Sumit6258/Faultline/systems/ride-sharing/internal/geo"
)

// What the service holds per online driver, so capacity claims about memory rest on a measurement.
func TestMemoryPerOnlineDriver(t *testing.T) {
	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	s, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	const n = 50000
	before := heap()
	for i := 0; i < n; i++ {
		s.UpdateLocation(fmt.Sprintf("driver-%06d", i), geo.Point{Lat: 12.86 + float64(i%250)*0.001, Lon: 77.46 + float64(i/250%300)*0.001})
	}
	after := heap()
	per := float64(after-before) / n
	t.Logf("%d online drivers hold %.1f MB, %.0f bytes each", n, float64(after-before)/1e6, per)
	if per > 1000 {
		t.Errorf("%.0f bytes per driver is more than this design should need", per)
	}
	runtime.KeepAlive(s)
}
