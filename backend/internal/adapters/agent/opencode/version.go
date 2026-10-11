package opencode

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var versionPattern = regexp.MustCompile(`^(?:opencode\s+)?v?([0-9]+)\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.+-]+)?$`)

var versionProbeTimeout = 10 * time.Second

// IncompatibleVersionError reports that the selected OpenCode executable is
// installed, but its major version does not match the selected harness.
type IncompatibleVersionError struct {
	ExpectedMajor int
	FoundMajor    int
	FoundVersion  string
	Path          string
}

func (e *IncompatibleVersionError) Error() string {
	return fmt.Sprintf("opencode: selected harness requires OpenCode %d, but %q reports OpenCode %d (%s); select the matching harness or put OpenCode %d on PATH", e.ExpectedMajor, e.Path, e.FoundMajor, e.FoundVersion, e.ExpectedMajor)
}

// ResolveBinaryForMajor returns the opencode executable whose major version
// matches. OpenCode 1 and 2 share an executable name and can be installed side
// by side, so every candidate is probed in resolution order. Results are never
// cached across attempts. Candidate discovery and probing share one deadline;
// when none match, a classified version mismatch takes precedence over generic
// shim failures so readiness can provide actionable guidance.
func ResolveBinaryForMajor(ctx context.Context, major int) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	candidates, err := BinaryCandidates(probeCtx)
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("opencode: %w", ports.ErrAgentBinaryNotFound)
	}
	var firstErr error
	var mismatchErr error
	for _, candidate := range candidates {
		binary, err := probeBinaryMajor(probeCtx, candidate, major)
		if err == nil {
			return binary, nil
		}
		var mismatch *IncompatibleVersionError
		if errors.As(err, &mismatch) && mismatchErr == nil {
			mismatchErr = err
		}
		if probeCtx.Err() != nil {
			return "", probeCtx.Err()
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if mismatchErr != nil {
		return "", mismatchErr
	}
	return "", firstErr
}

func probeBinaryMajor(ctx context.Context, binary string, major int) (string, error) {
	binary, err := filepath.Abs(binary)
	if err != nil {
		return "", err
	}
	cmd := aoprocess.CommandContext(ctx, binary, "--version")
	// A wrapper may leave descendants holding stdout open after cancellation.
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", fmt.Errorf("opencode: version probe for %q: %w", binary, ctx.Err())
	}
	if err != nil {
		return "", fmt.Errorf("opencode: version probe for %q failed: %w", binary, err)
	}
	version := strings.TrimSpace(string(out))
	match := versionPattern.FindStringSubmatch(version)
	if match == nil {
		return "", fmt.Errorf("opencode: cannot determine version of %q", binary)
	}
	found, err := strconv.Atoi(match[1])
	if err != nil {
		return "", fmt.Errorf("opencode: cannot determine version of %q", binary)
	}
	if found != major {
		return "", &IncompatibleVersionError{
			ExpectedMajor: major,
			FoundMajor:    found,
			FoundVersion:  version,
			Path:          binary,
		}
	}
	return binary, nil
}
