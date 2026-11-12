package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// SaveSessionPRLink stores the session's link to a PR. With replace, an
// existing link takes the new relation and provider facts (an explicit
// link-pr); without it, an existing link is left alone (an automatic signal).
// A new link past domain.MaxSessionPRLinks fails with
// domain.ErrSessionPRLinkLimit. It reports whether anything was written.
func (s *Store) SaveSessionPRLink(ctx context.Context, link domain.SessionPRLink, replace bool) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	saved := false
	err := s.inTx(ctx, "save session pr link", func(q *gen.Queries) error {
		exists, err := q.SessionPRLinkExists(ctx, gen.SessionPRLinkExistsParams{SessionID: string(link.SessionID), URL: link.Ref.URL})
		if err != nil {
			return err
		}
		if exists && !replace {
			return nil
		}
		if !exists {
			count, err := q.CountSessionPRLinks(ctx, string(link.SessionID))
			if err != nil {
				return err
			}
			if count >= domain.MaxSessionPRLinks {
				return domain.ErrSessionPRLinkLimit
			}
		}
		saved = true
		return q.UpsertSessionPRLink(ctx, gen.UpsertSessionPRLinkParams{
			SessionID: string(link.SessionID), URL: link.Ref.URL, Provider: link.Ref.Provider, Host: link.Ref.Host,
			Repository: link.Ref.Repository, Number: int64(link.Ref.Number), Relation: string(link.Relation),
			Title: link.Title, State: string(link.State), Author: link.Author, CreatedAt: link.CreatedAt,
		})
	})
	if errors.Is(err, domain.ErrSessionPRLinkLimit) {
		return false, err
	}
	if err != nil {
		return false, fmt.Errorf("link pr %s to session %s: %w", link.Ref.URL, link.SessionID, err)
	}
	return saved, nil
}

// DeleteSessionPRLink removes the session's link to url and reports whether
// there was one.
func (s *Store) DeleteSessionPRLink(ctx context.Context, id domain.SessionID, url string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.DeleteSessionPRLink(ctx, gen.DeleteSessionPRLinkParams{SessionID: string(id), URL: url})
	if err != nil {
		return false, fmt.Errorf("unlink pr %s from session %s: %w", url, id, err)
	}
	return n > 0, nil
}

// ListSessionPRLinksForSessions returns the given sessions' links, oldest
// first, in one statement.
func (s *Store) ListSessionPRLinksForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.SessionPRLink, error) {
	out := make(map[domain.SessionID][]domain.SessionPRLink)
	if len(ids) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal session ids: %w", err)
	}
	rows, err := s.qr.ListSessionPRLinksForSessions(ctx, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("list session pr links: %w", err)
	}
	for _, row := range rows {
		id := domain.SessionID(row.SessionID)
		out[id] = append(out[id], domain.SessionPRLink{
			SessionID: id,
			Ref: domain.ChangeRequestReference{
				URL: row.URL, Provider: row.Provider, Host: row.Host, Repository: row.Repository, Number: int(row.Number),
			},
			Relation:  domain.SessionPRRelation(row.Relation),
			Title:     row.Title,
			State:     domain.PRState(row.State),
			Author:    row.Author,
			CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}
