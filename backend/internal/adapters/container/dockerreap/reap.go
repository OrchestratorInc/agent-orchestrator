package dockerreap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const SessionLabel = "ao.session"

const SpareLabel = "ao.spare"

const reapTimeout = 10 * time.Second

type commandRunner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.Bytes(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

type Reaper struct {
	run commandRunner
}

func New() *Reaper {
	return &Reaper{run: execRunner{}}
}

func newWithRunner(r commandRunner) *Reaper {
	return &Reaper{run: r}
}

func (r *Reaper) ReapSessionContainers(ctx context.Context, id domain.SessionID) (int, error) {
	sessionID := strings.TrimSpace(string(id))
	if sessionID == "" {
		return 0, nil
	}

	ctx, cancel := context.WithTimeout(ctx, reapTimeout)
	defer cancel()

	out, err := r.run.Output(ctx, "docker", "ps", "-a",
		"--filter", fmt.Sprintf("label=%s=%s", SessionLabel, sessionID),
		"--format", "{{.ID}}\t{{.Label \""+SpareLabel+"\"}}")
	if err != nil {
		if isDockerUnavailable(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("dockerreap: list containers for session %s: %w", sessionID, err)
	}

	removed := 0
	var errs []error
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		containerID := strings.TrimSpace(fields[0])
		if containerID == "" {
			continue
		}
		spared := len(fields) > 1 && strings.EqualFold(strings.TrimSpace(fields[1]), "true")
		if spared {
			continue
		}
		if _, err := r.run.Output(ctx, "docker", "rm", "-f", containerID); err != nil {
			errs = append(errs, fmt.Errorf("dockerreap: remove container %s (session %s): %w", containerID, sessionID, err))
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

func isDockerUnavailable(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "cannot connect to the docker daemon")
}
