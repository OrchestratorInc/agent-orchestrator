package integration

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
)

type recordingSCMMerger struct {
	calls int
}

func (m *recordingSCMMerger) MergePullRequest(context.Context, ports.SCMMergeRequest) (ports.SCMMergeResult, error) {
	m.calls++
	return ports.SCMMergeResult{MergeCommitSHA: "cafecafecafecafecafecafecafecafecafecafe"}, nil
}

// TestInAppMergeRecordsMergedStateImmediately guards the in-app Merge button:
// once the provider confirms the merge, the tracked PR must read as merged and
// lifecycle must react without waiting for the next SCM poll.
func TestInAppMergeRecordsMergedStateImmediately(t *testing.T) {
	ctx := context.Background()
	f := newSCMFixture(t, "feat/x")
	f.enableTerminateOnPRMerge(t)
	const (
		prURL   = "https://github.com/octocat/hello/pull/42"
		headSHA = "0123456789abcdef0123456789abcdef01234567"
	)
	open := mergedSCMObservation(prURL, 42, headSHA)
	open.PR.State = string(domain.PRStateOpen)
	open.PR.Merged = false
	f.provider.detected["feat/x"] = ports.SCMPRObservation{
		URL: prURL, Number: 42, SourceBranch: "feat/x", HeadRepo: scmTestRepo.Repo, TargetBranch: "main", HeadSHA: headSHA, Author: "octocat",
	}
	f.provider.observations[42] = open
	f.provider.reviews[42] = ports.SCMReviewObservation{Decision: string(domain.ReviewApproved)}
	if err := f.observer.Poll(ctx); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got, ok, err := f.store.GetPR(ctx, prURL); err != nil || !ok || got.Merged {
		t.Fatalf("seed PR = %+v ok=%v err=%v, want tracked open PR", got, ok, err)
	}

	merger := &recordingSCMMerger{}
	svc := prsvc.NewActionService(prsvc.ActionDeps{
		Store:    f.store,
		Merger:   merger,
		Reader:   f.provider,
		Recorder: scmobserve.NewMergeRecorder(f.store, f.lcm, nil, slog.New(slog.NewTextHandler(io.Discard, nil))),
	})
	if _, err := svc.Merge(ctx, prsvc.MergeRequest{PRID: "42", PRURL: prURL, ExpectedHeadSHA: headSHA}); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if merger.calls != 1 {
		t.Fatalf("merge calls = %d, want 1", merger.calls)
	}

	// The provider still reports the PR open and no poll has run: only the
	// merge response can have recorded it.
	got, ok, err := f.store.GetPR(ctx, prURL)
	if err != nil || !ok {
		t.Fatalf("GetPR ok=%v err=%v", ok, err)
	}
	if !got.Merged || got.Mergeability != domain.MergeUnknown || got.MergeCommitSHA != "cafecafecafecafecafecafecafecafecafecafe" {
		t.Fatalf("PR after in-app merge = merged:%v mergeability:%q mergeCommit:%q, want merged with unknown mergeability", got.Merged, got.Mergeability, got.MergeCommitSHA)
	}
	if got.Review != domain.ReviewApproved {
		t.Fatalf("review decision = %q, want preserved %q", got.Review, domain.ReviewApproved)
	}
	sess, ok, err := f.store.GetSession(ctx, f.session.ID)
	if err != nil || !ok {
		t.Fatalf("GetSession ok=%v err=%v", ok, err)
	}
	if !sess.IsTerminated {
		t.Fatal("terminate-on-merge session still live after in-app merge")
	}

	// The next poll's provider view agrees; it must not undo the merged state.
	f.provider.observations[42] = mergedSCMObservation(prURL, 42, headSHA)
	if err := f.observer.Poll(ctx); err != nil {
		t.Fatalf("Poll after merge: %v", err)
	}
	if got, _, _ := f.store.GetPR(ctx, prURL); !got.Merged {
		t.Fatal("PR reverted from merged after the next poll")
	}
}
