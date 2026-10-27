package providerkeys

import (
	"context"
	"errors"
	"os/exec"
)

// Publish updates the current user's launchd environment for future children.
// AO reapplies saved keys after restarting in a new login session.
// launchctl exposes the value briefly as a command argument; the user-level
// environment itself is readable by processes launched in that context.
func Publish(ctx context.Context, name, value string) error {
	if !validName(name) || !validValue(value) || value == "" {
		return errors.New("invalid provider credential")
	}
	return exec.CommandContext(ctx, "launchctl", "setenv", name, value).Run()
}
