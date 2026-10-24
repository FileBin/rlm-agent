// Package budget implements the decrementing retry budget that keeps the
// orchestrator from looping forever between planning and review.
package budget

import "sync"

// DefaultMax is the retry budget used when the caller has no opinion.
const DefaultMax = 10

// Budget is a fixed-size pool of retries. It is safe for concurrent use: the
// orchestrator may share one budget across agent goroutines.
type Budget struct {
	mu        sync.Mutex
	remaining int
}

// NewBudget returns a budget with max retries. A negative max is clamped to 0.
func NewBudget(max int) *Budget {
	if max < 0 {
		max = 0
	}
	return &Budget{remaining: max}
}

// NewDefaultBudget returns a budget with DefaultMax retries.
func NewDefaultBudget() *Budget {
	return NewBudget(DefaultMax)
}

// Remaining reports how many retries are left.
func (b *Budget) Remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.remaining
}

// CanRetry reports whether at least one retry is left.
func (b *Budget) CanRetry() bool {
	return b.Remaining() > 0
}

// Consume spends one retry and returns the new remaining count. It never goes
// below zero, so an exhausted budget stays exhausted instead of wrapping.
func (b *Budget) Consume() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.remaining > 0 {
		b.remaining--
	}
	return b.remaining
}
