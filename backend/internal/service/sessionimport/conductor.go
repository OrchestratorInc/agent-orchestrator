package sessionimport

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const conductorSource domain.AgentHarness = "conductor"

var errClaudeHasConversation = errors.New("claude code already holds this conversation; scan again")

var claudeProjectDirChars = regexp.MustCompile(`[^a-zA-Z0-9]`)

func conductorRoot(home string) string {
	if runtime.GOOS != "darwin" || home == "" {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "com.conductor.app")
}

func conductorDB(root string) (*sql.DB, error) {
	path := filepath.Join(root, "conductor.db")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	dsn := opencode.ReadOnlyDSN(path, 2000)
	if _, err := os.Stat(path + "-wal"); errors.Is(err, os.ErrNotExist) {
		dsn += "&immutable=1"
	}
	return sql.Open("sqlite", dsn)
}

type conductorChat struct {
	id, agent, nativeID, title, cwd, checkout string
	updated                                   time.Time
	count                                     int64
}

func conductorColumns(ctx context.Context, db *sql.DB) (map[string]map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT t.name, c.name FROM sqlite_master t JOIN pragma_table_info(t.name) c WHERE t.type = 'table' AND t.name IN ('sessions', 'workspaces', 'session_messages', 'session_events', 'repos')`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	cols := map[string]map[string]bool{"sessions": {}, "workspaces": {}, "session_messages": {}, "session_events": {}, "repos": {}}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return nil, err
		}
		cols[table][column] = true
	}
	return cols, rows.Err()
}

func conductorEvents(cols map[string]map[string]bool) bool {
	e := cols["session_events"]
	return e["session_id"] && e["session_index"] && e["event_type"] && e["content"] && e["received_at"] && e["reverted_at"]
}

func conductorChats(ctx context.Context, db *sql.DB, sessionID string) ([]conductorChat, error) {
	cols, err := conductorColumns(ctx, db)
	if err != nil {
		return nil, err
	}
	need := map[string][]string{
		"sessions":         {"id", "workspace_id", "agent_type", "claude_session_id", "title", "is_hidden", "updated_at"},
		"workspaces":       {"id", "local_id", "repository_id", "directory_name", "workspace_path"},
		"repos":            {"id", "name", "root_path", "storage_version"},
		"session_messages": {"session_id", "role", "content", "created_at", "sent_at"},
	}
	for table, names := range need {
		for _, name := range names {
			if !cols[table][name] {
				return nil, errors.New("conductor.db has an unknown layout")
			}
		}
	}
	nativeID, count := "COALESCE(NULLIF(s.claude_session_id, ''), s.id)", "(SELECT COUNT(*) FROM session_messages m WHERE m.session_id = s.id AND m.sent_at IS NOT NULL)"
	if conductorEvents(cols) {
		nativeID = `COALESCE((SELECT id FROM (
			SELECT CASE WHEN json_valid(e.content) THEN COALESCE(json_extract(e.content, '$.rawPayload.thread_id'), json_extract(e.content, '$.rawPayload.session_id')) END AS id, e.session_index
			FROM session_events e WHERE e.session_id = s.id AND e.event_type = 'agent' AND e.reverted_at IS NULL)
			WHERE id IS NOT NULL ORDER BY session_index DESC LIMIT 1), ` + strings.TrimPrefix(nativeID, "COALESCE(")
		count += " + (SELECT COUNT(*) FROM session_events e WHERE e.session_id = s.id AND e.reverted_at IS NULL AND e.event_type IN ('userMessage', 'agent'))"
	}
	where, args := "COALESCE(s.agent_type, 'claude') IN ('claude', 'codex') AND COALESCE(s.is_hidden, 0) = 0", []any{}
	if sessionID != "" {
		where, args = where+" AND s.id = ?", append(args, sessionID)
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT s.id, COALESCE(s.agent_type, 'claude'), %s, COALESCE(s.title, ''), COALESCE(w.workspace_path, ''),
		       COALESCE(r.root_path, ''), COALESCE(r.name, ''), COALESCE(r.storage_version, 1), COALESCE(w.directory_name, ''), s.updated_at, %s
		FROM sessions s
		JOIN workspaces w ON s.workspace_id IN (w.local_id, w.id)
		LEFT JOIN repos r ON r.id = w.repository_id
		WHERE %s
		ORDER BY s.updated_at DESC`, nativeID, count, where), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	workspaces := conductorWorkspacesRoot(ctx, db)
	var out []conductorChat
	for rows.Next() {
		var chat conductorChat
		var updated any
		var repoName, dir string
		var version int64
		if err := rows.Scan(&chat.id, &chat.agent, &chat.nativeID, &chat.title, &chat.cwd, &chat.checkout, &repoName, &version, &dir, &updated, &chat.count); err != nil {
			return nil, err
		}
		if chat.cwd == "" {
			chat.cwd = conductorWorkspacePath(version, chat.checkout, filepath.Join(workspaces, repoName), dir)
		}
		if version == 1 && chat.checkout != "" {
			chat.checkout = filepath.Join(chat.checkout, ".main")
		}
		if !filepath.IsAbs(chat.checkout) {
			chat.checkout = ""
		}
		if chat.count > 0 && nativeIDPattern.MatchString(chat.nativeID) {
			chat.updated = conductorTime(updated)
			out = append(out, chat)
		}
	}
	return out, rows.Err()
}

