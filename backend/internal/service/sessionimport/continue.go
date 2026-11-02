package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func continueRoot(home string) string {
	if root := os.Getenv("CONTINUE_GLOBAL_DIR"); root != "" {
		return root
	}
	return filepath.Join(home, ".continue")
}

func discoverContinue(ctx context.Context, root string) ([]sourceFiles, error) {
	provider, err := os.OpenRoot(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = provider.Close() }()
	dir, err := provider.Open("sessions")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := dir.ReadDir(32769)
	_ = dir.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > 32768 {
		return nil, errors.New("continue session inventory exceeds read budget")
	}
	var out []sourceFiles
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") || !nativeIDPattern.MatchString(id) {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Size() == 0 || info.Size() > 64<<20 {
			continue
		}
		out = append(out, sourceFiles{harness: domain.HarnessContinue, root: root, key: id, paths: []string{filepath.Join(root, "sessions", entry.Name())}, modTime: info.ModTime(), size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].modTime.After(out[j].modTime) })
	return out, nil
}

func readContinueTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessContinue, root: canonical(root)}
	if len(paths) != 1 {
		return t, errors.New("continue requires one native session file")
	}
	t.nativeID = strings.TrimSuffix(filepath.Base(paths[0]), ".json")
	rel := filepath.Join("sessions", t.nativeID+".json")
	if !nativeIDPattern.MatchString(t.nativeID) || filepath.Clean(paths[0]) != filepath.Join(t.root, rel) {
		return t, errors.New("continue native session path is invalid")
	}
	provider, err := os.OpenRoot(t.root)
	if err != nil {
		return t, err
	}
	defer func() { _ = provider.Close() }()
	info, err := provider.Lstat(rel)
	if err != nil {
		return t, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return t, errors.New("continue native session is not a bounded regular file")
	}
	f, err := provider.Open(rel)
	if err != nil {
		return t, err
	}
	data, err := io.ReadAll(io.LimitReader(f, (64<<20)+1))
	_ = f.Close()
	if err != nil {
		return t, err
	}
	if len(data) > 64<<20 {
		return t, errors.New("continue native session exceeds read budget")
	}
	var session struct {
		ID        string `json:"sessionId"`
		Workspace string `json:"workspaceDirectory"`
		History   []struct {
			Message struct {
				Role    string      `json:"role"`
				Content contentText `json:"content"`
			} `json:"message"`
		} `json:"history"`
	}
	if err := json.Unmarshal(data, &session); err != nil {
		return t, err
	}
	if session.ID != t.nativeID || !filepath.IsAbs(session.Workspace) || len(session.History) == 0 {
		return t, errors.New("continue native identity, workspace or history missing")
	}
	t.path, t.paths, t.cwd, t.last = paths[0], append([]string(nil), paths...), session.Workspace, info.ModTime()
	visible := 0
	for _, item := range session.History {
		if err := ctx.Err(); err != nil {
			return t, err
		}
		message := item.Message
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		text := strings.TrimSpace(string(message.Content))
		if text == "" {
			continue
		}
		if message.Role == "user" && t.title == "" {
			t.title = importTitle(text)
		}
		if visible+len(text) > maxVisibleBytes {
			t.shortened = true
			continue
		}
		visible += len(text)
		t.messages = append(t.messages, Message{Role: domain.MessageRole(message.Role), Text: text, At: t.last})
	}
	if t.title == "" || len(t.messages) == 0 {
		return t, errors.New("continue native history has no readable user message")
	}
	return t, nil
}
