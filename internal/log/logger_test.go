package log

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// lineRe pins the record shape: timestamp, bracketed level, message.
var lineRe = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[[A-Z]+\] (.*)$`)

func TestLogfWritesAllRecordsInOrder(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)

	l.Logf(LevelDebug, "planning %s", "started")
	l.Logf(LevelInfo, "developer wrote %d files", 2)
	l.Logf(LevelWarn, "retry %d of %d", 1, 10)
	l.Logf(LevelError, "flake check failed: %s", "missing attribute")

	if err := l.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
	// Close waits for the writer, so every record must already be flushed and
	// the buffer must not be touched again.
	if buf.Len() == 0 {
		t.Fatal("writer received nothing after Close")
	}

	raw := buf.String()
	if !strings.HasSuffix(raw, "\n") {
		t.Fatalf("output does not end with a newline: %q", raw)
	}

	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4: %q", len(lines), raw)
	}

	wantLevels := []string{"DEBUG", "INFO", "WARN", "ERROR"}
	wantMsgs := []string{
		"planning started",
		"developer wrote 2 files",
		"retry 1 of 10",
		"flake check failed: missing attribute",
	}

	for i, line := range lines {
		m := lineRe.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("line %d = %q, does not match %q", i, line, lineRe.String())
		}
		if want := "[" + wantLevels[i] + "]"; !strings.Contains(line, want) {
			t.Errorf("line %d = %q, want level %s", i, line, want)
		}
		if m[1] != wantMsgs[i] {
			t.Errorf("line %d message = %q, want %q", i, m[1], wantMsgs[i])
		}
	}
}

func TestLogfNormalisesLevel(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)

	l.Logf("Info", "mixed case")
	l.Logf(" warn ", "padded")
	l.Close()

	raw := buf.String()
	if !strings.Contains(raw, "[INFO] mixed case") {
		t.Errorf("output = %q, want a normalised [INFO] level", raw)
	}
	if !strings.Contains(raw, "[WARN] padded") {
		t.Errorf("output = %q, want a normalised [WARN] level", raw)
	}
}

// TestConcurrentLogf stresses the queue from ten goroutines. The 256-slot
// buffer is smaller than the payload, so senders block and the writer has to
// drain concurrently. Run with plain `go test`; the ordering and the total
// count are what is being asserted.
func TestConcurrentLogf(t *testing.T) {
	const (
		goroutines   = 10
		perGoroutine = 100
		total        = goroutines * perGoroutine
	)

	var buf bytes.Buffer
	l := New(&buf)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				l.Logf(LevelInfo, "agent-%02d record-%03d", g, i)
			}
		}(g)
	}
	wg.Wait()

	if err := l.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != total {
		t.Fatalf("got %d lines, want %d", len(lines), total)
	}

	seen := make(map[string]bool, total)
	for _, line := range lines {
		m := lineRe.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("line %q does not match %q", line, lineRe.String())
		}
		if !strings.Contains(line, "[INFO]") {
			t.Fatalf("line %q has no [INFO] level", line)
		}
		if seen[m[1]] {
			t.Fatalf("duplicate record %q", m[1])
		}
		seen[m[1]] = true
	}
	if len(seen) != total {
		t.Fatalf("got %d distinct records, want %d", len(seen), total)
	}

	// Every goroutine must also keep its own records in issue order: the
	// single writer serialises them, so per-agent sequence numbers ascend.
	last := make(map[int]int, goroutines)
	for _, line := range lines {
		m := lineRe.FindStringSubmatch(line)
		var agent, seq int
		if _, err := fmt.Sscanf(m[1], "agent-%02d record-%03d", &agent, &seq); err != nil {
			t.Fatalf("cannot parse %q: %v", m[1], err)
		}
		if prev, ok := last[agent]; ok && seq != prev+1 {
			t.Fatalf("agent %d: record %d after %d, order broken", agent, seq, prev)
		}
		last[agent] = seq
	}
	if len(last) != goroutines {
		t.Fatalf("saw %d agents, want %d", len(last), goroutines)
	}
}

func TestCloseIsIdempotentAndLogfAfterCloseIsSafe(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.Logf(LevelInfo, "before close")

	if err := l.Close(); err != nil {
		t.Fatalf("first Close() error = %v, want nil", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}

	// A shutting-down agent may still log; that must be dropped, not panic.
	l.Logf(LevelError, "after close")

	if got := strings.Count(buf.String(), "\n"); got != 1 {
		t.Fatalf("got %d lines, want 1: %q", got, buf.String())
	}
}

func TestNewNilWriterDiscards(t *testing.T) {
	l := New(nil)
	l.Logf(LevelInfo, "into the void")
	if err := l.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
}
