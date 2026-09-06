package pool

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// DialContext may return a lazy protocol connection before its handshake. Only
// inbound data confirms transport health; writes alone are not remote evidence.
// Each connection contributes at most one observation. Local cancellation/close
// and an unused connection contribute none, and normal EOF after data is harmless.
type trafficObservation struct {
	mu        sync.Mutex
	done      atomic.Bool
	closed    bool
	ctx       context.Context
	pool      *poolOutbound
	member    *memberState
	targetKey string
	started   time.Time
	traffic   *trafficSession
}

func (p *poolOutbound) newTrafficObservation(ctx context.Context, member *memberState, targetKey string, started time.Time, traffic *trafficSession) *trafficObservation {
	return &trafficObservation{ctx: ctx, pool: p, member: member, targetKey: targetKey, started: started, traffic: traffic}
}

func (o *trafficObservation) observe(received bool, err error) {
	if o == nil || o.done.Load() || (!received && err == nil) {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done.Load() || o.closed || (o.ctx != nil && o.ctx.Err() != nil) {
		return
	}
	o.done.Store(true)
	if received {
		o.pool.recordSuccess(o.member, o.targetKey, time.Since(o.started))
	} else {
		o.pool.recordFailure(o.member, err)
	}
	if o.traffic != nil {
		o.traffic.resultMu.Lock()
		o.traffic.event.Success = received
		o.traffic.event.ErrorCategory = ""
		if !received {
			o.traffic.event.ErrorCategory = trafficErrorCategory(err)
		}
		o.traffic.resultMu.Unlock()
	}
}

func (o *trafficObservation) close() {
	if o != nil {
		o.mu.Lock()
		o.closed = true
		o.mu.Unlock()
	}
}