func conductorWorkspacesRoot(ctx context.Context, db *sql.DB) string {
	var root string
	_ = db.QueryRowContext(ctx, `SELECT TRIM(value) FROM settings WHERE key = 'conductor_root_path'`).Scan(&root)
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "conductor")
	}
	return filepath.Join(root, "workspaces")
}

func conductorWorkspacePath(version int64, checkout, repoWorkspaces, dir string) string {
	var path string
	switch {
	case dir == "":
	case version == 1 && dir == ".":
		path = filepath.Join(checkout, ".main")
	case version == 1:
		path = filepath.Join(checkout, dir)
	case version == 2 && dir == ".":
		path = checkout
	case version == 2:
		path = filepath.Join(checkout, ".conductor", dir)
	default:
		path = filepath.Join(repoWorkspaces, dir)
	}
	if !filepath.IsAbs(path) {
		return ""
	}
	return path
}

func conductorTime(value any) time.Time {
	switch v := value.(type) {
	case time.Time:
		return v.UTC()
	case int64:
		if v > 1e12 {
			return time.UnixMilli(v)
		}
		return time.Unix(v, 0)
	case []byte:
		return conductorTime(string(v))
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999"} {
			if at, err := time.Parse(layout, v); err == nil {
				return at
			}
		}
	}
	return time.Time{}
}

func claudeTranscripts(root string) map[string]string {
	ids := map[string]string{}
	dirs, _ := os.ReadDir(filepath.Join(root, "projects"))
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(root, "projects", dir.Name()))
		for _, file := range files {
			if id, ok := strings.CutSuffix(file.Name(), ".jsonl"); ok {
				ids[id] = filepath.Join(root, "projects", dir.Name(), file.Name())
			}
		}
	}
	return ids
}

func conductorClaudeRoot() string {
	home, _ := os.UserHomeDir()
	return canonical(claudeRoot(home))
}

func conductorCodexRoot() string {
	home, _ := os.UserHomeDir()
	return canonical(codexRoot(home))
}

func discoverConductor(ctx context.Context, root string) ([]sourceFiles, error) {
	db, err := conductorDB(root)
	if err != nil || db == nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	chats, err := conductorChats(ctx, db, "")
	if err != nil {
		return nil, err
	}
	claude := claudeTranscripts(conductorClaudeRoot())
	codex, err := walkSources(domain.HarnessCodex, "sessions", codexKey)(ctx, conductorCodexRoot())
	if err != nil {
		return nil, err
	}
	rollouts := make(map[string]sourceFiles, len(codex))
	for _, rollout := range codex {
		rollouts[rollout.key] = rollout
	}
	out := make([]sourceFiles, 0, len(chats))
	for _, chat := range chats {
		file := sourceFiles{harness: conductorSource, root: root, key: chat.nativeID, paths: []string{chat.id}, modTime: chat.updated, size: chat.count}
		if rollout, ok := rollouts[chat.nativeID]; ok && chat.agent == "codex" {
			file.paths, file.modTime, file.size = append(file.paths, rollout.paths...), rollout.modTime, rollout.size
		} else if path := claude[chat.nativeID]; path != "" && chat.agent == "claude" {
			if info, err := os.Stat(path); err == nil {
				file.paths, file.modTime, file.size = append(file.paths, path), info.ModTime(), info.Size()
			}
		}
		out = append(out, file)
	}
	return out, nil
}

type conductorRecord struct {
	role    string
	message map[string]json.RawMessage
	blocks  []map[string]json.RawMessage
	uuid    string
	at      time.Time
}

