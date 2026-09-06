package pool

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/silencoo/proxyfleet/internal/monitor"
)

func TestRegressionSupersededProbeMustNotMutateRouting(t *testing.T) {
	ResetSharedStateStore()
	t.Cleanup(ResetSharedStateStore)
	mgr, err := monitor.NewManager(monitor.Config{ProbeTarget: "http://example.test/old"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Stop)
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	outbound := &probeTransportOutbound{dial: func(context.Context) (net.Conn, error) {
		close(started)
		<-release
		return nil, errors.New("synthetic old-target protocol failure")
	}}
	state := acquireSharedState("synthetic-generation")
	h := mgr.Register(monitor.NodeInfo{Tag: state.tag})
	state.attachEntry(h)
	member := &memberState{tag: state.tag, shared: state, entry: h, outbound: outbound}
	p := newIndexedTestPool(member, Options{BlacklistDuration: 24 * time.Hour, TransientCooldown: time.Minute})
	p.monitor = mgr
	t.Cleanup(func() { _ = p.Close() })
	p.registerProbe(member)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _, err := mgr.Probe(ctx, state.tag); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := mgr.SetProbeTarget("http://example.test/new", false); err != nil {
		t.Fatal(err)
	}
	state.recordSuccessWithLatency(time.Millisecond)
	if !mgr.Snapshot()[0].Available {
		t.Fatal("fixture did not establish current-generation health")
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-done; err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("obsolete probe was not rejected: %v", err)
	}
	s := mgr.Snapshot()[0]
	if state.isBlocked(time.Now()) || s.Blacklisted || s.CoolingDown || p.selectEligibleMember("tcp") == nil {
		t.Fatalf("rejected old probe still changed current routing: available=%v, blacklisted=%v, cooldown=%v, current passive successes=%d", s.Available, s.Blacklisted, s.CoolingDown, s.SuccessCount)
	}
}

func TestStaleProbeSuccessCannotClearNewerFailure(t *testing.T) {
	for _, changeTarget := range []bool{false, true} {
		t.Run(fmt.Sprint(changeTarget), func(t *testing.T) {
			p, member, mgr := newTrafficHealthFixture(t)
			p.monitor = mgr
			if err := mgr.SetProbeTarget("http://example.test/old", false); err != nil {
				t.Fatal(err)
			}
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			member.outbound = &probeTransportOutbound{dial: func(ctx context.Context) (net.Conn, error) {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				client, peer := net.Pipe()
				go func() {
					defer peer.Close()
					if _, err := http.ReadRequest(bufio.NewReader(peer)); err == nil {
						_, _ = io.WriteString(peer, "HTTP/1.1 204 No Content\r\n\r\n")
					}
				}()
				return client, nil
			}}
			p.registerProbe(member)
			done := make(chan error, 1)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			go func() { _, err := mgr.Probe(ctx, member.tag); done <- err }()
			<-started
			if changeTarget {
				if err := mgr.SetProbeTarget("http://example.test/new", false); err != nil {
					t.Fatal(err)
				}
			}
			member.shared.recordFailure(errors.New("new protocol failure"), 1, time.Hour, time.Minute)
			before := mgr.Snapshot()[0]
			once.Do(func() { close(release) })
			if err := <-done; err == nil || !strings.Contains(err.Error(), "replaced") {
				t.Fatalf("obsolete success result: %v", err)
			}
			after := mgr.Snapshot()[0]
			if !after.Blacklisted || after.Available || !after.BlacklistedUntil.Equal(before.BlacklistedUntil) || after.LastError != before.LastError || after.FailureCount != before.FailureCount {
				t.Fatal("stale success released or erased a newer failure")
			}
		})
	}
}

func TestSharedStateRetirementWaitsForOtherOwnersAndDrainingConnections(t *testing.T) {
	p, member, mgr := newTrafficHealthFixture(t)
	member.outbound = &closeTestOutbound{}
	secondState := acquireSharedState(member.tag)
	secondMember := &memberState{tag: member.tag, shared: secondState, entry: member.entry, outbound: member.outbound}
	secondPool := newIndexedTestPool(secondMember, Options{})
	secondPool.monitor = mgr
	secondPool.registerProbe(secondMember)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if secondState.closed.Load() || secondState.entry.Load() == nil {
		t.Fatal("closing one pool detached another owner's state")
	}
	secondState.incActive()
	if err := secondPool.Close(); err != nil {
		t.Fatal(err)
	}
	if secondState.closed.Load() || secondState.entry.Load() == nil {
		t.Fatal("state retired before its connection drained")
	}
	secondState.decActive()
	if !secondState.closed.Load() || secondState.entry.Load() != nil {
		t.Fatal("drained state retained runtime references")
	}
	if _, ok := lookupSharedState(member.tag); ok {
		t.Fatal("retired state remained in global store")
	}
	if err := secondPool.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentSharedStateAcquisitionAndRetirement(t *testing.T) {
	ResetSharedStateStore()
	t.Cleanup(ResetSharedStateStore)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < 50; iteration++ {
				state := acquireSharedState("concurrent-retirement")
				if state.closed.Load() {
					t.Error("acquired a retired state")
				}
				state.releaseOwner()
			}
		}()
	}
	wg.Wait()
	if _, ok := lookupSharedState("concurrent-retirement"); ok {
		t.Fatal("unowned state survived concurrent retirement")
	}
}

