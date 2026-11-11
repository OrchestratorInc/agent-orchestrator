package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	shelltermsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

var _ shelltermsvc.Store = (*Store)(nil)

// InsertShellTerminal records a standalone shell terminal against an app run.
func (s *Store) InsertShellTerminal(ctx context.Context, rec shelltermsvc.ShellTerminalRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.qw.InsertShellTerminal(ctx, insertShellTerminalParams(rec))
	if err != nil {
		return fmt.Errorf("insert shell terminal %s: %w", rec.HandleID, err)
	}
	return nil
}

func insertShellTerminalParams(rec shelltermsvc.ShellTerminalRecord) gen.InsertShellTerminalParams {
	return gen.InsertShellTerminalParams{
		HandleID:                  rec.HandleID,
		ProjectID:                 optionalProjectID(rec.ProjectID),
		SessionID:                 optionalSessionID(rec.SessionID),
		WorkingDir:                rec.WorkingDir,
		Title:                     rec.Title,
		TitleExplicit:             rec.TitleExplicit,
		AppRunID:                  rec.AppRunID,
		Transient:                 rec.Transient,
		CommandOwner:              rec.CommandOwner,
		CreatedAt:                 rec.CreatedAt,
		PreviewCapabilityVerifier: rec.PreviewCapabilityVerifier,
	}
}

// SelectShellTerminalByHandleID looks up one shell terminal's row, reporting
// whether it existed so the caller can answer 404 for an unknown handle.
// CloseShellTerminal uses this to learn the row's session id BEFORE
// destroying it, so it can take that session's teardown gate first.
func (s *Store) SelectShellTerminalByHandleID(ctx context.Context, handleID string) (shelltermsvc.ShellTerminalRecord, bool, error) {
	row, err := s.qr.SelectShellTerminalByHandleID(ctx, handleID)
	if errors.Is(err, sql.ErrNoRows) {
		return shelltermsvc.ShellTerminalRecord{}, false, nil
	}
	if err != nil {
		return shelltermsvc.ShellTerminalRecord{}, false, fmt.Errorf("select shell terminal %s: %w", handleID, err)
	}
	return shellTerminalFromGen(row), true, nil
}

// SelectShellTerminalsByAppRunID returns the shell terminals owned by one app
// run, oldest first so the UI renders tabs in the order they were opened.
func (s *Store) SelectShellTerminalsByAppRunID(ctx context.Context, appRunID string) ([]shelltermsvc.ShellTerminalRecord, error) {
	rows, err := s.qr.SelectShellTerminalsByAppRunID(ctx, appRunID)
	if err != nil {
		return nil, fmt.Errorf("select shell terminals for app run %s: %w", appRunID, err)
	}
	return shellTerminalsFromGen(rows), nil
}

// SelectShellTerminalsBySessionID returns the shell terminals scoped to one
// session, oldest first. Session Manager uses this to close them before the
// session's worktree is torn down.
func (s *Store) SelectShellTerminalsBySessionID(ctx context.Context, sessionID domain.SessionID) ([]shelltermsvc.ShellTerminalRecord, error) {
	rows, err := s.qr.SelectShellTerminalsBySessionID(ctx, optionalSessionID(sessionID))
	if err != nil {
		return nil, fmt.Errorf("select shell terminals for session %s: %w", sessionID, err)
	}
	return shellTerminalsFromGen(rows), nil
}

// SelectShellTerminalsFromPreviousAppRuns returns shell terminals left behind
// by any app run other than the one given. Boot reconciliation preserves live
// durable shells and destroys only abandoned transient commands.
func (s *Store) SelectShellTerminalsFromPreviousAppRuns(ctx context.Context, appRunID string) ([]shelltermsvc.ShellTerminalRecord, error) {
	rows, err := s.qr.SelectShellTerminalsFromPreviousAppRuns(ctx, appRunID)
	if err != nil {
		return nil, fmt.Errorf("select orphaned shell terminals: %w", err)
	}
	return shellTerminalsFromGen(rows), nil
}

// UpdateShellTerminalTitle renames one shell terminal, returning the updated row
// and whether a row existed so the caller can answer 404 for an unknown handle.
func (s *Store) UpdateShellTerminalTitle(ctx context.Context, handleID, title string) (shelltermsvc.ShellTerminalRecord, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	row, err := s.qw.UpdateShellTerminalTitle(ctx, gen.UpdateShellTerminalTitleParams{Title: title, HandleID: handleID})
	if errors.Is(err, sql.ErrNoRows) {
		return shelltermsvc.ShellTerminalRecord{}, false, nil
	}
	if err != nil {
		return shelltermsvc.ShellTerminalRecord{}, false, fmt.Errorf("rename shell terminal %s: %w", handleID, err)
	}
	return shellTerminalFromGen(row), true, nil
}

// DeleteShellTerminalByHandleID forgets one shell terminal, reporting whether a
// row actually existed so the caller can answer 404 for an unknown handle.
func (s *Store) DeleteShellTerminalByHandleID(ctx context.Context, handleID string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	affected, err := s.qw.DeleteShellTerminalByHandleID(ctx, handleID)
	if err != nil {
		return false, fmt.Errorf("delete shell terminal %s: %w", handleID, err)
	}
	return affected > 0, nil
}

