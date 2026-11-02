package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func grokRoot(home string) string {
	if root := strings.TrimSpace(os.Getenv("GROK_HOME")); len(root) > 0 {
		return root
	}
	return filepath.Join(home, ".grok")
}

func discoverGrok(ctx context.Context, root string) ([]sourceFiles, error) {
	root = canonical(root)
	r, err := os.OpenRoot(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	byID := map[string]sourceFiles{}
	err = fs.WalkDir(r.FS(), "sessions", func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return nil
		}
		if d.IsDir() || d.Name() != "updates.jsonl" {
			return nil
		}
		id := filepath.Base(filepath.Dir(path))
		if !nativeIDPattern.MatchString(id) {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		if old, ok := byID[id]; ok && !info.ModTime().After(old.modTime) {
			return nil
		}
		byID[id] = sourceFiles{harness: domain.HarnessGrok, root: root, key: id, paths: []string{filepath.Join(root, path)}, modTime: info.ModTime(), size: info.Size()}
		return nil
	})
	var out []sourceFiles
	for _, file := range byID {
		out = append(out, file)
	}
	return out, err
}

func readGrokTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	root = canonical(root)
	t := transcript{harness: domain.HarnessGrok, root: root, paths: paths}
	if len(paths) != 1 {
		return t, errors.New("invalid grok transcript handle")
	}
	t.path = paths[0]
	t.nativeID = filepath.Base(filepath.Dir(t.path))
	if !nativeIDPattern.MatchString(t.nativeID) {
		return t, errors.New("invalid grok session id")
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return t, err
	}
	defer func() { _ = r.Close() }()
	rel, err := filepath.Rel(root, filepath.Join(filepath.Dir(t.path), "summary.json"))
	if err != nil || !filepath.IsLocal(rel) {
		return t, errors.New("summary outside provider home")
	}
	var summary struct {
		Info struct{ ID, CWD string } `json:"info"`
		Title string `json:"generated_title"`
	}
	summaryFile, err := r.Open(rel)
	if err != nil {
		return t, err
	}
	defer func() { _ = summaryFile.Close() }()
	if err := json.NewDecoder(io.LimitReader(summaryFile, 4<<20)).Decode(&summary); err != nil {
		return t, err
	}
	if summary.Info.ID != t.nativeID || !filepath.IsAbs(summary.Info.CWD) {
		return t, errors.New("invalid grok summary identity or working directory")
	}
	t.cwd = summary.Info.CWD
	var items []tagged
	var role, text string
	var at time.Time
	flush := func() {
		if len(strings.TrimSpace(text)) > 0 {
			items = append(items, tagged{Message: Message{Role: domain.MessageRole(role), Text: text, At: at}})
		}
		role, text = "", ""
		before := len(items)
		items = trimVisible(items)
		t.shortened = t.shortened || len(items) < before
	}
	err = readOne(r, root, t.path, func(raw []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var event struct {
			Timestamp int64 `json:"timestamp"`
			Params struct {
				SessionID string `json:"sessionId"`
				Update struct {
					Kind string `json:"sessionUpdate"`
					Content struct{ Type, Text string } `json:"content"`
				} `json:"update"`
			} `json:"params"`
		}
		if json.Unmarshal(raw, &event) != nil || event.Params.SessionID != t.nativeID {
			return nil
		}
		u := event.Params.Update
		if u.Kind == "turn_completed" {
			flush()
			return nil
		}
		next := ""
		switch u.Kind {
		case "user_message_chunk":
			next = "user"
		case "agent_message_chunk":
			next = "assistant"
		}
		if len(next) == 0 || u.Content.Type != "text" {
			return nil
		}
		if next != role {
			flush()
			role = next
			at = time.UnixMilli(event.Timestamp)
		}
		if len(text)+len(u.Content.Text) <= maxVisibleBytes {
			text += u.Content.Text
		} else {
			t.shortened = true
		}
		if at.After(t.last) {
			t.last = at
		}
		return nil
	})
	if err != nil {
		return t, err
	}
	flush()
	hasUser := false
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
		if item.Role == domain.MessageRoleUser && !hasUser {
			hasUser = true
			t.title = item.Text
		}
	}
	if !hasUser {
		return t, errors.New("no readable native conversation")
	}
	if len(summary.Title) > 0 {
		t.title = summary.Title
	}
	t.title = strings.Join(strings.Fields(t.title), " ")
	if runes := []rune(t.title); len(runes) > 100 {
		t.title = string(runes[:100]) + "…"
	}
	return t, nil
}
