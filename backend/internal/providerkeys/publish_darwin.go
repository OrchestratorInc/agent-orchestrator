package providerkeys

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// DeviceSupported reports that Publish can reach programs started outside AO.
const DeviceSupported = true

// Publish updates the current user's launchd environment for future children.
// AO reapplies saved keys after restarting in a new login session. A value
// already set there that is neither value nor previous, AO's earlier copy, is
// the user's own: Publish leaves it and returns ErrUserValue.
// launchctl exposes the value briefly as a command argument; the user-level
// environment itself is readable by processes launched in that context.
func Publish(ctx context.Context, name, value, previous string) error {
	if !validName(name) || !validValue(value) || value == "" {
		return errors.New("invalid provider credential")
	}
	current, err := launchdValue(ctx, name)
	if err != nil {
		return err
	}
	if alreadyUsers(current, value, previous) {
		return ErrAlreadySet
	}
	if !ownedValue(current, value, previous) {
		return ErrUserValue
	}
	return process.CommandContext(ctx, "launchctl", "setenv", name, value).Run()
}

// Unpublish removes name from the current user's launchd environment while it
// still holds value, AO's copy; a value the user set since is left alone.
// Processes that already inherited it keep their copy.
func Unpublish(ctx context.Context, name, value string) error {
	if !validName(name) {
		return errors.New("invalid provider credential")
	}
	current, err := launchdValue(ctx, name)
	if err != nil {
		return err
	}
	if current == "" || current != value {
		return nil
	}
	return process.CommandContext(ctx, "launchctl", "unsetenv", name).Run()
}

func launchdValue(ctx context.Context, name string) (string, error) {
	out, err := process.CommandContext(ctx, "launchctl", "getenv", name).Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(out) == 0 {
		// Some launchctl versions report an unset variable by exit status.
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}
