package systeminstall

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// UpdateWhileRunning is how safely an update can replace a harness binary that
// open sessions are still running.
type UpdateWhileRunning string

const (
	// UpdateWhileRunningSafe means the installer keeps the running version on
	// disk, so live sessions keep working on it until they restart.
	UpdateWhileRunningSafe UpdateWhileRunning = "safe"
	// UpdateWhileRunningWarn means the installer may remove files the running
	// version still loads. The effective update gate blocks these methods
	// while sessions are live.
	UpdateWhileRunningWarn UpdateWhileRunning = "warn"
	// UpdateWhileRunningBlocked means live sessions must end before the update.
	UpdateWhileRunningBlocked UpdateWhileRunning = "blocked"
	// UpdateWhileRunningIdle means the update waits until every live session
	// is idle, then stops them, updates and resumes them (Windows, where a
	// running executable cannot be replaced).
	UpdateWhileRunningIdle UpdateWhileRunning = "idle"
)

// HarnessActiveError reports the live work that kept a harness operation from
// starting. It unwraps to ErrHarnessActive so existing callers keep matching.
type HarnessActiveError struct {
	Target      Target
	SessionIDs  []domain.SessionID
	ReviewerIDs []domain.SessionID
	Level       UpdateWhileRunning
	// Starting means a session or reviewer launch holds the harness right now.
	Starting bool
}

func (e *HarnessActiveError) Error() string {
	switch {
	case e.Starting:
		return fmt.Sprintf("%v: a %s session is starting", ErrHarnessActive, e.Target)
	default:
		return fmt.Sprintf("%v: end %d %s session(s) before changing its installation", ErrHarnessActive, e.Count(), e.Target)
	}
}

func (e *HarnessActiveError) Unwrap() error { return ErrHarnessActive }

// Count is the number of distinct sessions running the harness, directly or
// through their reviewer.
func (e *HarnessActiveError) Count() int {
	return len(uniqueSessionIDs(append(slices.Clone(e.SessionIDs), e.ReviewerIDs...)))
}

// liveSession reports whether a session row may have a harness process
// running. Terminated, hibernated, and exited sessions start a fresh process
// on their next launch, so they never hold the installed binary open.
func liveSession(session domain.SessionRecord) bool {
	return !session.IsTerminated && !session.IsTaskPreparation &&
		session.HibernatedAt == nil && session.Activity.State != domain.ActivityExited
}

// updateWhileRunningLevel classifies an update method by what its installer
// does to the version open sessions run. Vendor installers that keep each
// release in its own directory, and Homebrew formulas with cleanup deferred,
// leave the running release in place (safe). Casks, other self-updaters and
// package managers that rewrite a shared tree replace it (warn): the update
// waits until those sessions are idle and restarts them around the install. Managed package
// installs are promoted to safe only after selecting the side-by-side plan.
// Windows cannot replace a running executable at all, so its updates
// wait for idle sessions and restart them around the install.
func updateWhileRunningLevel(goos string, target Target, method string, cask bool, pkg string) UpdateWhileRunning {
	if goos == "windows" {
		return UpdateWhileRunningBlocked
	}
	switch method {
	case "homebrew":
		if cask && (pkg == "" || singleBinaryCasks[target] != pkg) {
			return UpdateWhileRunningWarn
		}
		return UpdateWhileRunningSafe
	case "official-installer":
		if versionedVendorInstalls[target] {
			return UpdateWhileRunningSafe
		}
		return UpdateWhileRunningWarn
	case "npm", "bun", "uv", "pipx":
		return UpdateWhileRunningWarn
	default:
		return UpdateWhileRunningBlocked
	}
}

// singleBinaryCasks names, per target, the one cask whose layout was checked
// to ship a single self-contained executable. A cask upgrade deletes the old
// version's folder, but a running process keeps the file it already opened
// and loads nothing else from that folder. Any other cask owning the target
// (another tap, a channel cask) has a layout nobody checked and stays warn.
// Codex's cask also ships codex-resources, which a running session can load
// later, so it stays out.
var singleBinaryCasks = map[Target]string{
	TargetClaudeCode: "claude-code",
	TargetCopilot:    "copilot-cli",
	TargetDroid:      "droid",
}

// versionedVendorInstalls keep every release in its own file or directory and
// switch a launcher link, so an update never touches the running release.
var versionedVendorInstalls = map[Target]bool{
	TargetClaudeCode: true,
	TargetCursor:     true,
	TargetGrok:       true,
}

// effectiveUpdateLevel applies AO_UPDATE_WHILE_RUNNING and AO_UPDATE_WHEN_IDLE.
// An installer that cannot retain live releases waits for idle sessions,
// stops them, updates and resumes them; already idle sessions are stopped
// right away. Without update-when-idle it is refused while sessions run.
func (s *Service) effectiveUpdateLevel(level UpdateWhileRunning) UpdateWhileRunning {
	if s.updateWhileRunningDisabled || level == "" {
		return UpdateWhileRunningBlocked
	}
	if level == UpdateWhileRunningWarn || level == UpdateWhileRunningBlocked {
		if s.updateWhenIdle && s.idleCoordinatorReady() {
			return UpdateWhileRunningIdle
		}
		return UpdateWhileRunningBlocked
	}
	return level
}

