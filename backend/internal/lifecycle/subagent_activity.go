package lifecycle

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// Hook facts are refreshed by child lifecycle/tool events. If no such event
	// arrives for this long, a terminal-idle signal can safely recover the
	// parent from a lost child stop/start hook.
	subagentFactTTL = 5 * time.Minute
	// Keep delayed-event tombstones useful without allowing Codex's unbounded
	// child history to grow the session row forever.
	maxSubagentChildTombstones = 128
	maxSubagentPendingSpawns   = 128
)

// These are native hook facts, not a second display status. A stopped child id
// remains to guard against delayed hooks; Claude snapshots can prune it.
type subagentChildFact struct {
	Running bool  `json:"running"`
	At      int64 `json:"at"`
}

type subagentActivityFacts struct {
	LaunchID        string                       `json:"launchId"`
	NativeSessionID string                       `json:"nativeSessionId,omitempty"`
	ParentState     domain.ActivityState         `json:"parentState"`
	ParentAt        int64                        `json:"parentAt"`
	LastChildAt     int64                        `json:"lastChildAt,omitempty"`
	SnapshotAt      int64                        `json:"snapshotAt,omitempty"`
	Children        map[string]subagentChildFact `json:"children,omitempty"`
	PendingSpawns   map[string]int64             `json:"pendingSpawns,omitempty"`
}

