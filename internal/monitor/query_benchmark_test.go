package monitor

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

// Exercise a dashboard page with full diagnostic timelines. Population is
// outside the timer; no listeners or external probes are started.
func BenchmarkDashboardNodes(b *testing.B) {
	for _, size := range []int{420, 1000, 10000} {
		b.Run(fmt.Sprintf("nodes_%d", size), func(b *testing.B) {
			manager, err := NewManager(Config{})
			if err != nil {
				b.Fatal(err)
			}
			defer manager.Stop()
			for index := 0; index < size; index++ {
				tag := fmt.Sprintf("node-%05d", index)
				handle := manager.Register(NodeInfo{Tag: tag, Name: tag, Region: "jp", Country: "Japan"})
				handle.MarkInitialCheckDone(true)
				handle.ref.lastProbe = time.Duration(1+(index*37)%999) * time.Millisecond
				for event := 0; event < maxTimelineSize; event++ {
					handle.RecordSuccessWithLatency(handle.ref.lastProbe)
				}
			}
			server := &Server{mgr: manager}
			request := httptest.NewRequest("GET", "/api/nodes?page=1&page_size=50&region=all&status=all&sort=latency&order=asc", nil)
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				response := httptest.NewRecorder()
				server.handleNodes(response, request)
				if response.Code != 200 {
					b.Fatalf("dashboard status %d", response.Code)
				}
			}
		})
	}
}
