package ports

import "context"

type agentAuthDataDirKey struct{}

// WithAgentAuthDataDir scopes native authentication to the daemon's canonical
// data root, which configuration resolves before the daemon changes directory.
func WithAgentAuthDataDir(ctx context.Context, dataDir string) context.Context {
	return context.WithValue(ctx, agentAuthDataDirKey{}, dataDir)
}

// AgentAuthDataDir returns the canonical data root when the daemon supplied it.
// Standalone adapter callers may fall back to their existing environment.
func AgentAuthDataDir(ctx context.Context) string {
	dataDir, _ := ctx.Value(agentAuthDataDirKey{}).(string)
	return dataDir
}
