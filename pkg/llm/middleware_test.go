package llm_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

// fakeProvider is a programmable Provider for middleware unit tests. It
// fails the first failChat calls to Chat, then succeeds.
type fakeProvider struct {
	name      string
	failChat  atomic.Int32
	chatCalls atomic.Int32
	err       error
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	f.chatCalls.Add(1)
	if f.failChat.Load() > 0 {
		f.failChat.Add(-1)
		return llm.ChatResponse{}, f.err
	}
	return llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}}, nil
}
func (f *fakeProvider) Stream(context.Context, llm.ChatRequest) (<-chan llm.Chunk, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeProvider) Embed(context.Context, llm.EmbedRequest) (llm.EmbedResponse, error) {
	return llm.EmbedResponse{}, errors.New("not implemented")
}
func (f *fakeProvider) Models(context.Context) ([]llm.Model, error) {
	return nil, errors.New("not implemented")
}

func TestRetrySucceedsAfterTransient(t *testing.T) {
	t.Parallel()
	f := &fakeProvider{name: "fake", err: errors.New("transient")}
	f.failChat.Store(2)

	p := llm.Retry(5, llm.ExponentialBackoff(time.Millisecond, time.Millisecond), nil)(f)
	resp, err := p.Chat(context.Background(), llm.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("got %q, want ok", resp.Message.Content)
	}
	if f.chatCalls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", f.chatCalls.Load())
	}
}

func TestRetryGivesUpAfterMax(t *testing.T) {
	t.Parallel()
	f := &fakeProvider{name: "fake", err: errors.New("broken")}
	f.failChat.Store(100)

	p := llm.Retry(3, llm.ExponentialBackoff(time.Millisecond, time.Millisecond), nil)(f)
	_, err := p.Chat(context.Background(), llm.ChatRequest{Model: "m"})
	if err == nil {
		t.Fatal("expected error")
	}
	if f.chatCalls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", f.chatCalls.Load())
	}
}

func TestRetryHonorsShouldRetry(t *testing.T) {
	t.Parallel()
	f := &fakeProvider{name: "fake", err: errors.New("never-retry-me")}
	f.failChat.Store(100)

	p := llm.Retry(5, nil, func(error) bool { return false })(f)
	_, err := p.Chat(context.Background(), llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if f.chatCalls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", f.chatCalls.Load())
	}
}

func TestRateLimitThrottles(t *testing.T) {
	t.Parallel()
	f := &fakeProvider{name: "fake"}
	// 2 requests per 50ms of "interval" is not the shape — use 1/10ms with burst 1.
	p := llm.RateLimit(10*time.Millisecond, 1)(f)

	start := time.Now()
	for i := 0; i < 5; i++ {
		if _, err := p.Chat(context.Background(), llm.ChatRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	// 4 throttled waits at 10ms each ≈ 40ms minimum.
	if elapsed < 35*time.Millisecond {
		t.Fatalf("rate limit too lax: 5 requests in %v", elapsed)
	}
}

func TestFallbackTriesBackupOnPrimaryError(t *testing.T) {
	t.Parallel()
	primary := &fakeProvider{name: "primary", err: errors.New("down")}
	primary.failChat.Store(100)
	backup := &fakeProvider{name: "backup"}

	p := llm.Fallback(primary, backup)
	resp, err := p.Chat(context.Background(), llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("got %q, want ok", resp.Message.Content)
	}
	if primary.chatCalls.Load() != 1 || backup.chatCalls.Load() != 1 {
		t.Fatalf("calls: primary=%d backup=%d", primary.chatCalls.Load(), backup.chatCalls.Load())
	}
}

func TestWithComposesInOrder(t *testing.T) {
	t.Parallel()
	f := &fakeProvider{name: "fake", err: errors.New("nope")}
	f.failChat.Store(1)

	// Retry first (outermost) → log around each retry. We only assert that
	// With does NOT drop the Retry: Chat should succeed after one retry.
	p := llm.With(f,
		llm.Retry(3, llm.ExponentialBackoff(time.Millisecond, time.Millisecond), nil),
		llm.Log(nil),
	)
	_, err := p.Chat(context.Background(), llm.ChatRequest{})
	if err != nil {
		t.Fatalf("with composed: %v", err)
	}
	if f.chatCalls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", f.chatCalls.Load())
	}
}
