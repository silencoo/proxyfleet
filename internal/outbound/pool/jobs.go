package pool

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/silencoo/proxyfleet/internal/config"
	"github.com/silencoo/proxyfleet/internal/jobs"
)

var _ jobs.Controller = (*poolOutbound)(nil)

type jobRankCache struct {
	status          jobs.Status
	version         uint64
	eligibleVersion uint64
	at              time.Time
}

func (p *poolOutbound) job(name string) (config.JobConfig, bool) {
	for _, j := range p.options.Jobs {
		if j.Name == name {
			return j, true
		}
	}
	return config.JobConfig{}, false
}
func (p *poolOutbound) jobFromContext(ctx context.Context) (config.JobConfig, bool) {
	if metadata := adapter.ContextFrom(ctx); metadata != nil {
		for _, j := range p.options.Jobs {
			if j.Endpoint != "" && metadata.Inbound == config.EndpointInboundTag(j.Endpoint) {
				return j, true
			}
		}
	}
	return config.JobConfig{}, false
}

func (p *poolOutbound) startJobs() {
	hasAutomaticJob := false
	for _, j := range p.options.Jobs {
		hasAutomaticJob = hasAutomaticJob || j.AutomaticSelection()
	}
	if !hasAutomaticJob || p.jobStore == nil {
		return
	}
	ctx, cancel := context.WithCancel(p.ctx)
	p.healthMu.Lock()
	if p.closed.Load() || p.jobCancel != nil {
		p.healthMu.Unlock()
		cancel()
		return
	}
	p.jobCancel = cancel
	p.jobCtx = ctx
	p.healthMu.Unlock()
	// Each job owns its schedule. A slow target must not delay unrelated jobs;
	// the shared store still bounds total network probes across all workers.
	for _, j := range p.options.Jobs {
		if !j.AutomaticSelection() {
			continue
		}
		go func(j config.JobConfig) {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				if ctx.Err() != nil {
					return
				}
				_, _ = p.refreshJob(ctx, j, false)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}(j)
	}
}

// Manual jobs use the complete eligible Profile. Automatic jobs additionally
// require fresh target measurements; neither can escape into the global pool.
func (p *poolOutbound) jobStatus(j config.JobConfig) jobs.Status {
	s := jobs.Status{Name: j.Name, Mode: j.Mode, Selection: j.EffectiveSelection(), Selected: []jobs.SelectedNode{}}
	if j.AutomaticSelection() {
		s.TargetURL, s.Size = j.TargetURL, j.Size
	}
	if p.jobStore == nil || p.closed.Load() {
		return s
	}
	profile := p.profiles[j.Profile]
	if profile == nil {
		return s
	}
	if !j.AutomaticSelection() {
		for _, member := range p.members {
			if profileAllowsMember(profile, member) && supportsMemberNetwork(member, "tcp") {
				s.Candidates++
				if p.memberEligibleForProfile(member, "tcp", profile) {
					s.Selected = append(s.Selected, jobs.SelectedNode{Node: member.tag, Name: p.options.Metadata[member.tag].Name})
				}
			}
		}
		return s
	}
	p.jobRanksMu.Lock()
	defer p.jobRanksMu.Unlock()
	version := p.jobStore.Version(p.options.JobPolicies[j.Name])
	eligibleVersion := p.eligibleVersion.Load()
	if cached, ok := p.jobRanks[j.Name]; ok && cached.version == version && cached.eligibleVersion == eligibleVersion && time.Since(cached.at) < 500*time.Millisecond {
		status := cached.status
		valid := true
		for _, candidate := range status.Selected {
			// Quality thresholds and measurement expiry can change eligibility
			// without changing the transport index or publishing a new score.
			if !p.memberEligibleForProfile(p.memberByTag[candidate.Node], "tcp", profile) || candidate.Measurement == nil || time.Since(candidate.CheckedAt) > jobMeasurementMaxAge(j, status.Candidates) {
				valid = false
				break
			}
		}
		if valid {
			// Progress changes independently of measurements, including when a
			// cancelled probe publishes no score. Never cache this control state.
			status.Refreshing = p.jobStore.IsRefreshing(p.options.JobPolicies[j.Name])
			return status
		}
	}
	values, refreshing := p.jobStore.Measurements(p.options.JobPolicies[j.Name])
	s.Refreshing = refreshing
	allowed := make(map[string]bool)
	for _, member := range p.members {
		if profileAllowsMember(profile, member) && supportsMemberNetwork(member, "tcp") {
			s.Candidates++
			allowed[member.tag] = p.memberEligibleForProfile(member, "tcp", profile)
			if _, ok := values[member.tag]; ok {
				s.Measured++
			}
		}
	}
	maxAge := jobMeasurementMaxAge(j, s.Candidates)
	ranked := jobs.Rank(values, allowed, maxAge, time.Now())
	if len(ranked) > j.Size {
		ranked = ranked[:j.Size]
	}
	for _, measurement := range ranked {
		s.Selected = append(s.Selected, jobs.SelectedNode{Node: measurement.Node, Name: p.options.Metadata[measurement.Node].Name, Measurement: &measurement})
	}
	if p.jobRanks == nil {
		p.jobRanks = make(map[string]jobRankCache)
	}
	p.jobRanks[j.Name] = jobRankCache{status: s, version: version, eligibleVersion: eligibleVersion, at: time.Now()}
	return s
}