func eachConductorRecord(ctx context.Context, db *sql.DB, sessionID string, fn func(conductorRecord) error) (version string, err error) {
	cols, err := conductorColumns(ctx, db)
	if err != nil {
		return "", err
	}
	query, args := `SELECT 0, role, content, created_at, sent_at, rowid FROM session_messages WHERE session_id = ? AND sent_at IS NOT NULL`, []any{sessionID}
	if conductorEvents(cols) {
		query += ` UNION ALL SELECT 1, event_type, content, received_at, '', session_index FROM session_events WHERE session_id = ? AND reverted_at IS NULL AND event_type IN ('userMessage', 'agent')`
		args = append(args, sessionID)
	}
	rows, err := db.QueryContext(ctx, query+` ORDER BY 1, 5, 6`, args...)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	prompts := map[string]bool{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var event int
		var role, content sql.NullString
		var created, sent, seq any
		if err := rows.Scan(&event, &role, &content, &created, &sent, &seq); err != nil {
			return "", err
		}
		if event == 1 {
			var e struct {
				ID, Message, State string
				RawPayload         json.RawMessage `json:"rawPayload"`
			}
			if json.Unmarshal([]byte(content.String), &e) != nil {
				continue
			}
			switch {
			case role.String == "agent":
				content.String = string(e.RawPayload)
			case e.State == "sent" && !prompts[e.ID]:
				prompts[e.ID] = true
				text, _ := json.Marshal(e.Message)
				rec := conductorRecord{role: "user", message: map[string]json.RawMessage{"role": json.RawMessage(`"user"`), "content": text}, at: conductorTime(created)}
				if err := fn(rec); err != nil {
					return "", err
				}
				continue
			default:
				continue
			}
		}
		var sdk struct {
			Type      string                     `json:"type"`
			SessionID string                     `json:"session_id"`
			Message   map[string]json.RawMessage `json:"message"`
			Parent    *string                    `json:"parent_tool_use_id"`
			UUID      string                     `json:"uuid"`
			Timestamp string                     `json:"timestamp"`
			Version   string                     `json:"claude_code_version"`
		}
		rec := conductorRecord{at: conductorTime(created)}
		switch {
		case json.Unmarshal([]byte(content.String), &sdk) == nil && (sdk.SessionID != "" || sdk.Message != nil):
			if sdk.Version != "" {
				version = sdk.Version
			}
			if (sdk.Type != "user" && sdk.Type != "assistant") || sdk.Message == nil || sdk.Parent != nil && *sdk.Parent != "" {
				continue
			}
			rec.role, rec.message, rec.uuid = sdk.Type, sdk.Message, sdk.UUID
			if at := conductorTime(sdk.Timestamp); !at.IsZero() {
				rec.at = at
			}
		case role.String == "user" && strings.TrimSpace(content.String) != "":
			text, _ := json.Marshal(content.String)
			rec.role, rec.message = "user", map[string]json.RawMessage{"role": json.RawMessage(`"user"`), "content": text}
		default:
			continue
		}
		_ = json.Unmarshal(rec.message["content"], &rec.blocks)
		if err := fn(rec); err != nil {
			return "", err
		}
	}
	return version, rows.Err()
}

func rawString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func readConductorTranscript(ctx context.Context, root string, paths ...string) (transcript, error) {
	t := transcript{harness: conductorSource, root: root, paths: paths}
	if len(paths) == 0 {
		return t, errors.New("no transcript")
	}
	db, err := conductorDB(root)
	if err != nil || db == nil {
		return t, errors.Join(errNoReadableConversation, err)
	}
	defer func() { _ = db.Close() }()
	chats, err := conductorChats(ctx, db, paths[0])
	if err != nil || len(chats) != 1 {
		return t, errors.Join(errNoReadableConversation, err)
	}
	chat := chats[0]
	named := func(t transcript, err error) (transcript, error) {
		if chat.title != "Untitled" && chat.title != "" {
			t.title = importTitle(chat.title)
		}
		t.placeAt = chat.checkout
		return t, err
	}
	switch native := paths[1:]; {
	case chat.agent == "codex" && len(native) > 0:
		return named(readCodexTranscript(ctx, conductorCodexRoot(), native...))
	case chat.agent == "codex":
		return t, errNoReadableConversation
	case len(native) > 0:
		return named(readClaudeTranscript(ctx, conductorClaudeRoot(), native...))
	case !filepath.IsAbs(chat.cwd):
		return t, errNoReadableConversation
	}
	t.nativeID, t.cwd, t.path = chat.nativeID, chat.cwd, paths[0]
	var items []tagged
	prompted, visible := false, 0
	_, err = eachConductorRecord(ctx, db, chat.id, func(r conductorRecord) error {
		if r.at.After(t.last) {
			t.last = r.at
		}
		text := visibleText(r.message["content"])
		if r.role == "user" {
			var command bool
			text, command = claudePrompt(text)
			prompted = prompted || !command && strings.TrimSpace(text) != ""
		}
		if strings.TrimSpace(text) == "" {
			return nil
		}
		visible += len(text)
		id := rawString(r.message["id"])
		if n := len(items); r.role == "assistant" && id != "" && n > 0 && items[n-1].id == id {
			items[n-1].Text += "\n\n" + text
		} else {
			items = append(items, tagged{Message: Message{Role: domain.MessageRole(r.role), Text: text, At: r.at}, kind: "claude", id: id})
		}
		if visible > 2*maxVisibleBytes {
			n := len(items)
			items = trimVisible(items)
			t.shortened, visible = t.shortened || len(items) < n, 0
			for _, item := range items {
				visible += len(item.Text)
			}
		}
		return nil
	})
	if err != nil {
		return t, err
	}
	n := len(items)
	items = trimVisible(items)
	t.shortened = t.shortened || len(items) < n
	for _, item := range items {
		t.messages = append(t.messages, item.Message)
	}
	if !prompted {
		return t, errNoReadableConversation
	}
	err = finishTranscript(&t)
	return named(t, err)
}

