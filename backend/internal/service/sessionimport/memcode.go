package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var memcodeImportID = regexp.MustCompile(`^sess_([0-9a-f]{8}|[0-9a-f]{16})$`)

func discoverMemcodeRegistered(ctx context.Context, projects []string) ([]sourceFiles, error) {
	var out []sourceFiles
	for _, project := range projects {
		files, err := discoverMemcodeProject(ctx, project)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue // One unreadable registered project does not hide the others.
		}
		out = append(out, files...)
	}
	return out, nil
}

func discoverMemcodeProject(ctx context.Context, project string) ([]sourceFiles, error) {
	root, err := os.OpenRoot(project)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	dir, err := root.Open(filepath.Join(".memcode", "sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return nil, err
	}
	project = canonical(project)
	var out []sourceFiles
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !memcodeImportID.MatchString(entry.Name()) {
			continue
		}
		rel := filepath.Join(".memcode", "sessions", entry.Name(), "messages.json")
		info, err := root.Lstat(rel)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 16<<20 {
			continue
		}
		out = append(out, sourceFiles{harness: domain.HarnessMemcode, root: project, key: entry.Name(), paths: []string{filepath.Join(project, rel)}, modTime: info.ModTime(), size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].modTime.After(out[j].modTime) })
	return out, nil
}

func readMemcodeTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessMemcode, root: canonical(root)}
	if len(paths) != 1 {
		return t, errors.New("memcode requires one native messages file")
	}
	t.nativeID = filepath.Base(filepath.Dir(paths[0]))
	rel := filepath.Join(".memcode", "sessions", t.nativeID, "messages.json")
	if !memcodeImportID.MatchString(t.nativeID) || canonical(paths[0]) != filepath.Join(t.root, rel) {
		return t, errors.New("memcode native transcript path is invalid")
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
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return t, errors.New("memcode native transcript is not a bounded regular file")
	}
	f, err := provider.Open(rel)
	if err != nil {
		return t, err
	}
	data, err := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	_ = f.Close()
	if err != nil {
		return t, err
	}
	if len(data) > 16<<20 {
		return t, errors.New("memcode native transcript exceeds read budget")
	}
	var history struct {
		ID       string    `json:"session_id"`
		SavedAt  time.Time `json:"saved_at"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &history); err != nil {
		return t, err
	}
	if history.ID != t.nativeID || len(history.Messages) == 0 {
		return t, errors.New("memcode native transcript identity or history missing")
	}
	t.path, t.paths, t.cwd, t.last = paths[0], append([]string(nil), paths...), t.root, info.ModTime()
	if !history.SavedAt.IsZero() && history.SavedAt.Before(time.Now().Add(time.Hour)) {
		t.last = history.SavedAt
	}
	for _, message := range history.Messages {
		if err := ctx.Err(); err != nil {
			return t, err
		}
		var blocks []json.RawMessage
		if json.Unmarshal(message.Content, &blocks) != nil || blocks == nil {
			return t, errors.New("memcode native message has invalid wire content")
		}
		if message.Role != "user" && message.Role != "assistant" {
			if message.Role != "system" && message.Role != "tool" {
				return t, errors.New("memcode native message has invalid role")
			}
			continue
		}
		text := strings.TrimSpace(visibleText(message.Content))
		if text == "" {
			continue
		}
		if message.Role == "user" && t.title == "" {
			t.title = importTitle(text)
		}
		t.messages = append(t.messages, Message{Role: domain.MessageRole(message.Role), Text: text, At: t.last})
	}
	if t.title == "" {
		return t, errors.New("memcode native history has no readable user message")
	}
	return t, nil
}
