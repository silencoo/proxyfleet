package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/silencoo/proxyfleet/internal/config"
	"github.com/silencoo/proxyfleet/internal/jobs"
)

func jobPoolFixture(t *testing.T) *poolOutbound {
	t.Helper()
	ResetSharedStateStore()
	t.Cleanup(ResetSharedStateStore)
	s, _ := jobs.Open("")
	p := &poolOutbound{ctx: context.Background(), jobStore: s, memberByTag: make(map[string]*memberState), eligibleTCP: newMemberSet(3), eligibleUDP: newMemberSet(0), profiles: map[string]*compiledProfile{"fast": {name: "fast", allowed: map[string]struct{}{"a": {}, "b": {}, "c": {}}}}, options: normalizeOptions(Options{
		Jobs:             []config.JobConfig{{Name: "catalog", Mode: "pooled", Selection: "auto", Profile: "fast", Endpoint: "catalog", Size: 2, Concurrency: 4, Timeout: "1s", RefreshInterval: "1m", ProbeBatchSize: 3, ProbeConcurrency: 2, MaxResponseBytes: 1024, ExpectedStatus: 200, TargetURL: "https://example.test/"}, {Name: "accounts", Mode: "pinned", Selection: "auto", Profile: "fast", Size: 2, Concurrency: 2, Timeout: "1s", RefreshInterval: "1m", ProbeBatchSize: 3, SessionTTL: "1h", MaxSessions: 10, ExpectedStatus: 200}},
		JobPolicies:      map[string]string{"catalog": "catalog-policy", "accounts": "accounts-policy"},
		JobEndpoints:     map[string]config.EndpointConfig{"catalog": {Address: "127.0.0.1", Port: 2324}},
		DedicatedMembers: map[string]string{"in-a": "a"}, Metadata: map[string]MemberMeta{"a": {ListenAddress: "127.0.0.1", Port: 24001}, "b": {ListenAddress: "127.0.0.1", Port: 24002}, "c": {ListenAddress: "127.0.0.1", Port: 24003}}, FailOpen: true,
	})}
	for i, tag := range []string{"a", "b", "c"} {
		member := &memberState{tag: tag, shared: acquireSharedState(tag)}
		p.members = append(p.members, member)
		p.memberByTag[tag] = member
		p.eligibleTCP.add(member)
		for _, key := range p.options.JobPolicies {
			s.Record(key, jobs.Measurement{Node: tag, Success: true, DurationMS: float64((i + 1) * 10)})
		}
	}
	p.initialized.Store(true)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestJobSessionListingIsReadOnlyAndIncludesRemovedJobs(t *testing.T) {
	p := jobPoolFixture(t)
	j := p.options.Jobs[1]
	for _, item := range []struct{ session, policy, ttl string }{
		{"ready", "accounts-policy", "1h"}, {"expired", "accounts-policy", "-1s"}, {"changed", "previous-policy", "1h"},
	} {
		j.SessionTTL = item.ttl
		if _, err := p.jobStore.Acquire(j, item.policy, item.session, []jobs.SelectedNode{{Node: "a"}}); err != nil {
			t.Fatal(err)
		}
	}
	j.Name = "deleted"
	if _, err := p.jobStore.Acquire(j, "old", "orphan", []jobs.SelectedNode{{Node: "a"}}); err != nil {
		t.Fatal(err)
	}
	before := p.jobStore.Leases()
	want := map[string]string{"ready": "ready", "expired": "expired", "changed": "policy_changed", "orphan": "removed"}
	for _, item := range p.ListJobSessions() {
		if item.State != want[item.Session] {
			t.Fatalf("incorrect session state: %+v", item)
		}
	}
	after := p.jobStore.Leases()
	if len(before) != len(after) {
		t.Fatal("listing changed bindings")
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatal("listing changed or renewed a session")
		}
	}
	p.setMemberEligible(p.memberByTag["a"], false)
	for _, item := range p.ListJobSessions() {
		if item.Session == "ready" && item.State != "paused" {
			t.Fatal("listing did not reflect node failure")
		}
	}
}

