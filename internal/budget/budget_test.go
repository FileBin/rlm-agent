package budget

import (
	"sync"
	"testing"
)

func TestConsumeToZero(t *testing.T) {
	b := NewBudget(3)
	if b.Remaining() != 3 {
		t.Fatalf("Remaining() = %d, want 3", b.Remaining())
	}

	for want := 2; want >= 0; want-- {
		if !b.CanRetry() {
			t.Fatalf("CanRetry() = false with %d left", b.Remaining())
		}
		if got := b.Consume(); got != want {
			t.Fatalf("Consume() = %d, want %d", got, want)
		}
		if b.Remaining() != want {
			t.Fatalf("Remaining() = %d, want %d", b.Remaining(), want)
		}
	}

	if b.CanRetry() {
		t.Fatal("CanRetry() = true on an exhausted budget")
	}
}

func TestConsumeNeverNegative(t *testing.T) {
	b := NewBudget(1)
	b.Consume()

	for i := 0; i < 5; i++ {
		if got := b.Consume(); got != 0 {
			t.Fatalf("Consume() on exhausted budget = %d, want 0", got)
		}
		if b.Remaining() != 0 {
			t.Fatalf("Remaining() = %d, want 0", b.Remaining())
		}
	}
	if b.CanRetry() {
		t.Fatal("CanRetry() = true after exhausting the budget")
	}
}

func TestZeroBudget(t *testing.T) {
	b := NewBudget(0)
	if b.Remaining() != 0 {
		t.Fatalf("Remaining() = %d, want 0", b.Remaining())
	}
	if b.CanRetry() {
		t.Fatal("CanRetry() = true for a zero budget")
	}
	if got := b.Consume(); got != 0 {
		t.Fatalf("Consume() = %d, want 0", got)
	}
}

func TestNegativeMaxClamped(t *testing.T) {
	b := NewBudget(-5)
	if b.Remaining() != 0 {
		t.Fatalf("Remaining() = %d, want 0", b.Remaining())
	}
	if b.CanRetry() {
		t.Fatal("CanRetry() = true for a negative budget")
	}
}

func TestDefault(t *testing.T) {
	if DefaultMax != 10 {
		t.Fatalf("DefaultMax = %d, want 10", DefaultMax)
	}
	b := NewDefaultBudget()
	if b.Remaining() != 10 {
		t.Fatalf("default Remaining() = %d, want 10", b.Remaining())
	}
	for i := 0; i < 9; i++ {
		b.Consume()
	}
	if !b.CanRetry() {
		t.Fatal("CanRetry() = false with one retry left")
	}
	b.Consume()
	if b.CanRetry() {
		t.Fatal("CanRetry() = true after consuming all 10 retries")
	}
}

// TestConcurrentConsume proves the budget accounts for every retry exactly once
// when agent goroutines share it.
func TestConcurrentConsume(t *testing.T) {
	const (
		max       = 100
		workers   = 20
		perWorker = max / workers
	)

	b := NewBudget(max)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				b.Consume()
			}
		}()
	}
	wg.Wait()

	if b.Remaining() != 0 {
		t.Fatalf("Remaining() = %d after %d consumes, want 0", b.Remaining(), max)
	}
	if got := b.Consume(); got != 0 {
		t.Fatalf("Consume() = %d past zero, want 0", got)
	}
}
