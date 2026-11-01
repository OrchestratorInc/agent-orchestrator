package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// qwenSessionLive reports positive evidence that a live Qwen process holds this
// conversation. Qwen writes one JSON record per running session under
// <configDir>/sessions/<pid>.json carrying its pid, startedAt, sessionId and cwd.
// A record whose cwd is inside this session's own AO workspace is an earlier AO
// attempt, not another owner. Records outlive crashes and PIDs are reused, so the
// pid must still be a live Qwen/node process that started no later than the
// session was registered (qwenProcessLive), to avoid a stale-PID false positive.
func qwenSessionLive(ctx context.Context, configDir, nativeID, ownWorkspace string) bool {
	dir := filepath.Join(configDir, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		var live struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			CWD       string `json:"cwd"`
			StartedAt int64  `json:"startedAt"`
		}
		if err != nil || json.Unmarshal(body, &live) != nil || live.SessionID != nativeID || live.PID <= 0 {
			continue
		}
		if ownWorkspace != "" && pathWithin(ownWorkspace, live.CWD) {
			continue
		}
		if qwenProcessLive(ctx, live.PID, live.StartedAt) {
			return true
		}
	}
	return false
}

// qwenProcessLive checks a Qwen session record against the process now holding
// its PID: it must still be a live Qwen (or its node host) process that started
// no later than the record claims, so a reused PID is not read as a live owner.
func qwenProcessLive(ctx context.Context, pid int, registeredAtMillis int64) bool {
	if !processalive.Alive(pid) {
		return false
	}
	name, started, ok := processStart(ctx, pid)
	name = strings.ToLower(name)
	if !ok || !strings.Contains(name, "qwen") && !strings.Contains(name, "node") {
		return false
	}
	return registeredAtMillis <= 0 || !started.After(time.UnixMilli(registeredAtMillis))
}