func TestNewSessionSkipsUnhealthyCachedCandidates(t *testing.T) {
	p := jobPoolFixture(t)
	p.jobStatus(p.options.Jobs[1])
	// Transport health changes must override the previous ranking immediately.
	p.setMemberEligible(p.memberByTag["a"], false)
	access, err := p.AcquireJob("accounts", "new-session")
	if err != nil || access.State != "ready" || access.Node != "b" {
		t.Fatalf("new session used a failed cached node: %+v %v", access, err)
	}
	p.setMemberEligible(p.memberByTag["b"], false)
	p.setMemberEligible(p.memberByTag["c"], false)
	if _, err := p.AcquireJob("accounts", "unavailable-session"); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatalf("unavailable profile allocated a new session: %v", err)
	}
	if _, ok := p.jobStore.Lease("accounts", "unavailable-session"); ok {
		t.Fatal("failed acquisition persisted an unusable binding")
	}
	again, err := p.AcquireJob("accounts", "new-session")
	if err != nil || again.State != "paused" || again.Node != "b" {
		t.Fatalf("existing session silently switched: %+v %v", again, err)
	}
}

func TestBenchmarkProgressDoesNotUseCachedRefreshState(t *testing.T) {
	p := jobPoolFixture(t)
	j := p.options.Jobs[0]
	if p.jobStatus(j).Refreshing {
		t.Fatal("unexpected initial refresh")
	}
	key := p.options.JobPolicies[j.Name]
	if !p.jobStore.BeginRefresh(key, time.Minute, true) {
		t.Fatal("refresh did not start")
	}
	if !p.jobStatus(j).Refreshing {
		t.Fatal("cached status hid the running benchmark")
	}
	p.jobStore.EndRefresh(key)
	if p.jobStatus(j).Refreshing {
		t.Fatal("cached status left a completed benchmark running")
	}
}

func TestJobRankingRejectsScoresReplacedAfterCacheEviction(t *testing.T) {
	for index, mode := range []string{"pooled", "pinned"} {
		t.Run(mode, func(t *testing.T) {
			p := jobPoolFixture(t)
			j := p.options.Jobs[index]
			if status := p.jobStatus(j); len(status.Selected) != 2 || status.Selected[0].Node != "a" {
				t.Fatal("fixture did not cache the initial ranking")
			}
			if mode == "pinned" {
				if access, err := p.AcquireJob(j.Name, "existing"); err != nil || access.Node != "a" || access.State != "ready" {
					t.Fatalf("initial assignment: %+v %v", access, err)
				}
			}
			// Configuration changes leave old policy scores behind. Force eviction of
			// the initial score set, then publish equally many, but different, results.
			for i := range 128 {
				key := fmt.Sprintf("retired-policy-%d", i)
				p.jobStore.BeginRefresh(key, time.Minute, false)
				p.jobStore.Record(key, jobs.Measurement{Node: "spare", Success: true, DurationMS: 10})
				p.jobStore.EndRefresh(key)
			}
			key := p.options.JobPolicies[j.Name]
			if values, _ := p.jobStore.Measurements(key); len(values) != 0 {
				t.Fatal("fixture did not evict the original measurements")
			}
			p.jobStore.Record(key, jobs.Measurement{Node: "a", Success: false})
			p.jobStore.Record(key, jobs.Measurement{Node: "b", Success: false})
			p.jobStore.Record(key, jobs.Measurement{Node: "c", Success: true, DurationMS: 30})
			// Keep the ranking cache within its TTL regardless of the host's speed.
			cached := p.jobRanks[j.Name]
			cached.at = time.Now()
			p.jobRanks[j.Name] = cached
			if mode == "pooled" {
				member, err := p.pickJobMember(j, "tcp", nil)
				if err != nil || member.tag != "c" {
					t.Fatalf("routing reused a ranking from evicted measurements: member=%v err=%v", member, err)
				}
			} else {
				if access, err := p.AcquireJob(j.Name, "new"); err != nil || access.Node != "c" || access.State != "ready" {
					t.Fatalf("new session used the obsolete ranking: %+v %v", access, err)
				}
				if access, err := p.AcquireJob(j.Name, "existing"); err != nil || access.Node != "a" || access.State != "paused" {
					t.Fatalf("cache eviction changed the existing binding: %+v %v", access, err)
				}
			}
		})
	}
}

