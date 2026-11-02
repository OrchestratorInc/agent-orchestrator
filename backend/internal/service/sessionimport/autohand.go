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

// Autohand resolves the complete UUID-timestamp id globally, independently of
// --path. metadata.json describes the original project; conversation.jsonl is
// the persisted message history used by its Terminal resume implementation.
var autohandImportID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}-[0-9]+$`)

func autohandRoot(home string) string {
	if root := strings.TrimSpace(os.Getenv("AUTOHAND_HOME")); root != "" {
		return root
	}
	return filepath.Join(home, ".autohand")
}

func discoverAutohand(ctx context.Context, root string) ([]sourceFiles, error) {
	entries, err := os.ReadDir(filepath.Join(root, "sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []sourceFiles
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !autohandImportID.MatchString(entry.Name()) {
			continue
		}
		group := sourceFiles{harness: domain.HarnessAutohand, root: root, key: entry.Name()}
		for _, name := range []string{"metadata.json", "conversation.jsonl"} {
			path := filepath.Join(root, "sessions", entry.Name(), name)
			info, statErr := os.Lstat(path)
			if statErr != nil || !info.Mode().IsRegular() {
				break
			}
			group.paths = append(group.paths, path)
			group.size += info.Size()
			if info.ModTime().After(group.modTime) {
				group.modTime = info.ModTime()
			}
		}
		if len(group.paths) == 2 {
			out = append(out, group)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].modTime.After(out[j].modTime) })
	return out, nil
}

func readAutohandTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessAutohand, root: canonical(root)}
	if len(paths) == 1 {
		paths = []string{filepath.Join(filepath.Dir(paths[0]), "metadata.json"), paths[0]}
	}
	if len(paths) != 2 {
		return t, errors.New("autohand metadata and conversation required")
	}
	t.nativeID = filepath.Base(filepath.Dir(paths[1]))
	if !autohandImportID.MatchString(t.nativeID) || filepath.Base(paths[0]) != "metadata.json" || filepath.Base(paths[1]) != "conversation.jsonl" || filepath.Dir(paths[0]) != filepath.Dir(paths[1]) {
		return t, errors.New("invalid autohand conversation paths")
	}
	provider, err := os.OpenRoot(t.root)
	if err != nil {
		return t, err
	}
	defer func() { _ = provider.Close() }()
	rel, err := filepath.Rel(t.root, canonical(paths[0]))
	if err != nil || !filepath.IsLocal(rel) {
		return t, errors.New("autohand metadata outside provider home")
	}
	f, err := provider.Open(rel)
	if err != nil {
		return t, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		_ = f.Close()
		return t, errors.New("autohand metadata exceeds read budget or is not a file")
	}
	var meta struct {
		ID      string    `json:"sessionId"`
		CWD     string    `json:"projectPath"`
		Model   string    `json:"model"`
		Created time.Time `json:"createdAt"`
		Last    time.Time `json:"lastActiveAt"`
	}
	err = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&meta)
	_ = f.Close()
	if err != nil {
		return t, err
	}
	if meta.ID != t.nativeID || !filepath.IsAbs(meta.CWD) {
		return t, errors.New("autohand identity or working directory invalid")
	}
	t.cwd, t.model, t.last = meta.CWD, meta.Model, meta.Created
	now := time.Now().Add(time.Hour)
	if meta.Last.After(t.last) && meta.Last.Before(now) {
		t.last = meta.Last
	}
	var items []tagged
	err = readOne(provider, t.root, paths[1], func(line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var message struct {
			Role    string      `json:"role"`
			Content contentText `json:"content"`
			At      time.Time   `json:"timestamp"`
		}
		if json.Unmarshal(line, &message) != nil {
			return nil // A torn append must not hide earlier complete messages.
		}
		if message.At.After(t.last) && message.At.Before(now) {
			t.last = message.At
		}
		if message.Role != "user" && message.Role != "assistant" {
			return nil
		}
		text := string(message.Content)
		if message.Role == "user" {
			text = codexPrompt(text)
			if strings.HasPrefix(strings.TrimSpace(text), "<system-reminder>") {
				return nil
			}
		}
		if strings.Contains(text, omittedText) {
			t.shortened = true
			text = strings.ReplaceAll(text, omittedText, "")
		}
		if strings.TrimSpace(text) == "" {
			return nil
		}
		if t.title == "" && message.Role == "user" {
			t.title = strings.Join(strings.Fields(text), " ")
			if runes := []rune(t.title); len(runes) > 100 {
				t.title = string(runes[:100]) + "…"
			}
		}
		items = append(items, tagged{Message: Message{Role: domain.MessageRole(message.Role), Text: text, At: message.At}})
		before := len(items)
		items = trimVisible(items)
		t.shortened = t.shortened || len(items) < before
		return nil
	})
	if err != nil {
		return t, err
	}
	if t.title == "" {
		return t, errors.New("no readable autohand prompt")
	}
	t.path, t.paths = paths[1], append([]string(nil), paths...)
	for _, item := range items {
		if item.At.IsZero() {
			item.At = t.last
		}
		t.messages = append(t.messages, item.Message)
	}
	return t, nil
}
