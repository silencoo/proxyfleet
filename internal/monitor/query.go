package monitor

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const maximumNodePageSize = 500

type NodeQuery struct {
	Page     int
	PageSize int
	Search   string
	Region   string
	Status   string
	Sort     string
	Order    string
}

type NodePagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	TotalItems int `json:"total_items"`
	TotalPages int `json:"total_pages"`
}

type NodeSummary struct {
	TotalNodes          int     `json:"total_nodes"`
	HealthyNodes        int     `json:"healthy_nodes"`
	UnavailableNodes    int     `json:"unavailable_nodes"`
	BlacklistedNodes    int     `json:"blacklisted_nodes"`
	ActiveConnections   int64   `json:"active_connections"`
	AverageQualityScore float64 `json:"average_quality_score"`
}

func parseNodeQuery(r *http.Request) (NodeQuery, bool, error) {
	query := NodeQuery{Page: 1, PageSize: 50, Region: "all", Status: "all", Sort: "latency", Order: "asc"}
	if r == nil {
		return query, false, nil
	}
	values := r.URL.Query()
	paginated := values.Has("page") || values.Has("page_size") || values.Has("search") || values.Has("region") || values.Has("status") || values.Has("sort") || values.Has("order")
	if value := strings.TrimSpace(values.Get("page")); value != "" {
		page, err := strconv.Atoi(value)
		if err != nil || page < 1 {
			return query, paginated, errors.New("page must be a positive integer")
		}
		query.Page = page
	}
	if value := strings.TrimSpace(values.Get("page_size")); value != "" {
		pageSize, err := strconv.Atoi(value)
		if err != nil || pageSize < 1 || pageSize > maximumNodePageSize {
			return query, paginated, errors.New("page_size must be between 1 and 500")
		}
		query.PageSize = pageSize
	}
	query.Search = strings.ToLower(strings.TrimSpace(values.Get("search")))
	if len(query.Search) > 256 {
		return query, paginated, errors.New("search is too long")
	}
	if value := strings.ToLower(strings.TrimSpace(values.Get("region"))); value != "" {
		query.Region = value
	}
	if value := strings.ToLower(strings.TrimSpace(values.Get("status"))); value != "" {
		query.Status = value
	}
	switch query.Status {
	case "all", "healthy", "unavailable", "blacklisted", "cooling", "unknown":
	default:
		return query, paginated, errors.New("unsupported node status filter")
	}
	if value := strings.ToLower(strings.TrimSpace(values.Get("sort"))); value != "" {
		query.Sort = value
	}
	switch query.Sort {
	case "status", "region", "name", "port", "latency", "connections", "failures", "score":
	default:
		return query, paginated, errors.New("unsupported node sort key")
	}
	if value := strings.ToLower(strings.TrimSpace(values.Get("order"))); value != "" {
		query.Order = value
	}
	if query.Order != "asc" && query.Order != "desc" {
		return query, paginated, errors.New("order must be asc or desc")
	}
	return query, paginated, nil
}

func queryNodes(snapshots []Snapshot, query NodeQuery) ([]Snapshot, NodePagination) {
	filtered := make([]Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		region := strings.ToLower(displayRegion(snapshot))
		if query.Region != "all" && region != query.Region {
			continue
		}
		if !nodeMatchesStatus(snapshot, query.Status) {
			continue
		}
		if query.Search != "" {
			haystack := strings.ToLower(strings.Join([]string{snapshot.Name, snapshot.Tag, region, snapshot.Country, strconv.Itoa(int(snapshot.Port))}, " "))
			if !strings.Contains(haystack, query.Search) {
				continue
			}
		}
		snapshot.Region = region
		filtered = append(filtered, snapshot)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		comparison := compareSnapshots(filtered[i], filtered[j], query.Sort)
		if comparison == 0 {
			comparison = strings.Compare(strings.ToLower(filtered[i].Name), strings.ToLower(filtered[j].Name))
		}
		if query.Order == "desc" {
			return comparison > 0
		}
		return comparison < 0
	})
	total := len(filtered)
	totalPages := 0
	if total > 0 {
		totalPages = (total + query.PageSize - 1) / query.PageSize
	}
	if totalPages > 0 && query.Page > totalPages {
		query.Page = totalPages
	}
	start := (query.Page - 1) * query.PageSize
	if start < 0 || start > total {
		start = total
	}
	end := start + query.PageSize
	if end > total {
		end = total
	}
	return append([]Snapshot(nil), filtered[start:end]...), NodePagination{Page: query.Page, PageSize: query.PageSize, TotalItems: total, TotalPages: totalPages}
}

