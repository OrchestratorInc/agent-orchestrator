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

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var versionPattern = regexp.MustCompile(`^(?:opencode\s+)?v?([0-9]+)\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.+-]+)?$`)

var versionProbeTimeout = 10 * time.Second

type IncompatibleVersionError struct {
	ExpectedMajor int
	FoundMajor    int
	FoundVersion  string
	Path          string
}

func (e *IncompatibleVersionError) Error() string {
	return fmt.Sprintf("opencode: selected harness requires OpenCode %d, but %q reports OpenCode %d (%s); select the matching harness or put OpenCode %d on PATH", e.ExpectedMajor, e.Path, e.FoundMajor, e.FoundVersion, e.ExpectedMajor)
}

func (e *IncompatibleVersionError) ReplacedOpenCode1Message() string {
	if e.ExpectedMajor != 1 || e.FoundMajor != 2 || isPrivateV2Location(e.Path) {
		return ""
	}
	if linkedByHomebrewV2(e.Path) {
		return fmt.Sprintf("OpenCode 1 is not installed: %s is OpenCode 2 (%s), linked by Homebrew's opencode-v2 formula where OpenCode 1 belongs. "+
			"Run `brew unlink opencode-v2` (Orchestrator still finds OpenCode 2 through Homebrew's opt link), then reinstall OpenCode 1 and check again.", e.Path, e.FoundVersion)
	}
	return fmt.Sprintf("OpenCode 1 is not installed: %s is OpenCode 2 (%s), which replaced OpenCode 1 at its default location. "+
		"To run both, first reinstall OpenCode 2 from Orchestrator, which keeps it in its own folder, then reinstall OpenCode 1 "+
		"(official installer, Homebrew, or npm opencode-ai) and check again.", e.Path, e.FoundVersion)
}

func isPrivateV2Location(path string) bool {
	if path == "" {
		return false
	}
	path = filepath.Clean(path)
	name := strings.ToLower(filepath.Base(path))
	if name == "opencode-v2" || name == "opencode-v2.cmd" || name == "opencode-v2.exe" || name == "opencode-v2.bat" {
		if _, managed := binaryutil.ManagedCurrentDir(path, "opencode-v2"); managed {
			return true
		}
	}
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

func linkedByHomebrewV2(path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return strings.Contains(filepath.ToSlash(resolved), "/Cellar/opencode-v2/")
}

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
