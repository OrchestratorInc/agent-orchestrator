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
// UUID and absolute cwd, followed by records (model_change, thinking_level_change,
// message) linked into a tree by id/parentId. Only user and assistant message
// text is a display message; thinking, tool calls, and tool results are noise.
//
// OMP and Kimchi are Pi forks with the same layout and tree. OMP adds title
// records and Kimchi adds session_info names and context_edit records; each
// fork simply never writes the others' records.
//
// The file is a tree, not a list: rewinding or branching appends new records
// under an earlier parent, so the abandoned turns remain in the file. Only the
// active branch is a conversation to continue, so this walks parent links back
// from the last appended record, exactly as the Claude reader does.

// piRecord is the subset of a Pi-family JSONL record this import reads. The
// folder name dashes the cwd lossily, so cwd is taken from the header instead.
type piRecord struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	ParentID  string    `json:"parentId"`
	CWD       string    `json:"cwd"`
	Timestamp time.Time `json:"timestamp"`
	// Omitted is set by the oversize-record rewriter when a record was too large
	// to decode; the dropped parts are reported as shortened history.
	Omitted bool `json:"aoOmitted"`
	Message struct {
		Role    string      `json:"role"`
		Content contentText `json:"content"`
	} `json:"message"`
	Title string `json:"title"`
	Name  string `json:"name"`
	// context_edit replaces a message's visible content; null retracts it.
	TargetID    string `json:"targetId"`
	Replacement *struct {
		Content contentText `json:"content"`
	} `json:"replacement"`
}

// piNode keeps only display facts and the tree link, so tool payloads are not
// retained while walking ancestry.
type piNode struct {
	id, parent, role, text string
	name, editTarget       string
	at                     time.Time
	dropped                bool
}

// piFamilyNativeID is the conversation UUID encoded in a Pi, OMP or Kimchi
// session file name, `<ISO-time>_<uuid>.jsonl`. It matches the id in the
// file's session header. A name without the separator falls back to the whole
// stem so grouping stays stable.
func piFamilyNativeID(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if i := strings.LastIndex(name, "_"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func piFamilyReader(harness domain.AgentHarness) func(context.Context, string, ...string) (transcript, error) {
	return func(ctx context.Context, root string, paths ...string) (transcript, error) {
		return readPiFamilyTranscript(ctx, harness, root, paths...)
	}
}

func readPiFamilyTranscript(ctx context.Context, harness domain.AgentHarness, root string, paths ...string) (transcript, error) {
	t := transcript{harness: harness, root: root}
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
	t.nativeID = piFamilyNativeID(paths[0])

	var (
		nodes     []piNode
		index     = map[string]int{}
		leaf      string
		sawHeader bool
		held      int // bytes of held node text
		firstText = -1
		drop      int // the oldest node whose text may still be held
	)
	now := time.Now()
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
				return errNativeIdentityMismatch
			}
			t.cwd = r.CWD
			return nil
		case "title", "title_change":
			if title := strings.TrimSpace(r.Title); title != "" {
				t.title = title
			}
		}
		if r.ID == "" {
			return nil // Without an id a record cannot take its place in the tree.
		}
		node := piNode{id: r.ID, parent: r.ParentID, at: r.Timestamp}
		switch r.Type {
		case "message":
			if r.Message.Role == "user" || r.Message.Role == "assistant" {
				node.role, node.text, node.dropped = r.Message.Role, string(r.Message.Content), r.Omitted
			}
		case "session_info":
			node.name = strings.TrimSpace(r.Name)
		case "context_edit":
			node.editTarget = r.TargetID
			if r.Replacement != nil {
				node.text = string(r.Replacement.Content)
			}
		}
		leaf = r.ID
		idx, seen := index[r.ID]
		if seen {
			// Records are rewritten in place; replace and re-account the text.
			held -= len(nodes[idx].text)
			nodes[idx] = node
		} else {
			idx = len(nodes)
			index[r.ID] = idx
			nodes = append(nodes, node)
		}
		if firstText < 0 && node.text != "" {
			firstText = idx
		}
		held += len(nodes[idx].text)
		dropOldestHeld(nodes, &held, &drop, firstText, func(n *piNode) (*string, *bool) { return &n.text, &n.dropped })
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
		return t, errNoReadableConversation
	}

	chain := activeBranch(nodes, index, leaf, func(n piNode) string { return n.parent })

	var items []tagged
	add := func(role, text string, at time.Time) {
		if role != "user" && role != "assistant" {
			return
		}
		if strings.Contains(text, omittedText) {
			t.shortened, text = true, strings.ReplaceAll(text, omittedText, "")
		}
		if strings.TrimSpace(text) == "" {
			return
		}
		items = append(items, tagged{Message: Message{Role: domain.MessageRole(role), Text: text, At: at}})
	}
	edits := map[string]piNode{}
	for _, node := range chain {
		if node.editTarget != "" {
			edits[node.editTarget] = node
		}
	}
	var name string
	for _, node := range chain {
		if edit, ok := edits[node.id]; ok {
			node.text, node.dropped = edit.text, edit.dropped
		}
		t.shortened = t.shortened || node.dropped
		add(node.role, node.text, node.at)
		if node.name != "" {
			name = node.name
		}
	}
	n := len(items)
	items = trimVisible(items)
	t.shortened = t.shortened || len(items) < n
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
	}
	// A session with no real prompt, or a non-UUID name, is not a conversation to continue.
	if !nativeIDPattern.MatchString(t.nativeID) || !slices.ContainsFunc(t.messages, func(m Message) bool { return m.Role == domain.MessageRoleUser }) {
		return t, errNoReadableConversation
	}
	if name != "" {
		t.title = name
	}
	finishErr := finishTranscript(&t)
	return t, finishErr
}
