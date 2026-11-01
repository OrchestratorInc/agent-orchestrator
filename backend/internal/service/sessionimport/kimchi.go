package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Kimchi (binary "kimchi", driver "kimchiacp") is a Pi fork (@kimchi-dev/cli)
// that keeps one JSONL per conversation at
// KIMCHI_CODING_AGENT_DIR/sessions/--<cwd-dashed>--/<ISO-time>_<uuid>.jsonl.
// The file opens with a `session` header record carrying the conversation's own
// UUID and absolute cwd, followed by records (model_change,
// thinking_level_change, message, session_info, custom, custom_message, label,
// compaction) linked into a tree by id/parentId. Only user and assistant
// message text is a display message; thinking blocks, tool calls, custom
// records and provider internal state are noise.
//
// The file is a tree, not a list: rewinding or branching appends new records
// under an earlier parent, so abandoned turns remain in the file. Only the
// active branch is a conversation to continue, so this walks parent links back
// from the last appended record, exactly as the Pi reader does.

// kimchiRecord is the subset of a Kimchi JSONL record this import reads. The
// folder name dashes the cwd lossily, so cwd is taken from the header instead.
type kimchiRecord struct {
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
	// thinking level Kimchi answers with from that point on the branch.
	Provider      string `json:"provider"`
	ModelID       string `json:"modelId"`
	ThinkingLevel string `json:"thinkingLevel"`

	// session_info records carry the conversation's display name (its title).
	Name string `json:"name"`
}

// kimchiNode keeps only display facts and the tree link, so tool payloads are
// not retained while walking ancestry.
type kimchiNode struct {
	id, parent, role, text string
	model, thinking, name  string
	at                     time.Time
	dropped                bool
}

// kimchiNativeID is the conversation UUID encoded in a Kimchi session file
// name, `<ISO-time>_<uuid>.jsonl`. It matches the id in the file's session
// header.
func kimchiNativeID(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if i := strings.LastIndex(name, "_"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// discoverKimchi lists Kimchi conversations without reading full histories.
// Each JSONL under sessions/<cwd-dashed>/ is one conversation. Folders whose
// conversation cwd is inside an excluded AO data directory are skipped: those
// hold AO's own copy-and-resume copies of imported chats (see the kimchi agent
// adapter), so offering them again would duplicate the original the copy was
// made from. The header cwd, not the lossy dashed folder name, decides this.
func discoverKimchi(ctx context.Context, root string, excluded []string) ([]sourceFiles, error) {
	byKey := map[string]*sourceFiles{}
	dir := filepath.Join(root, "sessions")
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return nil //nolint:nilerr // Unreadable provider files are deliberately hidden.
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		if kimchiCopyExcluded(path, excluded) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil //nolint:nilerr // Unreadable files are ineligible.
		}
		group := &sourceFiles{harness: domain.HarnessKimchi, root: root, key: path}
		group.paths = append(group.paths, path)
		group.size += info.Size()
		group.modTime = info.ModTime()
		byKey[path] = group
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	out := make([]sourceFiles, 0, len(byKey))
	for _, group := range byKey {
		out = append(out, *group)
	}
	return out, nil
}

// kimchiCopyExcluded reports whether a session file is AO's own resume copy,
// detected by its header cwd falling inside an excluded AO data directory. The
// header is the first JSONL record; an unreadable or headerless file is not
// treated as excluded, so a genuine conversation is never hidden by a read
// error.
func kimchiCopyExcluded(path string, excluded []string) bool {
	if len(excluded) == 0 {
		return false
	}
	f, err := os.Open(path) //nolint:gosec // provider-owned path from the scanned root
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var header struct {
		Type string `json:"type"`
		CWD  string `json:"cwd"`
	}
	dec := json.NewDecoder(f)
	if err := dec.Decode(&header); err != nil || header.Type != "session" || header.CWD == "" {
		return false
	}
	cwd := canonical(header.CWD)
	for _, dir := range excluded {
		if dir != "" && pathWithin(dir, cwd) {
			return true
		}
	}
	return false
}

// readKimchiTranscript reads one Kimchi conversation JSONL. It reuses the
// shared os.Root-confined reader and oversize-record skimming so a huge tool
// result neither hides the text beside it nor is retained, and it follows the
// active branch so a rewound prompt is not shown beside its replacement.
func readKimchiTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessKimchi, root: root}
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
	t.nativeID = kimchiNativeID(paths[0])

	var (
		nodes     []kimchiNode
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
		var r kimchiRecord
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
				return errors.New("native identity mismatch")
			}
			t.cwd = r.CWD
			return nil
		}
		if r.ID == "" {
			return nil // Without an id a record cannot take its place in the tree.
		}
		node := kimchiNode{id: r.ID, parent: r.ParentID, at: r.Timestamp}
		switch {
		case r.Type == "model_change" && r.Provider != "" && r.ModelID != "":
			node.model = r.Provider + "/" + r.ModelID
		case r.Type == "thinking_level_change":
			node.thinking = r.ThinkingLevel
		case r.Type == "session_info":
			node.name = strings.TrimSpace(r.Name)
		}
		if r.Type == "message" && (r.Message.Role == "user" || r.Message.Role == "assistant") {
			node.role = r.Message.Role
			node.text = string(r.Message.Content)
		}
		leaf = r.ID
		idx, seen := index[r.ID]
		if seen {
			// Kimchi rewrites some records in place; replace and re-account its text.
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
		// Drop the oldest text first, as trimVisible would, but keep the opening prompt.
		for held > maxClaudeHeld && drop < len(nodes) {
			if drop != firstText && nodes[drop].text != "" {
				held -= len(nodes[drop].text)
				nodes[drop].text, nodes[drop].dropped = "", true
			}
			drop++
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

	// Follow the active branch from the last appended record so rewound or
	// abandoned branches are not shown as history. A dangling parent must not
	// hide the conversation, so stop at the first unreachable link.
	var chain []kimchiNode
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
		items = append(items, tagged{Message: Message{Role: domain.MessageRole(role), Text: text, At: at}, kind: "kimchi"})
	}
	var activeTitle string
	for _, node := range chain {
		t.shortened = t.shortened || node.dropped
		add(node.role, node.text, node.at)
		// The active branch's latest choice is what Kimchi itself resumes with.
		if node.model != "" {
			t.model = node.model
		}
		if node.thinking != "" {
			t.effort = node.thinking
		}
		if node.name != "" {
			activeTitle = node.name
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
	// Kimchi names a session with session_info.name; fall back to the opening
	// prompt when it never set one.
	t.title = activeTitle
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
