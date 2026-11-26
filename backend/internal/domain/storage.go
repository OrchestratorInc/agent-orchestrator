package domain

import (
	"fmt"
	"time"
)

// StorageCategory names one kind of AO-owned data under the AO state dir. The
// storage report sizes each one; cleanup actions target a subset of them.
type StorageCategory string

// Storage categories.
const (
	// StorageWorktrees is every session worktree under the managed root.
	StorageWorktrees StorageCategory = "worktrees"
	// StorageDatabase is ao.db with its WAL and shared-memory files.
	StorageDatabase StorageCategory = "database"
	// StorageDBBackups are one-time ao.db copies left by migration repairs.
	StorageDBBackups StorageCategory = "db_backups"
	// StorageHarnesses are agent CLIs AO downloaded or manages.
	StorageHarnesses StorageCategory = "harnesses"
	// StorageBrowserData is the desktop's Chromium profile (~/.ao/electron).
	StorageBrowserData StorageCategory = "browser_data"
	// StorageLogs are the daemon and desktop log files.
	StorageLogs StorageCategory = "logs"
	// StorageDevProfiles are isolated data dirs from AO dev builds (~/.ao/dev).
	StorageDevProfiles StorageCategory = "dev_profiles"
	// StorageDependencyTemplates are dependency trees kept for copy-on-write
	// restores of offloaded sessions.
	StorageDependencyTemplates StorageCategory = "dependency_templates"
	// StorageOther is everything else under the state dir.
	StorageOther StorageCategory = "other"
)

// StorageCleanKind is one kind of cleanup action. Each maps to one policy in
// the storage design (offload, ended-session worktrees, aged files, vacuum).
type StorageCleanKind string

// Storage cleanup kinds.
const (
	// StorageCleanOffload removes git-ignored dependency and build folders from
	// a quiet open session; Setup or a template clone restores them on resume.
	StorageCleanOffload StorageCleanKind = "offload"
	// StorageCleanEndedWorktrees removes worktrees of terminated sessions through
	// the no-force session cleanup path.
	StorageCleanEndedWorktrees StorageCleanKind = "ended_worktrees"
	// StorageCleanOrphanWorktrees removes clean git worktrees under the managed
	// root that no session owns. Never automatic.
	StorageCleanOrphanWorktrees StorageCleanKind = "orphan_worktrees"
	// StorageCleanBackups removes migration backups past their age, keeping the
	// newest one.
	StorageCleanBackups StorageCleanKind = "db_backups"
	// StorageCleanLogs removes rotated log files past their age.
	StorageCleanLogs StorageCleanKind = "logs"
	// StorageCleanDevProfiles removes unused dev-build data dirs. Opt-in.
	StorageCleanDevProfiles StorageCleanKind = "dev_profiles"
	// StorageCleanTemplates evicts dependency templates unused past their age.
	StorageCleanTemplates StorageCleanKind = "dependency_templates"
	// StorageCleanDatabase returns free database pages to the disk.
	StorageCleanDatabase StorageCleanKind = "database"
	// StorageCleanTrash finishes deletes an earlier pass moved aside.
	StorageCleanTrash StorageCleanKind = "trash"
)

// StorageCleanRestore is the audit-log kind of a dependency restore. It is not
// a cleanup a person can request.
const StorageCleanRestore StorageCleanKind = "restore"

// StorageCleanKinds lists every cleanup kind in report order.
var StorageCleanKinds = []StorageCleanKind{
	StorageCleanOffload,
	StorageCleanEndedWorktrees,
	StorageCleanOrphanWorktrees,
	StorageCleanBackups,
	StorageCleanLogs,
	StorageCleanDevProfiles,
	StorageCleanTemplates,
	StorageCleanDatabase,
	StorageCleanTrash,
}

// ParseStorageCleanKind reports whether raw names a cleanup kind.
func ParseStorageCleanKind(raw string) (StorageCleanKind, bool) {
	for _, kind := range StorageCleanKinds {
		if string(kind) == raw {
			return kind, true
		}
	}
	return "", false
}

