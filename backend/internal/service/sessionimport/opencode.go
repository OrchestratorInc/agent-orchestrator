package sessionimport

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite" // read-only SQLite driver for OpenCode's session store

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// OpenCode keeps every conversation in one global SQLite database rather than a
// file per chat, so discovery and parsing read rows instead of walking a
// projects tree. The database is opened read-only (mode=ro) and never written
// or checkpointed; a busy timeout tolerates OpenCode holding a write lock.
const openCodeDBFile = "opencode.db"

// openCodeNativeID matches OpenCode's session identifiers (for example
// ses_e830c4a8affelmmC9j6HFbCZAr). The ACP resume path stores this verbatim.
var openCodeNativeID = regexp.MustCompile(`^ses_[A-Za-z0-9]+$`)

// openCodeRoot resolves OpenCode's data directory exactly as the provider does:
// OPENCODE_DATA_DIR, else $XDG_DATA_HOME/opencode, else ~/.local/share/opencode.
func openCodeRoot(home string) string {
	if dir := strings.TrimSpace(os.Getenv("OPENCODE_DATA_DIR")); dir != "" {
		return dir
	}
	if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" {
		return filepath.Join(dataHome, "opencode")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "share", "opencode")
}

// openCodeDB opens the provider's store read-only. A missing database is not an
// error: it simply yields no candidates.
func openCodeDB(root string) (*sql.DB, error) {
	path := filepath.Join(root, openCodeDBFile)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	// mode=ro never creates or migrates; busy_timeout rides out OpenCode's writes.
	return sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(2000)")
}

