package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skyforce77/tinyagents/pkg/actor"
	"github.com/skyforce77/tinyagents/pkg/llm"
	"github.com/skyforce77/tinyagents/pkg/memory/buffer"
	"github.com/skyforce77/tinyagents/pkg/tool"
)

// fakeProvider is a deterministic llm.Provider used to drive the Agent
// through specific scenarios. Replies is consumed one entry per Chat call.
// Streams is consumed one entry per Stream call.
type fakeProvider struct {
	name    string
	replies []llm.ChatResponse
	streams [][]llm.Chunk
	errs    []error

	callIdx    atomic.Int32
	streamIdx  atomic.Int32
	seenReqs   []llm.ChatRequest
	mu         chan struct{} // simple "mutex" for seenReqs appends
	chatCalled atomic.Int32
}

func newFakeProvider(name string) *fakeProvider {
	return &fakeProvider{name: name, mu: make(chan struct{}, 1)}
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Chat(_ context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.mu <- struct{}{}
	f.seenReqs = append(f.seenReqs, req)
	<-f.mu
	f.chatCalled.Add(1)
	i := int(f.callIdx.Add(1)) - 1
	if i < len(f.errs) && f.errs[i] != nil {
		return llm.ChatResponse{}, f.errs[i]
	}
	if i >= len(f.replies) {
		return llm.ChatResponse{}, errors.New("fakeProvider: out of replies")
	}
	return f.replies[i], nil
}

func (f *fakeProvider) Stream(_ context.Context, req llm.ChatRequest) (<-chan llm.Chunk, error) {
	f.mu <- struct{}{}
	f.seenReqs = append(f.seenReqs, req)
	<-f.mu
	i := int(f.streamIdx.Add(1)) - 1
	if i >= len(f.streams) {
		return nil, errors.New("fakeProvider: out of streams")
	}
	out := make(chan llm.Chunk, len(f.streams[i]))
	for _, c := range f.streams[i] {
		out <- c
	}
	close(out)
	return out, nil
}

func (f *fakeProvider) Embed(context.Context, llm.EmbedRequest) (llm.EmbedResponse, error) {
	return llm.EmbedResponse{}, errors.New("not implemented")
}

func (f *fakeProvider) Models(context.Context) ([]llm.Model, error) { return nil, nil }

func newTestSystem(t *testing.T) *actor.System {
	t.Helper()
	sys := actor.NewSystem("agent-test")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = sys.Stop(ctx)
	})
	return sys
}

func TestAgentSimplePrompt(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	prov := newFakeProvider("fake")
	prov.replies = []llm.ChatResponse{
		{
			Message:      llm.Message{Role: llm.RoleAssistant, Content: "hello back"},
			FinishReason: "stop",
			Usage:        &llm.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
		},
	}

	ref, err := sys.Spawn(Spec("simple", Agent{
		ID:       "simple",
		Provider: prov,
		Model:    "m1",
		System:   "be kind",
	}))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := ref.Ask(ctx, Prompt{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	r, ok := resp.(Response)
	if !ok {
		t.Fatalf("got %T, want Response", resp)
	}
	if r.Message.Content != "hello back" {
		t.Fatalf("content = %q, want %q", r.Message.Content, "hello back")
	}
	if r.Usage == nil || r.Usage.TotalTokens != 5 {
		t.Fatalf("usage = %+v, want TotalTokens=5", r.Usage)
	}

	// Verify system prompt + user prompt were sent to the provider.
	if len(prov.seenReqs) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.seenReqs))
	}
	got := prov.seenReqs[0].Messages
	if len(got) != 2 || got[0].Role != llm.RoleSystem || got[1].Role != llm.RoleUser {
		t.Fatalf("message shape unexpected: %+v", got)
	}
	if got[0].Content != "be kind" || got[1].Content != "hi" {
		t.Fatalf("message contents unexpected: %+v", got)
	}
}

