package agent

import (
	"errors"
	"sync"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

// DefaultMaxTurns caps the number of tool-use iterations an Agent performs
// per Prompt when Policy.MaxTurns is left at zero.
const DefaultMaxTurns = 5

// Policy bounds what an Agent does for each Prompt. All fields are optional.
type Policy struct {
	// MaxTurns caps the number of assistant→tool→assistant iterations a
	// single Prompt may trigger. Zero means DefaultMaxTurns. One disables
	// tool-use loops entirely (the first assistant reply is always final).
	MaxTurns int

	// Temperature is forwarded to ChatRequest.Temperature if non-nil.
	Temperature *float64

	// MaxTokens is forwarded to ChatRequest.MaxTokens when > 0.
	MaxTokens int

	// Budget, when non-nil, tracks every Usage the Agent observes. A single
	// Budget can be shared by several Agents for a team-wide ceiling.
	Budget *Budget
}

// ErrBudgetExceeded is returned by Budget.Record and by the Agent loop when
// the MaxTokens ceiling is crossed.
var ErrBudgetExceeded = errors.New("agent: budget exceeded")

// Budget is a concurrent-safe running total of prompt + completion tokens.
// It is opt-in: an Agent without Policy.Budget consumes tokens without
// accounting.
type Budget struct {
	// MaxTokens is the hard ceiling. Zero disables enforcement — Record
	// still accumulates, useful for observability without gating.
	MaxTokens int

	mu         sync.Mutex
	usedTokens int
}

// Used reports the cumulative tokens charged against the Budget.
func (b *Budget) Used() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usedTokens
}

// Remaining returns MaxTokens - Used, clamped at zero. Returns MaxInt if
// MaxTokens == 0 (unbounded).
func (b *Budget) Remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.MaxTokens <= 0 {
		return int(^uint(0) >> 1)
	}
	if b.usedTokens >= b.MaxTokens {
		return 0
	}
	return b.MaxTokens - b.usedTokens
}

// Reserve checks whether the Budget currently has headroom. It returns
// ErrBudgetExceeded if Used has already met or exceeded MaxTokens. Reserve
// does not mutate — it's a pre-call guard.
func (b *Budget) Reserve() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.MaxTokens > 0 && b.usedTokens >= b.MaxTokens {
		return ErrBudgetExceeded
	}
	return nil
}

// Record adds u to the running total. Returns ErrBudgetExceeded if the new
// total breaches MaxTokens. A nil Usage is a no-op.
func (b *Budget) Record(u *llm.Usage) error {
	if u == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.usedTokens += u.TotalTokens
	if b.MaxTokens > 0 && b.usedTokens > b.MaxTokens {
		return ErrBudgetExceeded
	}
	return nil
}
