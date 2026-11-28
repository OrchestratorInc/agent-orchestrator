package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

// agentProfileReadLimit caps how much of a candidate profile is read while
// looking for AO's sentinel comment. AO writes the sentinel right after the
// profile's front matter, so a profile carrying it only past this point is
// treated as user-authored: preserving too much is safe, deleting is not.
const agentProfileReadLimit = 64 << 10

// errAgentProfileUnreadable marks a probe that listed a candidate profile
// but could not read it. Git cannot see such a file, so callers must not
// defer to git: an unread candidate may be user work.
var errAgentProfileUnreadable = errors.New("gitworktree: agent profile unreadable")

// unmanagedExcludedAgentProfiles lists the user-authored Copilot agent
// profiles a plain `git status` cannot see: untracked files under
// .github/agents/ matching AO's own ao-*.agent.md exclude glob that do NOT
// carry AO's managed-sentinel comment. AO writes one such profile per
// session and marks its own with the sentinel; anything else matching the
// glob is user work teardown must preserve rather than silently delete
// (#9007).
//
// A file vanishing mid-probe is skipped (nothing left to protect); any other
// read failure aborts the probe with errAgentProfileUnreadable so callers
// fail closed.
func (w *Workspace) unmanagedExcludedAgentProfiles(ctx context.Context, worktree string) ([]string, error) {
	out, err := w.run(ctx, w.binary, ignoredAgentProfilesArgs(worktree)...)
	if err != nil {
		return nil, fmt.Errorf("gitworktree: list ignored agent profiles: %w", err)
	}
	var profiles []string
	for _, entry := range strings.Split(string(out), "\x00") {
		if !isExcludedAgentProfilePath(entry) {
			continue
		}
		managed, err := profileCarriesSentinel(filepath.Join(worktree, filepath.FromSlash(entry)))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("%w: %q: %w", errAgentProfileUnreadable, entry, err)
		}
		if !managed {
			profiles = append(profiles, entry)
		}
	}
	return profiles, nil
}

// isExcludedAgentProfilePath reports whether a git-relative path matches
// AO's /.github/agents/ao-*.agent.md exclude glob. Git always reports
// slash-separated paths, so this uses package path rather than filepath,
// whose Windows separator would never match. Matching ignores case because
// git folds case for exclude rules under core.ignoreCase (the default on
// macOS and Windows); over-matching only preserves an ignored file, which is
// the safe direction.
func isExcludedAgentProfilePath(entry string) bool {
	entry = strings.ToLower(entry)
	if entry == "" || path.Dir(entry) != ".github/agents" {
		return false
	}
	name := path.Base(entry)
	return strings.HasPrefix(name, "ao-") && strings.HasSuffix(name, ".agent.md")
}

// profileCarriesSentinel reports whether the file at name contains AO's
// managed-profile sentinel within the first agentProfileReadLimit bytes.
func profileCarriesSentinel(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, agentProfileReadLimit))
	if err != nil {
		return false, err
	}
	return strings.Contains(string(data), hookutil.CopilotAgentProfileSentinel), nil
}
