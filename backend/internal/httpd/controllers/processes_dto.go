package controllers

import "time"

// Wire shapes for the /system/processes surface. Kept beside the controller
// (not in dto.go) per the codex_accounts_dto.go precedent: the spec reflects
// these types verbatim.

// ProcessGroupSummaryDTO is the RSS rollup for one process group (the daemon
// itself, or the tmux server) as the status bar shows it.
type ProcessGroupSummaryDTO struct {
	PID      int   `json:"pid"`
	Present  bool  `json:"present"`
	RSSBytes int64 `json:"rssBytes"`
}

// ProcessTreeDTO describes one session's process tree in the /system/processes
// inventory.
type ProcessTreeDTO struct {
	SessionID  string `json:"sessionId"`
	RootPID    int    `json:"rootPid"`
	RootLstart string `json:"rootLstart"`
	PIDCount   int    `json:"pidCount"`
	RSSBytes   int64  `json:"rssBytes"`
	// Kind is the DB session kind ("worker" | "orchestrator"); empty when the
	// session has no live row (orphan/foreign trees).
	Kind string `json:"kind"`
	// State is owned | orphan | foreign. Only orphan trees are killable.
	State string `json:"state"`
	// Attached reports whether the root's parent is this daemon; an owned tree
	// with attached=false was adopted after a daemon restart.
	Attached bool `json:"attached"`
	// ActivityState is the live session's tracked activity ("active", "idle",
	// "waiting_input", "blocked", "exited"); empty for orphan/foreign trees.
	ActivityState string `json:"activityState,omitempty"`
	// LastActivityAt is when that activity was last observed.
	LastActivityAt time.Time `json:"lastActivityAt"`
}

// ProcessRemnantDTO is a dead session that still holds resident memory (the
// tree is gone but the RSS rollup has not caught up).
type ProcessRemnantDTO struct {
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
	RSSBytes  int64  `json:"rssBytes"`
}

// ProcessTotalsDTO aggregates the inventory's RSS numbers by bucket.
type ProcessTotalsDTO struct {
	SessionsCount    int   `json:"sessionsCount"`
	SessionsRSSBytes int64 `json:"sessionsRssBytes"`
	OrphansCount     int   `json:"orphansCount"`
	OrphansRSSBytes  int64 `json:"orphansRssBytes"`
	ForeignCount     int   `json:"foreignCount"`
	ForeignRSSBytes  int64 `json:"foreignRssBytes"`
	DaemonRSSBytes   int64 `json:"daemonRssBytes"`
	TmuxRSSBytes     int64 `json:"tmuxRssBytes"`
}

// ProcessHostMemoryDTO is the host's memory snapshot as the OS reports it.
type ProcessHostMemoryDTO struct {
	TotalBytes  int64 `json:"totalBytes"`
	UsedBytes   int64 `json:"usedBytes"`
	FreeBytes   int64 `json:"freeBytes"`
	CachedBytes int64 `json:"cachedBytes"`
	// Wired/App/Compressed are darwin-only kinds (0 elsewhere, omitted).
	WiredBytes      int64 `json:"wiredBytes,omitempty"`
	AppBytes        int64 `json:"appBytes,omitempty"`
	CompressedBytes int64 `json:"compressedBytes,omitempty"`
	SwapTotalBytes  int64 `json:"swapTotalBytes"`
	SwapUsedBytes   int64 `json:"swapUsedBytes"`
	SwapFreeBytes   int64 `json:"swapFreeBytes"`
	// SwapMaxBytes is the honest ceiling swap can grow into: free disk on the
	// root volume on darwin; the current swap size on linux.
	SwapMaxBytes int64 `json:"swapMaxBytes"`
	// PressureFreePercent is the OS free-memory estimate; omitted when the
	// platform does not provide one.
	PressureFreePercent *int `json:"pressureFreePercent,omitempty"`
}

// ProcessInventoryResponse is the /system/processes payload: the whole
// per-session tree list plus the rollups the status bar renders.
type ProcessInventoryResponse struct {
	// Disabled is the AO_NO_PROCINVENTORY runtime gate (#93): true means the
	// daemon turned the surface off — clients hide the status bar and stop
	// polling. The rest of the struct is zero when disabled.
	Disabled    bool                   `json:"disabled"`
	GeneratedAt time.Time              `json:"generatedAt"`
	Daemon      ProcessGroupSummaryDTO `json:"daemon"`
	Tmux        ProcessGroupSummaryDTO `json:"tmux"`
	Trees       []ProcessTreeDTO       `json:"trees"`
	Remnants    []ProcessRemnantDTO    `json:"remnants"`
	Totals      ProcessTotalsDTO       `json:"totals"`
	// Host is the host memory snapshot; omitted on windows and older daemons
	// — the status bar hides its host section on nil.
	Host *ProcessHostMemoryDTO `json:"host,omitempty"`
}

// ProcessKillTargetDTO identifies one orphan tree the kill will target.
type ProcessKillTargetDTO struct {
	SessionID  string `json:"sessionId"`
	RootPID    int    `json:"rootPid"`
	RootLstart string `json:"rootLstart"`
}

// ProcessKillRequest is the kill endpoint's body: the orphan trees to remove.
type ProcessKillRequest struct {
	Targets []ProcessKillTargetDTO `json:"targets"`
}

// ProcessKillResultDTO is the per-target outcome of one kill call.
type ProcessKillResultDTO struct {
	SessionID string `json:"sessionId"`
	RootPID   int    `json:"rootPid"`
	// Status is killed | already_gone | skipped | failed. Skipped means the
	// tree drifted out of orphan state between render and confirm — normal,
	// not an error.
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// ProcessKillResponse is the kill endpoint's reply, one result per target.
type ProcessKillResponse struct {
	Results []ProcessKillResultDTO `json:"results"`
}

// OrphanWorkerReapResponse reports the trees the allow decision removed. The
// sessions were re-derived from a fresh snapshot; an empty list means nothing
// provable remained (the normal second click).
type OrphanWorkerReapResponse struct {
	Sessions []string `json:"sessions"`
	Killed   int      `json:"killed"`
}
