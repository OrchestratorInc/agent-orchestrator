package sessionimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// qwenHomeEnv overrides Qwen Code's state root; it defaults to ~/.qwen.
const qwenHomeEnv = "QWEN_HOME"

// Qwen Code is a Gemini-CLI fork. It stores one conversation per file at
// <root>/projects/<sanitized-cwd>/chats/<sessionId>.jsonl, where the file name is
// the native session id. On first open the Qwen adapter copies it to the
// project's raw ACP launch-cwd lookup path before resuming the same native id.

// qwenIsTranscript reports whether a walked file is a live Qwen chat transcript.
// Archived chats live under chats/archive and are intentionally skipped, as are
// the project directory's memory/, meta.json and other non-transcript files.
func qwenKey(path, name string) string {
	if qwenIsTranscript(path, name) {
		return path
	}
	return ""
}

func qwenIsTranscript(path, name string) bool {
	return strings.HasSuffix(name, ".jsonl") && filepath.Base(filepath.Dir(path)) == "chats"
}

// qwenRecord is one JSONL line. Provenance selects human/model messages;
// systemPayload.displayText separates a human prompt from model-bound context.
// UUID links select the active branch before projecting visible text.
type qwenRecord struct {
	UUID       string    `json:"uuid"`
	ParentUUID string    `json:"parentUuid"`
	Type       string    `json:"type"`
	Provenance string    `json:"provenance"`
	Subtype    string    `json:"subtype"`
	SessionID  string    `json:"sessionId"`
	CWD        string    `json:"cwd"`
	Timestamp  time.Time `json:"timestamp"`
	// Omitted is set by the oversize-record rewriter when a record was too large
	// to decode; its message parts are gone, so the history is reported shortened.
	Omitted bool `json:"aoOmitted"`
	Message struct {
		Role  string `json:"role"`
		Parts []struct {
			Text         string          `json:"text"`
			Thought      json.RawMessage `json:"thought"`
			FunctionCall json.RawMessage `json:"functionCall"`
		} `json:"parts"`
	} `json:"message"`
	SystemPayload struct {
		DisplayText *string `json:"displayText"`
	} `json:"systemPayload"`
}

// qwenNode retains links and display facts, not model-bound context or tools.
type qwenNode struct {
	id, parent, role, text string
	at                     time.Time
	omitted                bool
	projected              bool
}

// qwenVisibleText joins a message's plain text parts. A part that also carries a
// thought (reasoning) or a functionCall (tool call) is provider-internal, not a
// display message, so it is left out.
func qwenVisibleText(r qwenRecord) string {
	var parts []string
	for _, p := range r.Message.Parts {
		if qwenPartPresent(p.Thought) || qwenPartPresent(p.FunctionCall) {
			continue
		}
		if p.Text != "" {
			parts = append(parts, p.Text)
		}
	}
	return strings.Join(parts, "")
}

// qwenAppendText bounds a logical message across physical fragments and keeps
// a valid UTF-8 prefix when a fragment crosses the held-text budget.
func qwenAppendText(text, fragment string) (string, bool) {
	remaining := max(0, maxHeldText-len(text))
	shortened := len(fragment) > remaining
	if shortened {
		fragment = strings.ToValidUTF8(fragment[:remaining], "")
	}
	return text + fragment, shortened
}

