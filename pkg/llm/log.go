package llm

import (
	"context"
	"log/slog"
	"time"
)

// Log emits one slog line per Chat / Stream / Embed / Models call with
// duration and success status. It passes the underlying call through
// untouched, so Log is composable with any other Middleware.
//
// A nil logger falls back to slog.Default().
func Log(logger *slog.Logger) Middleware {
	return func(next Provider) Provider {
		if logger == nil {
			logger = slog.Default()
		}
		return &logMW{next: next, log: logger.With("llm", next.Name())}
	}
}

type logMW struct {
	next Provider
	log  *slog.Logger
}

func (l *logMW) Name() string { return l.next.Name() }

func (l *logMW) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	start := time.Now()
	resp, err := l.next.Chat(ctx, req)
	l.emit("chat", req.Model, time.Since(start), err, resp.Usage)
	return resp, err
}

func (l *logMW) Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	start := time.Now()
	ch, err := l.next.Stream(ctx, req)
	// Log only the handshake; the stream itself is the caller's concern.
	l.emit("stream-open", req.Model, time.Since(start), err, nil)
	return ch, err
}

func (l *logMW) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	start := time.Now()
	resp, err := l.next.Embed(ctx, req)
	l.emit("embed", req.Model, time.Since(start), err, resp.Usage)
	return resp, err
}

func (l *logMW) Models(ctx context.Context) ([]Model, error) {
	start := time.Now()
	out, err := l.next.Models(ctx)
	l.emit("models", "", time.Since(start), err, nil)
	return out, err
}

func (l *logMW) emit(op, model string, dur time.Duration, err error, usage *Usage) {
	attrs := []any{"op", op, "duration_ms", dur.Milliseconds()}
	if model != "" {
		attrs = append(attrs, "model", model)
	}
	if usage != nil {
		attrs = append(attrs, "tokens_in", usage.PromptTokens, "tokens_out", usage.CompletionTokens)
	}
	if err != nil {
		attrs = append(attrs, "err", err.Error())
		l.log.Warn("llm call failed", attrs...)
		return
	}
	l.log.Info("llm call", attrs...)
}
