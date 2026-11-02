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

// Goose uses a single global database, independent of the launch directory.
// GOOSE_PATH_ROOT contains config/data/state; XDG_DATA_HOME contains goose/.
func gooseRoot(home string) string {
	if root := os.Getenv("GOOSE_PATH_ROOT"); filepath.IsAbs(root) {
		return filepath.Join(root, "data")
	}
	if root := os.Getenv("XDG_DATA_HOME"); root != "" {
		return filepath.Join(root, "goose")
	}
	return filepath.Join(home, ".local", "share", "goose")
}

func gooseDB(root string) (*sql.DB, error) {
	path := filepath.Join(root, "sessions", "sessions.db")
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro&_pragma=busy_timeout(2000)"}
	return sql.Open("sqlite", u.String())
}

func gooseTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999"} {
		if at, err := time.Parse(layout, value); err == nil {
			return at
		}
	}
	return time.Time{}
}

func discoverGoose(ctx context.Context, root string) ([]sourceFiles, error) {
	db, err := gooseDB(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, `SELECT s.id, s.updated_at, COUNT(m.id), COALESCE(MAX(m.id), 0)
		FROM sessions s LEFT JOIN messages m ON m.session_id = s.id
		WHERE s.session_type = 'user' AND s.archived_at IS NULL
		AND (s.parent_session_id IS NULL OR s.parent_session_id = '') GROUP BY s.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var files []sourceFiles
	for rows.Next() {
		var id, updated string
		var count, lastID int64
		if err := rows.Scan(&id, &updated, &count, &lastID); err != nil {
			return nil, err
		}
		files = append(files, sourceFiles{harness: domain.HarnessGoose, root: root, key: id, paths: []string{id}, modTime: gooseTime(updated), size: count + lastID})
	}
	return files, rows.Err()
}

func readGooseTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessGoose, root: root}
	if len(paths) != 1 || strings.TrimSpace(paths[0]) == "" {
		return t, errors.New("no goose session id")
	}
	t.nativeID, t.path, t.paths = paths[0], paths[0], paths
	db, err := gooseDB(root)
	if err != nil {
		return t, err
	}
	defer func() { _ = db.Close() }()
	var name, description, updated string
	err = db.QueryRowContext(ctx, `SELECT working_dir, name, description, updated_at FROM sessions
		WHERE id = ? AND session_type = 'user' AND archived_at IS NULL
		AND (parent_session_id IS NULL OR parent_session_id = '')`, t.nativeID).Scan(&t.cwd, &name, &description, &updated)
	if err != nil {
		return t, err
	}
	t.last = gooseTime(updated)
	rows, err := db.QueryContext(ctx, `SELECT role, content_json, created_timestamp FROM messages
		WHERE session_id = ? AND role IN ('user', 'assistant')
		AND COALESCE(json_extract(metadata_json, '$.userVisible'), 1) != 0 ORDER BY created_timestamp, id`, t.nativeID)
	if err != nil {
		return t, err
	}
	defer func() { _ = rows.Close() }()
	visible := 0
	for rows.Next() {
		var role, content string
		var stamp int64
		if err := rows.Scan(&role, &content, &stamp); err != nil {
			return t, err
		}
		var blocks []textBlock
		if json.Unmarshal([]byte(content), &blocks) != nil {
			continue
		}
		text := strings.TrimSpace(blockText(blocks))
		if text == "" {
			continue
		}
		at := time.Unix(stamp, 0)
		if stamp > 100000000000 {
			at = time.UnixMilli(stamp)
		}
		if at.After(t.last) {
			t.last = at
		}
		if visible+len(text) > maxVisibleBytes {
			t.shortened = true
			continue
		}
		visible += len(text)
		t.messages = append(t.messages, Message{Role: domain.MessageRole(role), Text: text, At: at})
		if t.title == "" && role == "user" {
			t.title = importTitle(text)
		}
	}
	if err := rows.Err(); err != nil {
		return t, err
	}
	if len(t.messages) == 0 {
		return t, errors.New("no readable goose conversation")
	}
	if strings.TrimSpace(name) != "" && name != "New Chat" {
		t.title = importTitle(name)
	} else if t.title == "" {
		t.title = importTitle(description)
	}
	return t, nil
}
