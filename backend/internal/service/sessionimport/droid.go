package sessionimport

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Droid (Factory) keeps one conversation per JSONL file under
// <root>/sessions/-<cwd-dashed>/<id>.jsonl, beside a <id>.settings.json sidecar
// that import ignores. The file name is the native session id, matching the id
// in the session_start record and the id `droid exec --output-format acp-daemon`
// resolves for session/load (the daemon finds a session by id across every
// cwd-dashed folder, so an imported chat resumes from AO's worktree).
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

// droidRecord is one JSONL line. Only session_start and message carry display
// facts; todo_state, agent_turn_outcome and compaction_state are skipped.
// Content decodes through contentText, so tool_use/tool_result/thinking blocks
// are parsed but never retained — only text survives.
type droidRecord struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CWD       string    `json:"cwd"`
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

// handle streams one Droid record into the shared message builder. A torn or
// foreign line is skipped so it cannot hide the conversation around it.
func (d *droidParse) handle(line []byte, t *transcript, now time.Time, add func(kind, id, role, text string, at time.Time)) error {
	var r droidRecord
	if err := json.Unmarshal(line, &r); err != nil {
		return nil //nolint:nilerr // A torn or foreign record does not hide the rest of the conversation.
	}
	if !r.Timestamp.IsZero() && r.Timestamp.After(t.last) && r.Timestamp.Before(now.Add(time.Hour)) {
		t.last = r.Timestamp
	}
	switch r.Type {
	case "session_start":
		if d.sawStart {
			return nil // Forks and later segments repeat ancestor metadata.
		}
		d.sawStart = true
		if r.ID != "" && !strings.EqualFold(r.ID, t.nativeID) {
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
