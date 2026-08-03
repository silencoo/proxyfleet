package subscription

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
)

const subscriptionPreviewTTL = 10 * time.Minute
const subscriptionPreviewNameLimit = 50

type subscriptionPreviewPlan struct {
	desired          *config.Config
	fetch            subscriptionFetchPlan
	preview          monitor.SubscriptionPreview
	expectedRevision uint64
	clear            bool
}

func (m *Manager) PreviewConfigAtRevision(ctx context.Context, request monitor.SubscriptionPreviewRequest, expectedRevision uint64) (monitor.SubscriptionPreview, error) {
	if m == nil || m.boxMgr == nil {
		return monitor.SubscriptionPreview{}, errors.New("subscription manager is unavailable")
	}
	live, revision := m.boxMgr.ConfigSnapshot()
	if live == nil {
		return monitor.SubscriptionPreview{}, errors.New("active config is unavailable")
	}
	if revision != expectedRevision {
		return monitor.SubscriptionPreview{}, configRevisionConflict(expectedRevision, revision)
	}
	desired := live.Clone()
	if request.SourcesProvided {
		if err := desired.SetSubscriptionSources(request.Sources); err != nil {
			return monitor.SubscriptionPreview{}, err
		}
	} else {
		cleanURLs, err := config.ValidateSubscriptionURLs(request.Subscriptions)
		if err != nil {
			return monitor.SubscriptionPreview{}, err
		}
		if err := desired.SetSubscriptionSources(config.SubscriptionSourcesFromURLs(cleanURLs)); err != nil {
			return monitor.SubscriptionPreview{}, err
		}
	}
	desired.SubscriptionRefresh.Enabled = request.Enabled
	desired.SubscriptionRefresh.Interval = request.Interval
	desired.SubscriptionRefresh.FetchConcurrency = config.NormalizeSubscriptionFetchConcurrency(request.FetchConcurrency)
	desired.SubscriptionRefresh.AllowPrivateNetworks = request.AllowPrivateNetworks
	desired.SubscriptionRefresh.MaxRemovedRatio = request.MaxRemovedRatio
	desired.SubscriptionRefresh.MinAvailableRatio = request.MinAvailableRatio
	desired.SubscriptionRefresh.NodeFailurePolicy = request.NodeFailurePolicy
	quarantine := request.QuarantineNewNodes
	desired.SubscriptionRefresh.QuarantineNewNodes = &quarantine

	fetch := subscriptionFetchPlan{cacheUpdates: make(map[string][]config.NodeConfig), activeKeys: make(map[string]struct{})}
	var err error
	if len(desired.Subscriptions) > 0 {
		fetch, err = m.fetchAllSubscriptions(ctx, desired, nodesFilePathForConfig(desired), false, nil)
		if err != nil {
			return monitor.SubscriptionPreview{}, err
		}
	}
	preview, err := newSubscriptionPreview(live.Nodes, fetch.nodes, desired)
	if err != nil {
		return monitor.SubscriptionPreview{}, err
	}
	plan := subscriptionPreviewPlan{desired: desired, fetch: fetch, preview: preview, expectedRevision: expectedRevision, clear: len(desired.Subscriptions) == 0}
	m.mu.Lock()
	if m.previews == nil {
		m.previews = make(map[string]subscriptionPreviewPlan)
	}
	now := time.Now()
	for token, existing := range m.previews {
		if !existing.preview.ExpiresAt.After(now) {
			delete(m.previews, token)
		}
	}
	m.previews[preview.Token] = plan
	m.mu.Unlock()
	return preview, nil
}

