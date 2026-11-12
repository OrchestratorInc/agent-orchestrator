package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// prLinkFakeStore adds related-PR storage to fakeStore.
type prLinkFakeStore struct {
	*fakeStore
	mu    sync.Mutex
	links map[domain.SessionID][]domain.SessionPRLink
}

func (s *prLinkFakeStore) SaveSessionPRLink(_ context.Context, link domain.SessionPRLink, replace bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.links[link.SessionID] {
		if existing.Ref.URL == link.Ref.URL {
			if !replace {
				return false, nil
			}
			s.links[link.SessionID][i] = link
			return true, nil
		}
	}
	s.links[link.SessionID] = append(s.links[link.SessionID], link)
	return true, nil
}

func (s *prLinkFakeStore) DeleteSessionPRLink(_ context.Context, id domain.SessionID, url string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, link := range s.links[id] {
		if link.Ref.URL == url {
			s.links[id] = append(s.links[id][:i], s.links[id][i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (s *prLinkFakeStore) ListSessionPRLinksForSessions(_ context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.SessionPRLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[domain.SessionID][]domain.SessionPRLink{}
	for _, id := range ids {
		out[id] = append([]domain.SessionPRLink(nil), s.links[id]...)
	}
	return out, nil
}

const someoneElsesPR = "https://github.com/acme/repo/pull/8020"

func newPRLinkService(t *testing.T, scm fakeSCM) (*Service, *prLinkFakeStore) {
	t.Helper()
	st := &prLinkFakeStore{fakeStore: newFakeStore(), links: map[domain.SessionID][]domain.SessionPRLink{}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	st.sessions["mer-orch"] = domain.SessionRecord{ID: "mer-orch", ProjectID: "mer", Kind: domain.KindOrchestrator}
	svc := NewWithDeps(Deps{Store: st, SCM: scm, PRClaimer: &fakePRClaimer{}, Clock: func() time.Time { return time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC) }})
	return svc, st
}

func fetchedPR(url string, number int, author string) fakeSCM {
	return fakeSCM{obs: ports.SCMObservation{Fetched: true, PR: ports.SCMPRObservation{
		URL: url, Number: number, Title: "Refactor sidebar update UI", State: "open", Author: author,
	}}}
}

func TestLinkPRShowsSomeoneElsesPRWithoutTakingOwnership(t *testing.T) {
	ctx := context.Background()
	svc, st := newPRLinkService(t, fetchedPR(someoneElsesPR, 8020, "contributor"))
	link, err := svc.LinkPR(ctx, "mer-1", someoneElsesPR, domain.SessionPRReviewing)
	if err != nil {
		t.Fatal(err)
	}
	if link.Relation != domain.SessionPRReviewing || link.Ref.Number != 8020 || link.Title != "Refactor sidebar update UI" ||
		link.Author != "contributor" || link.State != domain.PRStateOpen {
		t.Fatalf("link = %+v", link)
	}
	if prs, _ := st.ListPRFactsForSession(ctx, "mer-1"); len(prs) != 0 {
		t.Fatalf("linking claimed the PR: %+v", prs)
	}
	// Relinking replaces the relation; it does not add a second link.
	if _, err := svc.LinkPR(ctx, "mer-1", someoneElsesPR, domain.SessionPRContributing); err != nil {
		t.Fatalf("relink: %v", err)
	}
	sess, err := svc.Get(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.PRLinks) != 1 || sess.PRLinks[0].Relation != domain.SessionPRContributing {
		t.Fatalf("session PRLinks = %+v", sess.PRLinks)
	}
	if sess.Status == domain.StatusPROpen || len(sess.PRs) != 0 {
		t.Fatalf("related PR leaked into status/PRs: status=%s prs=%+v", sess.Status, sess.PRs)
	}

	if _, err := svc.UnlinkPR(ctx, "mer-1", "8020"); err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if sess, _ := svc.Get(ctx, "mer-1"); len(sess.PRLinks) != 0 {
		t.Fatalf("PRLinks after unlink = %+v", sess.PRLinks)
	}
}

func TestLinkPRRejections(t *testing.T) {
	ctx := context.Background()
	t.Run("invalid relation", func(t *testing.T) {
		svc, _ := newPRLinkService(t, fetchedPR(someoneElsesPR, 8020, "x"))
		_, err := svc.LinkPR(ctx, "mer-1", someoneElsesPR, "owning")
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "INVALID_PR_RELATION" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("orchestrator", func(t *testing.T) {
		svc, _ := newPRLinkService(t, fetchedPR(someoneElsesPR, 8020, "x"))
		if _, err := svc.LinkPR(ctx, "mer-orch", someoneElsesPR, domain.SessionPRReviewing); !errors.Is(err, ErrSessionNotClaimable) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("repository outside the project", func(t *testing.T) {
		svc, _ := newPRLinkService(t, fetchedPR("https://github.com/other/repo/pull/1", 1, "x"))
		if _, err := svc.LinkPR(ctx, "mer-1", "https://github.com/other/repo/pull/1", domain.SessionPRReviewing); !errors.Is(err, ErrProjectMismatch) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("PR the provider cannot fetch", func(t *testing.T) {
		svc, _ := newPRLinkService(t, fakeSCM{})
		if _, err := svc.LinkPR(ctx, "mer-1", someoneElsesPR, domain.SessionPRReviewing); !errors.Is(err, ErrPRNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("PR the session owns", func(t *testing.T) {
		svc, st := newPRLinkService(t, fetchedPR(someoneElsesPR, 8020, "x"))
		st.prFacts["mer-1"] = []domain.PRFacts{{URL: someoneElsesPR, Number: 8020}}
		_, err := svc.LinkPR(ctx, "mer-1", someoneElsesPR, domain.SessionPRReviewing)
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "PR_ALREADY_TRACKED" {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRecordReviewingPR(t *testing.T) {
	ctx := context.Background()
	// The provider cannot be reached: the link is still saved, without facts.
	svc, st := newPRLinkService(t, fakeSCM{})
	st.prFacts["mer-1"] = []domain.PRFacts{{URL: "https://github.com/acme/repo/pull/1", Number: 1}}
	svc.RecordReviewingPR(ctx, "mer-1", "https://github.com/acme/repo/pull/1")
	svc.RecordReviewingPR(ctx, "mer-orch", someoneElsesPR)
	if len(st.links["mer-1"]) != 0 || len(st.links["mer-orch"]) != 0 {
		t.Fatalf("owned PR or orchestrator linked: %+v", st.links)
	}
	svc.RecordReviewingPR(ctx, "mer-1", someoneElsesPR)
	if got := st.links["mer-1"]; len(got) != 1 || got[0].Relation != domain.SessionPRReviewing || got[0].Title != "" {
		t.Fatalf("links = %+v", got)
	}

	// It never replaces a link someone set explicitly.
	st.links["mer-1"][0].Relation = domain.SessionPRContributing
	svc.RecordReviewingPR(ctx, "mer-1", someoneElsesPR)
	if got := st.links["mer-1"]; len(got) != 1 || got[0].Relation != domain.SessionPRContributing {
		t.Fatalf("links after a second review = %+v", got)
	}
}

func TestMatchPRLinkByNumberRefusesAmbiguity(t *testing.T) {
	a, _ := domain.ParseChangeRequestURL("https://github.com/acme/repo/pull/7")
	b, _ := domain.ParseChangeRequestURL("https://github.com/acme/web/pull/7")
	links := []domain.SessionPRLink{{Ref: a}, {Ref: b}}
	var apiErr *apierr.Error
	if _, err := matchPRLink(links, "7"); !errors.As(err, &apiErr) || apiErr.Code != "AMBIGUOUS_PR_LINK" {
		t.Fatalf("err = %v", err)
	}
	if got, err := matchPRLink(links, "https://github.com/acme/web/pull/7"); err != nil || got.Ref != b {
		t.Fatalf("got %+v err=%v", got, err)
	}
	if _, err := matchPRLink(links, "https://github.com/acme/repo/pull/8"); !errors.As(err, &apiErr) || apiErr.Code != "PR_LINK_NOT_FOUND" {
		t.Fatalf("err = %v", err)
	}
}
