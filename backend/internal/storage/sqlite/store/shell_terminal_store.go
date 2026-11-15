package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	shelltermsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

var _ shelltermsvc.Store = (*Store)(nil)

func (s *Store) InsertShellTerminal(ctx context.Context, rec shelltermsvc.ShellTerminalRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.qw.InsertShellTerminal(ctx, gen.InsertShellTerminalParams{
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
	})
	if err != nil {
		return fmt.Errorf("insert shell terminal %s: %w", rec.HandleID, err)
	}
	return nil
}

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

func (s *Store) SelectShellTerminalsByAppRunID(ctx context.Context, appRunID string) ([]shelltermsvc.ShellTerminalRecord, error) {
	rows, err := s.qr.SelectShellTerminalsByAppRunID(ctx, appRunID)
	if err != nil {
		return nil, fmt.Errorf("select shell terminals for app run %s: %w", appRunID, err)
	}
	return shellTerminalsFromGen(rows), nil
}

func (s *Store) SelectShellTerminalsBySessionID(ctx context.Context, sessionID domain.SessionID) ([]shelltermsvc.ShellTerminalRecord, error) {
	rows, err := s.qr.SelectShellTerminalsBySessionID(ctx, optionalSessionID(sessionID))
	if err != nil {
		return nil, fmt.Errorf("select shell terminals for session %s: %w", sessionID, err)
	}
	return shellTerminalsFromGen(rows), nil
}

func (s *Store) SelectShellTerminalsFromPreviousAppRuns(ctx context.Context, appRunID string) ([]shelltermsvc.ShellTerminalRecord, error) {
	rows, err := s.qr.SelectShellTerminalsFromPreviousAppRuns(ctx, appRunID)
	if err != nil {
		return nil, fmt.Errorf("select orphaned shell terminals: %w", err)
	}
	return shellTerminalsFromGen(rows), nil
}

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

func (s *Store) DeleteShellTerminalByHandleID(ctx context.Context, handleID string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	affected, err := s.qw.DeleteShellTerminalByHandleID(ctx, handleID)
	if err != nil {
		return false, fmt.Errorf("delete shell terminal %s: %w", handleID, err)
	}
	return affected > 0, nil
}

func (s *Store) DeleteShellTerminalsFromPreviousAppRuns(ctx context.Context, appRunID string) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	affected, err := s.qw.DeleteShellTerminalsFromPreviousAppRuns(ctx, appRunID)
	if err != nil {
		return 0, fmt.Errorf("delete orphaned shell terminals: %w", err)
	}
	return affected, nil
}

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
	return rec
}

func shellTerminalsFromGen(rows []gen.ShellTerminal) []shelltermsvc.ShellTerminalRecord {
	out := make([]shelltermsvc.ShellTerminalRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, shellTerminalFromGen(row))
	}
	return out
}

func (s *Store) SelectRestorableShellTerminals(ctx context.Context, appRunID string) ([]shelltermsvc.ShellTerminalRecord, error) {
	rows, err := s.qr.SelectRestorableShellTerminals(ctx, appRunID)
	if err != nil {
		return nil, fmt.Errorf("select restorable shell terminals: %w", err)
	}
	return shellTerminalsFromGen(rows), nil
}
