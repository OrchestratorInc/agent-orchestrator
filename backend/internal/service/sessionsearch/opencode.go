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

type openCodeStore struct {
	db   *sql.DB
	path string
}

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
	db.SetMaxOpenConns(1)
	return &openCodeStore{db: db, path: path}, nil
}

func (s *openCodeStore) close() {
	if s == nil || s.db == nil {
		return
	}
	_ = s.db.Close()
}

func (s *openCodeStore) resolveSession(ctx context.Context, nativeID, workspacePath string) (string, bool, error) {
	var id string
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

type openCodeRead struct {
	lines           []nativeLine
	cursor, updated int64
	revisited       []int64
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

type openCodeToolRun struct {
	Input   json.RawMessage `json:"input"`
	Content json.RawMessage `json:"content"`
	Error   json.RawMessage `json:"error"`
}

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

func openCodeDatabasePath(dataHome string) string {
	if strings.TrimSpace(dataHome) == "" {
		return ""
	}
	return filepath.Join(dataHome, "opencode", "opencode.db")
}

func (s *Service) indexOpenCodeSession(ctx context.Context, target domain.SessionSearchTarget, dataHome string) bool {
	sourceKey := NativeSourceKey(target.Harness, string(target.ID))
	path := openCodeDatabasePath(dataHome)
	if path == "" {
		s.markUnsupported(ctx, sourceKey, target)
		return false
	}
	store, err := openOpenCodeStore(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
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
	fingerprint := nativeFormat + ":" + providerID
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
	if err := s.replaceOpenCodeRows(ctx, sourceKey, read.revisited, docs); err != nil {
		s.recordSourceError(ctx, sourceKey, domain.SessionSearchSourceNative, target, err)
		return false
	}
	if title, ok := s.sessionTitleDoc(sourceKey, target); ok {
		docs = append(docs, title)
	}
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