// StorageSettings are the user's storage-management preferences. One row,
// seeded by migration, so a read never handles absence.
type StorageSettings struct {
	// AutoClean lets the daemon apply the automatic policies on its own. Off,
	// the daemon still measures and plans, and manual cleanup still works.
	AutoClean bool `json:"autoClean"`
	// OffloadEnabled lets quiet open sessions have their dependencies offloaded.
	OffloadEnabled bool `json:"offloadEnabled"`
	// OffloadIdleHours is how long a session must be quiet before its
	// dependencies are offloaded.
	OffloadIdleHours int `json:"offloadIdleHours"`
	// OffloadNoPRGraceHours is the shorter quiet time for a session that has
	// not opened a pull request yet.
	OffloadNoPRGraceHours int `json:"offloadNoPrGraceHours"`
	// OffloadMergedIdleMinutes is the quiet time after every pull request of
	// the session merged or closed.
	OffloadMergedIdleMinutes int `json:"offloadMergedIdleMinutes"`
	// EndedWorktreeDays is how long a terminated session keeps its worktree
	// before automatic removal; 0 never removes it automatically.
	EndedWorktreeDays int `json:"endedWorktreeDays"`
	// BackupDays is the age past which migration backups are removed (the
	// newest one is always kept).
	BackupDays int `json:"backupDays"`
	// LogDays is the age past which rotated log files are removed.
	LogDays int `json:"logDays"`
	// CleanDevProfiles opts in to removing unused dev-build data dirs.
	CleanDevProfiles bool `json:"cleanDevProfiles"`
	// DevProfileDays is how long a dev-build data dir must be unused.
	DevProfileDays int `json:"devProfileDays"`
	// TemplateDays is how long an unused dependency template is kept.
	TemplateDays int `json:"templateDays"`
	// UpdatedAt is when a person last changed these settings.
	UpdatedAt time.Time `json:"updatedAt"`
}

// DefaultStorageSettings are the defaults the migration seeds.
func DefaultStorageSettings() StorageSettings {
	return StorageSettings{
		AutoClean:                true,
		OffloadEnabled:           true,
		OffloadIdleHours:         48,
		OffloadNoPRGraceHours:    2,
		OffloadMergedIdleMinutes: 30,
		EndedWorktreeDays:        7,
		BackupDays:               14,
		LogDays:                  14,
		CleanDevProfiles:         false,
		DevProfileDays:           7,
		TemplateDays:             30,
	}
}

// Validate reports the first setting outside its allowed range.
func (s StorageSettings) Validate() error {
	checks := []struct {
		name     string
		value    int
		min, max int
	}{
		{"offloadIdleHours", s.OffloadIdleHours, 1, 8760},
		{"offloadNoPrGraceHours", s.OffloadNoPRGraceHours, 1, 8760},
		{"offloadMergedIdleMinutes", s.OffloadMergedIdleMinutes, 0, 10080},
		{"endedWorktreeDays", s.EndedWorktreeDays, 0, 3650},
		{"backupDays", s.BackupDays, 1, 3650},
		{"logDays", s.LogDays, 1, 3650},
		{"devProfileDays", s.DevProfileDays, 1, 3650},
		{"templateDays", s.TemplateDays, 1, 3650},
	}
	for _, check := range checks {
		if check.value < check.min || check.value > check.max {
			return fmt.Errorf("%s must be between %d and %d", check.name, check.min, check.max)
		}
	}
	return nil
}

// SessionStorageFacts are the durable offload facts of one session. They are
// facts, not status: whether a session reads as offloaded is derived from them.
type SessionStorageFacts struct {
	SessionID SessionID
	// OffloadedAt is when the last offload finished; zero means never.
	OffloadedAt time.Time
	// OffloadedBytes is how much that offload freed.
	OffloadedBytes int64
	// OffloadedPaths are the worktree-relative paths it removed, slash-separated.
	OffloadedPaths []string
	// TemplateKeys maps an offloaded path to the dependency template its tree
	// was moved into, so a restore can clone it back.
	TemplateKeys map[string]string
	// RestoredAt is when dependencies were last restored; a restore at or after
	// OffloadedAt settles the offload.
	RestoredAt time.Time
	// RestoreError is the last restore failure, empty after a success.
	RestoreError string
}

// Offloaded reports an offload that no restore has settled yet.
func (f SessionStorageFacts) Offloaded() bool {
	return !f.OffloadedAt.IsZero() && f.RestoredAt.Before(f.OffloadedAt)
}

// StorageActionResult is the outcome of one storage action.
type StorageActionResult string

// Storage action results.
const (
	StorageActionPlanned StorageActionResult = "planned"
	StorageActionDone    StorageActionResult = "done"
	StorageActionSkipped StorageActionResult = "skipped"
	StorageActionFailed  StorageActionResult = "failed"
)

// StorageAction is one audit-log entry: what a cleanup did or would do, why,
// and how much it freed.
type StorageAction struct {
	ID        int64               `json:"id"`
	At        time.Time           `json:"at"`
	Kind      StorageCleanKind    `json:"kind"`
	Target    string              `json:"target"`
	SessionID SessionID           `json:"sessionId,omitempty"`
	Reason    string              `json:"reason"`
	Bytes     int64               `json:"bytes"`
	DryRun    bool                `json:"dryRun"`
	Result    StorageActionResult `json:"result"`
	Error     string              `json:"error,omitempty"`
}

// StorageTableSize is how many bytes one database table or index occupies.
type StorageTableSize struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// DiskPressureLevel is how full the volume holding AO's data is.
type DiskPressureLevel string

// Disk pressure levels.
const (
	DiskPressureNormal   DiskPressureLevel = "normal"
	DiskPressureLow      DiskPressureLevel = "low"
	DiskPressureCritical DiskPressureLevel = "critical"
)
