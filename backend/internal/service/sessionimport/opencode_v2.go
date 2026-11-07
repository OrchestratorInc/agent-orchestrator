package sessionimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// OpenCode 2 upgrades the user's opencode.db in place and from then on writes
// chats only to session_v2 and session_message (see opencode.HasV2Store). Such
// a store is read here, as OpenCode 2 chats; the OpenCode 1 reader skips it, so
// a chat is never listed twice or read from tables the upgrade froze.

// openCodeV2Placeholder is the title OpenCode 2 shows for an untitled chat.
const openCodeV2Placeholder = "Untitled session"

// discoverOpenCodeV2 lists an upgraded store's top-level, non-archived chats.
func discoverOpenCodeV2(ctx context.Context, root string) ([]sourceFiles, error) {
	db, err := openCodeDB(root)
	if err != nil || db == nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	if v2, err := opencode.HasV2Store(ctx, db); err != nil || !v2 {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT s.id, s.time_updated,
		       (SELECT COUNT(*) FROM session_message m WHERE m.session_id = s.id),
		       (SELECT COALESCE(MAX(m.time_created), 0) FROM session_message m WHERE m.session_id = s.id)
		FROM session_v2 s
		WHERE (s.parent_id IS NULL OR s.parent_id = '')
		  AND s.time_archived IS NULL
		ORDER BY s.time_updated DESC`)
	if err != nil {
		return nil, err
	}
	type row struct {
		id                    string
		updated, count, lastM int64
	}
	var found []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.updated, &r.count, &r.lastM); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if openCodeNativeID.MatchString(r.id) && r.count > 0 {
			found = append(found, r)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]sourceFiles, 0, len(found))
	for _, r := range found {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The fingerprint pins the history a selection saw; the import's copy in
		// AO's OpenCode 2 store must match it.
		fp, err := opencode.V2SessionFingerprint(ctx, db, r.id)
		if err != nil {
			return nil, err
		}
		out = append(out, sourceFiles{
			harness:     domain.HarnessOpenCodeV2,
			root:        root,
			key:         r.id,
			paths:       []string{r.id},
			modTime:     time.UnixMilli(max(r.updated, r.lastM)),
			fingerprint: fp,
			size:        r.count,
		})
	}
	return out, nil
}

// openCodeV2Message is the part of a session_message row's data the list shows:
// a user message's text, or an assistant message's text content blocks.
type openCodeV2Message struct {
	Text    string `json:"text"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// readOpenCodeV2Transcript reads one chat from an upgraded store. paths[0] is
// the session id. Only user text and assistant text blocks become visible
// messages; reasoning, tool calls and status rows stay out.
func readOpenCodeV2Transcript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessOpenCodeV2, root: root}
	if len(paths) == 0 {
		return t, errors.New("no transcript")
	}
	sessionID := paths[0]
	if !openCodeNativeID.MatchString(sessionID) {
		return t, errors.New("invalid OpenCode session id")
	}
	t.nativeID, t.path, t.paths = sessionID, sessionID, []string{sessionID}
	db, err := openCodeDB(root)
	if err != nil {
		return t, err
	}
	if db == nil {
		return t, errNoReadableConversation
	}
	defer func() { _ = db.Close() }()

	var (
		directory             string
		title, parentID       sql.NullString
		timeUpdated, archived sql.NullInt64
	)
	err = db.QueryRowContext(ctx, `
		SELECT directory, title, parent_id, time_updated, time_archived
		FROM session_v2 WHERE id = ?`, sessionID).Scan(&directory, &title, &parentID, &timeUpdated, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return t, errNoReadableConversation
	}
	if err != nil {
		return t, err
	}
	if archived.Valid {
		return t, errors.New("archived conversations are not resumable without unarchiving them in OpenCode")
	}
	if strings.TrimSpace(parentID.String) != "" {
		return t, errors.New("sub-agent sessions belong to their parent conversation")
	}
	t.cwd = directory

	rows, err := db.QueryContext(ctx, `
		SELECT type, data, time_created FROM session_message
		WHERE session_id = ? AND type IN ('user', 'assistant') ORDER BY seq`, sessionID)
	if err != nil {
		return t, err
	}
	defer func() { _ = rows.Close() }()
	now := time.Now()
	var items []tagged
	held, drop := 0, 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return t, err
		}
		var kind, data string
		var created int64
		if err := rows.Scan(&kind, &data, &created); err != nil {
			return t, err
		}
		var m openCodeV2Message
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			continue
		}
		text := m.Text
		if kind == "assistant" {
			var blocks []string
			for _, block := range m.Content {
				if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
					blocks = append(blocks, block.Text)
				}
			}
			text = strings.Join(blocks, "\n\n")
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		if len(text) > maxVisibleBytes {
			text, t.shortened = text[:maxVisibleBytes], true
		}
		at := time.UnixMilli(created)
		if at.After(t.last) && at.Before(now.Add(time.Hour)) {
			t.last = at
		}
		items = append(items, tagged{Message: Message{Role: domain.MessageRole(kind), Text: text, At: at}})
		held += len(text)
		// As the other readers do, drop the oldest text once the held text
		// passes maxHeldText; the opening message stays.
		dropOldestHeld(items, &held, &drop, 0, func(item *tagged) (*string, *bool) { return &item.Text, &t.shortened })
	}
	if err := rows.Err(); err != nil {
		return t, err
	}
	n := len(items)
	items = trimVisible(items)
	t.shortened = t.shortened || len(items) < n
	hasUser := false
	for _, item := range items {
		if item.Text == "" {
			continue
		}
		hasUser = hasUser || item.Role == domain.MessageRoleUser
		t.messages = append(t.messages, item.Message)
	}
	if !hasUser {
		return t, errNoReadableConversation
	}
	if t.last.IsZero() && timeUpdated.Valid {
		t.last = time.UnixMilli(timeUpdated.Int64)
	}
	if name := strings.TrimSpace(title.String); name != openCodeV2Placeholder {
		t.title = name
	}
	return t, finishTranscript(&t, errRelativeCWD)
}
