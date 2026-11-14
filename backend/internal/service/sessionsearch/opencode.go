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
// "{projectId}-{num}", while OpenCode mints its own id. The provider id AO
// recorded for the session (agent_session_id) is used when OpenCode still has
// it. Otherwise the two are matched on the workspace directory, and when
// several provider sessions share it the most recently updated one wins.
func (s *openCodeStore) resolveSession(ctx context.Context, nativeID, workspacePath string) (string, bool, error) {
	var id string
	// The provider session the AO session recorded is the one it ran, even when
	// other AO sessions share its directory (a project root, a restart in place).
	if nativeID = strings.TrimSpace(nativeID); nativeID != "" {
		err := s.db.QueryRowContext(ctx, `SELECT id FROM session WHERE id = ?`, nativeID).Scan(&id)
		if err == nil {
			return id, true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", false, err
		}
	}
	if strings.TrimSpace(workspacePath) == "" {
		return "", false, nil
	}
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
// it saw, which is what the incremental pass compares against, the latest
// update time it saw, and the rows at or below the cursor it read again
// because they changed (session_message only).
func (s *openCodeStore) messages(ctx context.Context, sessionID string, afterSeq, afterUpdated int64) (openCodeRead, error) {
	if ok, err := s.hasTable(ctx, "session_message"); err != nil {
		return openCodeRead{}, err
	} else if ok {
		return s.sessionMessages(ctx, sessionID, afterSeq, afterUpdated)
	}
	if ok, err := s.hasTable(ctx, "message"); err != nil {
		return openCodeRead{}, err
	} else if ok {
		lines, highest, err := s.legacyMessages(ctx, sessionID)
		return openCodeRead{lines: lines, cursor: highest}, err
	}
	return openCodeRead{}, fmt.Errorf("opencode store has no message table")
}

// openCodeRead is what one pass read from an OpenCode session.
type openCodeRead struct {
	lines []nativeLine
	// cursor is the highest seq read; updated is the latest row update read.
	cursor, updated int64
	// revisited are rows at or below the previous cursor read again because
	// they changed: their earlier documents are replaced, not added to.
	revisited []int64
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
//
// OpenCode updates a row in place, keeping its seq, while a reply is still
// being written, so a row read early is read again once it changes: rows past
// the cursor and rows updated at or after the latest update seen are both read.
// "At" matters: update times are whole numbers, so a row can change again
// within the same one. A row read again whose documents did not change is not
// rewritten (replaceOpenCodeRows).
func (s *openCodeStore) sessionMessages(ctx context.Context, sessionID string, afterSeq, afterUpdated int64) (openCodeRead, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT seq, type, data, COALESCE(time_created, 0), COALESCE(time_updated, 0)
FROM session_message
WHERE session_id = ? AND (seq > ? OR (? > 0 AND COALESCE(time_updated, 0) >= ?))
ORDER BY seq`, sessionID, afterSeq, afterUpdated, afterUpdated)
	if err != nil {
		return openCodeRead{}, err
	}
	defer func() { _ = rows.Close() }()
	read := openCodeRead{cursor: afterSeq, updated: afterUpdated}
	for rows.Next() {
		var seq int64
		var kind, data string
		var created, updated int64
		if err := rows.Scan(&seq, &kind, &data, &created, &updated); err != nil {
			return openCodeRead{}, err
		}
		read.cursor, read.updated = max(read.cursor, seq), max(read.updated, updated)
		if seq <= afterSeq {
			read.revisited = append(read.revisited, seq)
		}
		if kind != "user" && kind != "assistant" {
			continue
		}
		at := time.UnixMilli(created).UTC()
		for ordinal, unit := range openCodeMessageUnits(kind, data) {
			unit.at, unit.offset, unit.ordinal = at, seq, ordinal
			read.lines = append(read.lines, unit)
		}
	}
	return read, rows.Err()
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
// An assistant row is a list of parts, and most of a session is tool parts
// (shell commands and their output, files written and edited, web fetches),
// not prose. Each part is its own unit, with the kind AO Chat gives the same
// thing: a command and its output, a file change with its path, a tool call,
// or prose. Reasoning parts are not indexed (coverage.go).
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
	Input   json.RawMessage `json:"input"`
	Content json.RawMessage `json:"content"`
	Error   json.RawMessage `json:"error"`
}

// openCodeToolUnit turns one tool part into a unit: its name and remembered
// argument, then the start of its output (coverage.go). A shell command is a
// command row; a file read, written or edited is a path row whose path goes in
// the paths column. Written and edited contents are not indexed.
func openCodeToolUnit(name string, run openCodeToolRun) (nativeLine, bool) {
	output := strings.TrimSpace(toolResultText(run.Content) + "\n" + toolResultText(run.Error))
	body := toolText(name, toolArgument(run.Input), output)
	if body == "" {
		return nativeLine{}, false
	}
	var input struct {
		Command  string `json:"command"`
		Path     string `json:"path"`
		FilePath string `json:"filePath"`
	}
	_ = json.Unmarshal(run.Input, &input)
	path := input.Path
	if path == "" {
		path = input.FilePath
	}
	switch {
	case input.Command != "":
		return nativeLine{kind: domain.SessionSearchKindCommand, text: body}, true
	case path != "":
		return nativeLine{kind: domain.SessionSearchKindPath, text: body, paths: []string{path}}, true
	default:
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
	providerID, found, resolveErr := store.resolveSession(ctx, target.NativeSessionID, target.WorkspacePath)
	if resolveErr != nil {
		s.recordSourceError(ctx, sourceKey, domain.SessionSearchSourceNative, target, resolveErr)
		return false
	}
	// The cursor is a position in one provider session. A source read by an
	// older reader, or from another provider session (a recorded id replacing a
	// directory match, say), is read again from the start, and what it indexed
	// from the other session is dropped.
	fingerprint := nativeFormat + ":" + providerID
	// FileMtime holds the latest row update read (see sessionMessages).
	cursor, updated := int64(0), int64(0)
	if hadPrevious && previous.Path == path && previous.Fingerprint == fingerprint {
		cursor, updated = previous.CursorValue, previous.FileMtime
	} else if hadPrevious && found {
		if err := s.store.DeleteSessionSearchDocsForSource(ctx, sourceKey); err != nil {
			s.log.Warn("session search reset failed", "source", sourceKey, "error", err)
			return false
		}
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
	read, err := store.messages(ctx, providerID, cursor, updated)
	if err != nil {
		s.recordSourceError(ctx, sourceKey, domain.SessionSearchSourceNative, target, err)
		return false
	}
	now := s.now()
	docs := make([]domain.SessionSearchDoc, 0, len(read.lines)+1)
	for _, line := range read.lines {
		docs = append(docs, nativeDocs(sourceKey, target, line, now)...)
	}
	// A row read again may have been rewritten: a chunk or part it no longer has
	// must not stay searchable, so a changed row's documents are dropped before
	// its new ones are written. Rows not read again keep theirs.
	if err := s.replaceOpenCodeRows(ctx, sourceKey, read.revisited, docs); err != nil {
		s.recordSourceError(ctx, sourceKey, domain.SessionSearchSourceNative, target, err)
		return false
	}
	if title, ok := s.sessionTitleDoc(sourceKey, target); ok {
		docs = append(docs, title)
	}
	// A transcript is read forward from its cursor, so its documents only grow:
	// nothing is deleted here, and unchanged ones (the title) are not rewritten.
	if _, err := s.writeChanged(ctx, sourceKey, docs, false); err != nil {
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
		CursorValue: read.cursor,
		FileMtime:   read.updated,
		Fingerprint: fingerprint,
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

// replaceOpenCodeRows deletes the stored documents of each revisited row whose
// documents changed, so the new ones replace them whole. A row's documents are
// keyed "line:<seq>:<part>", with a "#<n>" suffix per chunk; one whose stored
// keys and fingerprints match the new ones is left alone, and writeChanged
// then skips it too.
func (s *Service) replaceOpenCodeRows(ctx context.Context, sourceKey string, seqs []int64, docs []domain.SessionSearchDoc) error {
	if len(seqs) == 0 {
		return nil
	}
	refs, err := s.store.SessionSearchSourceDocs(ctx, sourceKey)
	if err != nil {
		return err
	}
	rowOf := func(key string) string {
		if rest, ok := strings.CutPrefix(key, "line:"); ok {
			if seq, _, found := strings.Cut(rest, ":"); found {
				return seq
			}
		}
		return ""
	}
	fresh := map[string]map[string]int64{}
	for _, doc := range docs {
		if row := rowOf(doc.DocKey); row != "" {
			if fresh[row] == nil {
				fresh[row] = map[string]int64{}
			}
			fresh[row][doc.DocKey+"\x00"+string(doc.Kind)] = doc.Fingerprint()
		}
	}
	stored := map[string][]domain.SessionSearchDocRef{}
	for _, ref := range refs {
		if row := rowOf(ref.DocKey); row != "" {
			stored[row] = append(stored[row], ref)
		}
	}
	var ids []int64
	for _, seq := range seqs {
		row := fmt.Sprint(seq)
		same := len(stored[row]) == len(fresh[row])
		for _, ref := range stored[row] {
			if fingerprint, ok := fresh[row][ref.DocKey+"\x00"+string(ref.Kind)]; !ok || fingerprint != ref.Fingerprint {
				same = false
				break
			}
		}
		if same {
			continue
		}
		for _, ref := range stored[row] {
			ids = append(ids, ref.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return s.store.DeleteSessionSearchDocsByID(ctx, ids)
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