func TestJobRankingPromotesAndRestoresHealthyNodes(t *testing.T) {
	for _, index := range []int{0, 1} {
		t.Run([]string{"pooled", "pinned"}[index], func(t *testing.T) {
			p := jobPoolFixture(t)
			p.options.Jobs[index].Size = 1
			j := p.options.Jobs[index]
			session := ""
			if j.Mode == "pinned" {
				session = "existing"
			}
			if _, err := p.AcquireJob(j.Name, session); err != nil {
				t.Fatal(err)
			}
			p.setMemberEligible(p.memberByTag["a"], false)
			if j.Mode == "pooled" {
				member, err := p.pickJobMember(j, "tcp", nil)
				if err != nil || member.tag != "b" {
					t.Fatalf("healthy spare was not promoted: %v %v", member, err)
				}
			} else {
				access, err := p.AcquireJob(j.Name, "new")
				if err != nil || access.Node != "b" || access.State != "ready" {
					t.Fatalf("new session could not use healthy spare: %+v %v", access, err)
				}
				access, err = p.AcquireJob(j.Name, "existing")
				if err != nil || access.Node != "a" || access.State != "paused" {
					t.Fatalf("existing session was rebound: %+v %v", access, err)
				}
			}
			p.setMemberEligible(p.memberByTag["b"], false)
			p.setMemberEligible(p.memberByTag["c"], false)
			if status := p.jobStatus(j); len(status.Selected) != 0 {
				t.Fatalf("failed nodes remain in selection: %+v", status)
			}
			p.setMemberEligible(p.memberByTag["a"], true)
			if status := p.jobStatus(j); len(status.Selected) != 1 || status.Selected[0].Node != "a" {
				t.Fatalf("recovered node hidden by cached empty ranking: %+v", status)
			}
		})
	}
}

func TestJobRankingRejectsExpiredCachedMeasurements(t *testing.T) {
	p := jobPoolFixture(t)
	for i := range p.options.Jobs {
		// Compress the measurement lifetime while staying inside the rank cache TTL.
		p.options.Jobs[i].RefreshInterval = "40ms"
		if status := p.jobStatus(p.options.Jobs[i]); len(status.Selected) == 0 {
			t.Fatal("fixture measurements already expired")
		}
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := p.pickJobMember(p.options.Jobs[0], "tcp", nil); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatalf("pooled routing accepted stale cached measurements: %v", err)
	}
	if _, err := p.AcquireJob("accounts", "stale"); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatalf("pinned acquisition accepted stale cached measurements: %v", err)
	}
	if _, exists := p.jobStore.Lease("accounts", "stale"); exists {
		t.Fatal("stale ranking persisted an unusable session")
	}
}

func TestAutomaticJobsDoNotBlockEachOthersRefresh(t *testing.T) {
	slowEntered, fastEntered, releaseSlow := make(chan struct{}, 3), make(chan struct{}, 3), make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			slowEntered <- struct{}{}
			<-releaseSlow
		} else {
			fastEntered <- struct{}{}
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	defer close(releaseSlow)
	p := jobPoolFixture(t)
	u, _ := url.Parse(target.URL)
	for _, member := range p.members {
		member.outbound = &probeTransportOutbound{dial: func(ctx context.Context) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "tcp", u.Host) }}
	}
	for i := range p.options.Jobs {
		p.options.Jobs[i].Timeout = "10s"
		p.options.Jobs[i].ProbeConcurrency = 1
		p.options.Jobs[i].MaxResponseBytes = 1024
	}
	p.options.Jobs[0].TargetURL = target.URL + "/slow"
	p.options.Jobs[1].TargetURL = target.URL + "/fast"
	p.startJobs()
	select {
	case <-slowEntered:
	case <-time.After(time.Second):
		t.Fatal("slow job did not start")
	}
	select {
	case <-fastEntered:
	case <-time.After(time.Second):
		t.Fatal("an unrelated job's refresh waited for the slow target")
	}
}

