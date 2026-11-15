package providerkeys

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const DeviceSupported = true

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
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}
