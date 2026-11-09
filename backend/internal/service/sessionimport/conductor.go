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

// conductorSource reads Claude Code chats that Conductor keeps in conductor.db
// and has no Claude Code transcript for on this Mac, such as workspaces moved
// from Conductor Cloud. Its chats import as Claude Code chats.
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
	return sql.Open("sqlite", opencode.ReadOnlyDSN(path, 2000))
}

type conductorChat struct {
	id, nativeID, title, cwd string
	updated                  time.Time
	count                    int64
}

func conductorColumns(ctx context.Context, db *sql.DB) (map[string]map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT t.name, c.name FROM sqlite_master t JOIN pragma_table_info(t.name) c WHERE t.type = 'table' AND t.name IN ('sessions', 'workspaces', 'session_messages', 'repos')`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	cols := map[string]map[string]bool{"sessions": {}, "workspaces": {}, "session_messages": {}, "repos": {}}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return nil, err
		}
		cols[table][column] = true
	}
	return cols, rows.Err()
}

// conductorChats lists live Claude chats with sent messages, newest first.
// Conductor adds columns over time, so optional ones are checked first.
func conductorChats(ctx context.Context, db *sql.DB, sessionID string) ([]conductorChat, error) {
	cols, err := conductorColumns(ctx, db)
	if err != nil {
		return nil, err
	}
	s, w, m := cols["sessions"], cols["workspaces"], cols["session_messages"]
	if !s["id"] || !s["workspace_id"] || !s["title"] || !s["updated_at"] || !w["id"] || !w["workspace_path"] || !m["session_id"] || !m["role"] || !m["content"] || !m["created_at"] || !m["sent_at"] {
		return nil, errors.New("conductor.db has an unknown layout")
	}
	nativeID, join := "s.id", "w.id"
	if s["claude_session_id"] {
		nativeID = "COALESCE(NULLIF(s.claude_session_id, ''), s.id)"
	}
	if w["local_id"] {
		join = "w.local_id, w.id"
	}
	where := []string{"1 = 1"}
	for _, filter := range []struct {
		has  bool
		test string
	}{
		{s["agent_type"], "COALESCE(s.agent_type, 'claude') = 'claude'"},
		{s["is_hidden"], "COALESCE(s.is_hidden, 0) = 0"},
		{w["state"], "COALESCE(w.state, '') NOT IN ('archived', 'archiving')"},
		{w["DEPRECATED_archived"], "COALESCE(w.DEPRECATED_archived, 0) = 0"},
		{w["remote_archived_at"], "w.remote_archived_at IS NULL"},
	} {
		if filter.has {
			where = append(where, filter.test)
		}
	}
	var args []any
	if sessionID != "" {
		where, args = append(where, "s.id = ?"), append(args, sessionID)
	}
	repo, from := "'', '', 0, ''", "sessions s JOIN workspaces w ON s.workspace_id IN ("+join+")"
	if r := cols["repos"]; r["id"] && r["root_path"] && r["name"] && r["storage_version"] && w["repository_id"] && w["directory_name"] {
		repo = "COALESCE(r.root_path, ''), COALESCE(r.name, ''), COALESCE(r.storage_version, 0), COALESCE(w.directory_name, '')"
		from += " LEFT JOIN repos r ON r.id = w.repository_id"
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT s.id, %s, COALESCE(s.title, ''), COALESCE(w.workspace_path, ''), %s, s.updated_at,
		       (SELECT COUNT(*) FROM session_messages m WHERE m.session_id = s.id AND m.sent_at IS NOT NULL)
		FROM %s
		WHERE %s
		ORDER BY s.updated_at DESC`, nativeID, repo, from, strings.Join(where, " AND ")), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	workspaces := conductorWorkspacesRoot(ctx, db)
	var out []conductorChat
	for rows.Next() {
		var chat conductorChat
		var updated any
		var checkout, repoName, dir string
		var version int64
		if err := rows.Scan(&chat.id, &chat.nativeID, &chat.title, &chat.cwd, &checkout, &repoName, &version, &dir, &updated, &chat.count); err != nil {
			return nil, err
		}
		if chat.cwd == "" {
			chat.cwd = conductorWorkspacePath(version, checkout, filepath.Join(workspaces, repoName), dir)
		}
		if chat.count > 0 && nativeIDPattern.MatchString(chat.nativeID) {
			chat.updated = conductorTime(updated)
			out = append(out, chat)
		}
	}
	return out, rows.Err()
}

