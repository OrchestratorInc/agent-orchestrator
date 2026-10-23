package chat

import (
	"bufio"
	"crypto/md5" //nolint:gosec // Cursor names its chat folders by the MD5 of the workspace path.
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// handoffHistoryFileLimit caps how many of one agent's files the note names.
const handoffHistoryFileLimit = 3

// handoffHistoryFiles points the next agent at the own history of each agent
// whose answers AO did not record in this session. These agents report no
// turns through hooks, or report the request only. Every one files its history
// by workspace except agy, which is found only for the outgoing session.
func (s *Service) handoffHistoryFiles(record domain.SessionRecord, turns []domain.HandoffTurn) []domain.HandoffHistoryFile {
	workspace := strings.TrimSpace(record.Metadata.WorkspacePath)
	home, _ := os.UserHomeDir()
	if workspace == "" || home == "" {
		return nil
	}
	answered, asked := map[domain.AgentHarness]bool{}, map[domain.AgentHarness]bool{}
	for _, turn := range turns {
		asked[turn.Harness] = true
		if strings.TrimSpace(turn.AgentResponse) != "" {
			answered[turn.Harness] = true
		}
	}
	cursorKey := md5.Sum([]byte(workspace)) //nolint:gosec // a folder name, not a security use.
	sources := []struct {
		harness domain.AgentHarness
		paths   []string
		read    string
	}{
		{domain.HarnessKimi, kimiSessionWires(filepath.Join(s.dataDir, "kimi"), workspace),
			"JSON lines: the messages are the `agent.message.appended` lines, with `message.message.role` user or assistant."},
		{domain.HarnessDroid, newestFiles(filepath.Join(home, ".factory", "sessions", strings.ReplaceAll(workspace, string(filepath.Separator), "-"), "*.jsonl")),
			"JSON lines: the messages are the `\"type\":\"message\"` lines, with `message.role` user or assistant."},
		{domain.HarnessCursor, newestFiles(filepath.Join(home, ".cursor", "chats", hex.EncodeToString(cursorKey[:]), "*", "store.db")),
			"SQLite: `sqlite3 <file> \"select data from blobs where json_valid(data) and json_extract(data, '$.role') in ('user', 'assistant') order by rowid\"`."},
		{domain.HarnessAgy, agyConversation(home, record),
			"Binary: `strings <file>` shows the messages."},
	}
	var files []domain.HandoffHistoryFile
	for _, source := range sources {
		if answered[source.harness] {
			continue
		}
		for _, path := range source.paths {
			// A chat untouched since the session began is another session's.
			if info, err := os.Stat(path); err != nil || info.ModTime().Before(record.CreatedAt) {
				continue
			}
			files = append(files, domain.HandoffHistoryFile{Harness: source.harness, Path: path, Read: source.read, RequestsOnly: asked[source.harness]})
		}
	}
	return files
}

// kimiSessionWires reads Kimi Code's session index, which maps each working
// directory to its session folders and marks deleted sessions.
func kimiSessionWires(kimiHome, workspace string) []string {
	index, err := os.Open(filepath.Join(kimiHome, "session_index.jsonl")) //nolint:gosec // AO's own Kimi home.
	if err != nil {
		return nil
	}
	defer func() { _ = index.Close() }()
	dirs := map[string]string{}
	var order []string
	scanner := bufio.NewScanner(index)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var entry struct {
			SessionID  string `json:"sessionId"`
			SessionDir string `json:"sessionDir"`
			WorkDir    string `json:"workDir"`
			Deleted    bool   `json:"deleted"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry.SessionID == "" {
			continue
		}
		if entry.Deleted {
			delete(dirs, entry.SessionID)
		} else if filepath.Clean(entry.WorkDir) == filepath.Clean(workspace) && entry.SessionDir != "" {
			dirs[entry.SessionID] = entry.SessionDir
			order = append(order, entry.SessionID)
		}
	}
	var paths []string
	for index := len(order) - 1; index >= 0 && len(paths) < handoffHistoryFileLimit; index-- {
		dir, ok := dirs[order[index]]
		delete(dirs, order[index])
		if wire := filepath.Join(dir, "agents", "main", "wire.jsonl"); ok && regularFile(wire) {
			paths = append(paths, wire)
		}
	}
	return paths
}

// agyConversation is the outgoing agy session's conversation store.
func agyConversation(home string, record domain.SessionRecord) []string {
	id := strings.TrimSpace(record.Metadata.AgentSessionID)
	if record.Harness != domain.HarnessAgy || id == "" || strings.ContainsAny(id, `/\`) {
		return nil
	}
	gemini := strings.TrimSpace(os.Getenv("GEMINI_HOME"))
	if gemini == "" {
		gemini = filepath.Join(home, ".gemini")
	}
	if path := filepath.Join(gemini, "antigravity-cli", "conversations", id+".db"); regularFile(path) {
		return []string{path}
	}
	return nil
}

// newestFiles returns the newest files matching pattern, up to the limit.
func newestFiles(pattern string) []string {
	matches, _ := filepath.Glob(pattern)
	modified := map[string]int64{}
	var paths []string
	for _, path := range matches {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			modified[path] = info.ModTime().UnixNano()
			paths = append(paths, path)
		}
	}
	sort.Slice(paths, func(i, j int) bool { return modified[paths[i]] > modified[paths[j]] })
	return paths[:min(len(paths), handoffHistoryFileLimit)]
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
