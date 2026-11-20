package sessionimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/droid"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Droid (Factory) keeps one conversation per JSONL file under
// <root>/sessions/-<cwd-dashed>/<id>.jsonl, beside a <id>.settings.json sidecar
// that import ignores. The file name is the native session id, matching the id
// in the session_start record. Preparation creates a separate UUID whose
// session_start cwd is the AO worktree; the original is never resumed.
const (
	// droidHomeEnv overrides Droid's home; ".factory" is appended to it. It is a
	// home override, not a direct state-dir path, unlike CLAUDE_CONFIG_DIR.
	droidHomeEnv  = "FACTORY_HOME_OVERRIDE"
	droidDirName  = ".factory"
	droidContext  = "context-"
	droidSessions = "sessions"
)

// droidRoot is Droid's state directory: <FACTORY_HOME_OVERRIDE or home>/.factory.
func droidRoot(home string) string {
	if override := strings.TrimSpace(os.Getenv(droidHomeEnv)); override != "" {
		home = override
	}
	return filepath.Join(home, droidDirName)
}

func discoverDroid(ctx context.Context, root string) ([]sourceFiles, error) {
	return walkSources(domain.HarnessDroid, droidSessions, func(path, name string) string {
		id := strings.TrimSuffix(name, ".jsonl")
		if name != id+".jsonl" || !nativeIDPattern.MatchString(id) {
			return ""
		}
		rel, err := filepath.Rel(filepath.Join(root, droidSessions), path)
		if err != nil || !filepath.IsLocal(rel) {
			return ""
		}
		dir := filepath.Dir(rel)
		if dir != "." && (filepath.Dir(dir) != "." || dir != "btw" && !strings.HasPrefix(dir, "-")) {
			return ""
		}
		return id
	})(ctx, root)
}

func readDroidTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessDroid, root: canonical(root)}
	if len(paths) != 1 {
		return t, errors.New("droid: expected one conversation")
	}
	body, err := droid.ReadNativeHistory(ctx, t.root, paths[0])
	if err != nil {
		return t, err
	}
	t.path, t.paths, t.nativeID = paths[0], paths, strings.TrimSuffix(filepath.Base(paths[0]), ".jsonl")
	t.fingerprint = droid.NativeHistoryFingerprint(body)
	state := droidParse{}
	var items []tagged
	visible := 0
	add := func(kind, id, role, text string, at time.Time) {
		if strings.TrimSpace(text) == "" {
			return
		}
		if len(text) > maxVisibleBytes {
			text = text[:maxVisibleBytes]
			t.shortened = true
		}
		items = append(items, tagged{Message: Message{Role: domain.MessageRole(role), Text: text, At: at}, kind: kind, id: id})
		visible += len(text)
		if visible > 2*maxVisibleBytes {
			items = trimVisible(items)
			t.shortened = true
			visible = 0
			for _, item := range items {
				visible += len(item.Text)
			}
		}
	}
	for line := range bytes.SplitSeq(body, []byte("\n")) {
		if err := ctx.Err(); err != nil {
			return t, err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if err := state.handle(line, &t, time.Now(), add); err != nil {
			return t, err
		}
	}
	if !state.sawStart || !state.prompted || !nativeIDPattern.MatchString(t.nativeID) {
		return t, errNoReadableConversation
	}
	trimmed := trimVisible(items)
	t.shortened = t.shortened || len(trimmed) < len(items)
	for _, item := range trimmed {
		t.messages = append(t.messages, item.Message)
	}
	finishErr := finishTranscript(&t)
	return t, finishErr
}

// droidRecord is one JSONL line. Only session_start and message carry display
// facts; todo_state, agent_turn_outcome and compaction_state are skipped.
// Content decodes through contentText, so tool_use/tool_result/thinking blocks
// are parsed but never retained — only text survives.
type droidRecord struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CWD       string    `json:"cwd"`
	Version   int       `json:"version"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		Role    string      `json:"role"`
		Content contentText `json:"content"`
	} `json:"message"`
}

// droidParse holds the per-transcript state that outlives a single line: the
// one-per-file session_start, and whether a real prompt was seen.
type droidParse struct {
	sawStart bool
	prompted bool
}

// droidPlaceholderTitle is a not-yet-named chat, so import falls back to the
// first prompt as Droid's own list does.
func droidPlaceholderTitle(title string) bool {
	switch title {
	case "New Session", "Start new chat":
		return true
	}
	return false
}

// handle streams one Droid record into the visible message builder. Malformed
// records and unknown session schema versions refuse import.
func (d *droidParse) handle(line []byte, t *transcript, now time.Time, add func(kind, id, role, text string, at time.Time)) error {
	var r droidRecord
	if err := json.Unmarshal(line, &r); err != nil {
		return errors.New("droid: malformed conversation record")
	}
	if !r.Timestamp.IsZero() && r.Timestamp.After(t.last) && r.Timestamp.Before(now.Add(time.Hour)) {
		t.last = r.Timestamp
	}
	switch r.Type {
	case "session_start":
		if d.sawStart {
			return errors.New("droid: duplicate session header")
		}
		d.sawStart = true
		if r.ID != t.nativeID || r.Version != 2 {
			return errors.New("native identity mismatch")
		}
		if r.CWD != "" {
			t.cwd = r.CWD
		}
		if title := strings.TrimSpace(r.Title); title != "" && !droidPlaceholderTitle(title) {
			t.title = title
		}
	case "message":
		// Injected context (system reminders, tool catalogs) is a user-role
		// record whose id carries a "context-" prefix; it is never a prompt.
		if strings.HasPrefix(r.ID, droidContext) {
			return nil
		}
		role := strings.ToLower(r.Message.Role)
		if role != "user" && role != "assistant" {
			return nil
		}
		text := string(r.Message.Content)
		if role == "user" && strings.TrimSpace(text) != "" {
			d.prompted = true
		}
		add("droid", r.ID, role, text, r.Timestamp)
	}
	return nil
}
