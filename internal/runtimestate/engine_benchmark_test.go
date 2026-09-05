package runtimestate

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Measure replacement of an existing snapshot with the full 16-domain cache
// per node. Database creation and initial population are outside the timer.
func BenchmarkSaveSnapshot(b *testing.B) {
	for _, size := range []int{420, 1000, 10000} {
		b.Run(fmt.Sprintf("nodes_%d/domains_16", size), func(b *testing.B) {
			engine, err := Open(filepath.Join(b.TempDir(), "state.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer engine.Close()
			health := make([]HealthRecord, size)
			domains := make([]DomainLatencyRecord, 0, size*16)
			now := time.Now().UTC()
			for index := 0; index < size; index++ {
				tag := fmt.Sprintf("node-%05d", index)
				health[index] = HealthRecord{NodeID: tag, MonitorJSON: []byte(`{"available":true}`), UpdatedAt: now}
				for domain := 0; domain < 16; domain++ {
					domains = append(domains, DomainLatencyRecord{
						NodeID: tag, Domain: fmt.Sprintf("target-%d.example", domain), EWMAMs: 40,
						UpdatedAt: now, LastAccess: now,
					})
				}
			}
			if err := engine.SaveSnapshot(context.Background(), health, domains); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				if err := engine.SaveSnapshot(context.Background(), health, domains); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
