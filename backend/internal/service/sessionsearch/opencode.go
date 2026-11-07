package sessionsearch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// OpenCode keeps its history in its own SQLite database rather than a JSONL
// transcript, so it is read as a second native source shape: the database is
// opened strictly read-only and queried by session id.
//
// Two schemas exist in the wild. Older OpenCode v1 builds store one row per
// message in `message(session_id, data)`. Newer v1 builds and all v2 builds use
// `session_message(session_id, type, seq, data)`, where `type` is already
// `user`/`assistant`/`idle`. The reader probes which one the file has rather
// than assuming a version.

// openCodeStore is the read-only handle to one OpenCode database.
type openCodeStore struct {
	db   *sql.DB
	path string
}

// openOpenCodeStore opens an OpenCode database read-only. The DSN carries
// mode=ro, so a bug in this reader cannot modify the provider's store; the file
// is never created if missing.
func openOpenCodeStore(ctx context.Context, path string) (*openCodeStore, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("opencode store is not a regular file")
	}
	dsn := "file:" + url.PathEscape(path) + "?mode=ro&_pragma=busy_timeout(2000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	// One connection: this reader is sequential, and a pool would hold open
	// handles on a provider-owned file.
	db.SetMaxOpenConns(1)
	return &openCodeStore{db: db, path: path}, nil
}

func (s *openCodeStore) close() {
	if s == nil || s.db == nil {
		return
	}
	_ = s.db.Close()
}

// resolveSession finds the provider's own session row for an AO session.
//
// AO's session id and OpenCode's are different identifiers: AO mints
// "{projectId}-{num}", while OpenCode mints its own id. The two are matched on
// the workspace directory the provider recorded, which is the only shared fact
// both sides store. When several provider sessions share a directory, the most
// recently updated one wins.
func (s *openCodeStore) resolveSession(ctx context.Context, workspacePath string) (string, bool, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return "", false, nil
	}
	var id string
	err := s.db.QueryRowContext(ctx, `
SELECT id FROM session
WHERE directory = ?
ORDER BY COALESCE(time_updated, time_created, 0) DESC
LIMIT 1`, workspacePath).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// messages reads one session's messages, in order. It returns the searchable
// units and a cursor: the highest seq (session_message) or row count (message)
// it saw, which is what the incremental pass compares against.
func (s *openCodeStore) messages(ctx context.Context, sessionID string, afterSeq int64) ([]nativeLine, int64, error) {
	if ok, err := s.hasTable(ctx, "session_message"); err != nil {
		return nil, afterSeq, err
	} else if ok {
		return s.sessionMessages(ctx, sessionID, afterSeq)
	}
	if ok, err := s.hasTable(ctx, "message"); err != nil {
		return nil, afterSeq, err
	} else if ok {
		return s.legacyMessages(ctx, sessionID)
	}
	return nil, afterSeq, fmt.Errorf("opencode store has no message table")
}

func (s *openCodeStore) hasTable(ctx context.Context, name string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name,
	).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// sessionMessages reads the newer schema. Only `user` and `assistant` rows carry
// conversation; `idle` and other control rows are skipped.
func (s *openCodeStore) sessionMessages(ctx context.Context, sessionID string, afterSeq int64) ([]nativeLine, int64, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT seq, type, data, COALESCE(time_created, 0)
FROM session_message
WHERE session_id = ? AND seq > ?
ORDER BY seq`, sessionID, afterSeq)
	if err != nil {
		return nil, afterSeq, err
	}
	defer func() { _ = rows.Close() }()
	var lines []nativeLine
	highest := afterSeq
	for rows.Next() {
		var seq int64
		var kind, data string
		var created int64
		if err := rows.Scan(&seq, &kind, &data, &created); err != nil {
			return nil, afterSeq, err
		}
		highest = max(highest, seq)
		if kind != "user" && kind != "assistant" {
			continue
		}
		at := time.UnixMilli(created).UTC()
		for ordinal, unit := range openCodeMessageUnits(kind, data) {
			unit.at, unit.offset, unit.ordinal = at, seq, ordinal
			lines = append(lines, unit)
		}
	}
	return lines, highest, rows.Err()
}

// legacyMessages reads the older schema, whose rows carry their own ordering.
func (s *openCodeStore) legacyMessages(ctx context.Context, sessionID string) ([]nativeLine, int64, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, data, COALESCE(time_created, 0)
FROM message
WHERE session_id = ?
ORDER BY COALESCE(time_created, 0), id`, sessionID)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var lines []nativeLine
	var ordinal int64
	for rows.Next() {
		var id, data string
		var created int64
		if err := rows.Scan(&id, &data, &created); err != nil {
			return nil, 0, err
		}
		ordinal++
		role, text := openCodeLegacyMessage(data)
		if strings.TrimSpace(text) == "" {
			continue
		}
		searchKind := domain.SessionSearchKindAssistant
		if role == "user" {
			searchKind = domain.SessionSearchKindUser
		}
		lines = append(lines, nativeLine{
			kind: searchKind, text: text, at: time.UnixMilli(created).UTC(), offset: ordinal,
		})
	}
	return lines, ordinal, rows.Err()
}

