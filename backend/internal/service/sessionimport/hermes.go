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

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hermes"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func hermesRoot(home string) string {
	root, err := hermes.ResolveHome(map[string]string{"HOME": home})
	if err != nil {
		return ""
	}
	return root
}

func hermesDB(root string) (*sql.DB, error) {
	path := filepath.Join(root, "state.db")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro&_pragma=busy_timeout(2000)"}
	return sql.Open("sqlite", u.String())
}

func discoverHermes(ctx context.Context, root string) ([]sourceFiles, error) {
	db, err := hermesDB(root)
	if err != nil || db == nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(ctx, `SELECT s.id,COALESCE(MAX(m.timestamp),s.started_at),COUNT(m.id)
		FROM sessions s LEFT JOIN messages m ON m.session_id=s.id
		WHERE s.source IN ('cli','tui','oneshot') AND COALESCE(s.hidden,0)=0
		GROUP BY s.id ORDER BY 2 DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []sourceFiles
	for rows.Next() {
		var id string
		var updated float64
		var count int64
		if err := rows.Scan(&id, &updated, &count); err != nil {
			return nil, err
		}
		if len(id) == 0 {
			continue
		}
		out = append(out, sourceFiles{harness: domain.HarnessHermes, root: root, key: id, paths: []string{id}, modTime: time.UnixMilli(int64(updated * 1000)), size: count})
	}
	return out, rows.Err()
}

func readHermesTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessHermes, root: root, paths: paths}
	if len(paths) != 1 {
		return t, errors.New("invalid hermes conversation handle")
	}
	t.path, t.nativeID = paths[0], paths[0]
	db, err := hermesDB(root)
	if err != nil {
		return t, err
	}
	if db == nil {
		return t, errors.New("no readable native conversation")
	}
	defer func() { _ = db.Close() }()
	var cwd, title sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT cwd,title FROM sessions WHERE id=? AND source IN ('cli','tui','oneshot') AND COALESCE(hidden,0)=0`, t.nativeID).Scan(&cwd, &title); err != nil {
		return t, err
	}
	t.cwd = cwd.String
	if !filepath.IsAbs(t.cwd) {
		return t, errors.New("transcript working directory must be absolute")
	}
	rows, err := db.QueryContext(ctx, `SELECT role,substr(content,1,?),length(content),timestamp FROM messages
		WHERE session_id=? AND role IN ('user','assistant') AND COALESCE(_compressed_summary,0)=0
		ORDER BY id`, maxVisibleBytes, t.nativeID)
	if err != nil {
		return t, err
	}
	defer func() { _ = rows.Close() }()
	var items []tagged
	hasUser := false
	for rows.Next() {
		var role string
		var raw sql.NullString
		var size sql.NullInt64
		var stamp float64
		if err := rows.Scan(&role, &raw, &size, &stamp); err != nil {
			return t, err
		}
		at := time.UnixMilli(int64(stamp * 1000))
		if at.After(t.last) {
			t.last = at
		}
		if size.Int64 > int64(maxVisibleBytes) || len(raw.String) > maxVisibleBytes {
			t.shortened = true
			continue
		}
		text := strings.TrimSpace(raw.String)
		// Hermes stores plain text or serialized multimodal content blocks.
		if strings.HasPrefix(text, "[") && json.Valid([]byte(text)) {
			text = visibleText([]byte(text))
		}
		if len(text) == 0 || strings.HasPrefix(text, "[Context from previous conversation]") {
			continue
		}
		if role == "user" {
			hasUser = true
			if len(t.title) == 0 {
				t.title = text
			}
		}
		items = append(items, tagged{Message: Message{Role: domain.MessageRole(role), Text: text, At: at}})
		if len(items)%100 == 0 {
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
	if len(strings.TrimSpace(title.String)) > 0 {
		t.title = title.String
	}
	t.title = strings.Join(strings.Fields(t.title), " ")
	if runes := []rune(t.title); len(runes) > 100 {
		t.title = string(runes[:100]) + "…"
	}
	return t, nil
}
