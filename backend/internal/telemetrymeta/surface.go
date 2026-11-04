package telemetrymeta

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type surfaceContextKey struct{}

// WithSurface records which client a request came from, for telemetry that is
// emitted below the HTTP layer.
func WithSurface(ctx context.Context, surface domain.RequestSurface) context.Context {
	return context.WithValue(ctx, surfaceContextKey{}, surface)
}

// SurfaceFromContext returns the request's client surface, or ("", false) when
// the work did not start from an HTTP request (a schedule, the SCM poller).
func SurfaceFromContext(ctx context.Context) (domain.RequestSurface, bool) {
	surface, ok := ctx.Value(surfaceContextKey{}).(domain.RequestSurface)
	return surface, ok && surface != ""
}