func qwenPartPresent(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

// readQwenTranscript reads one Qwen conversation file into the shared transcript
// shape. It drops injected context (anything not real_user/assistant_output),
// tool results and thinking, and derives the title from the first real prompt.
func readQwenTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessQwen, root: root}
	root = canonical(root)
	t.root = root
	providerRoot, err := os.OpenRoot(root)
	if err != nil {
		return t, err
	}
	defer func() { _ = providerRoot.Close() }()
	t.nativeID = strings.TrimSuffix(filepath.Base(paths[0]), ".jsonl")

	var (
		items     []tagged
		nodes     []qwenNode
		index     = map[string]int{}
		leaf      string
		prompted  bool
		held      int
		drop      int
		firstText = -1
		now       = time.Now()
	)
	add := func(role, text string, at time.Time) {
		if strings.Contains(text, omittedText) {
			t.shortened, text = true, strings.ReplaceAll(text, omittedText, "")
		}
		if strings.TrimSpace(text) == "" {
			return
		}
		if role == "user" {
			prompted = true
		}
		items = append(items, tagged{Message: Message{Role: domain.MessageRole(role), Text: text, At: at}})
	}
	handle := func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var r qwenRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return nil //nolint:nilerr // A torn or foreign record does not hide the rest of the conversation.
		}
		if r.SessionID != "" && r.SessionID != t.nativeID {
			return errNativeIdentityMismatch
		}
		if r.Type != "user" && r.Type != "assistant" && r.Type != "tool_result" && r.Type != "system" {
			return nil
		}
		if r.Type == "system" {
			switch r.Subtype {
			case "session_artifact_event", "session_artifact_snapshot", "session_sources_snapshot", "managed_session_header_v1", "managed_session_event_v1", "managed_session_commit_v1", "managed_session_snapshot_base_v1":
				return nil // Qwen excludes these records when selecting the active leaf.
			}
		}
		if r.Timestamp.After(t.last) && r.Timestamp.Before(now.Add(time.Hour)) {
			t.last = r.Timestamp
		}
		if t.cwd == "" && r.CWD != "" {
			t.cwd = r.CWD
		}
		node := qwenNode{id: r.UUID, parent: r.ParentUUID, at: r.Timestamp}
		switch {
		case r.Type == "user" && r.Provenance == "real_user":
			node.role = "user"
			if r.SystemPayload.DisplayText != nil {
				// Presence matters: an empty projection must not expose model input.
				node.text, node.projected = *r.SystemPayload.DisplayText, true
			} else {
				node.text = qwenVisibleText(r) // Older records have no display projection.
			}
		case r.Type == "assistant" && r.Provenance == "assistant_output":
			node.role, node.text = "assistant", qwenVisibleText(r)
		}
		node.omitted = r.Omitted && node.role != ""
		idx := len(nodes)
		if node.id != "" {
			leaf = node.id
			if prior, found := index[node.id]; found {
				idx = prior
			} else {
				index[node.id] = idx
			}
		}
		if idx < len(nodes) {
			previous := nodes[idx]
			held -= len(previous.text)
			// Fragments keep the first record's parent, role and display projection.
			if previous.role != "" && !previous.projected {
				var shortened bool
				previous.text, shortened = qwenAppendText(previous.text, qwenVisibleText(r))
				previous.omitted = previous.omitted || shortened
			}
			previous.omitted = previous.omitted || r.Omitted && previous.role != ""
			if r.Timestamp.After(previous.at) {
				previous.at = r.Timestamp
			}
			nodes[idx] = previous
		} else {
			var shortened bool
			node.text, shortened = qwenAppendText("", node.text)
			node.omitted = node.omitted || shortened
			nodes = append(nodes, node)
		}
		if firstText < 0 && nodes[idx].text != "" {
			firstText = idx
		}
		if idx < drop && idx != firstText {
			nodes[idx].omitted = nodes[idx].omitted || nodes[idx].text != ""
			nodes[idx].text = ""
		}
		held += len(nodes[idx].text)
		// Bound text held across branches while preserving links and the opening prompt.
		dropOldestHeld(nodes, &held, &drop, firstText, func(n *qwenNode) (*string, *bool) { return &n.text, &n.omitted })
		return nil
	}
	for _, path := range paths {
		if err := readOne(providerRoot, root, path, handle); err != nil {
			return t, err
		}
		t.paths = append(t.paths, path)
	}
	t.path = t.paths[len(t.paths)-1]

	chain := nodes // Legacy records without UUIDs retain their linear order.
	if leaf != "" {
		chain = activeBranch(nodes, index, leaf, func(n qwenNode) string { return n.parent })
	}
	for _, node := range chain {
		if node.role == "" {
			continue
		}
		t.shortened = t.shortened || node.omitted
		add(node.role, node.text, node.at)
	}

	n := len(items)
	items = trimVisible(items)
	t.shortened = t.shortened || len(items) < n
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
	}

	if !nativeIDPattern.MatchString(t.nativeID) || !prompted {
		return t, errNoReadableConversation
	}
	finishErr := finishTranscript(&t)
	return t, finishErr
}