func TestAgentToolLoop(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	// Tool that echoes its `msg` argument back as `reply`.
	echoTool := tool.Func{
		ToolName:   "echo",
		ToolDesc:   "echo",
		ToolSchema: json.RawMessage(`{"type":"object"}`),
		Fn: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
			var in struct {
				Msg string `json:"msg"`
			}
			_ = json.Unmarshal(args, &in)
			out, _ := json.Marshal(map[string]string{"reply": in.Msg})
			return out, nil
		},
	}

	prov := newFakeProvider("fake")
	prov.replies = []llm.ChatResponse{
		{
			// Turn 1: assistant asks for the tool.
			Message: llm.Message{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:        "call_1",
					Name:      "echo",
					Arguments: json.RawMessage(`{"msg":"pong"}`),
				}},
			},
			FinishReason: "tool_calls",
			Usage:        &llm.Usage{TotalTokens: 10},
		},
		{
			// Turn 2: assistant replies with tool output woven in.
			Message:      llm.Message{Role: llm.RoleAssistant, Content: "tool said pong"},
			FinishReason: "stop",
			Usage:        &llm.Usage{TotalTokens: 7},
		},
	}

	ref, err := sys.Spawn(Spec("tooler", Agent{
		ID:       "tooler",
		Provider: prov,
		Model:    "m",
		Tools:    []tool.Tool{echoTool},
		Memory:   buffer.New(32),
	}))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := ref.Ask(ctx, Prompt{Text: "use echo"})
	if err != nil {
		t.Fatal(err)
	}
	r := resp.(Response)
	if r.Message.Content != "tool said pong" {
		t.Fatalf("final content = %q", r.Message.Content)
	}
	if r.Usage == nil || r.Usage.TotalTokens != 17 {
		t.Fatalf("aggregated usage = %+v, want TotalTokens=17", r.Usage)
	}
	if prov.chatCalled.Load() != 2 {
		t.Fatalf("provider called %d times, want 2", prov.chatCalled.Load())
	}
	// Second call should include the tool-role message with echo's JSON output.
	secondCall := prov.seenReqs[1].Messages
	found := false
	for _, m := range secondCall {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "pong") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("second call missing tool-role message with pong: %+v", secondCall)
	}
}

func TestAgentUnknownTool(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	prov := newFakeProvider("fake")
	prov.replies = []llm.ChatResponse{
		{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:        "call_x",
					Name:      "no-such-tool",
					Arguments: json.RawMessage(`{}`),
				}},
			},
		},
		{Message: llm.Message{Role: llm.RoleAssistant, Content: "sorry"}},
	}

	ref, _ := sys.Spawn(Spec("a", Agent{Provider: prov, Model: "m"}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := ref.Ask(ctx, Prompt{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	r := resp.(Response)
	if r.Message.Content != "sorry" {
		t.Fatalf("final content = %q, want sorry", r.Message.Content)
	}
	// Second call should carry a tool message whose Content mentions unknown tool.
	second := prov.seenReqs[1].Messages
	var toolMsg *llm.Message
	for i := range second {
		if second[i].Role == llm.RoleTool {
			toolMsg = &second[i]
		}
	}
	if toolMsg == nil {
		t.Fatal("no tool message in second call")
	}
	if !strings.Contains(toolMsg.Content, "unknown tool") {
		t.Fatalf("unknown-tool message = %q", toolMsg.Content)
	}
}

func TestAgentMaxTurnsExceeded(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	prov := newFakeProvider("fake")
	// Always replies with a tool call, never a final answer.
	for i := 0; i < 10; i++ {
		prov.replies = append(prov.replies, llm.ChatResponse{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:        "c",
					Name:      "noop",
					Arguments: json.RawMessage(`{}`),
				}},
			},
		})
	}

	noop := tool.Func{
		ToolName:   "noop",
		ToolSchema: json.RawMessage(`{"type":"object"}`),
		Fn: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
	}

	ref, _ := sys.Spawn(Spec("a", Agent{
		Provider: prov,
		Model:    "m",
		Tools:    []tool.Tool{noop},
		Policy:   Policy{MaxTurns: 2},
	}))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := ref.Ask(ctx, Prompt{Text: "go"})
	if err != nil {
		t.Fatal(err)
	}
	e, ok := resp.(Error)
	if !ok {
		t.Fatalf("got %T, want Error", resp)
	}
	if !strings.Contains(e.Err.Error(), "max turns") {
		t.Fatalf("error = %v, want max turns message", e.Err)
	}
}