func jobMeasurementMaxAge(j config.JobConfig, candidates int) time.Duration {
	// Bounded batches rotate through the candidate set; allow two whole sweeps.
	sweeps := 2 * ((candidates+j.ProbeBatchSize-1)/j.ProbeBatchSize + 1)
	if sweeps < 3 {
		sweeps = 3
	}
	maxAge := time.Duration(sweeps) * j.RefreshEvery()
	if maxAge > 48*time.Hour {
		maxAge = 48 * time.Hour
	}
	return maxAge
}
func (p *poolOutbound) ListJobs() []jobs.Status {
	result := make([]jobs.Status, 0, len(p.options.Jobs))
	for _, j := range p.options.Jobs {
		result = append(result, p.jobStatus(j))
	}
	return result
}

func (p *poolOutbound) ListJobSessions() []jobs.SessionInfo {
	result := []jobs.SessionInfo{}
	if p.jobStore == nil {
		return result
	}
	type view struct {
		job        config.JobConfig
		candidates int
		values     map[string]jobs.Measurement
	}
	views := make(map[string]view)
	for _, j := range p.options.Jobs {
		v := view{job: j}
		if j.AutomaticSelection() {
			v.candidates = p.jobStatus(j).Candidates
			v.values, _ = p.jobStore.Measurements(p.options.JobPolicies[j.Name])
		}
		views[j.Name] = v
	}
	for _, lease := range p.jobStore.Leases() {
		state := "removed"
		if v, ok := views[lease.Job]; ok {
			state = p.jobLeaseState(v.job, lease, v.values[lease.Node], v.candidates)
		}
		result = append(result, jobs.SessionInfo{Job: lease.Job, Session: lease.Session, Node: lease.Node, State: state, ExpiresAt: lease.ExpiresAt})
	}
	return result
}

func (p *poolOutbound) jobLeaseState(j config.JobConfig, lease jobs.Lease, sample jobs.Measurement, candidates int) string {
	if lease.Policy != p.options.JobPolicies[j.Name] {
		return "policy_changed"
	}
	if !time.Now().Before(lease.ExpiresAt) {
		return "expired"
	}
	if p.closed.Load() {
		return "unavailable"
	}
	profile := p.profiles[j.Profile]
	if profile == nil || !p.memberEligibleForProfile(p.memberByTag[lease.Node], "tcp", profile) {
		return "paused"
	}
	if j.AutomaticSelection() && (!sample.Success || time.Since(sample.CheckedAt) > jobMeasurementMaxAge(j, candidates)) {
		return "paused"
	}
	if j.Endpoint == "" {
		if meta, ok := p.options.Metadata[lease.Node]; !ok || meta.Port == 0 {
			return "paused"
		}
	}
	return "ready"
}
func (p *poolOutbound) pickJobMember(j config.JobConfig, network string, tried map[string]struct{}) (*memberState, error) {
	if network != "tcp" {
		return nil, fmt.Errorf("jobs support TCP HTTP/SOCKS connections only")
	}
	selected := p.jobStatus(j).Selected
	if len(selected) == 0 {
		return nil, jobs.ErrUnavailable
	}
	start := int(p.rrCounter.Add(1)-1) % len(selected)
	var best *memberState
	for i := range selected {
		tag := selected[(start+i)%len(selected)].Node
		if _, tried := tried[tag]; tried {
			continue
		}
		member := p.memberByTag[tag]
		if p.memberEligibleForProfile(member, network, p.profiles[j.Profile]) && (best == nil || activeConnections(member) < activeConnections(best)) {
			best = member
		}
	}
	if best == nil {
		return nil, jobs.ErrUnavailable
	}
	return best, nil
}

