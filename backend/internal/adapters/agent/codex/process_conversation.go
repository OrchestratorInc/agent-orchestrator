package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var _ ports.AgentProcessConversationReader = (*Plugin)(nil)

var defaultProcessOpenFiles = process.GroupOpenFiles

var processOpenFiles = defaultProcessOpenFiles

var openRolloutName = regexp.MustCompile(`^rollout-.+-([0-9a-fA-F-]{36})(?:_\d+)?\.jsonl$`)

// ProcessConversationID names the thread a running Codex process is in: of the
// rollouts its process group holds open, the most recently written one that is
// not a subagent's.
func (p *Plugin) ProcessConversationID(ctx context.Context, pid int) (string, bool, error) {
	paths, err := processOpenFiles(ctx, pid)
	if err != nil {
		return "", false, err
	}
	var id string
	var newest time.Time
	for _, path := range paths {
		match := openRolloutName.FindStringSubmatch(filepath.Base(path))
		if match == nil {
			continue
		}
		thread, ok := canonicalCodexThreadID(match[1])
		if !ok {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || (id != "" && !info.ModTime().After(newest)) || subagentRollout(path) {
			continue
		}
		id, newest = thread, info.ModTime()
	}
	return id, id != "", nil
}

func subagentRollout(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }() // read-only: a close error loses nothing
	var first struct {
		Type    string `json:"type"`
		Payload struct {
			Source json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if json.NewDecoder(f).Decode(&first) != nil || first.Type != "session_meta" {
		return false
	}
	var source struct {
		Subagent json.RawMessage `json:"subagent"`
	}
	return json.Unmarshal(first.Payload.Source, &source) == nil && source.Subagent != nil
}