func TestManualJobsUseOnlyProfileWithoutTargetMeasurements(t *testing.T) {
	p := jobPoolFixture(t)
	p.jobStore, _ = jobs.Open("") // no target measurements, including after a restart
	for i := range p.options.Jobs {
		p.options.Jobs[i].Selection = "manual"
		p.options.Jobs[i].Size = 1
	}
	p.profiles["fast"].allowed = map[string]struct{}{"a": {}, "b": {}} // c is outside the chosen set
	p.startJobs()
	if p.jobCancel != nil {
		t.Fatal("manual-only jobs started a benchmark scheduler")
	}
	s := p.ListJobs()[0]
	if s.Selection != "manual" || s.Candidates != 2 || len(s.Selected) != 2 || s.Measured != 0 || s.Refreshing || s.Size != 0 || s.TargetURL != "" {
		t.Fatalf("manual selection depended on benchmark settings: %+v", s)
	}
	selectedJSON, _ := json.Marshal(s.Selected)
	if string(selectedJSON) != `[{"node":"a"},{"node":"b"}]` {
		t.Fatalf("manual nodes advertised invented measurements: %s", selectedJSON)
	}
	if _, err := p.RefreshJob(context.Background(), "catalog"); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("manual refresh allowed target probing")
	}
	ctx := adapter.WithContext(context.Background(), &adapter.InboundContext{Inbound: config.EndpointInboundTag("catalog")})
	seen := map[string]bool{}
	for range 8 {
		m, err := p.pickMember(ctx, "tcp")
		if err != nil {
			t.Fatal(err)
		}
		seen[m.tag] = true
	}
	if len(seen) != 2 || seen["c"] {
		t.Fatalf("manual pool ignored profile or applied size limit: %v", seen)
	}
	if a, err := p.AcquireJob("catalog", ""); err != nil || a.State != "ready" || a.Selection != "manual" {
		t.Fatalf("manual pool unavailable: %+v %v", a, err)
	}
	a, err := p.AcquireJob("accounts", "first")
	if err != nil || a.Node != "a" || a.State != "ready" || a.Selection != "manual" {
		t.Fatalf("manual session unavailable: %+v %v", a, err)
	}
	b, err := p.AcquireJob("accounts", "second")
	if err != nil || b.Node != "b" {
		t.Fatalf("manual sessions did not spread: %+v %v", b, err)
	}
	if err := p.ReportJob("accounts", jobs.Feedback{Session: "first", Node: a.Node, Success: false, DurationMS: 10}); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("manual selection accepted target feedback")
	}
	p.jobStore.Record("accounts-policy", jobs.Measurement{Node: "a", Success: false})
	if again, _ := p.AcquireJob("accounts", "first"); again.State != "ready" || again.Node != "a" {
		t.Fatal("manual session was affected by target scores")
	}
	p.setMemberEligible(p.memberByTag["a"], false)
	if paused, _ := p.AcquireJob("accounts", "first"); paused.State != "paused" || paused.Node != "a" {
		t.Fatal("unhealthy manual session silently changed nodes")
	}
	p.setMemberEligible(p.memberByTag["b"], false)
	if _, err := p.pickMember(ctx, "tcp"); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatal("empty manual profile fell back to global pool")
	}
	if _, err := p.AcquireJob("accounts", "third"); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatal("manual session allocated outside profile")
	}
	p.setMemberEligible(p.memberByTag["a"], true)
	if again, _ := p.AcquireJob("accounts", "first"); again.State != "ready" || again.Node != "a" {
		t.Fatal("recovered node did not preserve assignment")
	}
}

