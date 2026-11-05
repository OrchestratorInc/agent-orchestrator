package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

func TestSessionListDerivesKanbanColumn(t *testing.T) {
	for _, tt := range []struct {
		name    string
		record  domain.SessionRecord
		pr      *domain.PRFacts
		runs    []domain.CurrentHeadReviewRun
		want    domain.KanbanColumn
		wantPRs bool
	}{
		{
			name:   "no pr is building",
			record: domain.SessionRecord{ID: "mer-1", ProjectID: "mer"},
			want:   domain.KanbanBuilding,
		},
		{
			name:   "terminated archives",
			record: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true},
			pr:     &domain.PRFacts{URL: "pr1", HeadSHA: "head1"},
			want:   domain.KanbanArchive,
		},
		{
			name:   "a running current-head pass is ao validation work",
			record: domain.SessionRecord{ID: "mer-1", ProjectID: "mer"},
			pr:     &domain.PRFacts{URL: "pr1", HeadSHA: "head1"},
			runs:   []domain.CurrentHeadReviewRun{{PRURL: "pr1", Status: domain.ReviewRunRunning}},
			want:   domain.KanbanValidating,
		},
		{
			name:   "a finished pass with auto review off hands over to a person",
			record: domain.SessionRecord{ID: "mer-1", ProjectID: "mer"},
			pr:     &domain.PRFacts{URL: "pr1", HeadSHA: "head1"},
			runs: []domain.CurrentHeadReviewRun{
				{PRURL: "pr1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved},
			},
			want: domain.KanbanNeedsReview,
		},
		{
			name:   "auto review keeps a changes-requested head validating, even without auto-inject",
			record: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", AutoReviewEnabled: true},
			pr:     &domain.PRFacts{URL: "pr1", HeadSHA: "head1"},
			runs: []domain.CurrentHeadReviewRun{
				{PRURL: "pr1", Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested},
			},
			want: domain.KanbanValidating,
		},
		{
			name:   "a failed auto review keeps a mergeable pr validating",
			record: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", AutoReviewEnabled: true},
			pr: &domain.PRFacts{
				URL: "pr1", HeadSHA: "head1", Mergeability: domain.MergeMergeable,
			},
			runs: []domain.CurrentHeadReviewRun{
				{PRURL: "pr1", Status: domain.ReviewRunFailed},
			},
			want: domain.KanbanValidating,
		},
		{
			name:   "a human approval on a blocked pr is ready",
			record: domain.SessionRecord{ID: "mer-1", ProjectID: "mer"},
			pr: &domain.PRFacts{
				URL: "pr1", HeadSHA: "head1", Review: domain.ReviewApproved,
				Mergeability: domain.MergeBlocked, ExternalApproved: true,
			},
			want: domain.KanbanReady,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions[tt.record.ID] = tt.record
			if tt.pr != nil {
				st.pr[tt.record.ID] = *tt.pr
			}
			st.reviewRuns[tt.record.ID] = tt.runs

			list, err := (&Service{store: st}).List(context.Background(), ListFilter{ProjectID: "mer"})
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 1 || list[0].KanbanColumn != tt.want {
				t.Fatalf("kanban column = %+v, want %q", list, tt.want)
			}
		})
	}
}

