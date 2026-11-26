package ports

import (
	"context"
	"time"
)

// PromptCacheState is where a native conversation's provider prompt cache
// stands after its latest request.
type PromptCacheState struct {
	// LastRequestAt is when the latest request reached the provider. A cache
	// hit refreshes the entry's lifetime, so expiry counts from here.
	LastRequestAt time.Time
	// TTL is the lifetime of the cache entry the conversation's requests
	// write. Zero means no recent request reported one.
	TTL time.Duration
	// ContextTokens is the size of the latest request's prompt.
	ContextTokens int64
}

// ExpiresAt is when the cache stops serving the conversation's history, or
// the zero time when the lifetime is unknown.
func (s PromptCacheState) ExpiresAt() time.Time {
	if s.LastRequestAt.IsZero() || s.TTL <= 0 {
		return time.Time{}
	}
	return s.LastRequestAt.Add(s.TTL)
}

// ChatPromptCacheReader is implemented by chat drivers that can read their
// provider's prompt cache position from native session state. found is false
// when the native history has no completed request yet.
type ChatPromptCacheReader interface {
	PromptCacheState(ctx context.Context, request NativeCheckpointRequest) (state PromptCacheState, found bool, err error)
}
