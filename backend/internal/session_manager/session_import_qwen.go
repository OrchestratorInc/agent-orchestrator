package sessionmanager

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// Qwen 0.26.4 publishes both a live-session registry and runtime sidecars.
// Both must name this native id, a different workspace, and a live Qwen/node
// process with a validated start identity. Stale files alone are not ownership.
func qwenSessionLive(ctx context.Context, configDir, nativeID, ownWorkspace string) bool {
	return qwenSessionLiveWithProbe(ctx, configDir, nativeID, ownWorkspace, qwenProcessLive)
}

func qwenSessionLiveWithProbe(ctx context.Context, configDir, nativeID, ownWorkspace string, live func(context.Context, int, int64, string) bool) bool {
	if configDir == "" || nativeID == "" || filepath.Base(nativeID) != nativeID {
		return false
	}
	root, err := os.OpenRoot(configDir)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	entries, _ := fs.ReadDir(root.FS(), "sessions")
	for _, entry := range entries {
		if ctx.Err() != nil {
			return false
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		body, err := root.ReadFile(filepath.Join("sessions", entry.Name()))
		var record struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			CWD       string `json:"cwd"`
			StartedAt int64  `json:"startedAt"`
			ProcStart string `json:"procStart"`
		}
		if err != nil || json.Unmarshal(body, &record) != nil || record.SessionID != nativeID || record.PID <= 0 {
			continue
		}
		if ownWorkspace != "" && pathWithin(ownWorkspace, record.CWD) {
			continue
		}
		if live(ctx, record.PID, record.StartedAt, record.ProcStart) {
			return true
		}
	}
	projects, _ := fs.ReadDir(root.FS(), "projects")
	host, hostErr := os.Hostname()
	for _, project := range projects {
		if ctx.Err() != nil {
			return false
		}
		if !project.IsDir() {
			continue
		}
		body, err := root.ReadFile(filepath.Join("projects", project.Name(), "chats", nativeID+".runtime.json"))
		var record struct {
			Schema    int     `json:"schema_version"`
			PID       int     `json:"pid"`
			SessionID string  `json:"session_id"`
			CWD       string  `json:"work_dir"`
			Hostname  string  `json:"hostname"`
			StartedAt float64 `json:"started_at"`
		}
		if err != nil || json.Unmarshal(body, &record) != nil || record.Schema != 1 || record.SessionID != nativeID || record.PID <= 0 || hostErr != nil || record.Hostname != host {
			continue
		}
		if ownWorkspace != "" && pathWithin(ownWorkspace, record.CWD) {
			continue
		}
		if live(ctx, record.PID, int64(record.StartedAt*1000), "") {
			return true
		}
	}
	return false
}

func qwenProcessLive(ctx context.Context, pid int, registeredAtMillis int64, token string) bool {
	if !processalive.Alive(pid) {
		return false
	}
	name, started, ok := processStart(ctx, pid)
	if !ok {
		return false
	}
	current := ""
	if token != "" && runtime.GOOS == "linux" {
		// Qwen's readProcStartToken: boot id + field 22 of /proc/<pid>/stat.
		boot, bootErr := os.ReadFile("/proc/sys/kernel/random/boot_id")
		stat, statErr := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if end := strings.LastIndexByte(string(stat), ')'); bootErr == nil && statErr == nil && end >= 0 {
			fields := strings.Fields(string(stat)[end+1:])
			if len(fields) > 19 {
				current = strings.TrimSpace(string(boot)) + ":" + fields[19]
			}
		}
	}
	return qwenProcessIdentityMatches(name, started, registeredAtMillis, token, current)
}

func qwenProcessIdentityMatches(name string, started time.Time, registeredAtMillis int64, token, current string) bool {
	name = strings.ToLower(name)
	if !strings.Contains(name, "qwen") && !strings.Contains(name, "node") {
		return false
	}
	if token != "" && token != current {
		return false
	}
	return registeredAtMillis > 0 && !started.After(time.UnixMilli(registeredAtMillis))
}