// A review pass recorded against an older commit is filtered out in SQL, so the
// service must not treat it as current-head validation work.
func TestSessionKanbanIgnoresPassesForAnEarlierHead(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", AutoReviewEnabled: true}
	st.pr["mer-1"] = domain.PRFacts{URL: "pr1", HeadSHA: "head2"}
	// The store returns nothing: the stale head1 pass never reaches the reducer.
	st.reviewRuns["mer-1"] = nil

	got, err := (&Service{store: st}).Get(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.KanbanColumn != domain.KanbanValidating {
		t.Fatalf("kanban column = %q, want %q for a fresh unreviewed head", got.KanbanColumn, domain.KanbanValidating)
	}
}

func TestSessionListDerivesDisplayStatus(t *testing.T) {
	for _, tt := range []struct {
		name   string
		record domain.SessionRecord
		pr     *domain.PRFacts
		runs   []domain.CurrentHeadReviewRun
		want   domain.DisplayStatus
	}{
		{
			name: "a working session with no pr reports the worker",
			record: domain.SessionRecord{
				ID: "mer-1", ProjectID: "mer",
				Activity: domain.Activity{State: domain.ActivityActive},
			},
			want: contract.DisplayWorking,
		},
		{
			name:   "terminated is archived",
			record: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true},
			pr:     &domain.PRFacts{URL: "pr1", HeadSHA: "head1"},
			want:   contract.DisplayTerminated,
		},
		{
			name:   "a pass in flight is reviewing",
			record: domain.SessionRecord{ID: "mer-1", ProjectID: "mer"},
			pr:     &domain.PRFacts{URL: "pr1", HeadSHA: "head1"},
			runs:   []domain.CurrentHeadReviewRun{{PRURL: "pr1", Status: domain.ReviewRunRunning}},
			want:   contract.DisplayReviewing,
		},
		{
			name:   "a cancelled pass leaves the review pending",
			record: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", AutoReviewEnabled: true},
			pr:     &domain.PRFacts{URL: "pr1", HeadSHA: "head1"},
			runs:   []domain.CurrentHeadReviewRun{{PRURL: "pr1", Status: domain.ReviewRunCancelled}},
			want:   contract.DisplayReviewPending,
		},
		{
			name: "failing checks on the current head are being fixed",
			record: domain.SessionRecord{
				ID: "mer-1", ProjectID: "mer", AutoInjectCI: true,
				Activity: domain.Activity{State: domain.ActivityActive},
			},
			pr: &domain.PRFacts{
				URL: "pr1", HeadSHA: "head1",
				CI: domain.CIFailing,
			},
			want: contract.DisplayFixingCI,
		},
		{
			name:   "a merged pr reports the merge",
			record: domain.SessionRecord{ID: "mer-1", ProjectID: "mer"},
			pr:     &domain.PRFacts{URL: "pr1", HeadSHA: "head1", Merged: true},
			want:   contract.DisplayMerged,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions[tt.record.ID] = tt.record
			if tt.pr != nil {
				st.pr[tt.record.ID] = *tt.pr
			}
			st.reviewRuns[tt.record.ID] = tt.runs

			got, err := (&Service{store: st}).Get(context.Background(), tt.record.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.DisplayStatus != tt.want {
				t.Fatalf("display status = %q, want %q", got.DisplayStatus, tt.want)
			}
		})
	}
}

