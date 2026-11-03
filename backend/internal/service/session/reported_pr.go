package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TrackReportedPR claims a PR the worker reported with `ao report
// --pr-created` when it lives outside the session's project repositories, so
// it is polled like the session's other PRs instead of staying a stateless
// reference. Project-repository PRs are left to branch-based discovery, and a
// PR the session already tracks, or another active session owns, is left as
// is. It reports whether a claim was made.
func (s *Service) TrackReportedPR(ctx context.Context, id domain.SessionID, raw string) (bool, error) {
	ref, err := domain.ParseChangeRequestURL(raw)
	if err != nil {
		return false, ErrInvalidPRRef
	}
	listing, err := s.ListPRListing(ctx, id)
	if err != nil {
		return false, err
	}
	pending := false
	for _, linked := range listing.Linked {
		if linked.Key() == ref.Key() {
			pending = true
			break
		}
	}
	if !pending {
		return false, nil
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil || !ok {
		return false, err
	}
	project, ok, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil || !ok {
		return false, err
	}
	current, err := s.currentRepositoryURL(ctx, ref)
	if err != nil {
		return false, err
	}
	if err := s.requireProjectPRRepository(ctx, project, current); !errors.Is(err, ErrProjectMismatch) {
		return false, err
	}
	if _, err := s.ClaimPR(ctx, id, ref.URL, ClaimPROptions{}); err != nil {
		return false, err
	}
	return true, nil
}

// currentRepositoryURL rewrites a reference under a renamed repository's old
// name to its current name, so a renamed project repository is not mistaken
// for an external one.
func (s *Service) currentRepositoryURL(ctx context.Context, ref domain.ChangeRequestReference) (string, error) {
	renames, err := s.store.ListRepositoryRenames(ctx)
	if err != nil {
		return "", err
	}
	for _, rename := range renames {
		if rename.Host == ref.Host && strings.EqualFold(rename.OldName, ref.Repository) {
			owner, name, _ := strings.Cut(rename.Name, "/")
			return prURLFromParts(ref.Host, owner, name, ref.Number), nil
		}
	}
	host, owner, name, number, err := parsePRURL(ref.URL)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidPRRef, err)
	}
	return prURLFromParts(host, owner, name, number), nil
}
