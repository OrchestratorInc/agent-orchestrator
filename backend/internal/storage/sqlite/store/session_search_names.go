package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func (s *Store) SessionSearchNames(ctx context.Context, sessionIDs, projectIDs []string) (map[string]string, map[string]string, error) {
	sessions := map[string]string{}
	projects := map[string]string{}
	if len(sessionIDs) > 0 {
		rows, err := s.readDB.QueryContext(ctx, `
SELECT id, display_name FROM sessions
WHERE id IN (SELECT value FROM json_each(?))`, jsonArray(sessionIDs))
		if err != nil {
			return nil, nil, fmt.Errorf("resolve session search session names: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id, name string
			if err := rows.Scan(&id, &name); err != nil {
				return nil, nil, fmt.Errorf("scan session search session name: %w", err)
			}
			if name == "" {
				name = id
			}
			sessions[id] = name
		}
		if err := rows.Err(); err != nil {
			return nil, nil, fmt.Errorf("iterate session search session names: %w", err)
		}
	}
	if len(projectIDs) > 0 {
		rows, err := s.readDB.QueryContext(ctx, `
SELECT id, display_name FROM projects
WHERE id IN (SELECT value FROM json_each(?))`, jsonArray(projectIDs))
		if err != nil {
			return nil, nil, fmt.Errorf("resolve session search project names: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id, name string
			if err := rows.Scan(&id, &name); err != nil {
				return nil, nil, fmt.Errorf("scan session search project name: %w", err)
			}
			if name == "" {
				name = id
			}
			projects[id] = name
		}
		if err := rows.Err(); err != nil {
			return nil, nil, fmt.Errorf("iterate session search project names: %w", err)
		}
	}
	return sessions, projects, nil
}

func (s *Store) SessionSearchStates(ctx context.Context, sessionIDs []string) (map[string]string, error) {
	states := map[string]string{}
	if len(sessionIDs) == 0 {
		return states, nil
	}
	rows, err := s.readDB.QueryContext(ctx, `
SELECT id, is_terminated, hibernated_at IS NOT NULL FROM sessions
WHERE id IN (SELECT value FROM json_each(?))`, jsonArray(sessionIDs))
	if err != nil {
		return nil, fmt.Errorf("resolve session search states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var terminated, hibernated bool
		if err := rows.Scan(&id, &terminated, &hibernated); err != nil {
			return nil, fmt.Errorf("scan session search state: %w", err)
		}
		switch {
		case terminated:
			states[id] = "terminated"
		case hibernated:
			states[id] = "hibernated"
		default:
			states[id] = "active"
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session search states: %w", err)
	}
	return states, nil
}

func jsonArray(values []string) string {
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func (s *Store) sessionsNamedFor(ctx context.Context, terms []domain.SessionSearchHighlightTerm) (string, error) {
	if len(terms) == 0 {
		return "", nil
	}
	rows, err := s.readDB.QueryContext(ctx, `
SELECT id, display_name FROM sessions
WHERE display_name <> '' AND display_name <> id`)
	if err != nil {
		return "", fmt.Errorf("list session names for search: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var named []string
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return "", fmt.Errorf("scan session name for search: %w", err)
		}
		if domain.SessionSearchTextHasAllTerms(name, terms) {
			named = append(named, id)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate session names for search: %w", err)
	}
	if len(named) == 0 {
		return "", nil
	}
	return jsonArray(named), nil
}

func (s *Store) SessionSearchSessionForPR(ctx context.Context, rawURL string) (string, bool, error) {
	normalized := strings.TrimSuffix(strings.TrimSpace(rawURL), "/")
	if normalized == "" {
		return "", false, nil
	}
	var sessionID string
	err := s.readDB.QueryRowContext(ctx,
		`SELECT session_id FROM pr WHERE url = ? LIMIT 1`, normalized,
	).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.readDB.QueryRowContext(ctx,
			`SELECT p.session_id FROM pr_url_alias a JOIN pr p ON p.url = a.canonical_url
			 WHERE a.alias_url = ? LIMIT 1`, normalized,
		).Scan(&sessionID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("resolve session for PR: %w", err)
	}
	return sessionID, true, nil
}
