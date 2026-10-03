package lifecycle

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// These are native hook facts, not a second display status. A stopped child id
// remains until an authoritative snapshot can guard against delayed hooks.
type claudeChildFact struct {
	Running bool  `json:"running"`
	At      int64 `json:"at"`
}

type claudeActivityFacts struct {
	LaunchID        string                     `json:"launchId"`
	NativeSessionID string                     `json:"nativeSessionId,omitempty"`
	ParentState     domain.ActivityState       `json:"parentState"`
	ParentAt        int64                      `json:"parentAt"`
	SnapshotAt      int64                      `json:"snapshotAt,omitempty"`
	Children        map[string]claudeChildFact `json:"children,omitempty"`
}

// reduceClaudeSubagentActivity combines the parent turn with native child
// lifetimes before the existing permission/tool precedence rule runs.
func reduceClaudeSubagentActivity(
	rec domain.SessionRecord, s ports.ActivitySignal, now time.Time,
) (ports.ActivitySignal, string, error) {
	if rec.Harness != domain.HarnessClaudeCode || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI ||
		s.LaunchID == "" || (!s.Valid && s.SubagentID == "") {
		return s, rec.Metadata.ClaudeActivityFacts, nil
	}
	if rec.Metadata.ClaudeActivityFacts == "" && s.SubagentID == "" &&
		(s.Event != "stop" || s.RunningSubagentIDs == nil) {
		return s, "", nil
	}
	var facts claudeActivityFacts
	if rec.Metadata.ClaudeActivityFacts != "" {
		if err := json.Unmarshal([]byte(rec.Metadata.ClaudeActivityFacts), &facts); err != nil {
			// Hook facts are derived from the provider stream. A damaged blob
			// cannot be allowed to reject every subsequent lifecycle signal.
			facts = claudeActivityFacts{}
		}
	}
	if facts.LaunchID != s.LaunchID ||
		(s.AgentSessionID != "" && facts.NativeSessionID != "" && facts.NativeSessionID != s.AgentSessionID) {
		facts = claudeActivityFacts{LaunchID: s.LaunchID, ParentState: rec.Activity.State}
	}
	if s.AgentSessionID != "" {
		facts.NativeSessionID = s.AgentSessionID
	}
	if facts.Children == nil {
		facts.Children = make(map[string]claudeChildFact)
	}
	at := timeOr(s.Timestamp, now).UnixNano()
	newerSnapshot := (s.Event == "stop" || s.Event == "subagent-stop") &&
		s.RunningSubagentIDs != nil && at > facts.SnapshotAt
	if s.Event == "terminal-idle" {
		for _, child := range facts.Children {
			if child.Running {
				// The parent's idle composer does not prove its background child
				// stopped. Suppress this observer signal without rewriting facts.
				s.Valid = false
				return s, rec.Metadata.ClaudeActivityFacts, nil
			}
		}
	}
	if s.SubagentID != "" {
		child, known := facts.Children[s.SubagentID]
		switch s.Event {
		case "subagent-stop":
			if (!known && at <= facts.SnapshotAt) || (known && (!child.Running || at < child.At)) {
				if !newerSnapshot {
					s.Valid = false
					return s, rec.Metadata.ClaudeActivityFacts, nil
				}
			} else {
				facts.Children[s.SubagentID] = claudeChildFact{At: at}
			}
		case "subagent-start", "pre-tool-use", "post-tool-use", "post-tool-use-failure", "permission-request":
			if !known && at <= facts.SnapshotAt {
				// A later parent snapshot already proved this child was absent.
				s.Valid = false
				return s, rec.Metadata.ClaudeActivityFacts, nil
			} else if !known || child.Running {
				if at > child.At {
					facts.Children[s.SubagentID] = claudeChildFact{Running: true, At: at}
				}
			} else {
				// SubagentStop is terminal for this native child id.
				s.Valid = false
				return s, rec.Metadata.ClaudeActivityFacts, nil
			}
		}
	} else if s.Valid && at >= facts.ParentAt {
		// Claude's permission notification has no agent_id. When children are
		// running it can describe a child's dialog, not the parent turn.
		// Keep the displayed block, but leave the parent state for the child's
		// correlated post-tool-use to reveal when the dialog is resolved.
		childRunning := false
		for _, child := range facts.Children {
			if child.Running {
				childRunning = true
				break
			}
		}
		if s.Event != "notification" || !childRunning {
			facts.ParentState = s.State
			facts.ParentAt = at
		}
	}
	if newerSnapshot {
		facts.SnapshotAt = at
		running := make(map[string]bool, len(*s.RunningSubagentIDs))
		for _, id := range *s.RunningSubagentIDs {
			running[id] = true
			child, known := facts.Children[id]
			if !known || (child.Running && at > child.At) {
				facts.Children[id] = claudeChildFact{Running: true, At: at}
			}
		}
		for id, child := range facts.Children {
			if child.Running && !running[id] && child.At <= at {
				facts.Children[id] = claudeChildFact{At: at}
			}
		}
		// The snapshot watermark rejects delayed starts for removed ids, so
		// tombstones at or before it no longer need to occupy the session row.
		for id, child := range facts.Children {
			if !child.Running && child.At <= facts.SnapshotAt {
				delete(facts.Children, id)
			}
		}
	}
	state := facts.ParentState
	if state != domain.ActivityExited && !state.NeedsInput() {
		for _, child := range facts.Children {
			if child.Running {
				state = domain.ActivityActive
				break
			}
		}
	}
	// A child permission hook must still enter blocked, even while its parent
	// is active. Correlated tool posts retain their raw active signal so the
	// existing precedence rule can release an approved dialog.
	if s.State.NeedsInput() {
		state = s.State
	}
	if s.SubagentID != "" && isToolUseEvent(s.Event) {
		state = s.State
	}
	if (s.Event == "subagent-start" || s.Event == "subagent-stop") && rec.Activity.State.IsSticky() {
		s.Valid = false
	} else if s.Valid || s.Event == "subagent-start" || s.Event == "subagent-stop" {
		s.Valid = true
		s.State = state
	}
	encoded, err := json.Marshal(facts)
	if err != nil {
		return s, "", fmt.Errorf("encode Claude activity facts: %w", err)
	}
	return s, string(encoded), nil
}
