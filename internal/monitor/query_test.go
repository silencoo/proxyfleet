package monitor

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func TestTopNodesPreservesStableRanking(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 100; iteration++ {
		snapshots := make([]Snapshot, rng.Intn(500))
		for index := range snapshots {
			snapshots[index] = Snapshot{
				NodeInfo:         NodeInfo{Tag: fmt.Sprint(index)},
				LastLatencyMs:    int64(rng.Intn(12) - 2),
				QualityScore:     float64(rng.Intn(10)),
				InitialCheckDone: rng.Intn(5) != 0,
				Available:        rng.Intn(5) != 0,
				Blacklisted:      rng.Intn(20) == 0,
				CoolingDown:      rng.Intn(20) == 0,
			}
		}
		original := append([]Snapshot{}, snapshots...)
		for _, key := range []string{"score", "latency"} {
			for _, limit := range []int{-1, 0, 1, 10, 50, 500} {
				got := topNodes(snapshots, key, limit)
				want := referenceTopNodes(snapshots, key, limit)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("iteration=%d key=%s limit=%d: ranking differs from stable sort", iteration, key, limit)
				}
				if !reflect.DeepEqual(snapshots, original) {
					t.Fatal("topNodes changed the input snapshots")
				}
			}
		}
	}
}

func TestTopNodesEmptyResults(t *testing.T) {
	for _, key := range []string{"score", "latency"} {
		for _, nodes := range [][]Snapshot{
			nil,
			{},
			{{InitialCheckDone: true, Available: false}},
			{{InitialCheckDone: true, Available: true, Blacklisted: true}},
			{{InitialCheckDone: true, Available: true, CoolingDown: true}},
		} {
			if got := topNodes(nodes, key, 10); got == nil || len(got) != 0 {
				t.Fatalf("key=%s nodes=%+v: got %+v, want a non-nil empty list", key, nodes, got)
			}
		}
	}
}

// Retain the full-sort implementation as an independent ranking oracle. Ties,
// health filtering, and missing latencies must keep their existing API behavior.
func referenceTopNodes(snapshots []Snapshot, key string, limit int) []Snapshot {
	if limit <= 0 {
		return nil
	}
	ordered := append([]Snapshot(nil), snapshots...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if key == "score" {
			return ordered[i].QualityScore > ordered[j].QualityScore
		}
		if ordered[i].LastLatencyMs <= 0 {
			return false
		}
		if ordered[j].LastLatencyMs <= 0 {
			return true
		}
		return ordered[i].LastLatencyMs < ordered[j].LastLatencyMs
	})
	result := make([]Snapshot, 0, limit)
	for _, snapshot := range ordered {
		if !nodeMatchesStatus(snapshot, "healthy") || key == "latency" && snapshot.LastLatencyMs <= 0 {
			continue
		}
		result = append(result, snapshot)
		if len(result) == limit {
			break
		}
	}
	return result
}
