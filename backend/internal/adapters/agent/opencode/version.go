package opencode

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
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

// ReplacedOpenCode1Message explains an OpenCode 1 check that found only
// OpenCode 2 at a default location: an OpenCode 2 install outside AO's private
// locations wrote its `opencode` over OpenCode 1's. It returns "" for any other
// mismatch, including OpenCode 2 installed only where AO keeps it apart.
func (e *IncompatibleVersionError) ReplacedOpenCode1Message() string {
	if e.ExpectedMajor != 1 || e.FoundMajor != 2 || isPrivateV2Location(e.Path) {
		return ""
	}
	if linkedByHomebrewV2(e.Path) {
		// The linked keg also blocks linking OpenCode 1's formula, and an npm
		// reinstall of OpenCode 2 would leave it linked, so unlink it first.
		return fmt.Sprintf("OpenCode 1 is not installed: %s is OpenCode 2 (%s), linked by Homebrew's opencode-v2 formula where OpenCode 1 belongs. "+
			"Run `brew unlink opencode-v2` (AO still finds OpenCode 2 through Homebrew's opt link), then reinstall OpenCode 1 and check again.", e.Path, e.FoundVersion)
	}
	return fmt.Sprintf("OpenCode 1 is not installed: %s is OpenCode 2 (%s), which replaced OpenCode 1 at its default location. "+
		"To run both, first reinstall OpenCode 2 from AO, which keeps it in its own folder, then reinstall OpenCode 1 "+
		"(official installer, Homebrew, or npm opencode-ai) and check again.", e.Path, e.FoundVersion)
}

// isPrivateV2Location reports whether path is where AO installs OpenCode 2
// apart from OpenCode 1: under the OpenCode 2 data home (npm prefix,
// installer home) or the opencode-v2 formula's opt link.
func isPrivateV2Location(path string) bool {
	if path == "" {
		return false
	}
	path = filepath.Clean(path)
	if slices.Contains(v2HomebrewCandidates(), filepath.ToSlash(path)) {
		return true
	}
	home, err := V2DataHome()
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(home, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// linkedByHomebrewV2 reports whether path is a Homebrew link into the
// opencode-v2 formula's keg (<prefix>/Cellar/opencode-v2/<version>/...).
func linkedByHomebrewV2(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return strings.Contains(filepath.ToSlash(resolved), "/Cellar/opencode-v2/")
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