func TestMixedJobSchedulerOnlyBenchmarksAutomaticJobs(t *testing.T) {
	p := jobPoolFixture(t)
	p.jobStore, _ = jobs.Open("")
	var manualRequests, autoRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manual" {
			manualRequests.Add(1)
		} else {
			autoRequests.Add(1)
		}
		_, _ = io.WriteString(w, "target")
	}))
	defer func() { _ = p.Close(); target.Close() }()
	u, _ := url.Parse(target.URL)
	for _, member := range p.members {
		member.outbound = &probeTransportOutbound{dial: func(ctx context.Context) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "tcp", u.Host) }}
	}
	p.options.Jobs[0].Selection, p.options.Jobs[0].TargetURL = "manual", target.URL+"/manual"
	p.options.Jobs[1].TargetURL = target.URL + "/auto"
	p.options.Jobs[1].ProbeConcurrency, p.options.Jobs[1].MaxResponseBytes = 2, 1024
	p.startJobs()
	deadline := time.Now().Add(2 * time.Second)
	for {
		values, refreshing := p.jobStore.Measurements("accounts-policy")
		if len(values) == 3 && !refreshing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("automatic benchmark never completed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if manualRequests.Load() != 0 || autoRequests.Load() != 3 {
		t.Fatalf("unexpected requests: manual=%d auto=%d", manualRequests.Load(), autoRequests.Load())
	}
	manual, automatic := p.ListJobs()[0], p.ListJobs()[1]
	if manual.Measured != 0 || len(manual.Selected) != 3 || automatic.Measured != 3 || len(automatic.Selected) != 2 {
		t.Fatalf("selection modes interfered: %+v %+v", manual, automatic)
	}
}
func TestJobPoolUsesTopNodesIgnoresStickyAndFailsClosed(t *testing.T) {
	p := jobPoolFixture(t)
	p.sticky = newStickyCache(time.Hour, 10)
	p.sticky.set("same-worker", "c", time.Now())
	ctx := adapter.WithContext(context.Background(), &adapter.InboundContext{Inbound: config.EndpointInboundTag("catalog")})
	seen := map[string]bool{}
	for range 8 {
		m, err := p.pickMemberExcludingForTarget(ctx, "tcp", nil, "same-worker", "")
		if err != nil {
			t.Fatal(err)
		}
		seen[m.tag] = true
	}
	if len(seen) != 2 || seen["c"] {
		t.Fatalf("did not use only the top two: %v", seen)
	}
	p.jobStore.Record("catalog-policy", jobs.Measurement{Node: "a", Success: false})
	status := p.ListJobs()[0]
	if len(status.Selected) != 2 || status.Selected[1].Node != "c" {
		t.Fatalf("spare was not promoted: %+v", status)
	}
	p.jobStore.Record("catalog-policy", jobs.Measurement{Node: "b", Success: false})
	p.jobStore.Record("catalog-policy", jobs.Measurement{Node: "c", Success: false})
	if _, err := p.pickMember(ctx, "tcp"); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatal("empty job escaped into global pool")
	}
}
func TestPinnedJobRetainsNodeOnFailureRemovalAndPolicyChange(t *testing.T) {
	p := jobPoolFixture(t)
	a, err := p.AcquireJob("accounts", "account-a")
	if err != nil || a.Node != "a" || a.State != "ready" {
		t.Fatalf("acquire: %+v %v", a, err)
	}
	b, err := p.AcquireJob("accounts", "account-b")
	if err != nil || b.Node != "b" {
		t.Fatal("accounts did not get separate assignments")
	}
	if err := p.ReportJob("accounts", jobs.Feedback{Session: "account-a", Node: "a", Success: false, DurationMS: 20, StatusCode: 429}); err != nil {
		t.Fatal(err)
	}
	paused, err := p.AcquireJob("accounts", "account-a")
	if err != nil || paused.Node != "a" || paused.State != "paused" || paused.ProxyURL != "" {
		t.Fatalf("failed session switched: %+v %v", paused, err)
	}
	p.jobStore.Record("accounts-policy", jobs.Measurement{Node: "a", Success: true, DurationMS: 10})
	delete(p.options.Metadata, "a")
	removed, _ := p.AcquireJob("accounts", "account-a")
	if removed.State != "paused" || removed.Node != "a" {
		t.Fatal("removed node silently replaced")
	}
	p.options.JobPolicies["accounts"] = "new-policy"
	changed, _ := p.AcquireJob("accounts", "account-a")
	if changed.State != "policy_changed" {
		t.Fatal("changed job reused old session")
	}
	if err := p.ReportJob("accounts", jobs.Feedback{Session: "account-b", Node: "c", DurationMS: 10}); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("foreign node feedback accepted")
	}
}

