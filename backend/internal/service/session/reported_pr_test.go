package session

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestTrackReportedPRClaimsOnlyUntrackedPRsOutsideTheProject(t *testing.T) {
	tracked := "https://github.com/acme/app/pull/7"
	for _, tc := range []struct {
		name, reported, wantClaim string
	}{
		{"external repository is claimed", "https://github.com/OrchestratorInc/ao-releases/pull/50", "https://github.com/orchestratorinc/ao-releases/pull/50"},
		{"external GitLab MR is claimed", "https://gitlab.com/release/notes/-/merge_requests/9", "https://gitlab.com/release/notes/-/merge_requests/9"},
		{"project repository is left to discovery", "https://github.com/acme/app/pull/8", ""},
		{"renamed project repository is left to discovery", "https://github.com/old-owner/app/pull/8", ""},
		{"already tracked PR is not reclaimed", "https://www.github.com/ACME/App/pull/7", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/app.git"}
			st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws", Branch: "ao/mer-1/root"}}
			st.prs["mer-1"] = []domain.PullRequest{{URL: tracked, SessionID: "mer-1", Number: 7, CI: domain.CIUnknown, Review: domain.ReviewNone, Mergeability: domain.MergeUnknown, UpdatedAt: time.Now().UTC()}}
			st.repositoryRenames = []domain.RepositoryRename{{Host: "github.com", OldName: "old-owner/app", Name: "acme/app"}}
			st.reportedPRURLs["mer-1"] = []string{tc.reported}
			scm, claimer := &claimTargetSCM{}, &fakePRClaimer{}
			svc := NewWithDeps(Deps{Store: st, SCM: scm, PRClaimer: claimer})

			claimed, err := svc.TrackReportedPR(context.Background(), "mer-1", tc.reported)
			if err != nil {
				t.Fatalf("TrackReportedPR: %v", err)
			}
			if tc.wantClaim == "" {
				if claimed || claimer.called || len(scm.refs) != 0 {
					t.Fatalf("claimed = %v, fetched %+v; want no claim", claimed, scm.refs)
				}
				return
			}
			if !claimed || !claimer.called || claimer.gotPR.SessionID != "mer-1" || claimer.gotPR.URL != tc.wantClaim {
				t.Fatalf("claimed = %v, persisted %+v; want %s", claimed, claimer.gotPR, tc.wantClaim)
			}
		})
	}
}
