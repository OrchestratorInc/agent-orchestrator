package sessionimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func devinRoot(home string) string {
	if root := os.Getenv("XDG_DATA_HOME"); root != "" {
		return filepath.Join(root, "devin", "cli")
	}
	return filepath.Join(home, ".local", "share", "devin", "cli")
}

// devinTime reads a Devin timestamp. The CLI writes Unix seconds; values too
// large to be seconds (anything after 2286) are taken as milliseconds.
func devinTime(v int64) time.Time {
	if v > 0 && v < 1e10 {
		return time.Unix(v, 0)
	}
	return time.UnixMilli(v)
}

func devinDB(root string) (*sql.DB, error) {
	path := filepath.Join(root, "sessions.db")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro&_pragma=busy_timeout(2000)"}
	return sql.Open("sqlite", u.String())
}

func discoverDevin(ctx context.Context, root string) ([]sourceFiles, error) {
	db, err := devinDB(root)
	if err != nil || db == nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, `SELECT id, last_activity_at,
		(SELECT COUNT(*) FROM message_nodes WHERE session_id=s.id)
		FROM sessions s WHERE hidden=0 ORDER BY last_activity_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []sourceFiles
	for rows.Next() {
		var id string
		var updated, count int64
		if err := rows.Scan(&id, &updated, &count); err != nil {
			return nil, err
		}
		if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
			continue
		}
		out = append(out, sourceFiles{harness: domain.HarnessDevin, root: root, key: id, paths: []string{id}, modTime: devinTime(updated), size: count})
	}
	return out, rows.Err()
}

// The main chain is a head node, not the largest node id. UNION (not UNION ALL)
// also bounds a corrupt cyclic graph. Other branches and subagent heads stay out.
func readDevinTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessDevin, root: root, paths: paths}
	if len(paths) != 1 {
		return t, errors.New("invalid devin conversation handle")
	}
	t.path, t.nativeID = paths[0], paths[0]
	db, err := devinDB(root)
	if err != nil {
		return t, err
	}
	if db == nil {
		return t, errors.New("no readable native conversation")
	}
	defer func() { _ = db.Close() }()
	var updated int64
	var title sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT working_directory, title, last_activity_at FROM sessions WHERE id=? AND hidden=0`, t.nativeID).Scan(&t.cwd, &title, &updated); err != nil {
		return t, err
	}
	if !filepath.IsAbs(t.cwd) {
		return t, errors.New("transcript working directory must be absolute")
	}
	t.last = devinTime(updated)
	rows, err := db.QueryContext(ctx, `WITH RECURSIVE chain(node_id,parent_node_id) AS (
		SELECT node_id,parent_node_id FROM message_nodes WHERE session_id=? AND node_id=(SELECT main_chain_id FROM sessions WHERE id=?)
		UNION SELECT n.node_id,n.parent_node_id FROM message_nodes n JOIN chain c ON n.node_id=c.parent_node_id WHERE n.session_id=?
	) SELECT substr(n.chat_message,1,?),length(n.chat_message),n.created_at FROM message_nodes n JOIN chain c ON c.node_id=n.node_id
		WHERE n.session_id=? ORDER BY n.node_id`, t.nativeID, t.nativeID, t.nativeID, maxVisibleBytes, t.nativeID)
	if err != nil {
		return t, err
	}
	defer func() { _ = rows.Close() }()
	var items []tagged
	hasUser := false
	for rows.Next() {
		var raw string
		var size, at int64
		if err := rows.Scan(&raw, &size, &at); err != nil {
			return t, err
		}
		if size > int64(maxVisibleBytes) || len(raw) > maxVisibleBytes {
			t.shortened = true
			continue
		}
		var m struct {
			Role     string      `json:"role"`
			Content  contentText `json:"content"`
			Metadata struct {
				IsUserInput bool `json:"is_user_input"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			continue
		}
		if m.Role != "assistant" && (m.Role != "user" || !m.Metadata.IsUserInput) {
			continue
		}
		text := strings.TrimSpace(string(m.Content))
		if text == "" {
			continue
		}
		if m.Role == "user" {
			hasUser = true
			if t.title == "" {
				t.title = text
			}
		}
		items = append(items, tagged{Message: Message{Role: domain.MessageRole(m.Role), Text: text, At: devinTime(at)}})
		if len(items) > 2 {
			before := len(items)
			items = trimVisible(items)
			t.shortened = t.shortened || len(items) < before
		}
	}
	if err := rows.Err(); err != nil {
		return t, err
	}
	if !hasUser {
		return t, errors.New("no readable native conversation")
	}
	before := len(items)
	items = trimVisible(items)
	t.shortened = t.shortened || len(items) < before
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
	}
	if strings.TrimSpace(title.String) != "" {
		t.title = title.String
	}
	t.title = importTitle(t.title)
	return t, nil
}
