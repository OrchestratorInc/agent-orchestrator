package providerkeys

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Publish updates the current user's launchd environment for future children.
// AO reapplies saved keys after restarting in a new login session.
// launchctl exposes the value briefly as a command argument; the user-level
// environment itself is readable by processes launched in that context.
func Publish(ctx context.Context, name, value string) error {
	if !validName(name) || !validValue(value) || value == "" {
		return errors.New("invalid provider credential")
	}
	return process.CommandContext(ctx, "launchctl", "setenv", name, value).Run()
}

// Unpublish removes name from the current user's launchd environment.
// Processes that already inherited it keep their copy.
func Unpublish(ctx context.Context, name string) error {
	if !validName(name) {
		return errors.New("invalid provider credential")
	}
	return process.CommandContext(ctx, "launchctl", "unsetenv", name).Run()
}
