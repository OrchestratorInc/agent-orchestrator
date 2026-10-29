package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// qwenHomeEnv overrides Qwen Code's state root; it defaults to ~/.qwen.
const qwenHomeEnv = "QWEN_HOME"

var (
	errNativeIdentityMismatch = errors.New("native identity mismatch")
	errNoReadableConversation = errors.New("no readable native conversation")
)

// Qwen Code is a Gemini-CLI fork. It stores one conversation per file at
// <root>/projects/<sanitized-cwd>/chats/<sessionId>.jsonl, where the file name is
// the native session id. Its ACP session/load is keyed to the original folder's
// project directory, so an imported chat cannot be resumed from AO's worktree;
// imports are history-only (see domain.SessionImportSource.HistoryOnly and the
// prepareImportedWorkspace guard).

// qwenIsTranscript reports whether a walked file is a live Qwen chat transcript.
// Archived chats live under chats/archive and are intentionally skipped, as are
// the project directory's memory/, meta.json and other non-transcript files.
func qwenIsTranscript(path, name string) bool {
	return strings.HasSuffix(name, ".jsonl") && filepath.Base(filepath.Dir(path)) == "chats"
}

// qwenRecord is one JSONL line. Only the human's prompts (provenance real_user)
// and the model's visible replies (provenance assistant_output) are display
// messages; injected context, tool traffic and thinking parts are dropped.
type qwenRecord struct {
	Type       string    `json:"type"`
	Provenance string    `json:"provenance"`
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
		items    []tagged
		prompted bool
		now      = time.Now()
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
		if r.Timestamp.After(t.last) && r.Timestamp.Before(now.Add(time.Hour)) {
			t.last = r.Timestamp
		}
		if t.cwd == "" && r.CWD != "" {
			t.cwd = r.CWD
		}
		switch {
		case r.Type == "user" && r.Provenance == "real_user":
			t.shortened = t.shortened || r.Omitted // A dropped oversize record may have held the prompt.
			add("user", qwenVisibleText(r), r.Timestamp)
		case r.Type == "assistant" && r.Provenance == "assistant_output":
			t.shortened = t.shortened || r.Omitted
			add("assistant", qwenVisibleText(r), r.Timestamp)
		}
		return nil
	}
	for _, path := range paths {
		if err := readOne(providerRoot, root, path, handle); err != nil {
			return t, err
		}
		t.paths = append(t.paths, path)
	}
	t.path = t.paths[len(t.paths)-1]

	n := len(items)
	items = trimVisible(items)
	t.shortened = t.shortened || len(items) < n
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
	}

	if !nativeIDPattern.MatchString(t.nativeID) || !prompted {
		return t, errNoReadableConversation
	}
	if t.cwd != "" && !filepath.IsAbs(t.cwd) {
		return t, errNoReadableConversation
	}
	if t.last.IsZero() {
		if info, err := os.Stat(t.path); err == nil {
			t.last = info.ModTime()
		}
	}
	for i := range t.messages {
		if t.messages[i].At.IsZero() {
			t.messages[i].At = t.last
		}
	}
	for _, m := range t.messages {
		if m.Role == domain.MessageRoleUser {
			t.title = m.Text
			break
		}
	}
	t.title = strings.Join(strings.Fields(t.title), " ")
	if runes := []rune(t.title); len(runes) > 100 {
		t.title = string(runes[:100]) + "…"
	}
	return t, nil
}
