package session

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A related PR is a pull request a session reviews or contributes to without
// owning it (#5986's "linked", as opposed to "tracked"). It is display data
// only: it lives in its own table, never in pr, so status, the SCM observer,
// CI and review routing, merge automation, and lifecycle cannot see it, and
// linking never changes which session owns the PR.

// prLinkStore is the optional storage capability for related PRs.
type prLinkStore interface {
	SaveSessionPRLink(ctx context.Context, link domain.SessionPRLink, replace bool) (bool, error)
	DeleteSessionPRLink(ctx context.Context, id domain.SessionID, url string) (bool, error)
	ListSessionPRLinksForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.SessionPRLink, error)
}

// ErrPRLinksUnavailable is returned when this daemon has no related-PR storage.
var ErrPRLinksUnavailable = errors.New("session: pr links unavailable")

// recordPRLinkTimeout bounds the provider lookup an automatic link makes.
const recordPRLinkTimeout = 5 * time.Second

// LinkPR records that a session reviews or contributes to a PR it does not
// own. The PR is validated like `ao session claim-pr`: in the project's
// repositories and fetchable with the user's credentials. Unlike a claim it
// takes no ownership, routes nothing to the session, and works for a PR
// another session owns. Relinking replaces the relation and refreshes the
// PR's title, state and author.
func (s *Service) LinkPR(ctx context.Context, id domain.SessionID, ref string, relation domain.SessionPRRelation) (domain.SessionPRLink, error) {
	if !relation.Valid() {
		return domain.SessionPRLink{}, apierr.Invalid("INVALID_PR_RELATION", "relation must be reviewing or contributing", nil)
	}
	if s.prLinks == nil {
		return domain.SessionPRLink{}, ErrPRLinksUnavailable
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionPRLink{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok || rec.IsTaskPreparation {
		return domain.SessionPRLink{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if rec.Kind == domain.KindOrchestrator {
		return domain.SessionPRLink{}, ErrSessionNotClaimable
	}
	project, ok, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return domain.SessionPRLink{}, fmt.Errorf("project %s: %w", rec.ProjectID, err)
	}
	if !ok {
		return domain.SessionPRLink{}, apierr.Invalid("PROJECT_NOT_RESOLVABLE", "Project is not registered or has no repo — register it with `ao project add`", nil)
	}
	if project.Kind.WithDefault() == domain.ProjectKindScratch {
		return domain.SessionPRLink{}, ErrSessionNotClaimable
	}
	if err := project.Config.ValidateCanonicalRepository(project.RepoOriginURL); err != nil {
		return domain.SessionPRLink{}, apierr.Invalid("INVALID_PROJECT_CONFIG", err.Error(), nil)
	}
	prURL, _, err := normalizePRRef(ref, firstNonEmpty(project.Config.CanonicalRepoURL, project.RepoOriginURL))
	if err != nil {
		return domain.SessionPRLink{}, err
	}
	if err := s.requireProjectPRRepository(ctx, project, prURL); err != nil {
		return domain.SessionPRLink{}, err
	}
	link, err := s.newPRLink(ctx, id, prURL, relation)
	if err != nil {
		return domain.SessionPRLink{}, err
	}
	if s.scm == nil {
		return domain.SessionPRLink{}, ErrSCMUnavailable
	}
	if err := s.fillPRLinkFacts(ctx, &link); err != nil {
		return domain.SessionPRLink{}, err
	}
	if _, err := s.prLinks.SaveSessionPRLink(ctx, link, true); err != nil {
		if errors.Is(err, domain.ErrSessionPRLinkLimit) {
			return domain.SessionPRLink{}, apierr.Conflict("PR_LINK_LIMIT", fmt.Sprintf("A session shows at most %d related PRs; unlink one first", domain.MaxSessionPRLinks), nil)
		}
		return domain.SessionPRLink{}, err
	}
	return link, nil
}

// UnlinkPR removes a related PR from a session. ref is the PR URL, or its
// number when exactly one related PR has it. It never touches a PR the
// session owns.
func (s *Service) UnlinkPR(ctx context.Context, id domain.SessionID, ref string) (domain.SessionPRLink, error) {
	if s.prLinks == nil {
		return domain.SessionPRLink{}, ErrPRLinksUnavailable
	}
	links, err := s.prLinks.ListSessionPRLinksForSessions(ctx, []domain.SessionID{id})
	if err != nil {
		return domain.SessionPRLink{}, err
	}
	target, err := matchPRLink(links[id], ref)
	if err != nil {
		return domain.SessionPRLink{}, err
	}
	if _, err := s.prLinks.DeleteSessionPRLink(ctx, id, target.Ref.URL); err != nil {
		return domain.SessionPRLink{}, err
	}
	return target, nil
}

func matchPRLink(links []domain.SessionPRLink, ref string) (domain.SessionPRLink, error) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	notFound := apierr.NotFound("PR_LINK_NOT_FOUND", "This session has no related PR matching "+ref)
	if n, err := strconv.Atoi(ref); err == nil && n > 0 {
		var found []domain.SessionPRLink
		for _, link := range links {
			if link.Ref.Number == n {
				found = append(found, link)
			}
		}
		switch len(found) {
		case 0:
			return domain.SessionPRLink{}, notFound
		case 1:
			return found[0], nil
		default:
			return domain.SessionPRLink{}, apierr.Invalid("AMBIGUOUS_PR_LINK", "Several related PRs have that number; pass the full PR URL", nil)
		}
	}
	want, err := domain.ParseChangeRequestURL(ref)
	if err != nil {
		return domain.SessionPRLink{}, ErrInvalidPRRef
	}
	for _, link := range links {
		if link.Ref.Key() == want.Key() {
			return link, nil
		}
	}
	return domain.SessionPRLink{}, notFound
}

// RecordReviewingPR is the automatic form of LinkPR for a review the session
// started on a PR it does not own. It is best effort: it never replaces an
// existing link, skips PRs the session owns and orchestrators, saves the link
// even when the provider lookup fails, and only logs errors.
func (s *Service) RecordReviewingPR(ctx context.Context, id domain.SessionID, rawURL string) {
	if s.prLinks == nil {
		return
	}
	if err := s.recordReviewingPR(ctx, id, rawURL); err != nil && s.logger != nil {
		s.logger.Warn("record reviewed pr", "session", id, "pr", rawURL, "err", err)
	}
}

func (s *Service) recordReviewingPR(ctx context.Context, id domain.SessionID, rawURL string) error {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil || !ok || rec.Kind == domain.KindOrchestrator || rec.IsTaskPreparation {
		return err
	}
	link, err := s.newPRLink(ctx, id, rawURL, domain.SessionPRReviewing)
	if err != nil {
		return err
	}
	if s.scm != nil {
		fetchCtx, cancel := context.WithTimeout(ctx, recordPRLinkTimeout)
		_ = s.fillPRLinkFacts(fetchCtx, &link)
		cancel()
	}
	_, err = s.prLinks.SaveSessionPRLink(ctx, link, false)
	if errors.Is(err, domain.ErrSessionPRLinkLimit) {
		return nil
	}
	return err
}

// newPRLink parses rawURL into a link and refuses a PR the session owns.
func (s *Service) newPRLink(ctx context.Context, id domain.SessionID, rawURL string, relation domain.SessionPRRelation) (domain.SessionPRLink, error) {
	ref, err := domain.ParseChangeRequestURL(strings.TrimSpace(rawURL))
	if err != nil {
		return domain.SessionPRLink{}, ErrInvalidPRRef
	}
	prs, err := s.store.ListPRFactsForSession(ctx, id)
	if err != nil {
		return domain.SessionPRLink{}, fmt.Errorf("pr facts %s: %w", id, err)
	}
	if ownedPRKeys(prs)[ref.Key()] {
		return domain.SessionPRLink{}, apierr.Conflict("PR_ALREADY_TRACKED", "This session already owns and tracks this PR; a link would only duplicate it", nil)
	}
	return domain.SessionPRLink{SessionID: id, Ref: ref, Relation: relation, CreatedAt: s.clock().UTC()}, nil
}

// fillPRLinkFacts fetches the PR and copies its title, state and author.
func (s *Service) fillPRLinkFacts(ctx context.Context, link *domain.SessionPRLink) error {
	repo, err := scmRepoForClaim(link.Ref.URL)
	if err != nil {
		return err
	}
	obs, err := s.fetchClaimObservation(ctx, ports.SCMPRRef{Repo: repo, Number: link.Ref.Number, URL: link.Ref.URL})
	if err != nil {
		return err
	}
	link.Title, link.Author, link.State = obs.PR.Title, obs.PR.Author, domain.PRState(obs.PR.State)
	switch {
	case obs.PR.Merged:
		link.State = domain.PRStateMerged
	case obs.PR.Closed:
		link.State = domain.PRStateClosed
	case link.State == "" && obs.PR.Draft:
		link.State = domain.PRStateDraft
	case link.State == "":
		link.State = domain.PRStateOpen
	}
	return nil
}

func ownedPRKeys(prs []domain.PRFacts) map[string]bool {
	keys := make(map[string]bool, len(prs))
	for _, pr := range prs {
		if ref, err := domain.ParseChangeRequestURL(pr.URL); err == nil {
			keys[ref.Key()] = true
		}
	}
	return keys
}

// applyPRLinks sets PRLinks on each session read, minus any PR the session
// has since come to own. Related PRs are cosmetic: a failed read leaves them
// empty rather than failing the board.
func (s *Service) applyPRLinks(ctx context.Context, sessions []domain.Session) {
	if s.prLinks == nil || len(sessions) == 0 {
		return
	}
	ids := make([]domain.SessionID, len(sessions))
	for i, sess := range sessions {
		ids[i] = sess.ID
	}
	links, err := s.prLinks.ListSessionPRLinksForSessions(ctx, ids)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("list related prs", "err", err)
		}
		return
	}
	for i := range sessions {
		sess := &sessions[i]
		if sess.Kind == domain.KindOrchestrator {
			continue
		}
		owned := ownedPRKeys(sess.PRs)
		for _, link := range links[sess.ID] {
			if !owned[link.Ref.Key()] {
				sess.PRLinks = append(sess.PRLinks, link)
			}
		}
	}
}
