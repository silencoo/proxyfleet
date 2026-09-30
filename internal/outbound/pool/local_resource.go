package pool

import (
	"errors"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
)

var errLocalResourceBackoff = errors.New("local socket resources unavailable; retry after backoff")

const localResourceBackoffDuration = 5 * time.Second

// isLocalResourceError deliberately requires a typed operating-system error.
// Text from a remote proxy (or an HTTP response) is not evidence that this
// machine has exhausted sockets. Address allocation errors are local faults,
// but are not, by themselves, proof of ephemeral-port exhaustion.
func isLocalResourceError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errLocalResourceBackoff) {
		return true
	}
	for _, code := range []syscall.Errno{
		syscall.EMFILE, syscall.ENFILE, syscall.ENOBUFS, syscall.ENOMEM,
		syscall.EADDRINUSE, syscall.EADDRNOTAVAIL,
	} {
		if errors.Is(err, code) {
			return true
		}
	}
	if runtime.GOOS == "windows" {
		// Winsock errors are not the synthetic POSIX Errno constants above.
		// 10024 WSAEMFILE, 10048 WSAEADDRINUSE, 10055 WSAENOBUFS;
		// 8/14/1450 are Windows memory/system-resource allocation failures.
		for _, code := range []syscall.Errno{10024, 10048, 10055, 8, 14, 1450} {
			if errors.Is(err, code) {
				return true
			}
		}
	}
	return false
}

// resourceBackoff is shared by live pool instances, including their Job and
// health probes. Rejected admissions do not renew it; only a fresh OS resource
// failure does. No per-node health state is involved. A nil receiver classifies
// errors without enabling the breaker (useful for isolated pool fixtures).
type resourceBackoff struct {
	until atomic.Int64
}

func (b *resourceBackoff) check() error {
	if b != nil && time.Now().UnixNano() < b.until.Load() {
		return errLocalResourceBackoff
	}
	return nil
}

func (b *resourceBackoff) observe(err error) bool {
	if !isLocalResourceError(err) {
		return false
	}
	if b == nil || errors.Is(err, errLocalResourceBackoff) {
		return true
	}
	until := time.Now().Add(localResourceBackoffDuration).UnixNano()
	for previous := b.until.Load(); until > previous; previous = b.until.Load() {
		if b.until.CompareAndSwap(previous, until) {
			break
		}
	}
	return true
}
