package sandbox

import "context"

type cleanupCommandsKey struct{}

// WithCleanupCommands carries a session's current project Cleanup commands
// (Project settings > Scripts) to Provider.Delete. The reconciler reads them
// when it deletes the sandbox, so Cleanup uses the project's config at removal
// time, as a local project does, rather than the commands the worker was
// launched with. An empty list means the project has no Cleanup now.
func WithCleanupCommands(ctx context.Context, commands []string) context.Context {
	return context.WithValue(ctx, cleanupCommandsKey{}, append([]string{}, commands...))
}

// CleanupCommands returns the commands WithCleanupCommands carried, and false
// when the caller could not look them up (the provider then falls back to the
// commands the worker installed).
func CleanupCommands(ctx context.Context) ([]string, bool) {
	commands, ok := ctx.Value(cleanupCommandsKey{}).([]string)
	return commands, ok
}

type skipCleanupKey struct{}

// WithoutCleanup tells Provider.Delete to delete at once without running or
// waiting for the project's Cleanup. The reconciler uses it for its last
// Delete before it gives up on a deletion, so a slow or restarted Cleanup can
// delay a deletion but never leave the workspace running.
func WithoutCleanup(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipCleanupKey{}, true)
}

// CleanupSkipped reports whether WithoutCleanup marked ctx.
func CleanupSkipped(ctx context.Context) bool {
	skip, _ := ctx.Value(skipCleanupKey{}).(bool)
	return skip
}
