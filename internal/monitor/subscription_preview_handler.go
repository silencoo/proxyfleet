package monitor

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"easy_proxies/internal/config"
)

type subscriptionPreviewHTTPBody struct {
	Subscriptions        []string `json:"subscriptions"`
	Enabled              bool     `json:"enabled"`
	Interval             string   `json:"interval"`
	FetchConcurrency     *int     `json:"fetch_concurrency,omitempty"`
	AllowPrivateNetworks *bool    `json:"allow_private_networks,omitempty"`
	MaxRemovedRatio      *float64 `json:"max_removed_ratio,omitempty"`
	MinAvailableRatio    *float64 `json:"min_available_ratio,omitempty"`
	QuarantineNewNodes   *bool    `json:"quarantine_new_nodes,omitempty"`
	NodeFailurePolicy    *string  `json:"node_failure_policy,omitempty"`
}

func (s *Server) handleSubscriptionPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONMethodNotAllowed(w, http.MethodPost)
		return
	}
	refresher := s.subscriptionRefresher()
	previewer, ok := refresher.(SubscriptionPreviewer)
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, "当前订阅管理器不支持变更预览")
		return
	}
	var body subscriptionPreviewHTTPBody
	if err := decodeStrictJSON(w, r, maxSubscriptionConfigBodyBytes, &body); err != nil {
		writeStrictJSONError(w, err)
		return
	}
	request, err := s.subscriptionPreviewRequest(body)
	if err != nil {
		writeSettingsBadRequest(w, err.Error())
		return
	}
	expectedRevision, err := parseSettingsIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeJSONError(w, http.StatusPreconditionRequired, "订阅设置版本已缺失，请重新载入后再预览")
		return
	}
	preview, err := previewer.PreviewConfigAtRevision(r.Context(), request, expectedRevision)
	if err != nil {
		if errors.Is(err, ErrSubscriptionConfigRevisionConflict) {
			writeJSONError(w, http.StatusPreconditionFailed, "订阅设置已被其他操作更新，请重新载入")
			return
		}
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("订阅预览失败: %v", err))
		return
	}
	writeJSON(w, preview)
}

func (s *Server) subscriptionPreviewRequest(body subscriptionPreviewHTTPBody) (SubscriptionPreviewRequest, error) {
	interval, err := time.ParseDuration(body.Interval)
	if err != nil || interval < 5*time.Minute {
		return SubscriptionPreviewRequest{}, errors.New("订阅刷新间隔格式无效或小于 5 分钟")
	}
	cleanURLs, err := config.ValidateSubscriptionURLs(body.Subscriptions)
	if err != nil {
		return SubscriptionPreviewRequest{}, fmt.Errorf("订阅链接无效: %w", err)
	}
	s.cfgMu.RLock()
	cfg := s.cfgSrc.Clone()
	s.cfgMu.RUnlock()
	fetchConcurrency := config.NormalizeSubscriptionFetchConcurrency(0)
	allowPrivateNetworks := false
	maxRemovedRatio := 0.5
	minAvailableRatio := 0.0
	quarantineNewNodes := true
	nodeFailurePolicy := "skip"
	if cfg != nil {
		fetchConcurrency = config.NormalizeSubscriptionFetchConcurrency(cfg.SubscriptionRefresh.FetchConcurrency)
		allowPrivateNetworks = cfg.SubscriptionRefresh.AllowPrivateNetworks
		maxRemovedRatio = cfg.SubscriptionMaxRemovedRatioOrDefault()
		minAvailableRatio = cfg.SubscriptionRefresh.MinAvailableRatio
		quarantineNewNodes = cfg.SubscriptionQuarantineNewNodesValue()
		nodeFailurePolicy = cfg.SubscriptionNodeFailurePolicyOrDefault()
	}
	if body.FetchConcurrency != nil {
		if *body.FetchConcurrency < 1 || *body.FetchConcurrency > 32 {
			return SubscriptionPreviewRequest{}, errors.New("订阅抓取并发数必须在 1 到 32 之间")
		}
		fetchConcurrency = *body.FetchConcurrency
	}
	if body.AllowPrivateNetworks != nil {
		allowPrivateNetworks = *body.AllowPrivateNetworks
	}
	if body.MaxRemovedRatio != nil {
		if *body.MaxRemovedRatio <= 0 || *body.MaxRemovedRatio > 1 {
			return SubscriptionPreviewRequest{}, errors.New("最大删除比例必须大于 0 且不超过 1")
		}
		maxRemovedRatio = *body.MaxRemovedRatio
	}
	if body.MinAvailableRatio != nil {
		if *body.MinAvailableRatio < 0 || *body.MinAvailableRatio > 1 {
			return SubscriptionPreviewRequest{}, errors.New("最小可用比例必须在 0 到 1 之间")
		}
		minAvailableRatio = *body.MinAvailableRatio
	}
	if body.QuarantineNewNodes != nil {
		quarantineNewNodes = *body.QuarantineNewNodes
	}
	if body.NodeFailurePolicy != nil {
		nodeFailurePolicy = strings.ToLower(strings.TrimSpace(*body.NodeFailurePolicy))
		if nodeFailurePolicy != "skip" && nodeFailurePolicy != "strict" {
			return SubscriptionPreviewRequest{}, errors.New("坏节点处理策略必须为 skip 或 strict")
		}
	}
	return SubscriptionPreviewRequest{
		Subscriptions: cleanURLs, Enabled: body.Enabled, Interval: interval,
		FetchConcurrency: fetchConcurrency, AllowPrivateNetworks: allowPrivateNetworks,
		MaxRemovedRatio: maxRemovedRatio, MinAvailableRatio: minAvailableRatio,
		QuarantineNewNodes: quarantineNewNodes,
		NodeFailurePolicy:  nodeFailurePolicy,
	}, nil
}
