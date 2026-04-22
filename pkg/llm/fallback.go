package llm

import "context"

// Fallback returns a Provider that delegates to primary, and on error
// retries once against backup. It is structural rather than a Middleware
// because it combines two providers.
//
// Fallback is intentionally dumb: any non-nil error from primary triggers
// the backup. Compose with Retry(..., shouldRetry=...) if finer control
// over which errors fall through is required.
func Fallback(primary, backup Provider) Provider {
	return &fallbackProvider{primary: primary, backup: backup}
}

type fallbackProvider struct {
	primary Provider
	backup  Provider
}

func (f *fallbackProvider) Name() string {
	return f.primary.Name() + "|" + f.backup.Name()
}

func (f *fallbackProvider) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	resp, err := f.primary.Chat(ctx, req)
	if err == nil {
		return resp, nil
	}
	return f.backup.Chat(ctx, req)
}

func (f *fallbackProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	ch, err := f.primary.Stream(ctx, req)
	if err == nil {
		return ch, nil
	}
	return f.backup.Stream(ctx, req)
}

func (f *fallbackProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	resp, err := f.primary.Embed(ctx, req)
	if err == nil {
		return resp, nil
	}
	return f.backup.Embed(ctx, req)
}

func (f *fallbackProvider) Models(ctx context.Context) ([]Model, error) {
	out, err := f.primary.Models(ctx)
	if err == nil {
		return out, nil
	}
	return f.backup.Models(ctx)
}
