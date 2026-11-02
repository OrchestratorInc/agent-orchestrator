package sessionimport

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// OpenHands stores local conversations by UUID hex, independently of cwd.
func openHandsRoot(home string) string {
	if root := os.Getenv("OPENHANDS_CONVERSATIONS_DIR"); root != "" {
		return root
	}
	if root := os.Getenv("OPENHANDS_PERSISTENCE_DIR"); root != "" {
		return filepath.Join(root, "conversations")
	}
	return filepath.Join(home, ".openhands", "conversations")
}

func discoverOpenHands(ctx context.Context, root string) ([]sourceFiles, error) {
	provider, err := os.OpenRoot(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = provider.Close() }()
	dir, err := provider.Open(".")
	if err != nil {
		return nil, err
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return nil, err
	}
	var out []sourceFiles
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, err := uuid.Parse(entry.Name())
		if err != nil || !entry.IsDir() {
			continue
		}
		state := filepath.Join(entry.Name(), "base_state.json")
		info, err := provider.Stat(state)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files := sourceFiles{harness: domain.HarnessOpenHands, root: root, key: id.String(), paths: []string{filepath.Join(root, state)}, modTime: info.ModTime(), size: info.Size()}
		events, err := provider.Open(filepath.Join(entry.Name(), "events"))
		if err != nil {
			continue
		}
		children, err := events.ReadDir(-1)
		_ = events.Close()
		if err != nil {
			continue
		}
		var paths []string
		for _, event := range children {
			if !event.Type().IsRegular() || !strings.HasPrefix(event.Name(), "event-") || !strings.HasSuffix(event.Name(), ".json") {
				continue
			}
			path := filepath.Join(entry.Name(), "events", event.Name())
			info, err := provider.Stat(path)
			if err != nil {
				continue
			}
			paths = append(paths, filepath.Join(root, path))
			files.size += info.Size()
			if info.ModTime().After(files.modTime) {
				files.modTime = info.ModTime()
			}
		}
		slices.SortFunc(paths, func(a, b string) int {
			aParts := strings.SplitN(filepath.Base(a), "-", 3)
			bParts := strings.SplitN(filepath.Base(b), "-", 3)
			aIndex, aErr := strconv.ParseUint(aParts[1], 10, 64)
			bIndex, bErr := strconv.ParseUint(bParts[1], 10, 64)
			if aErr == nil && bErr == nil && aIndex != bIndex {
				return cmp.Compare(aIndex, bIndex)
			}
			return strings.Compare(a, b)
		})
		files.paths = append(files.paths, paths...)
		if len(paths) > 0 {
			out = append(out, files)
		}
	}
	return out, nil
}

func readOpenHandsTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessOpenHands, root: root}
	// RefreshArchive persists just base_state.json. Rediscover the current event
	// set so turns added after the scan are included before the first open.
	if len(paths) == 1 {
		files, err := discoverOpenHands(ctx, root)
		if err != nil {
			return t, err
		}
		for _, file := range files {
			if filepath.Clean(file.paths[0]) == filepath.Clean(paths[0]) {
				return readOpenHandsTranscript(ctx, root, file.paths...)
			}
		}
	}
	if len(paths) < 2 {
		return t, errors.New("no openhands conversation events")
	}
	provider, err := os.OpenRoot(root)
	if err != nil {
		return t, err
	}
	defer func() { _ = provider.Close() }()
	rel, err := filepath.Rel(root, paths[0])
	if err != nil || !filepath.IsLocal(rel) || filepath.Base(rel) != "base_state.json" {
		return t, errors.New("invalid openhands state path")
	}
	file, err := provider.Open(rel)
	if err != nil {
		return t, err
	}
	var state struct {
		ID        string `json:"id"`
		Leaf      string `json:"leaf_event_id"`
		EmptyHead bool   `json:"head_is_empty"`
		Workspace struct {
			CWD string `json:"working_dir"`
		} `json:"workspace"`
	}
	err = json.NewDecoder(io.LimitReader(file, int64(maxRecordBytes))).Decode(&state)
	_ = file.Close()
	if err != nil {
		return t, err
	}
	id, err := uuid.Parse(state.ID)
	if err != nil || !filepath.IsAbs(state.Workspace.CWD) || state.EmptyHead {
		return t, errors.New("invalid openhands conversation state")
	}
	folderID, err := uuid.Parse(filepath.Base(filepath.Dir(paths[0])))
	if err != nil || id != folderID {
		return t, errors.New("native identity mismatch")
	}
	t.nativeID, t.cwd, t.path, t.paths = id.String(), state.Workspace.CWD, paths[0], paths
	type node struct {
		parent   string
		previous string
		item     tagged
	}
	nodes := map[string]node{}
	var order []string
	previous, first, held, drop := "", "", 0, 0
	fallbackLeaf := ""
	for _, path := range paths[1:] {
		if filepath.Dir(filepath.Dir(path)) != filepath.Dir(paths[0]) {
			return t, errors.New("event outside openhands conversation")
		}
		err := readOne(provider, root, path, func(line []byte) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			var event struct {
				ID      string `json:"id"`
				Parent  string `json:"parent_id"`
				Kind    string `json:"kind"`
				Source  string `json:"source"`
				At      string `json:"timestamp"`
				Message struct {
					Role    string      `json:"role"`
					Content contentText `json:"content"`
				} `json:"llm_message"`
			}
			if json.Unmarshal(line, &event) != nil || event.ID == "" {
				return nil //nolint:nilerr // Skip torn or identity-less events without discarding readable history.
			}
			at, _ := time.Parse(time.RFC3339Nano, event.At)
			if at.IsZero() {
				at, _ = time.Parse("2006-01-02T15:04:05.999999999", event.At)
			}
			if at.After(t.last) && at.Before(time.Now().Add(time.Hour)) {
				t.last = at
			}
			text := ""
			if (event.Source == "user" && event.Message.Role == "user") || (event.Source == "agent" && event.Message.Role == "assistant") {
				text = string(event.Message.Content) // extended_content is injected context
			}
			if strings.Contains(text, omittedText) {
				t.shortened, text = true, strings.ReplaceAll(text, omittedText, "")
			}
			if strings.TrimSpace(text) == "" {
				text = ""
			}
			if first == "" && text != "" {
				first = event.ID
			}
			if old, ok := nodes[event.ID]; ok {
				held -= len(old.item.Text)
			} else {
				order = append(order, event.ID)
			}
			nodes[event.ID] = node{parent: event.Parent, previous: previous, item: tagged{Message: Message{Role: domain.MessageRole(event.Message.Role), Text: text, At: at}, kind: "openhands"}}
			held += len(text)
			for held > maxClaudeHeld && drop < len(order) {
				key := order[drop]
				drop++
				if key == first {
					continue
				}
				n := nodes[key]
				held -= len(n.item.Text)
				n.item.Text = ""
				nodes[key], t.shortened = n, true
			}
			previous = event.ID
			if event.Kind != "ConversationStateUpdateEvent" && event.Kind != "ConversationErrorEvent" {
				fallbackLeaf = event.ID
			}
			return nil
		})
		if err != nil {
			return t, err
		}
	}
	// Match EventLog._effective_parent_id: bridge absent parents through the
	// preceding surviving event, while preserving an explicit tree root.
	for id, n := range nodes {
		if n.parent == "__root__" {
			n.parent = ""
		} else if _, ok := nodes[n.parent]; !ok {
			n.parent = n.previous
		}
		nodes[id] = n
	}
	leaf := state.Leaf
	if _, ok := nodes[leaf]; !ok {
		leaf = fallbackLeaf // ConversationState._repair_missing_leaf
	}
	var items []tagged
	seen := map[string]bool{}
	for leaf != "" && !seen[leaf] {
		n, ok := nodes[leaf]
		if !ok {
			break
		}
		seen[leaf] = true
		if n.item.Text != "" {
			items = append(items, n.item)
		}
		leaf = n.parent
	}
	slices.Reverse(items)
	before := len(items)
	items = trimVisible(items)
	t.shortened = t.shortened || len(items) < before
	hasUser := false
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
		if item.Role == domain.MessageRoleUser {
			hasUser = true
			if t.title == "" {
				t.title = item.Text
			}
		}
	}
	if !hasUser {
		return t, errors.New("no readable native conversation")
	}
	t.title = strings.Join(strings.Fields(t.title), " ")
	if runes := []rune(t.title); len(runes) > 100 {
		t.title = string(runes[:100]) + "…"
	}
	return t, nil
}
