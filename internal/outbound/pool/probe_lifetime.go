package pool

import "sync"

// probeLifetime keeps a probe's admission slot until both its caller and all
// admitted dial callbacks have finished. net/http can return on cancellation
// while its DialContext callback is still running. A callback that ignores
// cancellation must retain the slot, not admit a replacement probe.
type probeLifetime struct {
	mu      sync.Mutex
	refs    int
	closed  bool
	release func()
}

func newProbeLifetime(release func()) *probeLifetime {
	return &probeLifetime{refs: 1, release: release}
}

// Hold is called before starting any work in an HTTP dial callback. Once Close
// runs, callbacks scheduled late by net/http must not enter the outbound.
func (l *probeLifetime) Hold() (func(), bool) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, false
	}
	l.refs++
	l.mu.Unlock()
	var once sync.Once
	return func() { once.Do(l.drop) }, true
}

// Close retires the caller's reference and prevents new dial callbacks. It does
// not release the admission slot while a previously admitted callback is stuck.
func (l *probeLifetime) Close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	l.mu.Unlock()
	l.drop()
}

func (l *probeLifetime) drop() {
	l.mu.Lock()
	l.refs--
	last := l.refs == 0
	l.mu.Unlock()
	if last {
		l.release()
	}
}
