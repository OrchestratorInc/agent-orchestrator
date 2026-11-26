package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

// agentProfileReadLimit caps how much of a candidate profile is read while
// looking for AO's sentinel comment. AO writes the sentinel on the first
// line, so a profile carrying it only past this point is treated as
// user-authored: preserving too much is safe, deleting is not.
const agentProfileReadLimit = 64 << 10

// unmanagedExcludedAgentProfiles lists the user-authored Copilot agent
// profiles a plain `git status` cannot see: untracked files under
// .github/agents/ matching AO's own ao-*.agent.md exclude glob that do NOT
// carry AO's managed-sentinel comment. AO writes one such profile per
// session and marks its own with the sentinel; anything else matching the
// glob is user work teardown must preserve rather than silently delete
// (#9007).
//
// A file vanishing mid-probe is skipped (nothing left to protect); any other
// read failure aborts the probe so callers fail closed.
func (w *Workspace) unmanagedExcludedAgentProfiles(ctx context.Context, worktree string) ([]string, error) {
	out, err := w.run(ctx, w.binary, ignoredAgentProfilesArgs(worktree)...)
	if err != nil {
		return nil, fmt.Errorf("gitworktree: list ignored agent profiles: %w", err)
	}
	var profiles []string
	for _, entry := range strings.Split(string(out), "\x00") {
		if entry == "" {
			continue
		}
		name := filepath.Base(entry)
		if filepath.Dir(entry) != ".github/agents" ||
			!strings.HasPrefix(name, "ao-") ||
			!strings.HasSuffix(name, ".agent.md") {
			continue
		}
		managed, err := profileCarriesSentinel(filepath.Join(worktree, filepath.FromSlash(entry)))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("gitworktree: read agent profile %q: %w", entry, err)
		}
		if !managed {
			profiles = append(profiles, entry)
		}
	}
	return profiles, nil
}

// profileCarriesSentinel reports whether the file at path contains AO's
// managed-profile sentinel within the first agentProfileReadLimit bytes.
func profileCarriesSentinel(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, agentProfileReadLimit))
	if err != nil {
		return false, err
	}
	return strings.Contains(string(data), hookutil.CopilotAgentProfileSentinel), nil
}