func TestAgentBudgetExceeded(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	prov := newFakeProvider("fake")
	prov.replies = []llm.ChatResponse{
		{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}, Usage: &llm.Usage{TotalTokens: 100}},
		{Message: llm.Message{Role: llm.RoleAssistant, Content: "again"}, Usage: &llm.Usage{TotalTokens: 100}},
	}

	budget := &Budget{MaxTokens: 50}
	ref, _ := sys.Spawn(Spec("a", Agent{
		Provider: prov,
		Model:    "m",
		Policy:   Policy{Budget: budget},
	}))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// First prompt: budget has headroom (Reserve OK), call Records 100 → exceeds.
	resp, err := ref.Ask(ctx, Prompt{Text: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp.(Error); !ok {
		t.Fatalf("first prompt: got %T, want Error", resp)
	}

	// Second prompt: Reserve itself should fail now.
	resp, err = ref.Ask(ctx, Prompt{Text: "second"})
	if err != nil {
		t.Fatal(err)
	}
	e, ok := resp.(Error)
	if !ok {
		t.Fatalf("second prompt: got %T, want Error", resp)
	}
	if !errors.Is(e.Err, ErrBudgetExceeded) {
		t.Fatalf("second prompt err = %v, want ErrBudgetExceeded", e.Err)
	}
}

func TestAgentStream(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	prov := newFakeProvider("fake")
	prov.streams = [][]llm.Chunk{{
		{Delta: "hel"},
		{Delta: "lo"},
		{FinishReason: "stop", Usage: &llm.Usage{TotalTokens: 4}},
	}}

	ref, _ := sys.Spawn(Spec("s", Agent{Provider: prov, Model: "m"}))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	out := make(chan llm.Chunk, 8)
	done := make(chan Response, 1)
	go func() {
		resp, err := ref.Ask(ctx, Prompt{Text: "hi", Stream: out})
		if err != nil {
			t.Error(err)
			close(done)
			return
		}
		done <- resp.(Response)
	}()

	var got strings.Builder
	for c := range out {
		got.WriteString(c.Delta)
	}
	if got.String() != "hello" {
		t.Fatalf("aggregated stream = %q, want hello", got.String())
	}

	select {
	case r := <-done:
		if r.Message.Content != "hello" {
			t.Fatalf("response content = %q", r.Message.Content)
		}
		if r.Usage == nil || r.Usage.TotalTokens != 4 {
			t.Fatalf("usage = %+v", r.Usage)
		}
	case <-time.After(time.Second):
		t.Fatal("response not delivered")
	}
}

func TestAgentProviderError(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	prov := newFakeProvider("fake")
	prov.replies = []llm.ChatResponse{{}}
	prov.errs = []error{errors.New("upstream boom")}

	ref, _ := sys.Spawn(Spec("a", Agent{Provider: prov, Model: "m"}))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	resp, err := ref.Ask(ctx, Prompt{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	e, ok := resp.(Error)
	if !ok {
		t.Fatalf("got %T, want Error", resp)
	}
	if !strings.Contains(e.Err.Error(), "upstream boom") {
		t.Fatalf("err = %v", e.Err)
	}
}

func TestAgentMemoryPersistsAcrossPrompts(t *testing.T) {
	t.Parallel()
	sys := newTestSystem(t)

	prov := newFakeProvider("fake")
	prov.replies = []llm.ChatResponse{
		{Message: llm.Message{Role: llm.RoleAssistant, Content: "first"}},
		{Message: llm.Message{Role: llm.RoleAssistant, Content: "second"}},
	}

	mem := buffer.New(32)
	ref, _ := sys.Spawn(Spec("mem", Agent{
		Provider: prov,
		Model:    "m",
		Memory:   mem,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = ref.Ask(ctx, Prompt{Text: "A"})
	_, _ = ref.Ask(ctx, Prompt{Text: "B"})

	// Second call's transcript must include A + its assistant reply + B.
	if len(prov.seenReqs) != 2 {
		t.Fatalf("calls = %d, want 2", len(prov.seenReqs))
	}
	msgs := prov.seenReqs[1].Messages
	// Expect at least: user A, assistant first, user B.
	foundA, foundAssistA, foundB := false, false, false
	for _, m := range msgs {
		if m.Role == llm.RoleUser && m.Content == "A" {
			foundA = true
		}
		if m.Role == llm.RoleAssistant && m.Content == "first" {
			foundAssistA = true
		}
		if m.Role == llm.RoleUser && m.Content == "B" {
			foundB = true
		}
	}
	if !foundA || !foundAssistA || !foundB {
		t.Fatalf("second-call transcript missing pieces: %+v", msgs)
	}
}
