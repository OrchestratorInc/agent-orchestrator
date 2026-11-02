package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var vibeSessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// Vibe's explicit --resume ID searches the global store without a cwd filter.
// Older releases call the metadata file meta.json, newer ones metadata.json.
func vibeRoot(home string) string {
	if root := strings.TrimSpace(os.Getenv("VIBE_HOME")); len(root) > 0 {
		return root
	}
	return filepath.Join(home, ".vibe")
}

type vibeMetadata struct {
	ID     string `json:"session_id"`
	Parent string `json:"parent_session_id"`
	Title  string `json:"title"`
	Start  string `json:"start_time"`
	End    string `json:"end_time"`
	Env    struct {
		CWD string `json:"working_directory"`
	} `json:"environment"`
}

func readVibeMetadata(root *os.Root, dir string) (vibeMetadata, string, error) {
	var meta vibeMetadata
	for _, name := range []string{"metadata.json", "meta.json"} {
		path := filepath.Join(dir, name)
		file, err := root.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return meta, "", err
		}
		err = json.NewDecoder(io.LimitReader(file, int64(maxRecordBytes))).Decode(&meta)
		_ = file.Close()
		return meta, path, err
	}
	return meta, "", os.ErrNotExist
}

func discoverVibe(ctx context.Context, root string) ([]sourceFiles, error) {
	provider, err := os.OpenRoot(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = provider.Close() }()
	dir, err := provider.Open(filepath.Join("logs", "session"))
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
	var out []sourceFiles
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "session_") {
			continue
		}
		rel := filepath.Join("logs", "session", entry.Name())
		meta, metaPath, err := readVibeMetadata(provider, rel)
		if err != nil || !vibeSessionID.MatchString(meta.ID) || len(meta.Parent) > 0 {
			continue
		}
		messages := filepath.Join(rel, "messages.jsonl")
		info, err := provider.Stat(messages)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		updated, size := info.ModTime(), info.Size()
		if info, err := provider.Stat(metaPath); err == nil {
			size += info.Size()
			if info.ModTime().After(updated) {
				updated = info.ModTime()
			}
		}
		out = append(out, sourceFiles{harness: domain.HarnessVibe, root: root, key: meta.ID, paths: []string{filepath.Join(root, messages)}, modTime: updated, size: size})
	}
	return out, nil
}

func readVibeTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessVibe, root: root}
	if len(paths) != 1 {
		return t, errors.New("expected one vibe transcript")
	}
	provider, err := os.OpenRoot(root)
	if err != nil {
		return t, err
	}
	defer func() { _ = provider.Close() }()
	rel, err := filepath.Rel(root, paths[0])
	if err != nil || !filepath.IsLocal(rel) {
		return t, errors.New("vibe transcript outside state root")
	}
	meta, _, err := readVibeMetadata(provider, filepath.Dir(rel))
	if err != nil {
		return t, err
	}
	if !vibeSessionID.MatchString(meta.ID) || len(meta.Parent) > 0 || !filepath.IsAbs(meta.Env.CWD) {
		return t, errors.New("invalid vibe conversation metadata")
	}
	t.nativeID, t.cwd, t.title, t.path, t.paths = meta.ID, meta.Env.CWD, meta.Title, paths[0], paths
	t.last, _ = time.Parse(time.RFC3339Nano, meta.End)
	if t.last.IsZero() {
		t.last, _ = time.Parse(time.RFC3339Nano, meta.Start)
	}
	if info, err := provider.Stat(rel); err == nil && info.ModTime().After(t.last) {
		t.last = info.ModTime()
	}
	var items []tagged
	err = readOne(provider, root, paths[0], func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var message struct {
			Role    string      `json:"role"`
			Content contentText `json:"content"`
		}
		if json.Unmarshal(line, &message) != nil || (message.Role != "user" && message.Role != "assistant") {
			return nil
		}
		text := string(message.Content)
		if strings.Contains(text, omittedText) {
			t.shortened, text = true, strings.ReplaceAll(text, omittedText, "")
		}
		if len(strings.TrimSpace(text)) > 0 {
			items = append(items, tagged{Message: Message{Role: domain.MessageRole(message.Role), Text: text, At: t.last}, kind: "vibe"})
			before := len(items)
			items = trimVisible(items)
			t.shortened = t.shortened || len(items) < before
		}
		return nil
	})
	if err != nil {
		return t, err
	}
	hasUser := false
	for _, item := range items {
		hasUser = hasUser || item.Role == domain.MessageRoleUser
		t.messages = append(t.messages, item.Message)
		if len(t.title) == 0 && item.Role == domain.MessageRoleUser {
			t.title = item.Text
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
