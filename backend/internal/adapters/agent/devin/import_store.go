package devin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var importNativeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// ImportHome resolves only session storage, leaving the native credential home
// unchanged. DEVIN_SESSIONS_DIR contains cli/sessions.db in this verified build.
func ImportHome(home string) string {
	if root := strings.TrimSpace(os.Getenv("DEVIN_SESSIONS_DIR")); root != "" {
		return root
	}
	if root := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); root != "" {
		return filepath.Join(root, "devin")
	}
	return filepath.Join(home, ".local", "share", "devin")
}

// ImportArchive is the readable projection of a validated native conversation.
// Native preparation copies provider rows, never reconstructs context from text.
type ImportArchive struct {
	ID, CWD, Title, Fingerprint string
	Updated                     time.Time
	Messages                    []domain.ImportedMessage
	Shortened                   bool
	MessageCount                int
	OpeningText                 string
}

type importSession struct {
	id, cwd, backend, model string
	title                   sql.NullString
	head                    sql.NullInt64
	updated                 int64
	hidden                  int
	dirs, cogs, metadata    sql.NullString
}

type importNativeMessage struct {
	ID       string          `json:"message_id"`
	Role     string          `json:"role"`
	Content  string          `json:"content"`
	Images   json.RawMessage `json:"images"`
	Metadata struct {
		IsUserInput bool `json:"is_user_input"`
	} `json:"metadata"`
	ToolCalls []struct {
		ID string `json:"id"`
	} `json:"tool_calls"`
	ToolCallID string `json:"tool_call_id"`
}

func sourceImportSchema(ctx context.Context, db *sql.DB) error {
	// Check object types before querying any provider rows. Views, virtual tables
	// and generated columns execute source-owned SQL and are not native data.
	for _, table := range []string{"refinery_schema_history", "sessions", "message_nodes", "subagent_heads", "tool_call_state", "prompt_history", "rendered_commits", "live_row_times"} {
		var kind string
		if err := db.QueryRowContext(ctx, "SELECT type FROM pragma_table_list WHERE schema='main' AND name=?", table).Scan(&kind); err != nil || kind != "table" {
			return errors.New("devin: unsupported native table type")
		}
		var generated int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_xinfo(?) WHERE hidden<>0", table).Scan(&generated); err != nil || generated != 0 {
			return errors.New("devin: unsupported native generated columns")
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT version FROM refinery_schema_history ORDER BY version")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	expected := 1
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return err
		}
		if version != expected || expected > 20 {
			return errors.New("devin: unsupported native session-store version")
		}
		expected++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if expected != 21 {
		return errors.New("devin: unsupported native session-store version")
	}
	return nil
}

func loadImportSession(ctx context.Context, db *sql.DB, id string) (importSession, error) {
	var s importSession
	if !importNativeID.MatchString(id) {
		return s, errors.New("devin: invalid native session identity")
	}
	err := db.QueryRowContext(ctx, `SELECT id,working_directory,backend_type,model,title,main_chain_id,last_activity_at,hidden,workspace_dirs,cogs_json,metadata FROM sessions WHERE id=?`, id).Scan(&s.id, &s.cwd, &s.backend, &s.model, &s.title, &s.head, &s.updated, &s.hidden, &s.dirs, &s.cogs, &s.metadata)
	if err != nil {
		return s, err
	}
	if !filepath.IsAbs(s.cwd) || s.hidden != 0 || !s.head.Valid || s.head.Int64 < 0 || s.model == "" || (s.backend != "windsurf" && s.backend != "devin") || s.updated < 0 {
		return s, errors.New("devin: unsupported or empty native session")
	}
	if s.cogs.Valid && !json.Valid([]byte(s.cogs.String)) {
		return s, errors.New("devin: invalid native cog state")
	}
	if s.metadata.Valid && !json.Valid([]byte(s.metadata.String)) {
		return s, errors.New("devin: invalid native session metadata")
	}
	if _, err := remapImportDirectories(s.dirs, s.cwd, s.cwd); err != nil {
		return s, err
	}
	var children int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM subagent_heads WHERE session_id=?", id).Scan(&children); err != nil {
		return s, err
	}
	if children != 0 {
		return s, errors.New("devin: linked subagent history is unsupported")
	}
	return s, nil
}

func remapImportDirectories(raw sql.NullString, source, target string) (any, error) {
	if !raw.Valid {
		return nil, nil
	}
	var dirs []string
	if json.Unmarshal([]byte(raw.String), &dirs) != nil || dirs == nil {
		return nil, errors.New("devin: invalid native workspace directories")
	}
	for i, dir := range dirs {
		if !filepath.IsAbs(dir) {
			return nil, errors.New("devin: invalid native workspace directory")
		}
		rel, err := filepath.Rel(source, dir)
		if err != nil || !filepath.IsLocal(rel) {
			return nil, errors.New("devin: external native workspace is unsupported")
		}
		dirs[i] = filepath.Join(target, rel)
	}
	b, err := json.Marshal(dirs)
	return string(b), err
}