// openCodeMessageUnits reads one session_message row of the newer schema.
//
// An assistant row is a list of parts, and most of a session is not its text:
// on a real database 2.9 MB of 4.4 MB was tool parts (shell commands and their
// output, files written and edited, web fetches) and 1 MB reasoning, against
// 47 KB of prose. Reading only the text parts left nearly all of it
// unsearchable. Each part is its own unit, with the kind AO Chat gives the same
// thing: a command and its output, a file change with its path, a tool call,
// or prose.
func openCodeMessageUnits(role, data string) []nativeLine {
	var payload struct {
		Text    string          `json:"text"`
		Files   json.RawMessage `json:"files"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			State openCodeToolRun `json:"state"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(data), &payload) != nil {
		return nil
	}
	proseKind := domain.SessionSearchKindAssistant
	if role == "user" {
		proseKind = domain.SessionSearchKindUser
	}
	var units []nativeLine
	if strings.TrimSpace(payload.Text) != "" {
		units = append(units, nativeLine{kind: proseKind, text: payload.Text})
	}
	for _, part := range payload.Content {
		switch part.Type {
		case "text":
			if strings.TrimSpace(part.Text) != "" {
				units = append(units, nativeLine{kind: proseKind, text: part.Text})
			}
		case "reasoning":
			if strings.TrimSpace(part.Text) != "" {
				units = append(units, nativeLine{kind: domain.SessionSearchKindAssistant, text: part.Text})
			}
		case "tool":
			if unit, ok := openCodeToolUnit(part.Name, part.State); ok {
				units = append(units, unit)
			}
		}
	}
	return units
}

// openCodeToolRun is a tool part's state: what it was asked to do, what it
// printed, and the error if it failed.
type openCodeToolRun struct {
	Input struct {
		Command   string `json:"command"`
		Path      string `json:"path"`
		Content   string `json:"content"`
		OldString string `json:"oldString"`
		NewString string `json:"newString"`
		URL       string `json:"url"`
		Query     string `json:"query"`
	} `json:"input"`
	Content json.RawMessage `json:"content"`
	Error   json.RawMessage `json:"error"`
}

// openCodeToolUnit turns one tool part into a unit.
func openCodeToolUnit(name string, run openCodeToolRun) (nativeLine, bool) {
	output := strings.TrimSpace(rawJSONText(run.Content) + "\n" + rawJSONText(run.Error))
	switch {
	case run.Input.Command != "":
		return nativeLine{kind: domain.SessionSearchKindCommand, text: run.Input.Command + "\n" + output}, true
	case run.Input.Path != "":
		// write carries the file's content; edit the text it replaced and its
		// replacement. Both are what someone searches for later.
		body := strings.Join([]string{run.Input.Path, run.Input.Content, run.Input.OldString, run.Input.NewString}, "\n")
		return nativeLine{kind: domain.SessionSearchKindPath, text: body, paths: []string{run.Input.Path}}, true
	default:
		body := strings.TrimSpace(strings.Join([]string{name, run.Input.URL, run.Input.Query, output}, "\n"))
		if body == "" {
			return nativeLine{}, false
		}
		return nativeLine{kind: domain.SessionSearchKindTool, text: body}, true
	}
}

// openCodeLegacyMessage extracts the role and text from a legacy message row.
func openCodeLegacyMessage(data string) (role, text string) {
	var payload struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(data), &payload) != nil {
		return "", ""
	}
	var builder strings.Builder
	for _, part := range payload.Content {
		if part.Type != "text" || strings.TrimSpace(part.Text) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(part.Text)
	}
	return payload.Role, builder.String()
}

// openCodeDatabasePath resolves the OpenCode database for a data home.
func openCodeDatabasePath(dataHome string) string {
	if strings.TrimSpace(dataHome) == "" {
		return ""
	}
	return filepath.Join(dataHome, "opencode", "opencode.db")
}

