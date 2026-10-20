package procinventory

import (
	"context"
	"errors"
	"time"
)

// ErrKillInProgress reports that another kill batch is already running.
var ErrKillInProgress = errors.New("procinventory: another kill is in progress")

// Kill result statuses.
const (
	ResultKilled      = "killed"
	ResultAlreadyGone = "already_gone"
	ResultSkipped     = "skipped"
	ResultFailed      = "failed"
)

// KillTarget identifies a tree to kill. RootLstart must come from the same
// inventory the caller rendered: the kill re-validates it against a fresh
// snapshot, so a recycled PID can never be signalled.
type KillTarget struct {
	SessionID  string
	RootPID    int
	RootLstart string
}

type KillResult struct {
	SessionID string
	RootPID   int
	Status    string
	Detail    string
}

type KillReport struct {
	Results []KillResult
}

// Kill terminates orphaned trees. Only trees that re-classify as orphans in a
// fresh snapshot are signalled — a session restored between the caller's
// inventory render and this call comes back skipped, never killed. DB rows
// are deliberately untouched: an orphan has no live row, so the restore
// semantics stay out of this path entirely. One batch at a time; concurrent
// calls fail with ErrKillInProgress.
func (s *Service) Kill(ctx context.Context, targets []KillTarget) (KillReport, error) {
	select {
	case s.killMu <- struct{}{}:
		defer func() { <-s.killMu }()
	default:
		return KillReport{}, ErrKillInProgress
	}
	if len(targets) == 0 {
		return KillReport{Results: []KillResult{}}, nil
	}

	// Fresh evidence for every decision: scan, live set, re-classify.
	entries, err := s.deps.Scan(ctx)
	if err != nil {
		return KillReport{}, err
	}
	live, err := s.deps.LiveSessions(ctx)
	if err != nil {
		return KillReport{}, err
	}
	inv := BuildInventory(entries, live, s.deps.DaemonPID, s.deps.TmuxSocketName, s.deps.Now())

	report := KillReport{Results: make([]KillResult, 0, len(targets))}
	var pending []Tree
	for _, target := range targets {
		result := KillResult{SessionID: target.SessionID, RootPID: target.RootPID, Status: ResultSkipped}
		tree, ok := findTree(inv, target)
		switch {
		case !ok:
			result.Detail = "tree not found in fresh inventory"
		case tree.State != StateOrphan:
			result.Detail = "session is not an orphan (state " + tree.State + ")"
		case tree.RootLstart != target.RootLstart:
			result.Detail = "root pid was recycled (start time mismatch)"
		case tree.PGID != tree.RootPID:
			result.Detail = "root is not a process group leader"
		default:
			pending = append(pending, tree)
			continue
		}
		report.Results = append(report.Results, result)
	}

	// Phase TERM: one group signal reaches the whole tree (the root is a
	// session leader, so pgid == rootPID and every member is in that group).
	term := make(map[int]bool)
	for _, tree := range pending {
		err := s.deps.Signal(-tree.RootPID, SigTerm)
		if errors.Is(err, ErrProcessGone) {
			report.Results = append(report.Results, KillResult{SessionID: tree.SessionID, RootPID: tree.RootPID, Status: ResultAlreadyGone})
			continue
		}
		if err != nil {
			report.Results = append(report.Results, KillResult{SessionID: tree.SessionID, RootPID: tree.RootPID, Status: ResultFailed, Detail: err.Error()})
			continue
		}
		term[tree.RootPID] = true
	}

	// Shared grace across all targets, then SIGKILL for survivors.
	deadline := s.deps.Now().Add(s.deps.Grace)
	for len(term) > 0 && s.deps.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		for pid := range term {
			if !s.deps.Probe(pid) {
				delete(term, pid)
			}
		}
	}
	for _, tree := range pending {
		if !term[tree.RootPID] {
			report.Results = append(report.Results, KillResult{SessionID: tree.SessionID, RootPID: tree.RootPID, Status: ResultKilled})
			continue
		}
		if err := s.deps.Signal(-tree.RootPID, SigKill); err != nil && !errors.Is(err, ErrProcessGone) {
			s.deps.Log.Error("procinventory: SIGKILL failed", "session", tree.SessionID, "pid", tree.RootPID, "err", err)
			report.Results = append(report.Results, KillResult{SessionID: tree.SessionID, RootPID: tree.RootPID, Status: ResultFailed, Detail: err.Error()})
			continue
		}
		report.Results = append(report.Results, KillResult{SessionID: tree.SessionID, RootPID: tree.RootPID, Status: ResultKilled})
	}

	for _, result := range report.Results {
		if result.Status == ResultKilled {
			s.unregisterTree(ctx, result.SessionID)
		}
	}
	return report, nil
}

func findTree(inv Inventory, target KillTarget) (Tree, bool) {
	for _, tree := range inv.Trees {
		if tree.SessionID == target.SessionID && tree.RootPID == target.RootPID {
			return tree, true
		}
	}
	return Tree{}, false
}

// unregisterTree drops the killed orphan's PTY-host registry entry. Best
// effort: Scan prunes dead PIDs anyway, so a failure here only delays cleanup.
func (s *Service) unregisterTree(ctx context.Context, sessionID string) {
	if s.deps.Unregister == nil {
		return
	}
	if err := s.deps.Unregister(ctx, sessionID); err != nil {
		s.deps.Log.Debug("procinventory: registry unregister failed", "session", sessionID, "err", err)
	}
}
