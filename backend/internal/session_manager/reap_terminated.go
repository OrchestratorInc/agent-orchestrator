package sessionmanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/orphanreap"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// orphanReapDisabled mirrors the #93 convention (AO_NO_PROCINVENTORY):
// behavioral toggles are environment variables read at the point of behavior,
// not UI settings. AO_NO_ORPHANREAP=1|true turns the orphan pass off entirely:
// no boot scan, no bell report, and ReapOrphanWorkers answers an empty report.
// The feature is on without it.
func orphanReapDisabled() bool {
	switch os.Getenv("AO_NO_ORPHANREAP") {
	case "1", "true":
		return true
	default:
		return false
	}
}

// ReapTerminated destroys the stored runtime handles of sessions the database
// already marks terminated — the handle-based destroy that covers tmux and
// pty-host teardown when metadata is intact. Both the boot reconcile and the
// graceful shutdown run it. The orphaned-process decision is NOT here: those
// are only ever killed after the user allows it (ReapOrphanWorkers); the boot
// reports them instead (NotifyOrphanWorkers).
func (m *Manager) ReapTerminated(ctx context.Context) error {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("reap terminated: list sessions: %w", err)
	}
	for _, rec := range recs {
		if !rec.IsTerminated {
			continue
		}
		if err := m.reconcileReap(ctx, rec); err != nil {
			m.logger.Error("reap: runtime destroy failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	return nil
}

// NotifyOrphanWorkers reports the orphaned worker trees of terminated sessions
// as one bell notification and leaves them running. This is the fork issue #39
// ask step: the kill happens only through ReapOrphanWorkers, once the user
// allows it. One notification per daemon run — the SourceKey carries the
// daemonRunID — so a dismissed report asks again on the next start. Called
// only from the boot reconcile: a notification written during shutdown would
// sit next to the next boot's duplicate.
func (m *Manager) NotifyOrphanWorkers(ctx context.Context) {
	if m.notifier == nil || orphanReapDisabled() {
		return
	}
	_, _, _, victims, err := m.orphanScan(ctx)
	if err != nil {
		m.logger.Warn("reap: orphan scan skipped", "error", err)
		return
	}
	if len(victims) == 0 {
		return
	}
	ids := make([]string, 0, len(victims))
	for _, v := range victims {
		ids = append(ids, v.SessionID)
	}
	intent := ports.NotificationIntent{
		Type:      domain.NotificationOrphanWorkers,
		SessionID: domain.SessionID(ids[0]),
		SourceKey: "orphan_workers:" + m.daemonRunID,
		Detail: fmt.Sprintf("%d orphaned worker process trees of terminated sessions: %s. Allow to kill them; dismiss to keep them until the next start.",
			len(ids), strings.Join(ids, ", ")),
	}
	if err := m.notifier.Notify(ctx, intent); err != nil {
		m.logger.Warn("reap: orphan notification failed", "error", err)
	}
}

// ReapOrphanWorkers executes the user's allow decision: a fresh scan, the
// same evidence stack, and the kill. Stored PIDs are never trusted — the scan
// is re-run, so stale or reused pids cannot be signalled.
func (m *Manager) ReapOrphanWorkers(ctx context.Context) ([]string, error) {
	if orphanReapDisabled() {
		return nil, nil
	}
	deps, tbl, at, victims, err := m.orphanScan(ctx)
	if err != nil {
		return nil, fmt.Errorf("reap orphans: %w", err)
	}
	if len(victims) == 0 {
		return nil, nil
	}
	// Re-plans the same snapshot internally: the kill list must equal the plan.
	orphanreap.Reap(ctx, tbl, at, deps, m.orphanKiller)
	ids := make([]string, 0, len(victims))
	for _, v := range victims {
		ids = append(ids, v.SessionID)
	}
	return ids, nil
}

// orphanScan plans the current orphan set on one snapshot. Reap re-plans the
// same snapshot internally (idempotent, cheap) to keep the kill list identical
// to the plan.
func (m *Manager) orphanScan(ctx context.Context) (orphanreap.Deps, *orphanreap.Table, time.Time, []orphanreap.Victim, error) {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return orphanreap.Deps{}, nil, time.Time{}, nil, fmt.Errorf("reap: list sessions: %w", err)
	}
	terminated := make(map[string]struct{})
	for _, rec := range recs {
		if rec.IsTerminated {
			terminated[string(rec.ID)] = struct{}{}
		}
	}
	if len(terminated) == 0 {
		return orphanreap.Deps{}, nil, time.Time{}, nil, nil
	}
	tbl, at, err := m.processTable(ctx)
	if err != nil {
		return orphanreap.Deps{}, nil, time.Time{}, nil, err
	}
	exe, err := m.executable()
	if err != nil {
		return orphanreap.Deps{}, nil, time.Time{}, nil, err
	}
	deps := m.orphanDeps(exe, terminated)
	return deps, tbl, at, orphanreap.Plan(tbl, at, deps), nil
}

func (m *Manager) orphanDeps(exe string, terminated map[string]struct{}) orphanreap.Deps {
	return orphanreap.Deps{
		Boot:           m.bootStartedAt,
		WorktreesDir:   filepath.Join(m.dataDir, "worktrees"),
		ExecutableBase: filepath.Base(exe),
		Terminated:     terminated,
		Logger:         m.logger,
	}
}

// reapSupersededRuntime kills the stale pre-boot pty-host trees of one live
// session right after a relaunch bound a freshly created runtime to it (fork
// issue #115). A daemon restart orphans the old pty-host (PPID=1) and, when
// its registry entry was lost, leaves it invisible to Destroy — the relaunch
// then re-binds the same session id while the old tree leaks on. At that
// moment supersession is proven — the session's live runtime is the fresh
// host — so the same evidence stack that plans the boot report condemns the
// old tree, scoped to exactly this session id (no terminated evidence, so no
// other session's tree is decidable here). Best-effort: failures are logged
// and never fail the relaunch, and AO_NO_ORPHANREAP turns the pass off with
// the rest of the orphan mechanism.
func (m *Manager) reapSupersededRuntime(ctx context.Context, sessionID domain.SessionID) {
	if orphanReapDisabled() {
		return
	}
	// The relaunch request's deadline must not cut the teardown short; bound
	// the pass independently of the caller instead.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reapSupersededBudget)
	defer cancel()
	tbl, at, err := m.processTable(ctx)
	if err != nil {
		m.logger.Warn("reap: superseded-runtime scan skipped", "sessionID", sessionID, "error", err)
		return
	}
	exe, err := m.executable()
	if err != nil {
		m.logger.Warn("reap: superseded-runtime scan skipped", "sessionID", sessionID, "error", err)
		return
	}
	deps := m.orphanDeps(exe, nil)
	deps.Superseded = map[string]struct{}{string(sessionID): {}}
	orphanreap.Reap(ctx, tbl, at, deps, m.orphanKiller)
}

// reapSupersededBudget bounds one superseded-tree teardown inside a relaunch.
const reapSupersededBudget = 10 * time.Second

// snapshotProcessTable reads the process table for the orphan scan. etime (not
// time=) is the age column: wall-clock time since start, locale-free on Linux
// and macOS, so start-time evidence does not depend on the machine locale. On
// platforms without ps (Windows) the command fails and the caller degrades to
// a log — the pass is a no-op there, never a guess.
func snapshotProcessTable(ctx context.Context) (*orphanreap.Table, time.Time, error) {
	at := time.Now().UTC()
	out, err := aoprocess.CommandContext(ctx, "ps", "-axww", "-o", "pid=,ppid=,rss=,etime=,args=").Output()
	if err != nil {
		return nil, at, fmt.Errorf("reap: ps: %w", err)
	}
	tbl, err := orphanreap.ParseTable(string(out), at)
	if err != nil {
		return nil, at, err
	}
	return tbl, at, nil
}
