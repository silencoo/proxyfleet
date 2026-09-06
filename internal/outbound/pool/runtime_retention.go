package pool

import (
	"sort"
	"time"
)

const (
	retiredHealthRetention = 7 * 24 * time.Hour
	maxRetiredHealthNodes  = 4096
)

// PruneRetiredRuntimeState bounds weak historical caches after a committed
// inventory change. Current nodes and unexpired administrative/automatic bans
// are never evicted. No live transport or client connection is closed here.
func PruneRetiredRuntimeState(active map[string]struct{}) {
	pruneRetiredRuntimeState(active, time.Now())
}

func pruneRetiredRuntimeState(active map[string]struct{}, now time.Time) {
	healthPersistence.mu.Lock()
	defer healthPersistence.mu.Unlock()
	type retired struct {
		tag     string
		updated time.Time
	}
	candidates := make([]retired, 0)
	for tag, record := range healthPersistence.records {
		if _, current := active[tag]; current {
			continue
		}
		if record.BlacklistedUntil.After(now) || record.CooldownUntil.After(now) {
			continue
		}
		candidates = append(candidates, retired{tag, record.UpdatedAt})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].updated.Equal(candidates[j].updated) {
			return candidates[i].tag < candidates[j].tag
		}
		return candidates[i].updated.Before(candidates[j].updated)
	})
	for index, candidate := range candidates {
		if index >= len(candidates)-maxRetiredHealthNodes && !candidate.updated.Before(now.Add(-retiredHealthRetention)) {
			continue
		}
		delete(healthPersistence.records, candidate.tag)
		delete(healthPersistence.domains, candidate.tag)
		delete(healthPersistence.dirtyNodes, candidate.tag)
		delete(healthPersistence.dirtyDomains, candidate.tag)
		if healthPersistence.deletedNodes == nil {
			healthPersistence.deletedNodes = make(map[string]struct{})
		}
		healthPersistence.deletedNodes[candidate.tag] = struct{}{}
		healthPersistence.dirty = true
	}
	// Domain-only cache entries can exist when traffic persistence was disabled.
	for tag, values := range healthPersistence.domains {
		if _, current := active[tag]; current {
			continue
		}
		if _, known := healthPersistence.records[tag]; known {
			continue
		}
		latest := time.Time{}
		for _, value := range values {
			if value.UpdatedAt.After(latest) {
				latest = value.UpdatedAt
			}
		}
		if latest.Before(now.Add(-retiredHealthRetention)) {
			delete(healthPersistence.domains, tag)
			if healthPersistence.dirtyDomains == nil {
				healthPersistence.dirtyDomains = make(map[string]struct{})
			}
			healthPersistence.dirtyDomains[tag] = struct{}{}
			healthPersistence.domainDirty = true
		}
	}
}