func TestRegressionBrokenConnectionMustNotConfirmHealth(t *testing.T) {
	ResetSharedStateStore()
	t.Cleanup(ResetSharedStateStore)
	mgr, err := monitor.NewManager(monitor.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Stop)
	state := acquireSharedState("synthetic-dead-connection")
	h := mgr.Register(monitor.NodeInfo{Tag: state.tag})
	state.attachEntry(h)
	h.RestoreHealthState(monitor.PersistedHealthState{InitialCheckDone: true, Available: false, LastError: "previous timeout"})
	member := &memberState{tag: state.tag, shared: state, entry: h, outbound: &closeTestOutbound{}}
	p := newIndexedTestPool(member, Options{})
	t.Cleanup(func() { _ = p.Close() })
	conn, err := p.DialContext(context.Background(), "tcp", M.ParseSocksaddr("example.test:443"))
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := conn.Write([]byte("synthetic request"))
	var response [1]byte
	count, readErr := conn.Read(response[:])
	_ = conn.Close()
	if writeErr == nil || readErr == nil || count != 0 {
		t.Fatal("fixture must fail before any response")
	}
	s := mgr.Snapshot()[0]
	if s.Available || s.SuccessCount != 0 || !s.LastPassiveSuccess.IsZero() {
		t.Fatalf("zero-response broken connection promoted healthy: available=%v, successes=%d, failures=%d, last_passive_success_set=%v, write_error=%v, read_error=%v", s.Available, s.SuccessCount, s.FailureCount, !s.LastPassiveSuccess.IsZero(), writeErr, readErr)
	}
}

func TestRegressionRetiredNodesReleaseRuntimeReferences(t *testing.T) {
	ResetSharedStateStore()
	t.Cleanup(ResetSharedStateStore)
	mgr, err := monitor.NewManager(monitor.Config{ProbeTarget: "http://example.test/"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Stop)
	for index := 0; index < 50; index++ {
		tag := fmt.Sprintf("synthetic-retired-%d", index)
		state := acquireSharedState(tag)
		h := mgr.Register(monitor.NodeInfo{Tag: tag, URI: "socks5://" + tag + ":1080"})
		state.attachEntry(h)
		outbound := &closeTestOutbound{}
		member := &memberState{tag: tag, shared: state, entry: h, outbound: outbound}
		p := newIndexedTestPool(member, Options{})
		p.monitor = mgr
		p.registerProbe(member)
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
		mgr.RetainNodeURIs(map[string]struct{}{})
	}
	retained, attached, active, watchers := 0, 0, int32(0), 0
	sharedStateStore.Range(func(_, value any) bool {
		s := value.(*sharedMemberState)
		retained++
		if s.entry.Load() != nil {
			attached++
		}
		active += s.activeCount()
		s.watchMu.Lock()
		watchers += len(s.watchers)
		s.watchMu.Unlock()
		return true
	})
	if active != 0 || watchers != 0 || len(mgr.Snapshot()) != 0 {
		t.Fatal("fixture still has live users")
	}
	if attached != 0 {
		t.Fatalf("after 50 retirements: %d shared states and %d attached monitor handles still root their probe callbacks/closed pools, with 0 live nodes, 0 connections and 0 watchers", retained, attached)
	}
}