func TestPinnedEndpointBindsOnConnectAndNeverRetriesAnotherNode(t *testing.T) {
	p := jobPoolFixture(t)
	p.options.Jobs[1].Endpoint = "accounts"
	p.options.JobEndpoints["accounts"] = config.EndpointConfig{Address: "127.0.0.1", Port: 2325, Username: "worker", Password: "secret"}
	p.options.RetryEnabled, p.options.RetryAttempts = true, 3
	ctx := adapter.WithContext(context.Background(), &adapter.InboundContext{Inbound: config.EndpointInboundTag("accounts"), User: "worker-session-account-a"})
	m, err := p.pickMember(ctx, "tcp")
	if err != nil || m.tag != "a" {
		t.Fatalf("first connect: %v %v", m, err)
	}
	if p.maxAttempts(ctx) != 1 {
		t.Fatal("pinned traffic can retry another node")
	}
	a, err := p.AcquireJob("accounts", "account-a")
	if err != nil || a.ProxyURL != "http://worker-session-account-a:secret@127.0.0.1:2325" {
		t.Fatalf("incorrect endpoint: %+v %v", a, err)
	}
	p.jobStore.Record("accounts-policy", jobs.Measurement{Node: "a", Success: false})
	if _, err := p.pickMember(ctx, "tcp"); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatal("failed session escaped into another node")
	}
	l, _ := p.jobStore.Lease("accounts", "account-a")
	if l.Node != "a" {
		t.Fatal("assignment changed")
	}
	p.jobStore.Record("accounts-policy", jobs.Measurement{Node: "a", Success: true, DurationMS: 500})
	if m, err := p.pickMember(ctx, "tcp"); err != nil || m.tag != "a" {
		t.Fatal("healthy assigned node outside top N was not retained")
	}
	p.options.JobPolicies["accounts"] = "changed"
	if _, err := p.pickMember(ctx, "tcp"); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatal("policy change did not pause session")
	}
	p.options.JobPolicies["accounts"] = "accounts-policy"
	if err := p.ReleaseJob("accounts", "account-a"); err != nil {
		t.Fatal(err)
	}
	p.options.Jobs[1].SessionTTL = "-1s"
	if _, err := p.pickMember(ctx, "tcp"); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatal("expired session forwarded")
	}
	expired, err := p.AcquireJob("accounts", "account-a")
	if err != nil || expired.State != "expired" || expired.ProxyURL != "" {
		t.Fatalf("expired session reset: %+v %v", expired, err)
	}
}
func TestJobBenchmarkChecksFullHTTPResponseAndDeadline(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		delay  time.Duration
		want   string
	}{
		{"valid", 200, "valid page", 20 * time.Millisecond, ""}, {"blocked", 403, "blocked", 0, "unexpected_status"}, {"wrong content", 200, "captcha", 0, "body_mismatch"}, {"oversize", 200, string(make([]byte, 2048)), 0, "response_too_large"}, {"slow body", 200, "valid page", 200 * time.Millisecond, "body_read_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := jobPoolFixture(t)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				w.(http.Flusher).Flush()
				time.Sleep(test.delay)
				_, _ = io.WriteString(w, test.body)
			}))
			defer target.Close()
			u, _ := url.Parse(target.URL)
			member := p.memberByTag["a"]
			member.outbound = &probeTransportOutbound{dial: func(ctx context.Context) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "tcp", u.Host) }}
			j := p.options.Jobs[0]
			j.TargetURL = target.URL
			j.BodyContains = "valid"
			j.Timeout = "100ms"
			before := activeConnections(member)
			m := p.measureJobNode(context.Background(), j, member)
			if m.Error != test.want || m.Success != (test.want == "") {
				t.Fatalf("incorrect target measurement: %+v", m)
			}
			if m.Success && m.DurationMS < 15 {
				t.Fatal("measured headers instead of complete body")
			}
			if activeConnections(member) != before {
				t.Fatal("benchmark leaked active connection")
			}
		})
	}
}

func TestJobRefreshCloseCancelsBlockedBodyWithoutPublishingFailure(t *testing.T) {
	p := jobPoolFixture(t)
	p.profiles["fast"].allowed = map[string]struct{}{"a": {}}
	started := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	p.memberByTag["a"].outbound = &probeTransportOutbound{dial: func(ctx context.Context) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "tcp", u.Host) }}
	p.options.Jobs[0].TargetURL = target.URL
	p.options.Jobs[0].Timeout = "30s"
	p.jobCtx, p.jobCancel = context.WithCancel(context.Background())
	version := p.jobStore.Version("catalog-policy")
	done := make(chan error, 1)
	go func() { _, err := p.RefreshJob(context.Background(), "catalog"); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("probe never reached target")
	}
	_ = p.Close()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pool close left target probe running")
	}
	if p.jobStore.Version("catalog-policy") != version {
		t.Fatal("cancelled probe published a target failure")
	}
	if activeConnections(p.memberByTag["a"]) != 0 {
		t.Fatal("cancelled probe retained outbound")
	}
}
