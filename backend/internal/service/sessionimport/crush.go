package sessionimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // read-only Crush project databases

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type crushProject struct {
	Path    string `json:"path"`
	DataDir string `json:"data_dir"`
}

func crushRoot(home string) string {
	if root := os.Getenv("CRUSH_GLOBAL_DATA"); len(root) > 0 {
		return root
	}
	if root := os.Getenv("XDG_DATA_HOME"); len(root) > 0 {
		return filepath.Join(root, "crush")
	}
	return filepath.Join(home, ".local", "share", "crush")
}

func crushProjects(root string) ([]crushProject, error) {
	provider, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = provider.Close() }()
	file, err := provider.Open("projects.json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var registry struct {
		Projects []crushProject `json:"projects"`
	}
	err = json.NewDecoder(io.LimitReader(file, 4<<20)).Decode(&registry)
	return registry.Projects, err
}

func crushImportDB(path string) (*sql.DB, error) {
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return sql.Open("sqlite", uri.String()+"?mode=ro&_pragma=busy_timeout(2000)")
}

func discoverCrush(ctx context.Context, root string) ([]sourceFiles, error) {
	projects, err := crushProjects(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []sourceFiles
	seen := map[string]bool{}
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(project.Path) || !filepath.IsAbs(project.DataDir) {
			continue
		}
		path := filepath.Join(project.DataDir, "crush.db")
		if seen[path] {
			continue
		}
		seen[path] = true
		db, err := crushImportDB(path)
		if err != nil {
			continue
		}
		rows, err := db.QueryContext(ctx, `SELECT id, updated_at, message_count FROM sessions WHERE parent_session_id IS NULL OR parent_session_id = ''`)
		if err == nil {
			for rows.Next() {
				var id string
				var updated, count int64
				if err := rows.Scan(&id, &updated, &count); err != nil || !nativeIDPattern.MatchString(id) {
					continue
				}
				// A virtual row path preserves both the project DB and native ID
				// for RefreshArchive without introducing shared-file changes.
				out = append(out, sourceFiles{harness: domain.HarnessCrush, root: root, key: id, paths: []string{filepath.Join(path, id)}, modTime: time.Unix(updated, 0), size: count})
			}
			_ = rows.Close()
		}
		_ = db.Close()
	}
	return out, ctx.Err()
}

func readCrushTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: domain.HarnessCrush, root: root}
	if len(paths) != 1 || !nativeIDPattern.MatchString(filepath.Base(paths[0])) || filepath.Base(filepath.Dir(paths[0])) != "crush.db" {
		return t, errors.New("invalid crush conversation reference")
	}
	t.nativeID, t.path, t.paths = filepath.Base(paths[0]), paths[0], paths
	dbPath := filepath.Dir(paths[0])
	projects, err := crushProjects(root)
	if err != nil {
		return t, err
	}
	for _, project := range projects {
		if filepath.Join(project.DataDir, "crush.db") == dbPath && filepath.IsAbs(project.Path) {
			t.cwd = project.Path
			break
		}
	}
	if len(t.cwd) == 0 {
		return t, errors.New("crush database is not in the project registry")
	}
	db, err := crushImportDB(dbPath)
	if err != nil {
		return t, err
	}
	defer func() { _ = db.Close() }()
	var updated int64
	if err := db.QueryRowContext(ctx, `SELECT title, updated_at FROM sessions WHERE id = ? AND (parent_session_id IS NULL OR parent_session_id = '')`, t.nativeID).Scan(&t.title, &updated); err != nil {
		return t, err
	}
	t.last = time.Unix(updated, 0)
	rows, err := db.QueryContext(ctx, `
		SELECT m.id, m.role, m.created_at, json_extract(p.value, '$.data.text')
		FROM messages m, json_each(CASE WHEN json_valid(m.parts) THEN m.parts ELSE '[]' END) p
		WHERE m.session_id = ? AND m.role IN ('user', 'assistant')
		  AND json_extract(p.value, '$.type') = 'text'
		  AND COALESCE(json_extract(p.value, '$.data.hidden'), 0) = 0
		ORDER BY m.created_at, m.rowid, CAST(p.key AS INTEGER)`, t.nativeID)
	if err != nil {
		return t, err
	}
	defer func() { _ = rows.Close() }()
	var items []tagged
	for rows.Next() {
		var id, role string
		var text sql.NullString
		var created int64
		if err := rows.Scan(&id, &role, &created, &text); err != nil {
			return t, err
		}
		if len(strings.TrimSpace(text.String)) == 0 {
			continue
		}
		chunk := text.String
		if len(chunk) > maxVisibleBytes {
			chunk, t.shortened = chunk[:maxVisibleBytes], true
		}
		if len(items) > 0 && items[len(items)-1].id == id {
			last := &items[len(items)-1]
			if len(last.Text)+len(chunk)+1 <= maxVisibleBytes {
				last.Text += "\n" + chunk
			} else {
				t.shortened = true
			}
		} else {
			items = append(items, tagged{Message: Message{Role: domain.MessageRole(role), Text: chunk, At: time.Unix(created, 0)}, id: id, kind: "crush"})
		}
		before := len(items)
		items = trimVisible(items)
		t.shortened = t.shortened || len(items) < before
	}
	if err := rows.Err(); err != nil {
		return t, err
	}
	hasUser := false
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
		if item.Role == domain.MessageRoleUser {
			hasUser = true
			if len(strings.TrimSpace(t.title)) == 0 {
				t.title = item.Text
			}
		}
	}
	if !hasUser {
		return t, errors.New("no readable native conversation")
	}
	t.title = strings.Join(strings.Fields(t.title), " ")
	if runes := []rune(t.title); len(runes) > 100 {
		t.title = string(runes[:100]) + "…"
	}
	return t, nil
}