// reduceSubagentActivity combines the parent turn with native child
// lifetimes before the existing permission/tool precedence rule runs.
func reduceSubagentActivity(
	rec domain.SessionRecord, s ports.ActivitySignal, now time.Time,
) (ports.ActivitySignal, string, error) {
	var stored string
	switch rec.Harness {
	case domain.HarnessClaudeCode:
		stored = rec.Metadata.ClaudeActivityFacts
	case domain.HarnessCodex:
		stored = rec.Metadata.CodexActivityFacts
	default:
		return s, "", nil
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI ||
		s.LaunchID == "" || (!s.Valid && s.SubagentID == "") {
		return s, stored, nil
	}
	if stored == "" && s.SubagentID == "" &&
		(s.Event != "stop" || s.RunningSubagentIDs == nil) {
		return s, "", nil
	}
	var facts subagentActivityFacts
	if stored != "" {
		if err := json.Unmarshal([]byte(stored), &facts); err != nil {
			// Hook facts are derived from the provider stream. A damaged blob
			// cannot be allowed to reject every subsequent lifecycle signal.
			facts = subagentActivityFacts{}
		}
	}
	if facts.LaunchID != s.LaunchID ||
		(s.AgentSessionID != "" && facts.NativeSessionID != "" && facts.NativeSessionID != s.AgentSessionID) {
		facts = subagentActivityFacts{LaunchID: s.LaunchID, ParentState: rec.Activity.State}
	}
	if s.AgentSessionID != "" {
		facts.NativeSessionID = s.AgentSessionID
	}
	if facts.Children == nil {
		facts.Children = make(map[string]subagentChildFact)
	}
	at := timeOr(s.Timestamp, now).UnixNano()
	pruneSubagentFacts(&facts, at)
	newerSnapshot := rec.Harness == domain.HarnessClaudeCode && (s.Event == "stop" || s.Event == "subagent-stop") &&
		s.RunningSubagentIDs != nil && at > facts.SnapshotAt
	if s.Event == "terminal-idle" {
		expireRunningChildren(&facts, at)
		if len(facts.PendingSpawns) > 0 {
			s.Valid = false
			return s, stored, nil
		}
		for _, child := range facts.Children {
			if child.Running {
				// The parent's idle composer does not prove its background child
				// stopped. Suppress this observer signal without rewriting facts.
				s.Valid = false
				return s, stored, nil
			}
		}
	}
	if s.SubagentID != "" {
		if at > facts.LastChildAt {
			facts.LastChildAt = at
		}
		if rec.Harness == domain.HarnessCodex && s.Event == "subagent-spawn" {
			if facts.PendingSpawns == nil {
				facts.PendingSpawns = make(map[string]int64)
			}
			facts.PendingSpawns[s.SubagentID] = at
		}
		child, known := facts.Children[s.SubagentID]
		switch s.Event {
		case "subagent-stop":
			if rec.Harness == domain.HarnessCodex && !known {
				consumePendingSpawn(&facts)
			}
			if (!known && at <= facts.SnapshotAt) || (known && (!child.Running || at < child.At)) {
				if !newerSnapshot {
					s.Valid = false
					return s, stored, nil
				}
			} else {
				facts.Children[s.SubagentID] = subagentChildFact{At: at}
			}
		case "subagent-start", "pre-tool-use", "post-tool-use", "post-tool-use-failure", "permission-request", "user-prompt-submit":
			if !known && at <= facts.SnapshotAt {
				// A later parent snapshot already proved this child was absent.
				s.Valid = false
				return s, stored, nil
			} else if !known || child.Running ||
				(rec.Harness == domain.HarnessCodex && s.Event == "user-prompt-submit" && at > child.At) {
				if rec.Harness == domain.HarnessCodex && s.Event == "subagent-start" && (!known || !child.Running) {
					consumePendingSpawn(&facts)
				}
				// Codex's SubagentStop ends a child turn, not its reusable thread.
				// A later child prompt can start another turn with the same agent_id.
				if at > child.At {
					facts.Children[s.SubagentID] = subagentChildFact{Running: true, At: at}
				}
			} else {
				// A delayed child event cannot revive a stopped turn.
				s.Valid = false
				return s, stored, nil
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
		if s.Event != "notification" || (!childRunning && facts.ParentAt >= facts.LastChildAt) {
			facts.ParentState = s.State
			facts.ParentAt = at
		}
	}
	if newerSnapshot {
		facts.SnapshotAt = at
		running := make(map[string]bool, len(*s.RunningSubagentIDs))
		for _, id := range *s.RunningSubagentIDs {
			running[id] = true
			if at > facts.LastChildAt {
				facts.LastChildAt = at
			}
			child, known := facts.Children[id]
			if !known || (child.Running && at > child.At) {
				facts.Children[id] = subagentChildFact{Running: true, At: at}
			}
		}
		for id, child := range facts.Children {
			if child.Running && !running[id] && child.At <= at {
				facts.Children[id] = subagentChildFact{At: at}
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
	pruneSubagentFacts(&facts, at)
	state := facts.ParentState
	if state != domain.ActivityExited && !state.NeedsInput() {
		if len(facts.PendingSpawns) > 0 {
			state = domain.ActivityActive
		}
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
	childRunning := hasRunningChild(facts.Children)
	if s.State.NeedsInput() &&
		(s.SubagentID != "" || s.Event != "notification" || childRunning || facts.ParentAt >= facts.LastChildAt) {
		state = s.State
	}
	if s.SubagentID != "" && isToolUseEvent(s.Event) {
		state = s.State
	}
	if (s.Event == "subagent-start" || s.Event == "subagent-stop") && rec.Activity.State.IsSticky() {
		s.Valid = false
	} else if s.Valid || s.Event == "subagent-start" || s.Event == "subagent-stop" || s.Event == "subagent-spawn" {
		s.Valid = true
		s.State = state
	}
	encoded, err := json.Marshal(facts)
	if err != nil {
		return s, "", fmt.Errorf("encode subagent activity facts: %w", err)
	}
	return s, string(encoded), nil
}

func hasRunningChild(children map[string]subagentChildFact) bool {
	for _, child := range children {
		if child.Running {
			return true
		}
	}
	return false
}

func expireRunningChildren(facts *subagentActivityFacts, at int64) {
	ttl := subagentFactTTL.Nanoseconds()
	for id, child := range facts.Children {
		if !child.Running || (child.At > 0 && (at < child.At || at-child.At < ttl)) {
			continue
		}
		facts.Children[id] = subagentChildFact{At: at}
	}
}

func pruneSubagentFacts(facts *subagentActivityFacts, at int64) {
	ttl := subagentFactTTL.Nanoseconds()
	for id, spawnedAt := range facts.PendingSpawns {
		if spawnedAt <= 0 || (at >= spawnedAt && at-spawnedAt >= ttl) {
			delete(facts.PendingSpawns, id)
		}
	}
	if len(facts.PendingSpawns) > maxSubagentPendingSpawns {
		ids := make([]string, 0, len(facts.PendingSpawns))
		for id := range facts.PendingSpawns {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			return facts.PendingSpawns[ids[i]] < facts.PendingSpawns[ids[j]]
		})
		for _, id := range ids[:len(ids)-maxSubagentPendingSpawns] {
			delete(facts.PendingSpawns, id)
		}
	}

	tombstones := make([]string, 0, len(facts.Children))
	for id, child := range facts.Children {
		if child.Running {
			continue
		}
		if child.At <= 0 || (at >= child.At && at-child.At >= ttl) {
			delete(facts.Children, id)
			continue
		}
		tombstones = append(tombstones, id)
	}
	if len(tombstones) <= maxSubagentChildTombstones {
		return
	}
	sort.Slice(tombstones, func(i, j int) bool {
		return facts.Children[tombstones[i]].At < facts.Children[tombstones[j]].At
	})
	for _, id := range tombstones[:len(tombstones)-maxSubagentChildTombstones] {
		delete(facts.Children, id)
	}
}

// A successful Codex spawn tool result can precede the child's start hook.
// Replace one provisional spawn with the native child identity when it arrives.
func consumePendingSpawn(facts *subagentActivityFacts) {
	var oldestID string
	var oldestAt int64
	for id, at := range facts.PendingSpawns {
		if oldestID == "" || at < oldestAt {
			oldestID, oldestAt = id, at
		}
	}
	if oldestID != "" {
		delete(facts.PendingSpawns, oldestID)
	}
}
