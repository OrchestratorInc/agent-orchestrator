package providerkeys

import (
	"context"
	"errors"
	"os/exec"
)

// Publish updates the current user's launchd environment for future children.
// AO reapplies saved keys after restarting in a new login session.
func Publish(ctx context.Context, name, value string) error {
	if !validName(name) || !validValue(value) || value == "" {
		return errors.New("invalid provider credential")
	}
	return exec.CommandContext(ctx, "launchctl", "setenv", name, value).Run()
}
