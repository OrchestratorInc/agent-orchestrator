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
	if root := strings.TrimSpace(os.Getenv("GROK_HOME")); root != "" {
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
	var out []sourceFiles
	err = fs.WalkDir(r.FS(), "sessions", func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return nil //nolint:nilerr // An unreadable entry must not hide other conversations.
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
			return nil //nolint:nilerr // Skip unreadable or non-regular transcript files.
		}
		// Keep every cwd bucket until Scan applies project eligibility. Otherwise
		// AO's own newer copy, in an AO worktree's bucket, would hide the
		// eligible original with the same id.
		out = append(out, sourceFiles{harness: domain.HarnessGrok, root: root, key: id, paths: []string{filepath.Join(root, path)}, modTime: info.ModTime(), size: info.Size()})
		return nil
	})
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
		Info  struct{ ID, CWD string } `json:"info"`
		Title string                   `json:"generated_title"`
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
	var promptStarts []int
	var tracker grokPromptTracker
	held, drop := 0, 1
	var fallback time.Time
	if info, err := os.Stat(t.path); err == nil {
		fallback = info.ModTime()
	}
	// Grok streams a reply in token-sized chunks, so they are joined in a
	// builder rather than by repeated string concatenation.
	var role string
	var text strings.Builder
	var at time.Time
	flush := func() {
		if joined := text.String(); strings.TrimSpace(joined) != "" {
			items = append(items, tagged{Message: Message{Role: domain.MessageRole(role), Text: joined, At: at}})
			held += len(joined)
		}
		role = ""
		text.Reset()
		// Keep stable prompt indexes until all rewinds have been applied. Drop
		// old text under the same memory budget without deleting its boundary.
		for held > maxHeldText && drop < len(items) {
			held -= len(items[drop].Text)
			items[drop].Text = ""
			t.shortened = true
			drop++
		}
	}
	err = readOne(r, root, t.path, func(raw []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var event struct {
			Timestamp int64  `json:"timestamp"`
			Method    string `json:"method"`
			Params    struct {
				SessionID string `json:"sessionId"`
				Update    struct {
					Kind   string `json:"sessionUpdate"`
					Target *int   `json:"target_prompt_index"`
					Meta   struct {
						PromptIndex *int `json:"promptIndex"`
						HostTurn    bool `json:"hostTurn"`
					} `json:"_meta"`
					Content struct{ Type, Text string } `json:"content"`
				} `json:"update"`
			} `json:"params"`
		}
		if json.Unmarshal(raw, &event) != nil || event.Params.SessionID != t.nativeID {
			return nil //nolint:nilerr // Ignore torn/malformed records and updates for another session.
		}
		u := event.Params.Update
		eventAt := fallback
		if event.Timestamp > 0 {
			eventAt = time.Unix(event.Timestamp, 0)
		}
		if eventAt.After(t.last) {
			t.last = eventAt
		}
		if event.Method == "_x.ai/session/update" && u.Kind == "rewind_marker" && u.Target != nil && *u.Target >= 0 {
			flush()
			if target := *u.Target; target < len(promptStarts) {
				items = items[:promptStarts[target]]
				promptStarts = promptStarts[:target]
			}
			held, drop = 0, 1
			for _, item := range items {
				held += len(item.Text)
			}
			tracker.other()
			return nil
		}
		if u.Kind == "user_message_chunk" && event.Method != "_x.ai/session/update" && !u.Meta.HostTurn {
			if tracker.user(u.Meta.PromptIndex) {
				flush()
				promptStarts = append(promptStarts, len(items))
			}
		} else {
			tracker.other()
		}
		if u.Kind == "turn_completed" {
			flush()
			return nil
		}
		next := ""
		switch u.Kind {
		case "user_message_chunk":
			if !u.Meta.HostTurn && event.Method != "_x.ai/session/update" {
				next = "user"
			}
		case "agent_message_chunk":
			next = "assistant"
		}
		if next == "" || u.Content.Type != "text" {
			return nil
		}
		if next != role {
			flush()
			role = next
			at = eventAt
		}
		if text.Len()+len(u.Content.Text) <= maxVisibleBytes {
			text.WriteString(u.Content.Text)
		} else {
			t.shortened = true
		}
		return nil
	})
	if err != nil {
		return t, err
	}
	flush()
	before := len(items)
	items = trimVisible(items)
	t.shortened = t.shortened || len(items) < before
	hasUser := false
	for _, item := range items {
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		t.messages = append(t.messages, item.Message)
		if item.Role == domain.MessageRoleUser && !hasUser {
			hasUser = true
			t.title = item.Text
		}
	}
	if !hasUser {
		return t, errNoReadableConversation
	}
	if summary.Title != "" {
		t.title = summary.Title
	}
	finishErr := finishTranscript(&t)
	return t, finishErr
}