func (p *poolOutbound) pickPinnedJobMember(ctx context.Context, j config.JobConfig, network string) (*memberState, error) {
	if network != "tcp" {
		return nil, jobs.ErrUnavailable
	}
	metadata := adapter.ContextFrom(ctx)
	if metadata == nil {
		return nil, jobs.ErrConflict
	}
	session, valid := config.ProxySessionID(p.options.JobEndpoints[j.Name].Username, metadata.User)
	if !valid {
		return nil, jobs.ErrConflict
	}
	a, err := p.AcquireJob(j.Name, session)
	if err != nil {
		return nil, err
	}
	if a.State != "ready" {
		return nil, fmt.Errorf("%w: pinned session is %s", jobs.ErrUnavailable, a.State)
	}
	return p.memberByTag[a.Node], nil
}

func (p *poolOutbound) RefreshJob(ctx context.Context, name string) (jobs.Status, error) {
	j, ok := p.job(name)
	if !ok {
		return jobs.Status{}, jobs.ErrNotFound
	}
	return p.refreshJob(ctx, j, true)
}
func (p *poolOutbound) refreshJob(ctx context.Context, j config.JobConfig, force bool) (jobs.Status, error) {
	if !j.AutomaticSelection() {
		return jobs.Status{}, fmt.Errorf("%w: refresh requires selection: auto", jobs.ErrConflict)
	}
	if p.jobStore == nil || p.closed.Load() {
		return jobs.Status{}, jobs.ErrUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p.healthMu.RLock()
	lifecycle := p.jobCtx
	p.healthMu.RUnlock()
	if lifecycle != nil {
		stop := context.AfterFunc(lifecycle, cancel)
		defer stop()
	}
	profile := p.profiles[j.Profile]
	if profile == nil {
		return jobs.Status{}, jobs.ErrUnavailable
	}
	candidates := make([]*memberState, 0)
	for _, member := range p.members {
		if profileAllowsMember(profile, member) && supportsMemberNetwork(member, "tcp") {
			candidates = append(candidates, member)
		}
	}
	if len(candidates) == 0 {
		return p.jobStatus(j), nil
	}
	key := p.options.JobPolicies[j.Name]
	if !p.jobStore.BeginRefresh(key, j.RefreshEvery(), force) {
		return p.jobStatus(j), nil
	}
	defer p.jobStore.EndRefresh(key)
	values, _ := p.jobStore.Measurements(key)
	sort.Slice(candidates, func(i, k int) bool {
		a, b := values[candidates[i].tag].CheckedAt, values[candidates[k].tag].CheckedAt
		if a.Equal(b) {
			return candidates[i].tag < candidates[k].tag
		}
		return a.Before(b)
	})
	if len(candidates) > j.ProbeBatchSize {
		candidates = candidates[:j.ProbeBatchSize]
	}
	work := make(chan *memberState)
	var wg sync.WaitGroup
	for i := 0; i < j.ProbeConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for member := range work {
				if ctx.Err() != nil || p.closed.Load() {
					continue
				}
				m := p.measureJobNode(ctx, j, member)
				if ctx.Err() == nil && !p.closed.Load() && m.Error != "local_resource" {
					p.jobStore.Record(key, m)
				}
			}
		}()
	}
	for _, member := range candidates {
		select {
		case work <- member:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()
	if ctx.Err() != nil {
		return jobs.Status{}, ctx.Err()
	}
	s := p.jobStatus(j)
	s.Refreshing = false
	return s, nil
}

// Full HTTP body timing is distinct from the pool's transport-first-byte data.
// Probes are bounded, do not follow redirects, and never include URL secrets in errors.
func (p *poolOutbound) measureJobNode(parent context.Context, j config.JobConfig, member *memberState) jobs.Measurement {
	m := jobs.Measurement{Node: member.tag}
	if err := p.checkResourceBackoff(parent); err != nil {
		m.Error = "cancelled"
		if isLocalResourceError(err) {
			m.Error = "local_resource"
		}
		return m
	}
	release, err := p.jobStore.AdmitProbe(parent)
	if err != nil {
		m.Error = "cancelled"
		return m
	}
	lifetime := newProbeLifetime(release)
	defer lifetime.Close()
	ctx, cancel := context.WithTimeout(parent, j.RequestTimeout())
	defer cancel()
	var connMu sync.Mutex
	var probeConn *jobProbeConn
	finished := false
	defer func() {
		connMu.Lock()
		defer connMu.Unlock()
		finished = true
		if probeConn != nil {
			_ = probeConn.Close()
		}
	}()
	transport := &http.Transport{DisableKeepAlives: true, MaxResponseHeaderBytes: 32 << 10, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: p.options.JobSkipCertVerify}}
	transport.DialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		done, admitted := lifetime.Hold()
		if !admitted {
			return nil, context.Canceled
		}
		defer done() // Keep the slot until the actual outbound dial exits.
		if err := p.checkResourceBackoff(ctx); err != nil {
			return nil, err
		}
		if !p.admitDial(member) {
			return nil, errPoolClosed
		}
		conn, err := member.outbound.DialContext(ctx, network, M.ParseSocksaddr(address))
		if err != nil {
			p.pauseForLocalResourceError(err)
			p.decActive(member)
			return nil, err
		}
		tracked := &jobProbeConn{Conn: conn, release: func() { p.decActive(member) }}
		connMu.Lock()
		if finished {
			connMu.Unlock()
			_ = tracked.Close()
			return nil, context.Canceled
		}
		probeConn = tracked
		connMu.Unlock()
		context.AfterFunc(ctx, func() { _ = tracked.Close() })
		if ctx.Err() != nil || p.closed.Load() {
			_ = tracked.Close()
			return nil, context.Canceled
		}
		return tracked, nil
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.TargetURL, nil)
	if err != nil {
		m.Error = "invalid_target"
		return m
	}
	req.Header.Set("User-Agent", "ProxyFleet-Job-Probe/1.0")
	started := time.Now()
	response, err := client.Do(req)
	if err != nil {
		m.Error = "request_failed"
		if p.pauseForLocalResourceError(err) {
			m.Error = "local_resource"
		}
		return m
	}
	defer response.Body.Close()
	m.StatusCode = response.StatusCode
	if response.StatusCode != j.ExpectedStatus {
		m.Error = "unexpected_status"
		return m
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, j.MaxResponseBytes+1))
	m.DurationMS = float64(time.Since(started)) / float64(time.Millisecond)
	if err != nil {
		m.Error = "body_read_failed"
		if p.pauseForLocalResourceError(err) {
			m.Error = "local_resource"
		}
		return m
	}
	if int64(len(body)) > j.MaxResponseBytes {
		m.Error = "response_too_large"
		return m
	}
	if j.BodyContains != "" && !bytes.Contains(body, []byte(j.BodyContains)) {
		m.Error = "body_mismatch"
		return m
	}
	m.Success = true
	return m
}

type jobProbeConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *jobProbeConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }

func (p *poolOutbound) jobProxyURL(address string, port uint16, user, password string) string {
	host := strings.Trim(address, "[]")
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = p.options.ExternalIP
		if host == "" {
			host = "127.0.0.1"
		}
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(int(port)))}
	if user != "" {
		u.User = url.UserPassword(user, password)
	}
	return u.String()
}
func (p *poolOutbound) AcquireJob(name, session string) (jobs.Access, error) {
	j, ok := p.job(name)
	if !ok {
		return jobs.Access{}, jobs.ErrNotFound
	}
	if p.closed.Load() || p.jobStore == nil {
		return jobs.Access{}, jobs.ErrUnavailable
	}
	a := jobs.Access{Job: j.Name, Mode: j.Mode, Selection: j.EffectiveSelection(), State: "ready", Concurrency: j.Concurrency, Timeout: j.Timeout, TimeoutMS: j.RequestTimeout().Milliseconds(), ExpectedStatus: j.ExpectedStatus, BodyContains: j.BodyContains}
	status := p.jobStatus(j)
	// Cached ranking is an optimization, not authorization to persist a new
	// assignment to a node that has since failed a transport health check.
	selected := make([]jobs.SelectedNode, 0, len(status.Selected))
	for _, candidate := range status.Selected {
		if p.memberEligibleForProfile(p.memberByTag[candidate.Node], "tcp", p.profiles[j.Profile]) {
			selected = append(selected, candidate)
		}
	}
	if j.Mode == "pooled" {
		if session != "" {
			return a, fmt.Errorf("%w: pooled jobs do not take a session ID", jobs.ErrConflict)
		}
		if len(selected) == 0 {
			return a, jobs.ErrUnavailable
		}
		e := p.options.JobEndpoints[j.Name]
		a.ProxyURL = p.jobProxyURL(e.Address, e.Port, e.Username, e.Password)
		return a, nil
	}
	if !validJobSession(session) {
		return a, fmt.Errorf("%w: session ID must be 1-128 characters without control characters", jobs.ErrConflict)
	}
	if j.Endpoint != "" && !config.ValidProxySessionID(session) {
		return a, fmt.Errorf("%w: endpoint session ID must use 1-128 ASCII letters, digits, underscores or hyphens", jobs.ErrConflict)
	}
	key := p.options.JobPolicies[j.Name]
	// Serialize allocation with Close: a request that inspected the old pool
	// before cutover must not publish a lease after that pool has retired.
	p.healthMu.RLock()
	if p.closed.Load() {
		p.healthMu.RUnlock()
		return a, jobs.ErrUnavailable
	}
	l, err := p.jobStore.Acquire(j, key, session, selected)
	p.healthMu.RUnlock()
	if err != nil {
		return a, err
	}
	a.Session, a.Node, a.ExpiresAt = l.Session, l.Node, l.ExpiresAt
	// An existing session may retain a healthy node outside the current top N.
	var sample jobs.Measurement
	if j.AutomaticSelection() {
		values, _ := p.jobStore.Measurements(key)
		sample = values[l.Node]
	}
	a.State = p.jobLeaseState(j, l, sample, status.Candidates)
	if a.State != "ready" {
		return a, nil
	}
	if j.Endpoint != "" {
		e := p.options.JobEndpoints[j.Name]
		a.ProxyURL = p.jobProxyURL(e.Address, e.Port, e.Username+config.SessionUsernameSeparator+session, e.Password)
	} else {
		meta, exists := p.options.Metadata[l.Node]
		if !exists || meta.Port == 0 {
			a.State = "paused"
			return a, nil
		}
		a.ProxyURL = p.jobProxyURL(meta.ListenAddress, meta.Port, meta.Username, meta.Password)
	}
	return a, nil
}
func validJobSession(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func (p *poolOutbound) ReleaseJob(name, session string) error {
	if p.jobStore == nil {
		return jobs.ErrUnavailable
	}
	if !validJobSession(session) {
		return jobs.ErrConflict
	}
	// Also permit cleanup of assignments belonging to deleted jobs.
	return p.jobStore.Release(name, session)
}
func (p *poolOutbound) ReportJob(name string, feedback jobs.Feedback) error {
	j, ok := p.job(name)
	if !ok {
		return jobs.ErrNotFound
	}
	if p.jobStore == nil || j.Mode != "pinned" {
		return jobs.ErrConflict
	}
	if !j.AutomaticSelection() {
		return fmt.Errorf("%w: target feedback requires selection: auto", jobs.ErrConflict)
	}
	l, ok := p.jobStore.Lease(name, feedback.Session)
	if !ok || l.Node != feedback.Node || l.Policy != p.options.JobPolicies[name] || !time.Now().Before(l.ExpiresAt) {
		return jobs.ErrConflict
	}
	if math.IsNaN(feedback.DurationMS) || math.IsInf(feedback.DurationMS, 0) || feedback.DurationMS <= 0 || feedback.DurationMS > float64(j.RequestTimeout().Milliseconds())+1000 || feedback.StatusCode < 0 || feedback.StatusCode > 599 {
		return jobs.ErrConflict
	}
	if feedback.Success && feedback.StatusCode != j.ExpectedStatus {
		return jobs.ErrConflict
	}
	m := jobs.Measurement{Node: l.Node, Success: feedback.Success, DurationMS: feedback.DurationMS, StatusCode: feedback.StatusCode}
	if !m.Success {
		m.Error = "application_failure"
	}
	p.jobStore.Record(l.Policy, m)
	return nil
}