func TestSessionKanbanExternalChangesStayPersonOwned(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", AutoInjectReview: true,
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.pr["mer-1"] = domain.PRFacts{
		URL: "pr1", HeadSHA: "head1", Review: domain.ReviewChangesRequest, ExternalChangesRequested: true,
	}

	got, err := (&Service{store: st}).Get(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.KanbanColumn != domain.KanbanNeedsReview {
		t.Fatalf("kanban column = %q, want %q", got.KanbanColumn, domain.KanbanNeedsReview)
	}
	if got.DisplayStatus != contract.DisplayAddressingComments {
		t.Fatalf("display status = %q, want %q", got.DisplayStatus, contract.DisplayAddressingComments)
	}
}

func TestSessionKanbanExternalCommentsDriveNeedsReviewDisplayStatus(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer",
	}
	st.pr["mer-1"] = domain.PRFacts{
		URL: "pr1", HeadSHA: "head1", ReviewComments: true, ExternalComments: true,
	}

	got, err := (&Service{store: st}).Get(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.KanbanColumn != domain.KanbanNeedsReview {
		t.Fatalf("kanban column = %q, want %q", got.KanbanColumn, domain.KanbanNeedsReview)
	}
	if got.DisplayStatus != contract.DisplayCommented {
		t.Fatalf("display status = %q, want %q", got.DisplayStatus, contract.DisplayCommented)
	}
}

func TestSessionGetInfersArtifactOutputAndValidatingKanban(t *testing.T) {
	artifactDir := t.TempDir()
	writeArtifact := func(rel, content string) {
		t.Helper()
		path := filepath.Join(artifactDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	writeArtifact("a/index.html", "<html><body>artifact</body></html>")
	writeArtifact("b/readme.md", "# artifact\n")
	writeArtifact("c/output.txt", "plain text\n")

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Activity:  domain.Activity{State: domain.ActivityActive},
		Metadata:  domain.SessionMetadata{ArtifactDir: artifactDir},
		// OutputType is the persisted column lifecycle.Manager.
		// ReconcileSessionOutputType maintains; Get() no longer re-derives it
		// from the live artifact scan, so it must be set here directly for
		// Kanban placement to see it as artifact output.
		OutputType: domain.SessionOutputArtifact,
	}

	got, err := (&Service{store: st}).Get(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.OutputType != domain.SessionOutputArtifact {
		t.Fatalf("outputType = %q, want %q", got.OutputType, domain.SessionOutputArtifact)
	}
	if got.KanbanColumn != domain.KanbanValidating {
		t.Fatalf("kanban column = %q, want %q", got.KanbanColumn, domain.KanbanValidating)
	}
	if got.DisplayStatus != contract.DisplayWorking {
		t.Fatalf("display status = %q, want %q", got.DisplayStatus, contract.DisplayWorking)
	}
	if len(got.ArtifactFiles) != 3 {
		t.Fatalf("artifact files = %+v, want 3 inferred files", got.ArtifactFiles)
	}
	if got.ArtifactFiles[0].Path != "a/index.html" || got.ArtifactFiles[0].Kind != domain.SessionArtifactHTML {
		t.Fatalf("first artifact = %+v, want html artifact", got.ArtifactFiles[0])
	}
	if got.ArtifactFiles[1].Path != "b/readme.md" || got.ArtifactFiles[1].Kind != domain.SessionArtifactMarkdown {
		t.Fatalf("second artifact = %+v, want markdown artifact", got.ArtifactFiles[1])
	}
	if got.ArtifactFiles[2].Path != "c/output.txt" || got.ArtifactFiles[2].Kind != domain.SessionArtifactGeneric {
		t.Fatalf("third artifact = %+v, want generic file artifact", got.ArtifactFiles[2])
	}
}

func TestSessionGetUsesLatestCurrentHeadRunPerHarness(t *testing.T) {
	st := newSQLiteKanbanTestStore(t)
	ctx := context.Background()
	seedKanbanTestProject(t, st, "mer")
	now := time.Now().UTC().Truncate(time.Second)

	rec, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID:         "mer",
		Kind:              domain.KindWorker,
		Harness:           domain.HarnessClaudeCode,
		AutoReviewEnabled: true,
		AutoInjectReview:  true,
		CreatedAt:         now,
		UpdatedAt:         now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteSCMObservation(ctx, domain.PullRequest{
		URL: "pr/1", SessionID: rec.ID, Number: 1, HeadSHA: "head1", UpdatedAt: now, ObservedAt: now,
	}, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertReview(ctx, domain.Review{
		ID: "rev", SessionID: rec.ID, ProjectID: "mer", Harness: "claude-code",
		PRURL: "pr/1", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	insert := func(id string, createdAt time.Time, verdict domain.ReviewVerdict) {
		t.Helper()
		if err := st.InsertReviewRun(ctx, domain.ReviewRun{
			ID: id, ReviewID: "rev", SessionID: rec.ID, Harness: "claude-code",
			PRURL: "pr/1", TargetSHA: "head1", Status: domain.ReviewRunComplete,
			Verdict: verdict, CreatedAt: createdAt, AutoInjectReview: true,
		}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("old", now, domain.VerdictChangesRequested)
	insert("new", now.Add(time.Second), domain.VerdictApproved)

	got, err := (&Service{store: st}).Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.KanbanColumn != domain.KanbanNeedsReview {
		t.Fatalf("kanban column = %q, want %q after approved rerun on same head", got.KanbanColumn, domain.KanbanNeedsReview)
	}
	if got.DisplayStatus != contract.DisplayNeedsHumanReview {
		t.Fatalf("display status = %q, want %q after approved rerun on same head", got.DisplayStatus, contract.DisplayNeedsHumanReview)
	}
}

func newSQLiteKanbanTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	return sqlitetest.MustOpen(t)
}

func seedKanbanTestProject(t *testing.T, st *sqlite.Store, id string) {
	t.Helper()
	if err := st.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: id, Path: "/tmp/" + id, RegisteredAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

// The provider's reviewDecision stays empty on a repo that does not require
// reviews, so a finished AO review must still show on the session's PR facts
// (#6748). Status keeps following the provider's decision alone.
func TestSessionPRReviewReflectsCurrentHeadReviewRuns(t *testing.T) {
	approved := func(harness domain.ReviewerHarness) domain.CurrentHeadReviewRun {
		return domain.CurrentHeadReviewRun{PRURL: "pr1", Harness: harness, Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}
	}
	changes := func(harness domain.ReviewerHarness) domain.CurrentHeadReviewRun {
		return domain.CurrentHeadReviewRun{PRURL: "pr1", Harness: harness, Status: domain.ReviewRunDelivered, Verdict: domain.VerdictChangesRequested}
	}
	for _, tt := range []struct {
		name     string
		provider domain.ReviewDecision
		runs     []domain.CurrentHeadReviewRun
		want     domain.ReviewDecision
	}{
		{name: "no run keeps the provider decision", provider: domain.ReviewNone, want: domain.ReviewNone},
		{
			name:     "approved run fills an empty provider decision",
			provider: domain.ReviewNone,
			runs:     []domain.CurrentHeadReviewRun{approved("claude-code")},
			want:     domain.ReviewApproved,
		},
		{
			name:     "changes requested run fills an empty provider decision",
			provider: domain.ReviewNone,
			runs:     []domain.CurrentHeadReviewRun{changes("claude-code")},
			want:     domain.ReviewChangesRequest,
		},
		{
			name:     "any reviewer requesting changes outvotes an approval",
			provider: domain.ReviewNone,
			runs:     []domain.CurrentHeadReviewRun{approved("claude-code"), changes("codex")},
			want:     domain.ReviewChangesRequest,
		},
		{
			name:     "changes requested run outranks a provider approval",
			provider: domain.ReviewApproved,
			runs:     []domain.CurrentHeadReviewRun{changes("claude-code")},
			want:     domain.ReviewChangesRequest,
		},
		{
			name:     "approved run does not clear provider changes requested",
			provider: domain.ReviewChangesRequest,
			runs:     []domain.CurrentHeadReviewRun{approved("claude-code")},
			want:     domain.ReviewChangesRequest,
		},
		{
			name:     "approved run does not satisfy a provider review requirement",
			provider: domain.ReviewRequired,
			runs:     []domain.CurrentHeadReviewRun{approved("claude-code")},
			want:     domain.ReviewRequired,
		},
		{
			name:     "running failed and cancelled runs carry no verdict",
			provider: domain.ReviewNone,
			runs: []domain.CurrentHeadReviewRun{
				{PRURL: "pr1", Harness: "claude-code", Status: domain.ReviewRunRunning},
				{PRURL: "pr1", Harness: "codex", Status: domain.ReviewRunFailed},
				{PRURL: "pr1", Harness: "cursor", Status: domain.ReviewRunCancelled},
			},
			want: domain.ReviewNone,
		},
		{
			name:     "another PR's run does not apply",
			provider: domain.ReviewNone,
			runs:     []domain.CurrentHeadReviewRun{{PRURL: "pr2", Harness: "claude-code", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}},
			want:     domain.ReviewNone,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
			st.pr["mer-1"] = domain.PRFacts{URL: "pr1", HeadSHA: "head1", Review: tt.provider}
			st.reviewRuns["mer-1"] = tt.runs
			svc := &Service{store: st}

			assertSessionPRReview(t, svc, "mer-1", tt.want)
			got, err := svc.Get(context.Background(), "mer-1")
			if err != nil {
				t.Fatal(err)
			}
			wantSCM := deriveSCMStatus([]domain.PRFacts{{URL: "pr1", HeadSHA: "head1", Review: tt.provider}})
			if got.SCMStatus != wantSCM {
				t.Fatalf("scm status = %q, want %q from the provider decision alone", got.SCMStatus, wantSCM)
			}
		})
	}
}

// A terminated worker's PR must read the same from the list and the single
// session route, even though the Kanban reducer ignores its runs.
func TestSessionPRReviewReadsTheSameForATerminatedWorker(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true}
	st.pr["mer-1"] = domain.PRFacts{URL: "pr1", HeadSHA: "head1", Review: domain.ReviewNone}
	st.reviewRuns["mer-1"] = []domain.CurrentHeadReviewRun{{PRURL: "pr1", Harness: "claude-code", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}}

	assertSessionPRReview(t, &Service{store: st}, "mer-1", domain.ReviewApproved)
}

// Runs against the real store query: an approval of an older commit must not
// read as approved for the PR's current head, on the list or the single read.
func TestSessionPRReviewIgnoresRunsForAnEarlierHead(t *testing.T) {
	ctx := context.Background()
	st := newSQLiteKanbanTestStore(t)
	seedKanbanTestProject(t, st, "mer")
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	rec, err := st.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	observeHead := func(head string, at time.Time) {
		t.Helper()
		if err := st.WriteSCMObservation(ctx, domain.PullRequest{
			URL: "pr/1", SessionID: rec.ID, Number: 1, HeadSHA: head, UpdatedAt: at, ObservedAt: at,
		}, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
			t.Fatal(err)
		}
	}
	observeHead("head1", now)
	if err := st.UpsertReview(ctx, domain.Review{
		ID: "rev", SessionID: rec.ID, ProjectID: "mer", Harness: "claude-code",
		PRURL: "pr/1", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertReviewRun(ctx, domain.ReviewRun{
		ID: "run-1", ReviewID: "rev", SessionID: rec.ID, Harness: "claude-code",
		PRURL: "pr/1", TargetSHA: "head1", Status: domain.ReviewRunComplete,
		Verdict: domain.VerdictApproved, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{store: st}

	assertSessionPRReview(t, svc, rec.ID, domain.ReviewApproved)

	observeHead("head2", now.Add(time.Minute))
	assertSessionPRReview(t, svc, rec.ID, domain.ReviewNone)
}

// assertSessionPRReview checks the session's single PR on both read paths: the
// batched list and the single session read.
func assertSessionPRReview(t *testing.T, svc *Service, id domain.SessionID, want domain.ReviewDecision) {
	t.Helper()
	list, err := svc.List(context.Background(), ListFilter{ProjectID: "mer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || len(list[0].PRs) != 1 {
		t.Fatalf("listed %d sessions, want one with one PR", len(list))
	}
	if got := list[0].PRs[0].Review; got != want {
		t.Fatalf("listed PR review = %q, want %q", got, want)
	}
	sess, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.PRs) != 1 {
		t.Fatalf("session has %d PRs, want one", len(sess.PRs))
	}
	if got := sess.PRs[0].Review; got != want {
		t.Fatalf("session PR review = %q, want %q", got, want)
	}
}
