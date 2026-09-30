package pool

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestLocalResourceClassification(t *testing.T) {
	for _, code := range []syscall.Errno{syscall.EMFILE, syscall.ENFILE, syscall.ENOBUFS, syscall.ENOMEM, syscall.EADDRINUSE, syscall.EADDRNOTAVAIL} {
		err := &url.Error{Op: "Get", URL: "http://example.test", Err: &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("socket", code)}}
		if !isLocalResourceError(err) {
			t.Errorf("wrapped %v was not classified as local", code)
		}
	}
	for _, err := range []error{nil, context.Canceled, context.DeadlineExceeded, syscall.ECONNREFUSED, errors.New("remote: no buffer space available"), errors.New("proxy response: WSAENOBUFS 10055"), errors.New("too many open files")} {
		if isLocalResourceError(err) {
			t.Errorf("non-local/untyped failure classified as local: %v", err)
		}
	}
	for _, code := range []syscall.Errno{10024, 10048, 10055, 8, 14, 1450} {
		// Small native errno values can overlap on Unix; only test the Winsock
		// range negatively there. Windows must recognize the entire list.
		if runtime.GOOS != "windows" && code < 10000 {
			continue
		}
		err := fmt.Errorf("dial: %w", os.NewSyscallError("connectex", code))
		if got, want := isLocalResourceError(err), runtime.GOOS == "windows"; got != want {
			t.Errorf("code %d: local=%v, want %v", code, got, want)
		}
	}
}

func TestResourceBackoffDoesNotRenewOnRejection(t *testing.T) {
	b := new(resourceBackoff)
	if b.check() != nil || b.observe(syscall.ECONNREFUSED) {
		t.Fatal("remote failure activated local backoff")
	}
	if !b.observe(syscall.ENOBUFS) || !errors.Is(b.check(), errLocalResourceBackoff) {
		t.Fatal("resource failure did not activate backoff")
	}
	until := b.until.Load()
	if !b.observe(fmt.Errorf("wrapped: %w", b.check())) || b.until.Load() != until {
		t.Fatal("rejected admission prolonged backoff")
	}
	b.until.Store(time.Now().Add(-time.Second).UnixNano())
	if b.check() != nil {
		t.Fatal("expired backoff did not reopen")
	}
}

func TestResourceBackoffConcurrentObservations(t *testing.T) {
	b := new(resourceBackoff)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.observe(syscall.ENOBUFS)
			_ = b.check()
		}()
	}
	wg.Wait()
	if b.check() == nil {
		t.Fatal("concurrent failures lost the backoff deadline")
	}
}
