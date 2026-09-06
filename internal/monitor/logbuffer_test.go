package monitor

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func TestLogBufferMatchesRecentBytes(t *testing.T) {
	for _, size := range []int{0, 1, 7, 64, 1024} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			buffer := NewLogBuffer(size)
			random := rand.New(rand.NewSource(1))
			var expected []byte
			for step := 0; step < 1000; step++ {
				if step%71 == 0 {
					if count := buffer.Clear(); count != len(expected) {
						t.Fatalf("Clear = %d, want %d", count, len(expected))
					}
					expected = nil
				} else {
					data := make([]byte, random.Intn(size*3+4))
					_, _ = random.Read(data)
					if n, err := buffer.Write(data); err != nil || n != len(data) {
						t.Fatalf("Write = %d, %v", n, err)
					}
					expected = append(expected, data...)
					if len(expected) > size {
						expected = expected[len(expected)-size:]
					}
				}
				if content := buffer.Content(); content != string(expected) {
					t.Fatalf("step %d: Content = %q, want %q", step, content, expected)
				}
				if len(buffer.buf) != size || cap(buffer.buf) != size {
					t.Fatal("buffer storage grew beyond its configured capacity")
				}
			}
		})
	}
}

func TestLogBufferSnapshotAndClear(t *testing.T) {
	buffer := NewLogBuffer(8)
	_, _ = buffer.Write([]byte("password"))
	snapshot := buffer.Content()
	if buffer.Clear() != 8 || buffer.Content() != "" {
		t.Fatal("Clear did not reset the buffer")
	}
	if !bytes.Equal(buffer.buf, make([]byte, 8)) {
		t.Fatal("Clear retained old log bytes")
	}
	_, _ = buffer.Write([]byte("next"))
	if snapshot != "password" || buffer.Content() != "next" {
		t.Fatal("snapshot changed after a later write")
	}
	if NewLogBuffer(-1).Content() != "" {
		t.Fatal("negative capacity should create a disabled buffer")
	}
}

func TestLogBufferWritesDoNotAllocate(t *testing.T) {
	buffer := NewLogBuffer(64 * 1024)
	for _, size := range []int{256, 1024 * 1024} {
		data := bytes.Repeat([]byte("x"), size)
		if allocations := testing.AllocsPerRun(100, func() { _, _ = buffer.Write(data) }); allocations != 0 {
			t.Fatalf("%d-byte writes allocated %v times", size, allocations)
		}
	}
}

func TestLogBufferConcurrentAccess(t *testing.T) {
	buffer := NewLogBuffer(1024)
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for step := 0; step < 500; step++ {
				switch worker % 3 {
				case 0:
					_, _ = buffer.Write([]byte("a complete log line\n"))
				case 1:
					if len(buffer.Content()) > 1024 {
						t.Error("snapshot exceeded capacity")
					}
				case 2:
					buffer.Clear()
				}
			}
		}(worker)
	}
	wait.Wait()
}

func BenchmarkLogBufferWrite(b *testing.B) {
	data := bytes.Repeat([]byte("x"), 256)
	b.Run("circular", func(b *testing.B) {
		buffer := NewLogBuffer(64 * 1024)
		b.ReportAllocs()
		b.SetBytes(int64(len(data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = buffer.Write(data)
		}
	})
	b.Run("previous_append_tail", func(b *testing.B) {
		var mu sync.Mutex
		buffer := make([]byte, 0, 64*1024)
		b.ReportAllocs()
		b.SetBytes(int64(len(data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			mu.Lock()
			buffer = append(buffer, data...)
			if len(buffer) > 64*1024 {
				buffer = buffer[len(buffer)-64*1024:]
			}
			mu.Unlock()
		}
	})
}
