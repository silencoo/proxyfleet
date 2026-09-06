package monitor

import (
	"io"
	"strings"
	"sync"
)

// LogBuffer is a thread-safe ring buffer that captures recent log output.
// It implements io.Writer so it can be plugged into log.SetOutput via io.MultiWriter.
type LogBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
	next int
	used int
}

// NewLogBuffer creates a ring buffer that keeps the last `size` bytes of log output.
func NewLogBuffer(size int) *LogBuffer {
	size = max(0, size)
	return &LogBuffer{
		buf:  make([]byte, size),
		size: size,
	}
}

// Write implements io.Writer. Appends data and trims the front if over capacity.
func (lb *LogBuffer) Write(p []byte) (n int, err error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	if lb.size == 0 || len(p) == 0 {
		return len(p), nil
	}
	// Retain only the tail of a large write without allocating for its prefix.
	if len(p) >= lb.size {
		copy(lb.buf, p[len(p)-lb.size:])
		lb.next = 0
		lb.used = lb.size
		return len(p), nil
	}
	written := copy(lb.buf[lb.next:], p)
	copy(lb.buf, p[written:])
	lb.next = (lb.next + len(p)) % lb.size
	lb.used = min(lb.size, lb.used+len(p))
	return len(p), nil
}

// Content returns a copy of the current buffer contents.
func (lb *LogBuffer) Content() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	if lb.used == 0 {
		return ""
	}
	start := (lb.next - lb.used + lb.size) % lb.size
	first := min(lb.used, lb.size-start)
	var content strings.Builder
	content.Grow(lb.used)
	content.Write(lb.buf[start : start+first])
	content.Write(lb.buf[:lb.used-first])
	return content.String()
}

// Clear removes all buffered console output and returns the number of bytes removed.
func (lb *LogBuffer) Clear() int {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	cleared := lb.used
	clear(lb.buf)
	lb.next, lb.used = 0, 0
	return cleared
}

// SharedLogBuffer is the global log buffer accessible by the server.
var SharedLogBuffer *LogBuffer

func init() {
	SharedLogBuffer = NewLogBuffer(64 * 1024) // 64KB ring buffer
}

// LogWriter returns an io.Writer that can be used with io.MultiWriter to capture logs.
func LogWriter() io.Writer {
	return SharedLogBuffer
}
