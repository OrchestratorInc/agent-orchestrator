package telemetrymeta

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type surfaceContextKey struct{}

func WithSurface(ctx context.Context, surface domain.RequestSurface) context.Context {
	return context.WithValue(ctx, surfaceContextKey{}, surface)
}

func SurfaceFromContext(ctx context.Context) (domain.RequestSurface, bool) {
	surface, ok := ctx.Value(surfaceContextKey{}).(domain.RequestSurface)
	return surface, ok && surface != ""
}
