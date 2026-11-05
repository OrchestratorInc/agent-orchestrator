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
// The file is a tree, not a list: rewinding or branching appends new records
// under an earlier parent, so the abandoned turns remain in the file. Only the
// active branch is a conversation to continue, so this walks parent links back
// from the last appended record, exactly as the Claude reader does.

// piRecord is the subset of a Pi JSONL record this import reads. The folder
// name dashes the cwd lossily, so cwd is taken from the header instead.
type piRecord struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	ParentID  string    `json:"parentId"`
	CWD       string    `json:"cwd"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		Role    string      `json:"role"`
		Content contentText `json:"content"`
	} `json:"message"`

	// model_change and thinking_level_change records name the model and
	// thinking level Pi answers with from that point on the branch.
	Provider      string `json:"provider"`
	ModelID       string `json:"modelId"`
	ThinkingLevel string `json:"thinkingLevel"`
}

// piNode keeps only display facts and the tree link, so tool payloads are not
// retained while walking ancestry.
type piNode struct {
	id, parent, role, text string
	model, thinking        string
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
		if r.Type == "session" {
			if sawHeader {
				return nil // Later segments or forks repeat the header.
			}
			sawHeader = true
			if r.ID != "" && !strings.EqualFold(r.ID, t.nativeID) {
				return errNativeIdentityMismatch
			}
			t.cwd = r.CWD
			return nil
		}
		if r.ID == "" {
			return nil // Without an id a record cannot take its place in the tree.
		}
		node := piNode{id: r.ID, parent: r.ParentID, at: r.Timestamp}
		switch {
		case r.Type == "model_change" && r.Provider != "" && r.ModelID != "":
			node.model = r.Provider + "/" + r.ModelID
		case r.Type == "thinking_level_change":
			node.thinking = r.ThinkingLevel
		}
		if r.Type == "message" && (r.Message.Role == "user" || r.Message.Role == "assistant") {
			node.role = r.Message.Role
			node.text = string(r.Message.Content)
		}
		leaf = r.ID
		idx, seen := index[r.ID]
		if seen {
			// Pi rewrites some records in place; replace and re-account its text.
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

	// Follow the active branch from the last appended record so rewound or
	// abandoned branches are not shown as history. A dangling parent must not
	// hide the conversation, so stop at the first unreachable link.
	var chain []piNode
	visited := map[string]bool{}
	for leaf != "" {
		idx, found := index[leaf]
		if !found || visited[leaf] {
			break
		}
		visited[leaf] = true
		node := nodes[idx]
		chain = append(chain, node)
		leaf = node.parent
	}
	slices.Reverse(chain)

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
	for _, node := range chain {
		t.shortened = t.shortened || node.dropped
		add(node.role, node.text, node.at)
		// The active branch's latest choice is what Pi itself resumes with.
		if node.model != "" {
			t.model = node.model
		}
		if node.thinking != "" {
			t.effort = node.thinking
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
	return t, finishTranscript(&t, errRelativeCWD)
}
