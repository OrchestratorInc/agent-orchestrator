package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Pi (binary "pi", driver "piacp") keeps one JSONL per conversation at
// PI_CODING_AGENT_DIR/sessions/--<cwd-dashed>--/<ISO-time>_<uuid>.jsonl. The
// file opens with a `session` header record carrying the conversation's own
// UUID and absolute cwd, followed by `message` records whose content is an
// array of blocks (text/thinking/toolCall). Only user and assistant text is a
// display message; thinking, tool calls, and tool results are provider noise.

// piRecord is the subset of a Pi JSONL record this import reads. The folder
// name dashes the cwd lossily, so cwd is taken from the header instead.
type piRecord struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	CWD       string    `json:"cwd"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		Role    string      `json:"role"`
		Content contentText `json:"content"`
	} `json:"message"`
}

// piNativeID is the conversation UUID encoded in a Pi session file name,
// `<ISO-time>_<uuid>.jsonl`. It matches the id in the file's session header.
func piNativeID(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if i := strings.LastIndex(name, "_"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// readPiTranscript reads one Pi conversation JSONL. It reuses the shared
// os.Root-confined reader and oversize-record skimming so a huge tool result
// neither hides the text beside it nor is retained.
func readPiTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessPi, root: root}
	if len(paths) == 0 {
		return t, errors.New("no transcript")
	}
	root = canonical(root)
	providerRoot, err := os.OpenRoot(root)
	if err != nil {
		return t, err
	}
	defer func() { _ = providerRoot.Close() }()
	t.root = root
	t.nativeID = piNativeID(paths[0])

	var (
		items     []tagged
		ids       = map[string]int{}
		sawHeader bool
		visible   int
	)
	now := time.Now()
	add := func(id, role, text string, at time.Time) {
		if role != "user" && role != "assistant" {
			return
		}
		if strings.Contains(text, omittedText) {
			t.shortened, text = true, strings.ReplaceAll(text, omittedText, "")
		}
		if strings.TrimSpace(text) == "" {
			return
		}
		msg := tagged{Message: Message{Role: domain.MessageRole(role), Text: text, At: at}, kind: "pi", id: id}
		if id != "" {
			// Pi rewrites one message under the same id as it streams; keep its first position.
			if index, ok := ids["pi\x00"+id]; ok {
				visible += len(text) - len(items[index].Text)
				items[index] = msg
				return
			}
			ids["pi\x00"+id] = len(items)
		}
		visible += len(text)
		items = append(items, msg)
	}
	trim := func() {
		n := len(items)
		items = trimVisible(items)
		t.shortened = t.shortened || len(items) < n
	}
	handle := func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var r piRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return nil //nolint:nilerr // A torn or foreign record does not hide the rest of the conversation.
		}
		if r.Timestamp.After(t.last) && r.Timestamp.Before(now.Add(time.Hour)) {
			t.last = r.Timestamp
		}
		switch r.Type {
		case "session":
			if sawHeader {
				return nil // Later segments or forks repeat the header.
			}
			sawHeader = true
			if r.ID != "" && !strings.EqualFold(r.ID, t.nativeID) {
				return errors.New("native identity mismatch")
			}
			t.cwd = r.CWD
		case "message":
			add(r.ID, r.Message.Role, string(r.Message.Content), r.Timestamp)
		}
		if visible > 2*maxVisibleBytes {
			trim()
			visible = 0
			clear(ids)
			for i, item := range items {
				visible += len(item.Text)
				if item.id != "" {
					ids[item.kind+"\x00"+item.id] = i
				}
			}
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
	if !sawHeader {
		return t, errors.New("no readable native conversation")
	}
	trim()
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
	}
	// A session with no real prompt, or a non-UUID name, is not a conversation to continue.
	if !nativeIDPattern.MatchString(t.nativeID) || !slices.ContainsFunc(t.messages, func(m Message) bool { return m.Role == domain.MessageRoleUser }) {
		return t, errors.New("no readable native conversation")
	}
	if t.cwd != "" && !filepath.IsAbs(t.cwd) {
		return t, errors.New("transcript working directory must be absolute")
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
