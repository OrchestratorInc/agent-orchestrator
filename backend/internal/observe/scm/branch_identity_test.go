package scm

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

func TestPoll_GeneratedNamespacesSeparateInstallations(t *testing.T) {
	for _, kind := range []domain.ProjectKind{domain.ProjectKindSingleRepo, domain.ProjectKindWorkspace} {
		t.Run(string(kind), func(t *testing.T) {
			current := sessionmanager.DefaultSpawnBranch("p-1", domain.KindWorker, "p", kind, t.TempDir())
			foreign := sessionmanager.DefaultSpawnBranch("p-1", domain.KindWorker, "p", kind, t.TempDir())
			store := testStoreWithSession()
			store.sessions[0].Metadata.Branch = current
			project := store.projects["p"]
			project.Kind = kind
			store.projects["p"] = project
			provider := &fakeProvider{
				repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v1"}},
				openPRs: map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {
					{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: foreign, HeadRepo: "o/r", Author: "alice"},
					{URL: "https://github.com/o/r/pull/4", Number: 4, SourceBranch: "ao/p-1/root", HeadRepo: "o/r", Author: "alice"},
				}},
				observations: map[string]ports.SCMObservation{},
			}
			lc := &fakeLifecycle{}
			observer := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
			if err := observer.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(store.writes) != 0 || len(provider.fetchBatches) != 0 || len(lc.observed) != 0 {
				t.Fatalf("unrelated PR caused work: writes=%d fetches=%d lifecycle=%d", len(store.writes), len(provider.fetchBatches), len(lc.observed))
			}

			topic := strings.TrimSuffix(current, "/root") + "/fix"
			if kind == domain.ProjectKindWorkspace {
				topic = current + "-fix"
			}
			for i, branch := range []string{topic, topic + "/child"} {
				obs := testObs(i + 2)
				obs.PR.SourceBranch, obs.PR.HeadRepo, obs.PR.Author = branch, "o/r", "alice"
				if i == 1 {
					obs.PR.TargetBranch = topic
				}
				provider.openPRs[prKey(testRepo, 0)] = append(provider.openPRs[prKey(testRepo, 0)], obs.PR)
				provider.observations[prKey(testRepo, i+2)] = obs
			}
			provider.repoGuards[prKey(testRepo, 0)] = ports.SCMGuardResult{ETag: "v2"}
			if err := observer.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(lc.observed) != 2 {
				t.Fatalf("current and stacked PRs caused %d lifecycle calls, want 2", len(lc.observed))
			}
			for _, write := range store.writes {
				if write.pr.SessionID != "p-1" || (write.pr.Number != 2 && write.pr.Number != 3) {
					t.Fatalf("unexpected ownership: %+v", write.pr)
				}
			}
		})
	}
}
