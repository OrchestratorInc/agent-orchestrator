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

// ompConfigDirEnv overrides OMP's state root. OMP is a Pi fork and reuses Pi's
// variable name; it defaults to ~/.omp/agent.
const ompConfigDirEnv = "PI_CODING_AGENT_DIR"

// OMP is a Pi-fork, terminal-first coding harness. It stores one conversation
// per file at <root>/sessions/-<cwd-dashed>/<ISO-time>_<uuid>.jsonl, where the
// trailing UUID is the native session id (also recorded in the session header).
// Its ACP session/load finds conversations by cwd; the OMP adapter copies the
// transcript for AO's launch directory before first resume, keeping the native
// id and leaving the original file untouched.

// ompRoot resolves OMP's session-state root the way an OMP launch does:
// PI_CODING_AGENT_DIR when set, otherwise ~/.omp/agent.
func ompRoot(home string) string {
	if dir := strings.TrimSpace(os.Getenv(ompConfigDirEnv)); dir != "" {
		return dir
	}
	return filepath.Join(home, ".omp", "agent")
}

// ompNativeIDFromPath returns the native id: the UUID trailing the timestamp in
// an OMP transcript file name (<ISO-time>_<uuid>.jsonl). A name without the
// separator falls back to the whole stem so grouping stays stable.
func ompNativeIDFromPath(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if i := strings.LastIndex(stem, "_"); i >= 0 {
		return stem[i+1:]
	}
	return stem
}

// ompRecord is one JSONL line. Only user and assistant messages are display
// messages; the shared contentText keeps text blocks and drops thinking and
// toolCall parts, and toolResult-role records are left out by readOMPTranscript.
type ompRecord struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	ParentID  string    `json:"parentId"`
	CWD       string    `json:"cwd"`
	Title     string    `json:"title"`
	Timestamp time.Time `json:"timestamp"`
	Model     string    `json:"model"`
	Effort    string    `json:"thinkingLevel"`
	// Omitted is set by the oversize-record rewriter when a record was too large
	// to decode; the dropped parts are reported as shortened history.
	Omitted bool `json:"aoOmitted"`
	Message struct {
		Role    string      `json:"role"`
		Content contentText `json:"content"`
	} `json:"message"`
}

// ompNode is one entry in a session's parent tree. Every id-bearing entry is a
// node so a message's parent chain stays connected through the model_change and
// custom entries that also carry ids; only message nodes are display messages.
type ompNode struct {
	id, parent string
	role, text string
	model      string
	effort     string
	at         time.Time
	message    bool
	omitted    bool
}

// readOMPTranscript reads one OMP conversation file into the shared transcript
// shape. It drops tool traffic and thinking, derives cwd and native id from the
// session header (rejecting a header id that disagrees with the file name), and
// takes the title from the session's own title record or the first real prompt.
func readOMPTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessOMP, root: root}
	if len(paths) == 0 {
		return t, errors.New("no transcript")
	}
	root = canonical(root)
	t.root = root
	providerRoot, err := os.OpenRoot(root)
	if err != nil {
		return t, err
	}
	defer func() { _ = providerRoot.Close() }()
	t.nativeID = ompNativeIDFromPath(paths[0])

	var (
		nodes   []ompNode
		index   = map[string]int{}
		leafID  string
		sawMeta bool
		now     = time.Now()
	)
	handle := func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var r ompRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return nil //nolint:nilerr // A torn or foreign record does not hide the rest of the conversation.
		}
		if r.Timestamp.After(t.last) && r.Timestamp.Before(now.Add(time.Hour)) {
			t.last = r.Timestamp
		}
		switch r.Type {
		case "session":
			if sawMeta {
				return nil // Resume rewrites repeat the header; keep the first.
			}
			sawMeta = true
			if r.ID != "" && !strings.EqualFold(r.ID, t.nativeID) {
				return errNativeIdentityMismatch
			}
			t.cwd = r.CWD
			return nil // The header id is the session uuid, not a node in the parent tree.
		case "title", "title_change":
			if title := strings.TrimSpace(r.Title); title != "" {
				t.title = title
			}
			return nil
		}
		// Every id-bearing entry is a node in the parent tree. Non-message entries
		// (model_change, custom, …) carry no text but keep the chain connected.
		if r.ID == "" {
			return nil
		}
		node := ompNode{id: r.ID, parent: r.ParentID, at: r.Timestamp}
		if r.Type == "model_change" {
			node.model = r.Model
		}
		if r.Type == "thinking_level_change" {
			node.effort = r.Effort
		}
		if r.Type == "message" && (r.Message.Role == "user" || r.Message.Role == "assistant") {
			node.role, node.text, node.message, node.omitted = r.Message.Role, string(r.Message.Content), true, r.Omitted
		}
		if i, ok := index[r.ID]; ok {
			nodes[i] = node // OMP rewrites a record in place; keep its first position.
		} else {
			index[r.ID] = len(nodes)
			nodes = append(nodes, node)
		}
		leafID = r.ID
		return nil
	}
	for _, path := range paths {
		if err := readOne(providerRoot, root, path, handle); err != nil {
			return t, err
		}
		t.paths = append(t.paths, path)
	}
	t.path = t.paths[len(t.paths)-1]

	// Follow the active branch from the last entry so a rewound or branched prompt
	// is not shown as history. A dangling parent falls back to full file order.
	chain := nodes
	if leafID != "" {
		var walked []ompNode
		visited := map[string]bool{}
		dangling := false
		for id := leafID; id != ""; {
			i, ok := index[id]
			if !ok {
				dangling = true
				break
			}
			if visited[id] {
				dangling = true
				break // A cycle cannot be a valid chain; show file order instead.
			}
			visited[id] = true
			walked = append(walked, nodes[i])
			id = nodes[i].parent
		}
		if !dangling {
			slices.Reverse(walked)
			chain = walked
		}
	}

	var (
		items    []tagged
		prompted bool
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
	for _, node := range chain {
		if node.model != "" {
			t.model = node.model
		}
		if node.effort != "" {
			t.effort = node.effort
		}
		if !node.message {
			continue
		}
		t.shortened = t.shortened || node.omitted // A dropped oversize record may have held visible text.
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
	if t.title == "" {
		for _, m := range t.messages {
			if m.Role == domain.MessageRoleUser {
				t.title = m.Text
				break
			}
		}
	}
	t.title = strings.Join(strings.Fields(t.title), " ")
	if runes := []rune(t.title); len(runes) > 100 {
		t.title = string(runes[:100]) + "…"
	}
	return t, nil
}