// MarkShellTerminalEnded records that a shell's process is gone while keeping
// its row, reporting whether a not-yet-ended row was updated.
func (s *Store) MarkShellTerminalEnded(ctx context.Context, handleID string, endedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	affected, err := s.qw.MarkShellTerminalEnded(ctx, gen.MarkShellTerminalEndedParams{
		EndedAt:  sql.NullTime{Time: endedAt.UTC(), Valid: true},
		HandleID: handleID,
	})
	if err != nil {
		return false, fmt.Errorf("mark shell terminal %s ended: %w", handleID, err)
	}
	return affected > 0, nil
}

// UpdateShellTerminalAgentHarness remembers the coding agent last seen in a shell.
func (s *Store) UpdateShellTerminalAgentHarness(ctx context.Context, handleID string, harness domain.AgentHarness) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.UpdateShellTerminalAgentHarness(ctx, gen.UpdateShellTerminalAgentHarnessParams{
		LastAgentHarness: string(harness),
		HandleID:         handleID,
	}); err != nil {
		return fmt.Errorf("update shell terminal %s agent: %w", handleID, err)
	}
	return nil
}

// ReplaceShellTerminal swaps an ended shell's row for the shell that reopens
// it in one transaction, so the tab is never listed twice or not at all. It
// reports false, changing nothing, when the old row no longer exists.
func (s *Store) ReplaceShellTerminal(ctx context.Context, oldHandleID string, rec shelltermsvc.ShellTerminalRecord) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	replaced := false
	err := s.inTx(ctx, "replace shell terminal "+oldHandleID, func(q *gen.Queries) error {
		affected, err := q.DeleteShellTerminalByHandleID(ctx, oldHandleID)
		if err != nil || affected == 0 {
			return err
		}
		if _, err := q.InsertShellTerminal(ctx, insertShellTerminalParams(rec)); err != nil {
			return err
		}
		if rec.LastAgentHarness != "" {
			if _, err := q.UpdateShellTerminalAgentHarness(ctx, gen.UpdateShellTerminalAgentHarnessParams{
				LastAgentHarness: string(rec.LastAgentHarness),
				HandleID:         rec.HandleID,
			}); err != nil {
				return err
			}
		}
		replaced = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return replaced, nil
}

// DeleteShellTerminalsFromPreviousAppRuns clears every orphaned row in one
// statement and returns how many were removed.
func (s *Store) DeleteShellTerminalsFromPreviousAppRuns(ctx context.Context, appRunID string) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	affected, err := s.qw.DeleteShellTerminalsFromPreviousAppRuns(ctx, appRunID)
	if err != nil {
		return 0, fmt.Errorf("delete orphaned shell terminals: %w", err)
	}
	return affected, nil
}

// optionalProjectID maps the service's "no project" empty string onto the
// nullable column, so a project-less shell stores NULL rather than an empty
// string that would violate the projects FK.
func optionalProjectID(id domain.ProjectID) *domain.ProjectID {
	if id == "" {
		return nil
	}
	return &id
}

func projectIDValue(id *domain.ProjectID) domain.ProjectID {
	if id == nil {
		return ""
	}
	return *id
}

// optionalSessionID maps the service's "no session" empty string onto the
// nullable column, so a session-less shell stores NULL rather than an empty
// string that would violate the sessions FK.
func optionalSessionID(id domain.SessionID) sql.NullString {
	if id == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: string(id), Valid: true}
}

func shellTerminalFromGen(row gen.ShellTerminal) shelltermsvc.ShellTerminalRecord {
	rec := shelltermsvc.ShellTerminalRecord{
		HandleID:                  row.HandleID,
		WorkingDir:                row.WorkingDir,
		Title:                     row.Title,
		TitleExplicit:             row.TitleExplicit,
		AppTitle:                  row.AppTitle,
		AppRunID:                  row.AppRunID,
		Transient:                 row.Transient,
		CommandOwner:              row.CommandOwner,
		CreatedAt:                 row.CreatedAt,
		PreviewCapabilityVerifier: row.PreviewCapabilityVerifier,
	}
	if row.ProjectID != nil {
		rec.ProjectID = *row.ProjectID
	}
	if row.SessionID.Valid {
		rec.SessionID = domain.SessionID(row.SessionID.String)
	}
	if row.EndedAt.Valid {
		endedAt := row.EndedAt.Time.UTC()
		rec.EndedAt = &endedAt
	}
	rec.LastAgentHarness = domain.AgentHarness(row.LastAgentHarness)
	return rec
}

func shellTerminalsFromGen(rows []gen.ShellTerminal) []shelltermsvc.ShellTerminalRecord {
	out := make([]shelltermsvc.ShellTerminalRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, shellTerminalFromGen(row))
	}
	return out
}

// SelectRestorableShellTerminals includes durable shells from all app launches
// and trusted command terminals belonging to the current launch.
func (s *Store) SelectRestorableShellTerminals(ctx context.Context, appRunID string) ([]shelltermsvc.ShellTerminalRecord, error) {
	rows, err := s.qr.SelectRestorableShellTerminals(ctx, appRunID)
	if err != nil {
		return nil, fmt.Errorf("select restorable shell terminals: %w", err)
	}
	return shellTerminalsFromGen(rows), nil
}
