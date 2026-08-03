package monitor

import (
	"context"
	"errors"
	"time"

	"github.com/silencoo/proxyfleet/internal/config"
)

var ErrSubscriptionChangeGuard = errors.New("subscription change requires explicit confirmation")

type SubscriptionPreviewRequest struct {
	Subscriptions        []string
	Sources              []config.SubscriptionSourceConfig
	SourcesProvided      bool
	Enabled              bool
	Interval             time.Duration
	FetchConcurrency     int
	AllowPrivateNetworks bool
	MaxRemovedRatio      float64
	MinAvailableRatio    float64
	QuarantineNewNodes   bool
	NodeFailurePolicy    string
}

type SubscriptionPreview struct {
	Token                string    `json:"token"`
	Added                int       `json:"added"`
	Removed              int       `json:"removed"`
	Unchanged            int       `json:"unchanged"`
	PreviousTotal        int       `json:"previous_total"`
	CandidateTotal       int       `json:"candidate_total"`
	RemovedRatio         float64   `json:"removed_ratio"`
	MaxRemovedRatio      float64   `json:"max_removed_ratio"`
	Risky                bool      `json:"risky"`
	RequiresConfirmation bool      `json:"requires_confirmation"`
	QuarantineNewNodes   bool      `json:"quarantine_new_nodes"`
	AddedNames           []string  `json:"added_names,omitempty"`
	RemovedNames         []string  `json:"removed_names,omitempty"`
	ExpiresAt            time.Time `json:"expires_at"`
}

type SubscriptionPreviewer interface {
	PreviewConfigAtRevision(context.Context, SubscriptionPreviewRequest, uint64) (SubscriptionPreview, error)
	ApplyPreview(context.Context, string, uint64, bool) error
}
