package pool

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestProbeLifetimeRetainsSlotUntilDialReturns(t *testing.T) {
	var released atomic.Int32
	l := newProbeLifetime(func() { released.Add(1) })
	done, ok := l.Hold()
	if !ok {
		t.Fatal("live probe rejected dial")
	}
	l.Close()
	l.Close()
	if released.Load() != 0 {
		t.Fatal("timed-out caller released an unfinished dial's slot")
	}
	if _, ok := l.Hold(); ok {
		t.Fatal("late HTTP dial entered retired probe")
	}
	done()
	done()
	if released.Load() != 1 {
		t.Fatalf("slot released %d times", released.Load())
	}
}

func TestProbeLifetimeKeepsSlotThroughResponseBody(t *testing.T) {
	var released atomic.Int32
	l := newProbeLifetime(func() { released.Add(1) })
	done, _ := l.Hold()
	done()
	if released.Load() != 0 {
		t.Fatal("dial completion released the caller's response-body slot")
	}
	l.Close()
	if released.Load() != 1 {
		t.Fatal("completed probe did not release slot")
	}
}

func TestProbeLifetimeConcurrentRetirement(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		var released atomic.Int32
		l := newProbeLifetime(func() { released.Add(1) })
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if done, ok := l.Hold(); ok {
					done()
					done()
				}
			}()
		}
		l.Close()
		wg.Wait()
		if released.Load() != 1 {
			t.Fatalf("slot released %d times", released.Load())
		}
	}
}
