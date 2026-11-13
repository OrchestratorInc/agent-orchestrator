package storagemgr

import (
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// PlannedAction is one cleanup the policy proposes. Automatic actions run on
// the daemon's schedule; the rest wait for a person to ask.
type PlannedAction struct {
	Kind      domain.StorageCleanKind
	Target    string
	SessionID domain.SessionID
	ProjectID domain.ProjectID
	Reason    string
	Bytes     int64
	Automatic bool
}

// dbVacuumThreshold is the free database space worth returning to the disk.
const dbVacuumThreshold = 8 << 20

const day = 24 * time.Hour

type policyInput struct {
	Now      time.Time
	Settings domain.StorageSettings
	Pressure domain.DiskPressureLevel
	Snapshot *scanSnapshot
}

// plan is the pure storage policy: durable facts and measurements in,
// proposed actions out. It decides nothing about live processes; the executor
// re-checks every guard before acting.
func plan(in policyInput) []PlannedAction {
	snap := in.Snapshot
	if snap == nil {
		return nil
	}
	settings := in.Settings
	auto := settings.AutoClean
	sessions := make(map[domain.SessionID]sessionView, len(snap.Sessions))
	for _, sess := range snap.Sessions {
		sessions[sess.ID] = sess
	}
	var out []PlannedAction

	for _, wt := range snap.Worktrees {
		sess, owned := sessions[wt.SessionID]
		switch wt.State {
		case WorktreeQuiet:
			if !owned || wt.DependencyBytes == 0 || !settings.OffloadEnabled || sess.Orchestrator || sess.WorkspaceKind || sess.OffloadDisabled {
				continue
			}
			threshold, why := offloadThreshold(sess, settings, in.Pressure)
			idle := in.Now.Sub(sess.LastActivity)
			out = append(out, PlannedAction{
				Kind: domain.StorageCleanOffload, Target: wt.Path, SessionID: sess.ID, Bytes: wt.DependencyBytes,
				Reason:    fmt.Sprintf("quiet %s; %s", humanDuration(idle), why),
				Automatic: auto && idle >= threshold,
			})
		case WorktreeEnded:
			if !owned {
				continue
			}
			age := in.Now.Sub(sess.EndedAt)
			retry := sess.Cleanup != domain.DispositionPreservedDirty && sess.Cleanup != domain.DispositionFailed
			due := settings.EndedWorktreeDays > 0 && !sess.EndedAt.IsZero() && age >= time.Duration(settings.EndedWorktreeDays)*day
			reason := fmt.Sprintf("session ended %s ago", humanDuration(age))
			if !retry {
				reason += "; kept before because it had changes"
			}
			out = append(out, PlannedAction{
				Kind: domain.StorageCleanEndedWorktrees, Target: wt.Path, SessionID: sess.ID, Bytes: wt.Bytes,
				Reason: reason, Automatic: auto && retry && due,
			})
		case WorktreeOrphan:
			// Only a clean git worktree is offered, and never automatically:
			// AO cannot tell whose work an unowned folder holds.
			if wt.GitWorktree && wt.Clean {
				out = append(out, PlannedAction{
					Kind: domain.StorageCleanOrphanWorktrees, Target: wt.Path, ProjectID: wt.ProjectID, Bytes: wt.Bytes,
					Reason: "no session owns this worktree and it has no changes",
				})
			}
		}
	}

	// Backups are newest first; the newest is always kept.
	for i, backup := range snap.Backups {
		if i == 0 {
			continue
		}
		age := in.Now.Sub(backup.ModTime)
		out = append(out, PlannedAction{
			Kind: domain.StorageCleanBackups, Target: backup.Path, Bytes: backup.Bytes,
			Reason:    fmt.Sprintf("database backup from %s ago; a newer one is kept", humanDuration(age)),
			Automatic: auto && age >= time.Duration(settings.BackupDays)*day,
		})
	}

	for _, log := range snap.Logs {
		age := in.Now.Sub(log.ModTime)
		if age < time.Duration(settings.LogDays)*day {
			continue
		}
		out = append(out, PlannedAction{
			Kind: domain.StorageCleanLogs, Target: log.Path, Bytes: log.Bytes,
			Reason: fmt.Sprintf("log not written for %s", humanDuration(age)), Automatic: auto,
		})
	}

	for _, profile := range snap.DevProfiles {
		if profile.Running {
			continue
		}
		age := in.Now.Sub(profile.ModTime)
		out = append(out, PlannedAction{
			Kind: domain.StorageCleanDevProfiles, Target: profile.Path, Bytes: profile.Bytes,
			Reason:    fmt.Sprintf("dev build data unused for %s", humanDuration(age)),
			Automatic: auto && settings.CleanDevProfiles && age >= time.Duration(settings.DevProfileDays)*day,
		})
	}

	templateLimit := time.Duration(settings.TemplateDays) * day
	if in.Pressure == domain.DiskPressureCritical && templateLimit > day {
		templateLimit = day
	}
	for _, template := range snap.Templates {
		age := in.Now.Sub(template.LastUsed)
		out = append(out, PlannedAction{
			Kind: domain.StorageCleanTemplates, Target: template.Path, Bytes: template.Bytes,
			Reason:    fmt.Sprintf("dependency template unused for %s", humanDuration(age)),
			Automatic: auto && age >= templateLimit,
		})
	}

	if snap.Database.FreeBytes > 0 {
		out = append(out, PlannedAction{
			Kind: domain.StorageCleanDatabase, Target: "ao.db", Bytes: snap.Database.FreeBytes,
			Reason:    "free pages left by deleted rows",
			Automatic: auto && snap.Database.FreeBytes >= dbVacuumThreshold,
		})
	}

	for _, entry := range snap.Trash {
		// Finishing a delete an earlier pass started is always safe.
		out = append(out, PlannedAction{
			Kind: domain.StorageCleanTrash, Target: entry.Path, Bytes: entry.Bytes,
			Reason: "moved aside by an earlier cleanup", Automatic: true,
		})
	}
	return out
}

// offloadThreshold is how long a session must be quiet before its
// dependencies are offloaded, and why: shortest once its work is merged,
// short when it never opened a pull request, longest while one is open.
// Disk pressure shortens it.
func offloadThreshold(sess sessionView, settings domain.StorageSettings, pressure domain.DiskPressureLevel) (time.Duration, string) {
	var threshold time.Duration
	var why string
	switch {
	case sess.PRsDone:
		threshold = time.Duration(settings.OffloadMergedIdleMinutes) * time.Minute
		why = "its pull requests are merged or closed"
	case sess.PRCount == 0:
		threshold = time.Duration(settings.OffloadNoPRGraceHours) * time.Hour
		why = "no pull request yet"
	default:
		threshold = time.Duration(settings.OffloadIdleHours) * time.Hour
		why = "pull request still open"
	}
	switch pressure {
	case domain.DiskPressureLow:
		if threshold > 12*time.Hour {
			threshold = 12 * time.Hour
		}
		why += "; disk space is low"
	case domain.DiskPressureCritical:
		threshold = 0
		why += "; disk is almost full"
	}
	return threshold, why
}

func humanDuration(d time.Duration) string {
	switch {
	case d < 0:
		return "0m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 2*day:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/day))
	}
}
