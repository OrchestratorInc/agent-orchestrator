package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/openhands"
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
		if err != nil || !entry.IsDir() || entry.Name() != strings.ReplaceAll(id.String(), "-", "") {
			continue
		}
		paths, err := openhands.ConversationFiles(ctx, root, id.String())
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		files := sourceFiles{harness: domain.HarnessOpenHands, root: root, key: id.String(), paths: paths}
		valid := true
		for _, path := range paths {
			rel, err := filepath.Rel(root, path)
			if err != nil || !filepath.IsLocal(rel) {
				valid = false
				break
			}
			info, err := provider.Stat(rel)
			if err != nil {
				valid = false
				break
			}
			files.size += info.Size()
			if info.ModTime().After(files.modTime) {
				files.modTime = info.ModTime()
			}
		}
		if valid {
			out = append(out, files)
		}
	}
	return out, nil
}

func readOpenHandsTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessOpenHands, root: root}
	if len(paths) == 0 {
		return t, errors.New("no openhands conversation state")
	}
	rel, err := filepath.Rel(root, paths[0])
	if err != nil || !filepath.IsLocal(rel) || filepath.Base(rel) != "base_state.json" {
		return t, errors.New("invalid openhands state path")
	}
	folder := filepath.Dir(rel)
	folderID, err := uuid.Parse(folder)
	if err != nil || folder != strings.ReplaceAll(folderID.String(), "-", "") {
		return t, errors.New("invalid openhands conversation directory")
	}
	// Both preview and Import can retain multiple paths. Always refresh only
	// this conversation's event list, without recursion or global rediscovery.
	paths, err = openhands.ConversationFiles(ctx, root, folderID.String())
	if err != nil {
		return t, err
	}
	fingerprint, err := openhands.ConversationFingerprint(ctx, root, folderID.String())
	if err != nil {
		return t, err
	}
	provider, err := os.OpenRoot(root)
	if err != nil {
		return t, err
	}
	defer func() { _ = provider.Close() }()
	file, err := provider.Open(rel)
	if err != nil {
		return t, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(maxRecordBytes)+1))
	_ = file.Close()
	if err != nil {
		return t, err
	}
	if len(raw) > maxRecordBytes {
		return t, errors.New("openhands state is too large")
	}
	if err := openhands.ValidateImportedState(raw, folderID.String()); err != nil {
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
	if err := json.Unmarshal(raw, &state); err != nil {
		return t, err
	}
	if !filepath.IsAbs(state.Workspace.CWD) || state.EmptyHead {
		return t, errors.New("invalid openhands conversation state")
	}
	t.nativeID, t.cwd, t.path, t.paths = folderID.String(), state.Workspace.CWD, paths[0], paths
	type node struct {
		parent, previous, path string
	}
	nodes := map[string]node{}
	previous, fallbackLeaf := "", ""
	// Retain graph metadata only. Text on abandoned branches must not evict
	// active messages before the saved head has selected the native branch.
	for _, path := range paths[1:] {
		err := readOne(provider, root, path, func(line []byte) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			var event struct {
				ID     string `json:"id"`
				Parent string `json:"parent_id"`
				Kind   string `json:"kind"`
				At     string `json:"timestamp"`
			}
			if json.Unmarshal(line, &event) != nil || event.ID == "" {
				return nil //nolint:nilerr // Native recovery skips unreadable/missing events.
			}
			at := openHandsEventTime(event.At, time.Local)
			if at.After(t.last) && at.Before(time.Now().Add(time.Hour)) {
				t.last = at
			}
			nodes[event.ID] = node{parent: event.Parent, previous: previous, path: path}
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
	// Match the SDK's missing-parent and missing-leaf recovery, preserving an
	// explicit tree root instead of bridging it to an abandoned branch.
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
		leaf = fallbackLeaf
	}
	var branch []string
	seen := map[string]bool{}
	for leaf != "" && !seen[leaf] {
		n, ok := nodes[leaf]
		if !ok {
			break
		}
		seen[leaf] = true
		branch = append(branch, leaf)
		leaf = n.parent
	}
	slices.Reverse(branch)
	var items []tagged
	for _, id := range branch {
		err := readOne(provider, root, nodes[id].path, func(line []byte) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			var event struct {
				ID      string `json:"id"`
				Source  string `json:"source"`
				At      string `json:"timestamp"`
				Message struct {
					Role    string      `json:"role"`
					Content contentText `json:"content"`
				} `json:"llm_message"`
			}
			if json.Unmarshal(line, &event) != nil || event.ID != id {
				return nil //nolint:nilerr // Ignore nonmatching/invalid records, as in graph recovery.
			}
			visible := (event.Source == "user" && event.Message.Role == "user") || (event.Source == "agent" && event.Message.Role == "assistant")
			if !visible {
				return nil
			}
			text := string(event.Message.Content)
			if strings.Contains(text, omittedText) {
				t.shortened, text = true, strings.ReplaceAll(text, omittedText, "")
			}
			if strings.TrimSpace(text) == "" {
				return nil
			}
			items = append(items, tagged{Message: Message{Role: domain.MessageRole(event.Message.Role), Text: text, At: openHandsEventTime(event.At, time.Local)}, kind: "openhands"})
			before := len(items)
			items = trimVisible(items)
			t.shortened = t.shortened || len(items) < before
			return nil
		})
		if err != nil {
			return t, err
		}
	}
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
	t.title = importTitle(t.title)
	t.fingerprint, err = openhands.ConversationFingerprint(ctx, root, t.nativeID)
	if err != nil {
		return t, err
	}
	if t.fingerprint != fingerprint {
		return t, errors.New("openhands native source changed while reading; rescan")
	}
	return t, nil
}

func openHandsEventTime(value string, location *time.Location) time.Time {
	at, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return at
	}
	at, _ = time.ParseInLocation("2006-01-02T15:04:05.999999999", value, location)
	return at
}