// conductorWorkspacesRoot is where Conductor creates workspaces by default.
func conductorWorkspacesRoot(ctx context.Context, db *sql.DB) string {
	var root string
	_ = db.QueryRowContext(ctx, `SELECT TRIM(value) FROM settings WHERE key = 'conductor_root_path'`).Scan(&root)
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, "conductor")
	}
	return filepath.Join(root, "workspaces")
}

// conductorWorkspacePath places a workspace that has no recorded path the way
// Conductor does for the repository's storage_version.
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

// conductorTime reads SQLite datetime('now') text, which is UTC without a zone.
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

// claudeTranscriptIDs names every conversation Claude Code holds under root.
func claudeTranscriptIDs(root string) map[string]bool {
	ids := map[string]bool{}
	dirs, _ := os.ReadDir(filepath.Join(root, "projects"))
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(root, "projects", dir.Name()))
		for _, file := range files {
			if id, ok := strings.CutSuffix(file.Name(), ".jsonl"); ok {
				ids[id] = true
			}
		}
	}
	return ids
}

func conductorClaudeRoot() string {
	home, _ := os.UserHomeDir()
	return canonical(claudeRoot(home))
}

func discoverConductor(ctx context.Context, root string) ([]sourceFiles, error) {
	db, err := conductorDB(root)
	if err != nil || db == nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	chats, err := conductorChats(ctx, db, "")
	if err != nil || len(chats) == 0 {
		return nil, err
	}
	known := claudeTranscriptIDs(conductorClaudeRoot())
	var out []sourceFiles
	for _, chat := range chats {
		if !known[chat.nativeID] {
			out = append(out, sourceFiles{harness: conductorSource, root: root, key: chat.nativeID, paths: []string{chat.id}, modTime: chat.updated, size: chat.count})
		}
	}
	return out, nil
}

// conductorRecord is one sent message as Claude Code records it.
type conductorRecord struct {
	role    string
	message map[string]json.RawMessage
	blocks  []map[string]json.RawMessage
	uuid    string
	at      time.Time
}

// eachConductorRecord reads a chat in Amp's order. Assistant rows and tool
// results hold Claude Agent SDK stream JSON; a typed prompt can be plain text.
// Subagent messages and SDK status records are not part of the conversation.
func eachConductorRecord(ctx context.Context, db *sql.DB, sessionID string, fn func(conductorRecord) error) (version string, err error) {
	rows, err := db.QueryContext(ctx, `SELECT role, content, created_at FROM session_messages WHERE session_id = ? AND sent_at IS NOT NULL ORDER BY sent_at, rowid`, sessionID)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var role, content sql.NullString
		var created any
		if err := rows.Scan(&role, &content, &created); err != nil {
			return "", err
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

// readConductorTranscript reads one chat; paths[0] is Conductor's session id.
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
	t.nativeID, t.cwd, t.path = chat.nativeID, chat.cwd, paths[0]
	if chat.title != "Untitled" {
		t.title = chat.title
	}
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
	return t, err
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

// writeConductorTranscript writes the chat as a new Claude Code transcript, so
// Claude Code can continue it. A tool call without its result, or a result
// without its call, is left out: the Claude API rejects either one.
func writeConductorTranscript(ctx context.Context, t transcript) (transcript, error) {
	root := conductorClaudeRoot()
	if claudeTranscriptIDs(root)[t.nativeID] {
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
	// ponytail: Claude Code hashes folder names over 200 characters; such a
	// transcript still resumes by id, only Claude's own picker misses it.
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