func (m *Manager) ApplyPreview(ctx context.Context, token string, expectedRevision uint64, confirmRisky bool) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.refreshSlot:
	}
	defer func() { m.refreshSlot <- struct{}{} }()

	token = strings.TrimSpace(token)
	m.mu.Lock()
	plan, ok := m.previews[token]
	if ok {
		delete(m.previews, token)
	}
	m.mu.Unlock()
	if !ok || token == "" || !plan.preview.ExpiresAt.After(time.Now()) {
		return errors.New("subscription preview expired or does not exist")
	}
	if plan.expectedRevision != expectedRevision {
		return configRevisionConflict(plan.expectedRevision, expectedRevision)
	}
	if plan.preview.Risky && !confirmRisky {
		return fmt.Errorf("%w: removing %d/%d nodes (%.1f%%)", monitor.ErrSubscriptionChangeGuard, plan.preview.Removed, plan.preview.PreviousTotal, plan.preview.RemovedRatio*100)
	}
	_, revision := m.boxMgr.ConfigSnapshot()
	if revision != expectedRevision {
		return configRevisionConflict(expectedRevision, revision)
	}

	var committed *config.Config
	var nodesPath string
	var err error
	if plan.clear {
		committed, err = m.commitClearedSubscriptions(ctx, plan.desired, ^uint64(0), 0, &expectedRevision)
	} else {
		committed, nodesPath, err = m.commitRefreshPlan(ctx, plan.desired, plan.fetch.nodes, ^uint64(0), 0, &expectedRevision)
	}
	if err != nil {
		return err
	}
	if !plan.clear {
		m.recordSourceFetch(plan.desired, plan.fetch)
	}

	if plan.clear {
		m.recordSubscriptionNodeFailures(committed.SubscriptionNodeFailurePolicyOrDefault(), nil)
	}
	m.mu.Lock()
	m.baseCfg = committed.Clone()
	if plan.clear {
		m.sourceCache = make(map[string][]config.NodeConfig)
		m.lastSubHash = ""
		m.lastNodesModTime = time.Time{}
	} else {
		for key := range m.sourceCache {
			if _, active := plan.fetch.activeKeys[key]; !active {
				delete(m.sourceCache, key)
			}
		}
		for key, nodes := range plan.fetch.cacheUpdates {
			m.sourceCache[key] = cloneNodes(nodes)
		}
		committedSubscriptionNodes := make([]config.NodeConfig, 0, len(committed.Nodes))
		for _, node := range committed.Nodes {
			if node.Source == config.NodeSourceSubscription {
				committedSubscriptionNodes = append(committedSubscriptionNodes, node)
			}
		}
		m.lastSubHash = m.computeNodesHash(committedSubscriptionNodes)
		if info, statErr := os.Stat(nodesPath); statErr == nil {
			m.lastNodesModTime = info.ModTime()
		}
	}
	m.status.LastRefresh = time.Now()
	m.status.RefreshCount++
	m.status.LastError = ""
	m.status.NodesModified = false
	m.status.NodeCount = len(committed.Nodes)
	m.mu.Unlock()
	return nil
}

func newSubscriptionPreview(current, candidate []config.NodeConfig, desired *config.Config) (monitor.SubscriptionPreview, error) {
	currentByKey := subscriptionNodesByKey(current)
	candidateByKey := make(map[string]config.NodeConfig, len(candidate))
	for index := range candidate {
		candidateByKey[candidate[index].NodeKey()] = candidate[index]
	}
	preview := monitor.SubscriptionPreview{
		PreviousTotal:      len(currentByKey),
		CandidateTotal:     len(candidateByKey),
		MaxRemovedRatio:    desired.SubscriptionMaxRemovedRatioOrDefault(),
		QuarantineNewNodes: desired.SubscriptionQuarantineNewNodesValue(),
		ExpiresAt:          time.Now().Add(subscriptionPreviewTTL),
	}
	for key, node := range candidateByKey {
		if _, exists := currentByKey[key]; exists {
			preview.Unchanged++
		} else {
			preview.Added++
			preview.AddedNames = append(preview.AddedNames, previewNodeName(node))
		}
	}
	for key, node := range currentByKey {
		if _, exists := candidateByKey[key]; !exists {
			preview.Removed++
			preview.RemovedNames = append(preview.RemovedNames, previewNodeName(node))
		}
	}
	if preview.PreviousTotal > 0 {
		preview.RemovedRatio = float64(preview.Removed) / float64(preview.PreviousTotal)
	}
	preview.Risky = preview.PreviousTotal > 0 && preview.RemovedRatio > preview.MaxRemovedRatio
	preview.RequiresConfirmation = preview.Risky
	sort.Strings(preview.AddedNames)
	sort.Strings(preview.RemovedNames)
	preview.AddedNames = truncateNames(preview.AddedNames)
	preview.RemovedNames = truncateNames(preview.RemovedNames)
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return monitor.SubscriptionPreview{}, fmt.Errorf("generate preview token: %w", err)
	}
	preview.Token = hex.EncodeToString(tokenBytes)
	return preview, nil
}

func subscriptionNodesByKey(nodes []config.NodeConfig) map[string]config.NodeConfig {
	result := make(map[string]config.NodeConfig)
	for index := range nodes {
		node := nodes[index]
		if node.Source == config.NodeSourceInline {
			continue
		}
		result[node.NodeKey()] = node
	}
	return result
}

func previewNodeName(node config.NodeConfig) string {
	if name := strings.TrimSpace(node.Name); name != "" {
		return name
	}
	if name := strings.TrimSpace(config.ExtractNodeName(node.URI)); name != "" {
		return name
	}
	return node.NodeKey()
}

func truncateNames(names []string) []string {
	if len(names) <= subscriptionPreviewNameLimit {
		return names
	}
	return append([]string(nil), names[:subscriptionPreviewNameLimit]...)
}

func validateAutomaticSubscriptionChange(live *config.Config, candidate []config.NodeConfig) error {
	if live == nil || live.SubscriptionRefresh.MaxRemovedRatio <= 0 {
		return nil
	}
	preview, err := newSubscriptionPreview(live.Nodes, candidate, live)
	if err != nil {
		return err
	}
	if preview.Risky {
		return fmt.Errorf("%w: automatic refresh would remove %d/%d nodes (%.1f%% > %.1f%%)", monitor.ErrSubscriptionChangeGuard, preview.Removed, preview.PreviousTotal, preview.RemovedRatio*100, preview.MaxRemovedRatio*100)
	}
	return nil
}
