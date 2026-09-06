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

// Same populated database as the snapshot benchmark, but one node's traffic
// changed. This is the normal coalesced dirty-node persistence path.
func BenchmarkSaveChangedNode(b *testing.B) {
	for _, size := range []int{420, 1000, 10000} {
		b.Run(fmt.Sprintf("nodes_%d/domains_16", size), func(b *testing.B) {
			e, err := Open(filepath.Join(b.TempDir(), "state.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer e.Close()
			health := make([]HealthRecord, size)
			domains := make([]DomainLatencyRecord, 0, size*16)
			for n := 0; n < size; n++ {
				tag := fmt.Sprintf("node-%05d", n)
				health[n] = HealthRecord{NodeID: tag, MonitorJSON: []byte(`{"available":true}`)}
				for d := 0; d < 16; d++ {
					domains = append(domains, DomainLatencyRecord{NodeID: tag, Domain: fmt.Sprintf("target-%d.example", d), EWMAMs: 40})
				}
			}
			if err := e.SaveSnapshot(context.Background(), health, domains); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				health[0].Failures = n
				if err := e.SaveChanges(context.Background(), health[:1], domains[:16], []string{health[0].NodeID}, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