// harnessInUse lists the live sessions and reviewers that run target.
func (s *Service) harnessInUse(ctx context.Context, target Target) (sessions, reviewers []domain.SessionID, err error) {
	if s.sessions != nil {
		records, err := s.sessions.ListAllSessions(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("systeminstall: list sessions before harness operation: %w", err)
		}
		for _, record := range records {
			if record.Harness == domain.AgentHarness(target) && liveSession(record) {
				sessions = append(sessions, record.ID)
			}
		}
	}
	if s.reviewers != nil {
		handles, err := s.reviewers.ListLiveReviewerHandles(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("systeminstall: list reviewers before harness operation: %w", err)
		}
		for _, handle := range handles {
			if string(handle.Harness) == string(target) {
				reviewers = append(reviewers, handle.SessionID)
			}
		}
		chats, err := s.reviewers.ListRecoverableChatReviews(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("systeminstall: list reviewer chats before harness operation: %w", err)
		}
		for _, review := range chats {
			if string(review.Harness) == string(target) && review.ControllerGeneration != "" {
				reviewers = append(reviewers, review.SessionID)
			}
		}
	}
	return uniqueSessionIDs(sessions), uniqueSessionIDs(reviewers), nil
}

func uniqueSessionIDs(ids []domain.SessionID) []domain.SessionID {
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// gateRunningSessions applies the update-while-running decision table. It
// returns whether the operation proceeds with live sessions, which callers use
// to keep the previous release on disk where the installer allows it.
//
//	live  level      result
//	0     any        run
//	>0    safe       run; sessions on the old release restart on the new one once idle
//	>0    idle       wait for idle sessions, then stop, update, resume (errUpdateWhenIdle)
//	>0    blocked    HarnessActiveError (AO_UPDATE_WHILE_RUNNING=off, a reinstall,
//	                 or Windows without update-when-idle)
func (s *Service) gateRunningSessions(ctx context.Context, target Target, operation AgentOperation, plan Plan) (bool, error) {
	sessions, reviewers, err := s.harnessInUse(ctx, target)
	if err != nil {
		return false, err
	}
	if len(sessions)+len(reviewers) == 0 {
		return false, nil
	}
	level := UpdateWhileRunningBlocked
	if operation == AgentOperationUpdate {
		level = s.effectiveUpdateLevel(plan.UpdateWhileRunning)
	}
	active := &HarnessActiveError{Target: target, SessionIDs: sessions, ReviewerIDs: reviewers, Level: level}
	if level == UpdateWhileRunningBlocked {
		return false, active
	}
	if level == UpdateWhileRunningIdle {
		return false, errUpdateWhenIdle
	}
	// Install now and leave running processes alone: they keep
	// the release they loaded and restart on the new one once idle.
	return true, nil
}

// UpdateInUse describes the live work an update would run alongside, computed
// when the advisory is read rather than when its release check ran. It is
// absent when nothing runs the harness.
type UpdateInUse struct {
	LiveSessions int                `json:"liveSessions" description:"Sessions running this harness, directly or through their reviewer."`
	SessionIDs   []string           `json:"sessionIds,omitempty" description:"Sessions that run this harness or own a reviewer that does."`
	Level        UpdateWhileRunning `json:"level" enum:"safe,warn,blocked,idle" description:"Whether an update can run while they do: safe keeps the running release, warn may disturb it, blocked requires them to end, idle waits until they are idle and restarts them around the update."`
}

func (s *Service) updateInUse(ctx context.Context, target Target, advisory UpdateAdvisory) *UpdateInUse {
	sessions, reviewers, err := s.harnessInUse(ctx, target)
	if err != nil {
		return nil
	}
	if len(sessions)+len(reviewers) == 0 {
		return nil
	}
	ids := make([]string, 0, len(sessions)+len(reviewers))
	for _, id := range uniqueSessionIDs(append(slices.Clone(sessions), reviewers...)) {
		ids = append(ids, string(id))
	}
	level := updateWhileRunningLevel(s.goos, target, advisory.MaintenanceMethod, advisory.maintenanceCask, advisory.maintenancePackage)
	_, officialRecipe := officialManagedRecipes[target]
	officialManaged := advisory.MaintenanceMethod == "official-installer" && officialRecipe && (s.goos == "darwin" || s.goos == "linux")
	if _, ok := officialManagedCaskFor(s.goos, Plan{Target: target, Method: advisory.MaintenanceMethod, PackageCask: advisory.maintenanceCask, Package: advisory.maintenancePackage}); ok {
		officialManaged = true
	}
	if (managedMethods[advisory.MaintenanceMethod] || officialManaged) && s.managedRoot != "" {
		// MaintenanceMethod is the ownership-checked source of this advisory.
		// Use the same update recipe as StartAgentOperation: the raw method
		// overwrites files, but its eligible managed recipe keeps them intact.
		// Recheck capabilities so a rejected runtime cannot advertise safety.
		level = UpdateWhileRunningBlocked
		if planner, err := s.newRequestPlanner(ctx); err == nil {
			for _, plan := range planner.agentMethodPlans(target, AgentOperationUpdate) {
				if plan.Method == advisory.MaintenanceMethod && !plan.Unsupported {
					level = plan.UpdateWhileRunning
					break
				}
			}
		}
	}
	level = s.effectiveUpdateLevel(level)
	if strings.TrimSpace(advisory.MaintenanceMethod) == "" {
		level = UpdateWhileRunningBlocked
	}
	return &UpdateInUse{LiveSessions: len(ids), SessionIDs: ids, Level: level}
}
