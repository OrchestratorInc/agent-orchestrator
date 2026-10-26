package worker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// claudeConversationDir holds one record per Claude Code process that ran a
// hook in this sandbox, named by its pid.
const claudeConversationDir = "claude-conversations"

// ClaudeConversation is the conversation a Claude Code process is in, as its
// own hooks report it. /clear, /resume and a conversation switch inside the
// TUI start writing another transcript without restarting the process; every
// hook after that carries the new session id.
type ClaudeConversation struct {
	// First is the conversation the process was in when it first ran a hook.
	First string `json:"first"`
	// Current is the conversation of its latest hook, and Since when that
	// hook ran the first one in it.
	Current string `json:"current"`
	Since   int64  `json:"since"`
	// Started is when the process first ran a hook.
	Started int64 `json:"started"`
	// ProcStart is the process's start time in clock ticks since boot
	// (/proc/<pid>/stat), where Linux reports it. A later process that reuses
	// the pid has another.
	ProcStart string `json:"procStart,omitempty"`
}

// MaxClaudeConversations bounds the records kept.
const MaxClaudeConversations = 64

// RecordClaudeConversation notes the conversation a hook reports for the
// Claude Code process pid (the hook's CLAUDE_PID). It writes only when the
// conversation changes.
func RecordClaudeConversation(dataDir, pid, sessionID string, at time.Time) error {
	sessionID = strings.TrimSpace(sessionID)
	if dataDir == "" || sessionID == "" || len(sessionID) > 256 || strings.ContainsAny(sessionID, "/\\") {
		return os.ErrInvalid
	}
	if n, err := strconv.Atoi(pid); err != nil || n <= 0 {
		return os.ErrInvalid
	}
	dir := filepath.Join(dataDir, claudeConversationDir)
	path := filepath.Join(dir, pid+".json")
	record := ClaudeConversation{
		First: sessionID, Current: sessionID, Since: at.UnixNano(), Started: at.UnixNano(),
		ProcStart: processStart(pid),
	}
	created := true
	if payload, err := os.ReadFile(path); err == nil {
		var previous ClaudeConversation
		if json.Unmarshal(payload, &previous) == nil && previous.First != "" &&
			(previous.ProcStart == "" || previous.ProcStart == record.ProcStart) {
			if previous.Current == sessionID {
				return nil
			}
			record.First, record.Started, created = previous.First, previous.Started, false
		}
	}
	if created {
		// A new process: drop the records of processes that have exited, so the
		// directory holds about one record per live Claude process.
		PruneClaudeConversations(dataDir, pid, 0)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".conversation-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err := temp.Write(payload); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// ReadClaudeConversations returns every recorded Claude Code process's
// conversation, by pid.
func ReadClaudeConversations(dataDir string) map[string]ClaudeConversation {
	entries, err := os.ReadDir(filepath.Join(dataDir, claudeConversationDir))
	if err != nil {
		return nil
	}
	records := make(map[string]ClaudeConversation, len(entries))
	for _, entry := range entries {
		pid, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || strings.HasPrefix(pid, ".") {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(dataDir, claudeConversationDir, entry.Name()))
		if err != nil {
			continue
		}
		var record ClaudeConversation
		if json.Unmarshal(payload, &record) == nil && record.First != "" && record.Current != "" {
			records[pid] = record
		}
	}
	return records
}

// PruneClaudeConversations removes the records of Claude processes that have
// exited and, when limit is positive and more remain, the oldest started
// beyond limit. The record of keep (the terminal's process) is never removed.
func PruneClaudeConversations(dataDir, keep string, limit int) {
	dir := filepath.Join(dataDir, claudeConversationDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type live struct {
		pid     string
		started int64
	}
	var remaining []live
	kept := 0
	for _, entry := range entries {
		pid, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || strings.HasPrefix(pid, ".") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var record ClaudeConversation
		if payload, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(payload, &record)
		}
		switch {
		case pid == keep:
			kept = 1
		case !claudeProcessRunning(pid, record.ProcStart):
			_ = os.Remove(path)
		default:
			remaining = append(remaining, live{pid: pid, started: record.Started})
		}
	}
	excess := len(remaining) + kept - limit
	if limit <= 0 || excess <= 0 {
		return
	}
	sort.Slice(remaining, func(i, j int) bool { return remaining[i].started < remaining[j].started })
	for _, record := range remaining[:min(excess, len(remaining))] {
		_ = os.Remove(filepath.Join(dir, record.pid+".json"))
	}
}

// claudeProcessRunning is a variable so tests can fake process liveness.
var claudeProcessRunning = processRunning

// processRunning reports whether pid still runs and, when procStart is known,
// is the process that recorded it rather than a later one reusing the pid.
func processRunning(pid, procStart string) bool {
	n, err := strconv.Atoi(pid)
	if err != nil || n <= 0 {
		return false
	}
	if start := processStart(pid); start != "" {
		return procStart == "" || start == procStart
	}
	if _, err := os.Stat("/proc/self/stat"); err == nil {
		// /proc is mounted and has no entry for pid.
		return false
	}
	err = syscall.Kill(n, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// processStart is pid's start time from /proc/<pid>/stat (field 22), or ""
// where /proc does not report it.
func processStart(pid string) string {
	payload, err := os.ReadFile(filepath.Join("/proc", pid, "stat"))
	if err != nil {
		return ""
	}
	return statProcessStart(payload)
}

func statProcessStart(payload []byte) string {
	// The command name (field 2) is parenthesized and may hold spaces.
	end := strings.LastIndexByte(string(payload), ')')
	if end < 0 {
		return ""
	}
	fields := strings.Fields(string(payload[end+1:]))
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}