// discoverOpenCode lists top-level, non-archived conversations newest first. A
// child session is a spawned sub-agent that belongs to its parent, and an
// archived session is not offered as a live conversation to continue, mirroring
// how Codex hides archived rollouts.
func discoverOpenCode(ctx context.Context, root string) ([]sourceFiles, error) {
	db, err := openCodeDB(root)
	if err != nil || db == nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, `
		SELECT s.id, s.time_updated,
		       (SELECT COUNT(*) FROM message m WHERE m.session_id = s.id),
		       (SELECT COALESCE(MAX(m.time_created), 0) FROM message m WHERE m.session_id = s.id)
		FROM session s
		WHERE (s.parent_id IS NULL OR s.parent_id = '')
		  AND s.time_archived IS NULL
		ORDER BY s.time_updated DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []sourceFiles
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var id string
		var updated, count, lastMsg int64
		if err := rows.Scan(&id, &updated, &count, &lastMsg); err != nil {
			return nil, err
		}
		if !openCodeNativeID.MatchString(id) {
			continue
		}
		modMillis := updated
		if lastMsg > modMillis {
			modMillis = lastMsg
		}
		out = append(out, sourceFiles{
			harness: domain.HarnessOpenCode,
			root:    root,
			key:     id,
			// paths carries the session id so readTranscript and RefreshArchive
			// resolve the row without a provider file path.
			paths:   []string{id},
			modTime: time.UnixMilli(modMillis),
			// size doubles as a content-change signal for the scan cache.
			size: count,
		})
	}
	return out, rows.Err()
}

// openCodeMessageText returns each message's concatenated visible text, keyed
// by message id, and whether any block was truncated to the visible budget.
func openCodeMessageText(ctx context.Context, db *sql.DB, sessionID string) (map[string]string, bool, error) {
	// OpenCode injects text parts it generates itself (for example an @file
	// attachment that echoes the Read tool call and the file body into the user
	// turn) and marks them "synthetic", sometimes also "ignored". Its own UI
	// hides these; keeping them would show boilerplate the user never typed, spend
	// the visible-byte budget on it, and let it become the fallback title.
	rows, err := db.QueryContext(ctx, `
		SELECT message_id, json_extract(data, '$.text')
		FROM part
		WHERE session_id = ? AND json_extract(data, '$.type') = 'text'
		  AND COALESCE(json_extract(data, '$.synthetic'), 0) = 0
		  AND COALESCE(json_extract(data, '$.ignored'), 0) = 0
		ORDER BY time_created, id`, sessionID)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	textByMessage := map[string]string{}
	shortened := false
	// As Claude and Pi do, drop the oldest text once the held text passes
	// maxClaudeHeld, so a huge session is not held whole; the opening message
	// stays, and a dropped message's later parts stay dropped.
	var order []string
	dropped := map[string]bool{}
	held, drop := 0, 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		var messageID string
		var text sql.NullString
		if err := rows.Scan(&messageID, &text); err != nil {
			return nil, false, err
		}
		chunk := text.String
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		if len(chunk) > maxVisibleBytes {
			chunk = chunk[:maxVisibleBytes]
			shortened = true
		}
		if dropped[messageID] {
			continue
		}
		if existing := textByMessage[messageID]; existing != "" {
			textByMessage[messageID] = existing + "\n\n" + chunk
			held += len("\n\n" + chunk)
		} else {
			textByMessage[messageID] = chunk
			held += len(chunk)
			order = append(order, messageID)
		}
		for ; held > maxClaudeHeld && drop < len(order); drop++ {
			if drop == 0 {
				continue // the opening message stays
			}
			held -= len(textByMessage[order[drop]])
			delete(textByMessage, order[drop])
			dropped[order[drop]], shortened = true, true
		}
	}
	return textByMessage, shortened, rows.Err()
}

// readOpenCodeTranscript reads one conversation from the store. paths[0] is the
// session id. Only user and assistant text parts become visible messages; tool
// calls, reasoning and step markers are provider internals and stay out.
func readOpenCodeTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessOpenCode, root: root}
	if len(paths) == 0 {
		return t, errors.New("no transcript")
	}
	sessionID := paths[0]
	if !openCodeNativeID.MatchString(sessionID) {
		return t, errors.New("invalid OpenCode session id")
	}
	t.nativeID = sessionID
	t.path = sessionID
	t.paths = []string{sessionID}

	db, err := openCodeDB(root)
	if err != nil {
		return t, err
	}
	if db == nil {
		return t, errors.New("no readable native conversation")
	}
	defer func() { _ = db.Close() }()

	var (
		directory, title      string
		parentID              sql.NullString
		timeUpdated, archived sql.NullInt64
	)
	err = db.QueryRowContext(ctx, `
		SELECT directory, title, parent_id, time_updated, time_archived
		FROM session WHERE id = ?`, sessionID).Scan(&directory, &title, &parentID, &timeUpdated, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return t, errors.New("no readable native conversation")
	}
	if err != nil {
		return t, err
	}
	if archived.Valid {
		return t, errors.New("archived conversations are not resumable without unarchiving them in OpenCode")
	}
	if parentID.Valid && strings.TrimSpace(parentID.String) != "" {
		return t, errors.New("sub-agent sessions belong to their parent conversation")
	}
	t.cwd = directory

	// Collect each message's text parts, concatenating multiple blocks the way
	// Claude's reader joins a reply's content blocks.
	textByMessage, shortened, err := openCodeMessageText(ctx, db, sessionID)
	if err != nil {
		return t, err
	}
	t.shortened = t.shortened || shortened

	msgRows, err := db.QueryContext(ctx, `
		SELECT id, json_extract(data, '$.role'), time_created
		FROM message WHERE session_id = ? ORDER BY time_created, id`, sessionID)
	if err != nil {
		return t, err
	}
	defer func() { _ = msgRows.Close() }()
	now := time.Now()
	var items []tagged
	for msgRows.Next() {
		if err := ctx.Err(); err != nil {
			return t, err
		}
		var id string
		var role sql.NullString
		var created int64
		if err := msgRows.Scan(&id, &role, &created); err != nil {
			return t, err
		}
		if role.String != "user" && role.String != "assistant" {
			continue
		}
		text := textByMessage[id]
		if strings.TrimSpace(text) == "" {
			continue
		}
		at := time.UnixMilli(created)
		if at.After(t.last) && at.Before(now.Add(time.Hour)) {
			t.last = at
		}
		items = append(items, tagged{Message: Message{Role: domain.MessageRole(role.String), Text: text, At: at}, kind: "opencode", id: id})
	}
	if err := msgRows.Err(); err != nil {
		return t, err
	}

	n := len(items)
	items = trimVisible(items)
	t.shortened = t.shortened || len(items) < n
	hasUser := false
	for _, item := range items {
		if item.Role == domain.MessageRoleUser {
			hasUser = true
		}
		t.messages = append(t.messages, item.Message)
	}
	if !hasUser {
		return t, errors.New("no readable native conversation")
	}
	if t.cwd != "" && !filepath.IsAbs(t.cwd) {
		return t, errors.New("transcript working directory must be absolute")
	}
	if t.last.IsZero() && timeUpdated.Valid {
		t.last = time.UnixMilli(timeUpdated.Int64)
	}
	for i := range t.messages {
		if t.messages[i].At.IsZero() {
			t.messages[i].At = t.last
		}
	}
	// OpenCode titles an untitled chat "New session - <ISO>"; treat that
	// placeholder as no title and fall back to the opening prompt.
	t.title = strings.TrimSpace(title)
	if t.title == "" || strings.HasPrefix(t.title, "New session - ") {
		t.title = ""
		for _, m := range t.messages {
			if m.Role == domain.MessageRoleUser {
				t.title = m.Text
				break
			}
		}
	}
	t.title = strings.Join(strings.Fields(t.title), " ")
	if runes := []rune(t.title); len(runes) > 100 {
		t.title = string(runes[:100]) + "…"
	}
	return t, nil
}
