// Package log provides the orchestrator's channel-backed logger.
//
// Agent goroutines emit into a buffered channel; a single writer goroutine
// owns the io.Writer. That keeps log output line-atomic and totally ordered
// even when planner, developer and tester log concurrently, and it keeps a
// slow writer from taking a mutex on every agent goroutine.
package log

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Severity levels accepted by Logf.
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

const (
	// bufferSize is the channel depth. Sends block once it is full, so a
	// burst of agent output applies backpressure instead of dropping lines.
	bufferSize = 256

	// timeFormat is the Go log package's timestamp layout.
	timeFormat = "2006/01/02 15:04:05"
)

// entry is one buffered log record. The timestamp is taken at call time so
// the rendered order matches the order in which agents produced the events.
type entry struct {
	at    time.Time
	level string // already normalised to upper case
	msg   string
}

// Logger serialises log records onto a single writer through a channel.
//
// Logf is safe for concurrent use. A Logger must not be copied after New.
type Logger struct {
	// entries is unbuffered from the caller's perspective: sends block when
	// the 256-slot buffer is full.
	entries chan entry
	done    chan struct{}

	w io.Writer
	// err is written by the writer goroutine before done is closed and read
	// by Close afterwards; the channel close orders the two.
	err error

	// mu guards closed. Logf holds it for reading across the send so Close
	// cannot shut the channel while a sender is still in flight.
	mu     sync.RWMutex
	closed bool

	closeOnce sync.Once
}

// New starts a Logger writing to w. Records are flushed asynchronously by a
// single writer goroutine; Close drains the queue and stops it.
//
// A nil w discards everything, which keeps tests and dry runs from having to
// special-case a missing sink.
func New(w io.Writer) *Logger {
	if w == nil {
		w = io.Discard
	}

	l := &Logger{
		entries: make(chan entry, bufferSize),
		done:    make(chan struct{}),
		w:       w,
	}
	go l.run()
	return l
}

// Logf queues one record. level should be one of LevelDebug, LevelInfo,
// LevelWarn or LevelError; anything else is logged verbatim, in upper case.
//
// The call blocks while the buffer is full and returns immediately after
// Close: records emitted once the logger is closed are dropped rather than
// panicking a shutting-down agent goroutine.
func (l *Logger) Logf(level, format string, args ...any) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if l.closed {
		return
	}

	l.entries <- entry{
		at:    time.Now(),
		level: strings.ToUpper(strings.TrimSpace(level)),
		msg:   fmt.Sprintf(format, args...),
	}
}

// Close flushes queued records, stops the writer goroutine and waits for it to
// finish. It is idempotent and returns the first write error seen by the
// writer, if any.
func (l *Logger) Close() error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		close(l.entries)
		l.mu.Unlock()

		<-l.done
	})
	return l.err
}

// run is the sole writer goroutine: it renders records in queue order and
// records the first write failure. It reports completion by closing done,
// which also publishes l.err to Close.
func (l *Logger) run() {
	defer close(l.done)

	for e := range l.entries {
		line := fmt.Sprintf("%s [%s] %s\n", e.at.Format(timeFormat), e.level, e.msg)
		if _, err := io.WriteString(l.w, line); err != nil && l.err == nil {
			l.err = err
		}
	}
}