type claudeLine struct {
	ParentUUID  *string         `json:"parentUuid"`
	IsSidechain bool            `json:"isSidechain"`
	UserType    string          `json:"userType"`
	CWD         string          `json:"cwd"`
	SessionID   string          `json:"sessionId"`
	Version     string          `json:"version,omitempty"`
	Type        string          `json:"type"`
	Message     json.RawMessage `json:"message"`
	UUID        string          `json:"uuid"`
	Timestamp   string          `json:"timestamp"`
}

func writeConductorTranscript(ctx context.Context, t transcript) (transcript, error) {
	root := conductorClaudeRoot()
	if claudeTranscripts(root)[t.nativeID] != "" {
		return t, errClaudeHasConversation
	}
	db, err := conductorDB(t.root)
	if err != nil || db == nil {
		return t, errors.Join(errNoReadableConversation, err)
	}
	defer func() { _ = db.Close() }()
	calls, results := map[string]bool{}, map[string]bool{}
	version, err := eachConductorRecord(ctx, db, t.paths[0], func(r conductorRecord) error {
		for _, block := range r.blocks {
			switch rawString(block["type"]) {
			case "tool_use":
				calls[rawString(block["id"])] = true
			case "tool_result":
				results[rawString(block["tool_use_id"])] = true
			}
		}
		return nil
	})
	if err != nil {
		return t, err
	}
	dir := filepath.Join(root, "projects", claudeProjectDirChars.ReplaceAllString(t.cwd, "-"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return t, err
	}
	file, err := os.CreateTemp(dir, ".conductor-import-*")
	if err != nil {
		return t, err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	out := bufio.NewWriter(file)
	encoder := json.NewEncoder(out)
	var parent *string
	seen := map[string]bool{}
	_, err = eachConductorRecord(ctx, db, t.paths[0], func(r conductorRecord) error {
		if r.blocks != nil {
			kept := r.blocks[:0]
			for _, block := range r.blocks {
				switch rawString(block["type"]) {
				case "tool_use":
					if !results[rawString(block["id"])] {
						continue
					}
				case "tool_result":
					if !calls[rawString(block["tool_use_id"])] {
						continue
					}
				}
				kept = append(kept, block)
			}
			if len(kept) == 0 {
				return nil
			}
			r.message["content"], _ = json.Marshal(kept)
		}
		id := r.uuid
		if uuid.Validate(id) != nil || seen[id] {
			id = uuid.NewString()
		}
		seen[id] = true
		message, err := json.Marshal(r.message)
		if err != nil {
			return err
		}
		line := claudeLine{ParentUUID: parent, UserType: "external", CWD: t.cwd, SessionID: t.nativeID, Version: version, Type: r.role, Message: message, UUID: id, Timestamp: r.at.UTC().Format("2006-01-02T15:04:05.000Z")}
		parent = &id
		return encoder.Encode(line)
	})
	if err == nil {
		err = out.Flush()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return t, err
	}
	path := filepath.Join(dir, t.nativeID+".jsonl")
	if err := os.Link(file.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return t, errClaudeHasConversation
		}
		return t, err
	}
	t.harness, t.root, t.path, t.paths = domain.HarnessClaudeCode, root, path, []string{path}
	return t, nil
}
