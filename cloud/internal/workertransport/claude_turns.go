package workertransport

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
)

// Claude Code records an Esc interrupt as a user message holding only one of
// these markers (verified against Claude Code 2.1.287 and 2.1.302). No hook
// fires: not Stop, not StopFailure, not PostToolUse for the tool it cut short.
var claudeInterruptMarkers = []string{
	"[Request interrupted by user]",
	"[Request interrupted by user for tool use]",
}

// newClaudeTranscriptTail follows the terminal's Claude Code conversation
// transcript under a CLAUDE_CONFIG_DIR: projects/<project>/<session id>.jsonl.
// With the conversation's id known (the terminal launches Claude with
// --session-id or --resume), only that transcript is the terminal's: a
// headless claude the agent runs from a tool shares the config directory, and
// its transcript is often the newest. /clear, /resume or a conversation switch
// inside the TUI moves the terminal's Claude to another transcript; its hooks
// record the new conversation in dataDir (worker.RecordClaudeConversation)
// and the tail follows it from then on. Without a known id, the newest
// conversation transcript is the terminal's. Subagent transcripts live a
// level deeper (or, in older releases, as agent-*.jsonl beside the
// conversation) and are never the terminal's own turn. Reviewers run with
// their own config directory, and Chat never runs beside the terminal agent.
// Call it before the agent starts.
func newClaudeTranscriptTail(configDir, conversationID, dataDir string, started time.Time) *transcriptTail {
	conversation := &claudeConversation{dataDir: dataDir, current: strings.TrimSpace(conversationID)}
	owns := func(_, name string) (bool, bool) {
		if conversation.current != "" {
			return strings.TrimSuffix(name, ".jsonl") == conversation.current, true
		}
		return !strings.HasPrefix(name, "agent-"), true
	}
	tail := newTranscriptTail(filepath.Join(configDir, "projects"), 2, started, owns, claudeTurnRecord)
	if conversation.current != "" && dataDir != "" {
		conversation.launch = conversation.current
		tail.refresh = func() {
			if since, changed := conversation.refresh(); changed {
				tail.retarget(since)
			}
		}
	}
	return tail
}

// claudeConversation tracks which conversation the terminal's Claude Code
// process is in.
type claudeConversation struct {
	dataDir string
	// launch is the conversation the terminal launched into, current the one
	// it is in now.
	launch, current string
}

// refresh reads the conversations hooks recorded. The terminal's process is
// the one whose first conversation was the launch's: the latest started, as
// an earlier Claude resumed into it may have left a record. A headless claude
// starts its own conversation. It reports whether the terminal's conversation
// changed, and since when.
func (c *claudeConversation) refresh() (time.Time, bool) {
	var record worker.ClaudeConversation
	terminalPID := ""
	records := worker.ReadClaudeConversations(c.dataDir)
	for pid, candidate := range records {
		if candidate.First == c.launch && (terminalPID == "" || candidate.Started > record.Started) {
			record, terminalPID = candidate, pid
		}
	}
	if len(records) > worker.MaxClaudeConversations {
		// The hook helper drops exited processes' records; more than this
		// many means pids reused or a crowd of headless runs. Keep the
		// terminal's.
		worker.PruneClaudeConversations(c.dataDir, terminalPID, worker.MaxClaudeConversations)
	}
	if terminalPID == "" || record.Current == c.current {
		return time.Time{}, false
	}
	c.current = record.Current
	return time.Unix(0, record.Since), true
}

// claudeTerminalConversationID is the conversation a terminal Claude Code
// command resumes, or the id it starts a new one with.
func claudeTerminalConversationID(command workerexec.Command) string {
	if id := strings.TrimSpace(command.ConversationID); id != "" {
		return id
	}
	for i, arg := range command.Args {
		if arg == "--" {
			break
		}
		if arg == "--session-id" && i+1 < len(command.Args) {
			return strings.TrimSpace(command.Args[i+1])
		}
		if id, ok := strings.CutPrefix(arg, "--session-id="); ok {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

// claudeTurnRecord classifies a transcript record. An interrupt marker on the
// main conversation is a turn end, dated by the record. Any other main
// conversation user record (a new prompt, a tool result) means the agent is
// working again, so an interrupt before it is history.
func claudeTurnRecord(line []byte) (turnEnd, turnRecordKind) {
	// Most records are assistant output, progress and bookkeeping; skip them
	// before decoding.
	if !bytes.Contains(line, []byte(`"user"`)) {
		return turnEnd{}, turnRecordOther
	}
	var record struct {
		Type        string `json:"type"`
		IsSidechain bool   `json:"isSidechain"`
		Timestamp   string `json:"timestamp"`
		Message     struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "user" || record.IsSidechain {
		return turnEnd{}, turnRecordOther
	}
	if !claudeInterruptMarker(record.Message.Content) {
		return turnEnd{}, turnRecordStart
	}
	at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		at = time.Now()
	}
	return turnEnd{at: at, interrupted: true}, turnRecordEnd
}

// claudeInterruptMarker reports whether a user message's content is exactly
// an interrupt marker. Claude Code writes the marker as a one-element text
// block array; a typed prompt is recorded as a plain string, so a prompt that
// is exactly the marker text is not an interrupt. The marker's
// interruptedMessageId cannot be required: Claude sets it only when Esc
// cancelled a known API message (2.1.302 omits it before the first response).
func claudeInterruptMarker(content json.RawMessage) bool {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil || len(blocks) != 1 || blocks[0].Type != "text" {
		return false
	}
	for _, marker := range claudeInterruptMarkers {
		if blocks[0].Text == marker {
			return true
		}
	}
	return false
}
