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
	groups, err := hermesLineages(ctx, db)
	if err != nil {
		return nil, err
	}
	var out []sourceFiles
	for id, lineage := range groups {
		var updated float64
		var count int64
		for _, segment := range lineage {
			count += segment.count
			if segment.updated > updated {
				updated = segment.updated
			}
		}
		if id == "" || !hermesLineageVisible(lineage) {
			continue
		}
		out = append(out, sourceFiles{harness: domain.HarnessHermes, root: root, key: id, paths: []string{id}, modTime: time.UnixMilli(int64(updated * 1000)), size: count})
	}
	return out, nil
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
	groups, err := hermesLineages(ctx, db)
	if err != nil {
		return t, err
	}
	lineage := groups[t.nativeID]
	if !hermesLineageVisible(lineage) {
		return t, errors.New("no readable native conversation")
	}
	t.cwd = lineage[0].cwd
	title := lineage[0].title
	if !filepath.IsAbs(t.cwd) {
		return t, errors.New("transcript working directory must be absolute")
	}
	values := make([]string, 0, len(lineage))
	args := make([]any, 0, 2*len(lineage)+1)
	for position, segment := range lineage {
		values = append(values, "(?,?)")
		args = append(args, segment.id, position)
	}
	args = append(args, maxVisibleBytes)
	rows, err := db.QueryContext(ctx, `WITH lineage(id,position) AS (VALUES `+strings.Join(values, ",")+`)
		SELECT m.role,substr(m.content,1,?),length(m.content),m.timestamp FROM messages m
		JOIN lineage l ON l.id=m.session_id WHERE m.role IN ('user','assistant') AND COALESCE(m._compressed_summary,0)=0
		ORDER BY l.position,m.id`, args...)
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
		if text == "" || strings.HasPrefix(text, "[Context from previous conversation]") {
			continue
		}
		if role == "user" {
			hasUser = true
			if t.title == "" {
				t.title = text
			}
		}
		items = append(items, tagged{Message: Message{Role: domain.MessageRole(role), Text: text, At: at}})
		before := len(items)
		items = trimVisible(items)
		t.shortened = t.shortened || len(items) < before
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
	if strings.TrimSpace(title) != "" {
		t.title = title
	}
	t.title = importTitle(t.title)
	return t, nil
}
