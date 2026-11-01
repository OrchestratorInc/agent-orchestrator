package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// kimiSessionLock mirrors the holder record Kimi Code's SessionLockService
// writes to <home>/locks/sessions/<sessionId>.lock while a session is open.
type kimiSessionLock struct {
	PID       int    `json:"pid"`
	Hostname  string `json:"hostname"`
	StartedAt int64  `json:"startedAt"`
}

// kimiSessionLocked reports whether a live Kimi process still holds the native
// session's lock. Kimi publishes <configDir>/locks/sessions/<nativeID>.lock with
// the writer's pid, host, and start time, and appends to wire.jsonl with
// open/append/close, so the lock — not an open descriptor — is the reliable
// open-elsewhere signal. It mirrors Kimi's own liveness test: a lock from
// another host counts as held, otherwise the pid must still be alive.
func kimiSessionLocked(ctx context.Context, configDir, nativeID string) bool {
	if configDir == "" || nativeID == "" {
		return false
	}
	body, err := os.ReadFile(filepath.Join(configDir, "locks", "sessions", nativeID+".lock"))
	if err != nil {
		return false // No lock file (or unreadable) means no live writer.
	}
	var holder kimiSessionLock
	if json.Unmarshal(body, &holder) != nil || holder.PID <= 0 {
		return false
	}
	// Kimi treats a lock held on another machine as occupied; AO cannot verify a
	// remote pid, so it does the same rather than adopt the session underneath it.
	if host, err := os.Hostname(); err == nil && holder.Hostname != "" && holder.Hostname != host {
		return true
	}
	return kimiLockProcessLive(ctx, holder.PID, holder.StartedAt)
}

// kimiLockProcessLive checks the lock's pid against the process now holding it.
// Lock files outlive crashes and pids are reused, so the pid must still be live
// and (when its start time is readable) have started no later than the lock was
// written — the holder was already running when it recorded startedAt.
func kimiLockProcessLive(ctx context.Context, pid int, startedAtMillis int64) bool {
	if !processalive.Alive(pid) {
		return false
	}
	_, started, ok := processStart(ctx, pid)
	if !ok {
		return true // Alive but unidentifiable: treat as a live writer.
	}
	return startedAtMillis <= 0 || !started.After(time.UnixMilli(startedAtMillis))
}