// indexOpenCodeSession indexes one OpenCode session from its provider database.
// It is a native source like the JSONL ones: the cursor is the provider's own
// sequence, and the database is opened read-only.
func (s *Service) indexOpenCodeSession(ctx context.Context, target domain.SessionSearchTarget, dataHome string) bool {
	sourceKey := NativeSourceKey(target.Harness, string(target.ID))
	path := openCodeDatabasePath(dataHome)
	if path == "" {
		s.markUnsupported(ctx, sourceKey, target)
		return false
	}
	store, err := openOpenCodeStore(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		// No OpenCode install is not a failure: the session is still findable by
		// its title, and coverage stays quiet.
		s.markUnsupported(ctx, sourceKey, target)
		return false
	}
	if err != nil {
		s.recordSourceError(ctx, sourceKey, domain.SessionSearchSourceNative, target, err)
		return false
	}
	defer store.close()

	previous, hadPrevious, err := s.store.GetSessionSearchSource(ctx, sourceKey)
	if err != nil {
		s.log.Warn("session search source read failed", "source", sourceKey, "error", err)
		return false
	}
	cursor := int64(0)
	// A source read by an older reader is read again from the start (nativeFormat).
	if hadPrevious && previous.Path == path && previous.Fingerprint == nativeFormat {
		cursor = previous.CursorValue
	} else if hadPrevious {
		if err := s.store.DeleteSessionSearchDocsForSource(ctx, sourceKey); err != nil {
			s.log.Warn("session search reset failed", "source", sourceKey, "error", err)
			return false
		}
	}
	providerID, found, resolveErr := store.resolveSession(ctx, target.WorkspacePath)
	if resolveErr != nil {
		s.recordSourceError(ctx, sourceKey, domain.SessionSearchSourceNative, target, resolveErr)
		return false
	}
	if !found {
		// The provider has no session for this workspace. The title document is
		// still worth having, and the source is not broken.
		s.writeTitleDoc(ctx, sourceKey, target)
		_ = s.store.UpsertSessionSearchSource(ctx, domain.SessionSearchSource{
			SourceKey: sourceKey,
			Kind:      domain.SessionSearchSourceNative,
			SessionID: string(target.ID),
			ProjectID: string(target.ProjectID),
			Harness:   string(target.Harness),
			Path:      path,
			State:     domain.SessionSearchSourceActive,
			UpdatedAt: s.now(),
		})
		return false
	}
	lines, next, err := store.messages(ctx, providerID, cursor)
	if err != nil {
		s.recordSourceError(ctx, sourceKey, domain.SessionSearchSourceNative, target, err)
		return false
	}
	now := s.now()
	docs := make([]domain.SessionSearchDoc, 0, len(lines)+1)
	for _, line := range lines {
		if doc, ok := nativeDoc(sourceKey, target, s.machine, line, now); ok {
			docs = append(docs, doc)
		}
	}
	if title := strings.TrimSpace(target.DisplayName); title != "" {
		docs = append(docs, domain.SessionSearchDoc{
			SourceKey: sourceKey,
			DocKey:    "title",
			SessionID: string(target.ID),
			ProjectID: string(target.ProjectID),
			Harness:   string(target.Harness),
			Machine:   s.machine,
			Kind:      domain.SessionSearchKindTitle,
			Title:     truncateBytes(title, maxTitleBytes),
			Body:      searchableText(firstNonEmpty(target.LatestPrompt, target.Prompt)),
			CreatedAt: target.CreatedAt,
			IndexedAt: now,
		})
	}
	if err := s.writeDocs(ctx, docs); err != nil {
		s.recordSourceError(ctx, sourceKey, domain.SessionSearchSourceNative, target, err)
		return false
	}
	count, countErr := s.store.CountSessionSearchDocsForSource(ctx, sourceKey)
	if countErr != nil {
		s.log.Warn("session search count failed", "source", sourceKey, "error", countErr)
	}
	source := domain.SessionSearchSource{
		SourceKey:   sourceKey,
		Kind:        domain.SessionSearchSourceNative,
		SessionID:   string(target.ID),
		ProjectID:   string(target.ProjectID),
		Harness:     string(target.Harness),
		Path:        path,
		CursorValue: next,
		Fingerprint: nativeFormat,
		FileSize:    fileSize(path),
		DocCount:    count,
		State:       domain.SessionSearchSourceActive,
		UpdatedAt:   now,
	}
	if err := s.store.UpsertSessionSearchSource(ctx, source); err != nil {
		s.log.Warn("session search source update failed", "source", sourceKey, "error", err)
		return false
	}
	return len(docs) > 0
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
