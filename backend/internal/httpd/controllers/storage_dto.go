package controllers

import "time"

// StorageReportResponse is the body of GET /api/v1/storage: what AO keeps on
// disk, what the policy would free, and the storage settings.
type StorageReportResponse struct {
	ScannedAt            *time.Time             `json:"scannedAt,omitempty"`
	ScanDurationMs       int64                  `json:"scanDurationMs"`
	Scanning             bool                   `json:"scanning"`
	ScanError            string                 `json:"scanError,omitempty"`
	TotalBytes           int64                  `json:"totalBytes"`
	ReclaimableBytes     int64                  `json:"reclaimableBytes"`
	AutoReclaimableBytes int64                  `json:"autoReclaimableBytes"`
	Disk                 StorageDisk            `json:"disk"`
	Categories           []StorageCategoryUsage `json:"categories"`
	Worktrees            []StorageWorktree      `json:"worktrees"`
	Database             StorageDatabase        `json:"database"`
	Plan                 []StoragePlannedAction `json:"plan"`
	Settings             StorageSettings        `json:"settings"`
	AutoCleanAllowed     bool                   `json:"autoCleanAllowed"`
	CopyOnWrite          bool                   `json:"copyOnWrite"`
	Cleaning             bool                   `json:"cleaning"`
	LastClean            *StorageCleanResponse  `json:"lastClean,omitempty"`
}

// StorageDisk is the latest free-space reading of the volume holding AO's data.
type StorageDisk struct {
	Path       string     `json:"path"`
	TotalBytes int64      `json:"totalBytes"`
	FreeBytes  int64      `json:"freeBytes"`
	Level      string     `json:"level" enum:"normal,low,critical"`
	CheckedAt  *time.Time `json:"checkedAt,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// StorageCategoryUsage is how much one kind of AO data holds and could free.
type StorageCategoryUsage struct {
	Category         string `json:"category" enum:"worktrees,database,db_backups,harnesses,browser_data,logs,dev_profiles,dependency_templates,other"`
	Bytes            int64  `json:"bytes"`
	ReclaimableBytes int64  `json:"reclaimableBytes"`
}

// StorageWorktree is one worktree folder under AO's managed root.
type StorageWorktree struct {
	Path            string     `json:"path"`
	ProjectID       string     `json:"projectId,omitempty"`
	SessionID       string     `json:"sessionId,omitempty"`
	State           string     `json:"state" enum:"live,quiet,ended,orphan"`
	Bytes           int64      `json:"bytes"`
	DependencyBytes int64      `json:"dependencyBytes"`
	Offloaded       bool       `json:"offloaded"`
	OffloadedBytes  int64      `json:"offloadedBytes"`
	OffloadedAt     *time.Time `json:"offloadedAt,omitempty"`
	RestoreError    string     `json:"restoreError,omitempty"`
	LastUsedAt      *time.Time `json:"lastUsedAt,omitempty"`
}

// StorageDatabase sizes the SQLite database.
type StorageDatabase struct {
	FileBytes   int64              `json:"fileBytes"`
	WALBytes    int64              `json:"walBytes"`
	FreeBytes   int64              `json:"freeBytes"`
	Tables      []StorageTableSize `json:"tables"`
	TablesKnown bool               `json:"tablesKnown"`
}

// StorageTableSize is how many bytes one table or index occupies.
type StorageTableSize struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// StoragePlannedAction is one cleanup the storage policy proposes.
type StoragePlannedAction struct {
	Kind      string `json:"kind" enum:"offload,ended_worktrees,orphan_worktrees,db_backups,logs,dev_profiles,dependency_templates,database,trash"`
	Target    string `json:"target"`
	SessionID string `json:"sessionId,omitempty"`
	Reason    string `json:"reason"`
	Bytes     int64  `json:"bytes"`
	Automatic bool   `json:"automatic"`
}

// StorageSettings are the storage-management preferences.
type StorageSettings struct {
	AutoClean                bool       `json:"autoClean"`
	OffloadEnabled           bool       `json:"offloadEnabled"`
	OffloadIdleHours         int        `json:"offloadIdleHours"`
	OffloadNoPRGraceHours    int        `json:"offloadNoPrGraceHours"`
	OffloadMergedIdleMinutes int        `json:"offloadMergedIdleMinutes"`
	EndedWorktreeDays        int        `json:"endedWorktreeDays"`
	BackupDays               int        `json:"backupDays"`
	LogDays                  int        `json:"logDays"`
	CleanDevProfiles         bool       `json:"cleanDevProfiles"`
	DevProfileDays           int        `json:"devProfileDays"`
	TemplateDays             int        `json:"templateDays"`
	UpdatedAt                *time.Time `json:"updatedAt,omitempty"`
}

// UpdateStorageSettingsRequest changes some storage settings; omitted fields
// keep their value.
type UpdateStorageSettingsRequest struct {
	AutoClean                *bool `json:"autoClean,omitempty"`
	OffloadEnabled           *bool `json:"offloadEnabled,omitempty"`
	OffloadIdleHours         *int  `json:"offloadIdleHours,omitempty"`
	OffloadNoPRGraceHours    *int  `json:"offloadNoPrGraceHours,omitempty"`
	OffloadMergedIdleMinutes *int  `json:"offloadMergedIdleMinutes,omitempty"`
	EndedWorktreeDays        *int  `json:"endedWorktreeDays,omitempty"`
	BackupDays               *int  `json:"backupDays,omitempty"`
	LogDays                  *int  `json:"logDays,omitempty"`
	CleanDevProfiles         *bool `json:"cleanDevProfiles,omitempty"`
	DevProfileDays           *int  `json:"devProfileDays,omitempty"`
	TemplateDays             *int  `json:"templateDays,omitempty"`
}

// StorageCleanRequest is the body of POST /api/v1/storage/clean.
type StorageCleanRequest struct {
	Kinds      []string `json:"kinds,omitempty"`
	SessionIDs []string `json:"sessionIds,omitempty"`
	DryRun     bool     `json:"dryRun,omitempty"`
	IncludeDev bool     `json:"includeDev,omitempty"`
}

// StorageCleanResponse reports a cleanup: the plan of a dry run, the accepted
// actions of a started cleanup, or the outcome of a finished one.
type StorageCleanResponse struct {
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	Running    bool            `json:"running"`
	DryRun     bool            `json:"dryRun"`
	FreedBytes int64           `json:"freedBytes"`
	Actions    []StorageAction `json:"actions"`
}

// StorageAction is one storage audit-log entry.
type StorageAction struct {
	ID        int64     `json:"id,omitempty"`
	At        time.Time `json:"at"`
	Kind      string    `json:"kind" enum:"offload,ended_worktrees,orphan_worktrees,db_backups,logs,dev_profiles,dependency_templates,database,trash,restore"`
	Target    string    `json:"target"`
	SessionID string    `json:"sessionId,omitempty"`
	Reason    string    `json:"reason"`
	Bytes     int64     `json:"bytes"`
	DryRun    bool      `json:"dryRun"`
	Result    string    `json:"result" enum:"planned,done,skipped,failed"`
	Error     string    `json:"error,omitempty"`
}

// StorageActionsQuery is the query string of GET /api/v1/storage/actions.
type StorageActionsQuery struct {
	Limit *int64 `query:"limit,omitempty" minimum:"1" maximum:"1000"`
}

// StorageActionsResponse is the body of GET /api/v1/storage/actions.
type StorageActionsResponse struct {
	Actions []StorageAction `json:"actions"`
}