// ListImportSessions computes preview counts from one immutable main+WAL snapshot.
func ListImportSessions(ctx context.Context, root string) ([]ImportArchive, error) {
	s, err := readSourceStore(ctx, root)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if err := sourceImportSchema(ctx, s.db); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,working_directory,COALESCE(title,''),last_activity_at FROM sessions WHERE hidden=0 AND backend_type IN ('windsurf','devin') ORDER BY last_activity_at DESC LIMIT 10000`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ImportArchive
	for rows.Next() {
		var a ImportArchive
		var updated int64
		if err := rows.Scan(&a.ID, &a.CWD, &a.Title, &updated); err != nil {
			return nil, err
		}
		if !importNativeID.MatchString(a.ID) || !filepath.IsAbs(a.CWD) || updated < 0 {
			continue
		}
		a.Updated = time.Unix(updated, 0).UTC()
		a.Fingerprint = s.fingerprint
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return readImportPreviews(ctx, s, out)
}

func readImportPreviews(ctx context.Context, s *importStore, items []ImportArchive) ([]ImportArchive, error) {
	previews := make([]ImportArchive, 0, len(items))
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		a, err := readImportArchiveFromStore(ctx, s, item.ID)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			continue
		}
		a.MessageCount = len(a.Messages)
		if len(a.Messages) > 0 {
			a.OpeningText = a.Messages[0].Text
			if len(a.OpeningText) > 512 {
				a.OpeningText = strings.Clone(a.OpeningText[:512])
			}
		}
		a.Messages = nil // previews retain metadata/count, never all candidate text
		previews = append(previews, a)
	}
	return previews, nil
}

// ReadImportArchive validates the complete active ancestry, including parents
// before the first user. It never accepts a corrupt tree after finding a user.
func ReadImportArchive(ctx context.Context, root, id string) (ImportArchive, error) {
	a := ImportArchive{}
	s, err := readSourceStore(ctx, root)
	if err != nil {
		return a, err
	}
	defer s.Close()
	if err := sourceImportSchema(ctx, s.db); err != nil {
		return a, err
	}
	return readImportArchiveFromStore(ctx, s, id)
}

func readImportArchiveFromStore(ctx context.Context, s *importStore, id string) (ImportArchive, error) {
	a := ImportArchive{}
	native, err := loadImportSession(ctx, s.db, id)
	if err != nil {
		return a, err
	}
	a = ImportArchive{ID: id, CWD: native.cwd, Title: native.title.String, Fingerprint: s.fingerprint, Updated: time.Unix(native.updated, 0).UTC()}
	messages, err := importActiveAncestry(ctx, s.db, native)
	if err != nil {
		return a, err
	}
	slices.Reverse(messages)
	user, held := -1, 0
	for i, m := range messages {
		if user < 0 && m.Role == domain.MessageRoleUser && strings.TrimSpace(m.Text) != "" {
			user = i
		}
		held += len(m.Text)
	}
	if user < 0 {
		return a, errors.New("devin: native history has no nonblank user input")
	}
	retained := messages[:0]
	for i, m := range messages {
		if held > 32<<20 && i != user {
			held -= len(m.Text)
			a.Shortened = true
			continue
		}
		retained = append(retained, m)
	}
	clear(messages[len(retained):])
	a.Messages = retained
	if held > 32<<20 {
		return a, errors.New("devin: opening prompt exceeds import budget")
	}
	return a, nil
}

func importActiveAncestry(ctx context.Context, db *sql.DB, s importSession) ([]domain.ImportedMessage, error) {
	seen := map[int64]bool{}
	id := s.head
	var messages []domain.ImportedMessage
	held := 0
	for id.Valid {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if id.Int64 < 0 || seen[id.Int64] {
			return nil, errors.New("devin: cyclic native ancestry")
		}
		seen[id.Int64] = true
		var parent sql.NullInt64
		var raw string
		var at int64
		var size int64
		if err := db.QueryRowContext(ctx, `SELECT length(CAST(chat_message AS BLOB)) FROM message_nodes WHERE session_id=? AND node_id=?`, s.id, id.Int64).Scan(&size); err != nil {
			return nil, errors.New("devin: missing native ancestor")
		}
		if size > 16<<20 {
			return nil, errors.New("devin: native message exceeds import budget")
		}
		if err := db.QueryRowContext(ctx, `SELECT parent_node_id,chat_message,created_at FROM message_nodes WHERE session_id=? AND node_id=?`, s.id, id.Int64).Scan(&parent, &raw, &at); err != nil {
			return nil, err
		}
		var m importNativeMessage
		if json.Unmarshal([]byte(raw), &m) != nil || m.ID == "" || at < 0 {
			return nil, errors.New("devin: invalid native message")
		}
		if len(m.Images) != 0 && string(m.Images) != "null" && string(m.Images) != "[]" {
			return nil, errors.New("devin: native attachments require external state")
		}
		switch m.Role {
		case "user", "assistant", "system", "tool":
		default:
			return nil, errors.New("devin: unknown native message role")
		}
		if (m.Role == "user" && m.Metadata.IsUserInput) || m.Role == "assistant" {
			if strings.TrimSpace(m.Content) != "" {
				messages = append(messages, domain.ImportedMessage{Role: domain.MessageRole(m.Role), Text: m.Content, At: time.Unix(at, 0).UTC()})
				held += len(m.Content)
				if held > 64<<20 {
					return nil, errors.New("devin: native active history exceeds import budget")
				}
			}
		}
		id = parent
	}
	return messages, nil
}