func nodeMatchesStatus(snapshot Snapshot, status string) bool {
	switch status {
	case "healthy":
		return snapshot.InitialCheckDone && snapshot.Available && !snapshot.Blacklisted && !snapshot.CoolingDown
	case "unavailable":
		return snapshot.InitialCheckDone && (!snapshot.Available || snapshot.Blacklisted || snapshot.CoolingDown)
	case "blacklisted":
		return snapshot.Blacklisted
	case "cooling":
		return snapshot.CoolingDown
	case "unknown":
		return !snapshot.InitialCheckDone
	default:
		return true
	}
}

func nodeStatusRank(snapshot Snapshot) int {
	switch {
	case snapshot.Blacklisted:
		return 4
	case snapshot.CoolingDown:
		return 3
	case !snapshot.InitialCheckDone:
		return 2
	case !snapshot.Available:
		return 1
	default:
		return 0
	}
}

func compareSnapshots(left, right Snapshot, key string) int {
	switch key {
	case "status":
		return compareInt64(int64(nodeStatusRank(left)), int64(nodeStatusRank(right)))
	case "region":
		return strings.Compare(strings.ToLower(displayRegion(left)), strings.ToLower(displayRegion(right)))
	case "name":
		return strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
	case "port":
		return compareInt64(int64(left.Port), int64(right.Port))
	case "connections":
		return compareInt64(int64(left.ActiveConnections), int64(right.ActiveConnections))
	case "failures":
		return compareInt64(int64(left.FailureCount), int64(right.FailureCount))
	case "score":
		return compareFloat(left.QualityScore, right.QualityScore)
	default:
		leftLatency := left.LastLatencyMs
		rightLatency := right.LastLatencyMs
		if leftLatency < 0 {
			leftLatency = int64(^uint64(0) >> 1)
		}
		if rightLatency < 0 {
			rightLatency = int64(^uint64(0) >> 1)
		}
		return compareInt64(leftLatency, rightLatency)
	}
}

func compareInt64(left, right int64) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func compareFloat(left, right float64) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func summarizeNodes(snapshots []Snapshot) NodeSummary {
	summary := NodeSummary{TotalNodes: len(snapshots)}
	qualityTotal := 0.0
	for _, snapshot := range snapshots {
		if snapshot.InitialCheckDone && snapshot.Available && !snapshot.Blacklisted && !snapshot.CoolingDown {
			summary.HealthyNodes++
		} else if snapshot.InitialCheckDone {
			summary.UnavailableNodes++
		}
		if snapshot.Blacklisted || snapshot.CoolingDown {
			summary.BlacklistedNodes++
		}
		summary.ActiveConnections += int64(snapshot.ActiveConnections)
		qualityTotal += snapshot.QualityScore
	}
	if len(snapshots) > 0 {
		summary.AverageQualityScore = qualityTotal / float64(len(snapshots))
	}
	return summary
}

func topNodes(snapshots []Snapshot, key string, limit int) []Snapshot {
	if limit <= 0 {
		return nil
	}
	copyOfSnapshots := append([]Snapshot(nil), snapshots...)
	sort.SliceStable(copyOfSnapshots, func(i, j int) bool {
		if key == "score" {
			return copyOfSnapshots[i].QualityScore > copyOfSnapshots[j].QualityScore
		}
		left := copyOfSnapshots[i].LastLatencyMs
		right := copyOfSnapshots[j].LastLatencyMs
		if left <= 0 {
			return false
		}
		if right <= 0 {
			return true
		}
		return left < right
	})
	result := make([]Snapshot, 0, limit)
	for _, snapshot := range copyOfSnapshots {
		if !nodeMatchesStatus(snapshot, "healthy") {
			continue
		}
		if key == "latency" && snapshot.LastLatencyMs <= 0 {
			continue
		}
		result = append(result, snapshot)
		if len(result) == limit {
			break
		}
	}
	return result
}
