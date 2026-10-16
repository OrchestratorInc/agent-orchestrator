package daemon

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/activitydispatch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A report the hook command could not deliver, as it logs one.
var missedHook = regexp.MustCompile(`^(\S+) session=(\S+) ao hooks (\S+) (\S+): AO daemon is not running`)

type missedHookSessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// replayMissedHooks applies the last activity report each session made while
// the daemon was down. Without it a turn that ended meanwhile reads as working
// until the session's next turn.
func replayMissedHooks(ctx context.Context, dataDir string, sessions missedHookSessions, apply func(context.Context, domain.SessionID, ports.ActivitySignal) error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, "hooks.log")) //nolint:gosec // rooted in AO's own data dir
	if err != nil {
		return
	}
	last := map[domain.SessionID]ports.ActivitySignal{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		found := missedHook.FindStringSubmatch(line)
		if len(found) != 5 || found[4] == "subagent-stop" {
			continue
		}
		at, err := time.Parse(time.RFC3339, found[1])
		state, ok := activitydispatch.Derive(found[3], found[4], nil)
		if err != nil || !ok {
			continue
		}
		last[domain.SessionID(found[2])] = ports.ActivitySignal{Valid: true, State: state, Timestamp: at, Event: found[4]}
	}
	for id, signal := range last {
		// A report older than what the session last said belongs to an earlier launch or is already known.
		rec, ok, err := sessions.GetSession(ctx, id)
		if err != nil || !ok || rec.IsTerminated || !signal.Timestamp.After(rec.Activity.LastActivityAt) {
			continue
		}
		// A terminal's report counts only when it names the launch that is running.
		signal.LaunchID = rec.Metadata.RuntimeLaunchID
		_ = apply(ctx, id, signal)
	}
}
