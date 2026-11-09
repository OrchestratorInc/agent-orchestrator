package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	integrationservice "github.com/aoagents/agent-orchestrator/backend/internal/service/integration"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// m4CheckLocalPush 删除独立授权、登记限制或 expected SHA CAS 会允许未批推送、扩大目的地或覆盖并发写入。
func m4CheckLocalPush(t *testing.T, f *m2ffFixture, svc *integrationservice.Service, plan domain.IntegrationPlan, entries []domain.IntegrationRepoPreview, artifacts []domain.ArtifactSnapshot, fence domain.SchedulerFence) {
	t.Helper()
	ctx := context.Background()
	remotes := map[string]string{}
	req := domain.IntegrationApplyRequest{IdempotencyKey: "local-push", ExpectedRevision: plan.Revision, PlanDigest: plan.PlanDigest, Entries: entries}
	for i, a := range artifacts {
		key := string(a.Repo.RepoKey)
		root := filepath.Join(t.TempDir(), "bare")
		m2smGit(t, a.Repo.RepoPath, "clone", "--bare", "--no-local", a.Repo.RepoPath, root)
		m2smGit(t, root, "update-ref", a.Repo.TargetRef, entries[i].ExpectedTargetSHA, entries[i].MergeCommitSHA)
		if err := os.WriteFile(filepath.Join(root, "hooks", "reference-transaction"), []byte("#!/bin/sh\nexit 42\n"), 0700); err != nil {
			t.Fatal(err)
		}
		identity, err := gitworktree.LocalBareIdentity(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		remotes[key] = root
		req.PushTargets = append(req.PushTargets, domain.IntegrationPushTarget{RepoKey: a.Repo.RepoKey, RemoteKey: key, RemoteIdentity: identity, TargetRef: a.Repo.TargetRef, ExpectedRemoteSHA: entries[i].ExpectedTargetSHA, CommitSHA: entries[i].MergeCommitSHA})
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	approval := m4Approve(t, svc.Approvals, integrationservice.Path(plan.WorkflowID, plan.ID, "push"), raw)
	if _, err := svc.Push(ctx, plan.WorkflowID, plan.ID, raw, approval, fence); err == nil {
		t.Fatal("default-disabled push executed")
	}
	svc.BareRemotes = remotes
	first := req.PushTargets[0]
	root := remotes[first.RemoteKey]
	err = gitworktree.ApplyLocalPush(ctx, artifacts[0], root, first, func(ctx context.Context, write func(context.Context) error) error {
		m2smGit(t, root, "-c", "core.hooksPath="+os.DevNull, "update-ref", first.TargetRef, entries[0].TaskCommitSHA, first.ExpectedRemoteSHA)
		return write(ctx)
	})
	if !errors.Is(err, domain.ErrArtifactStale) {
		t.Fatalf("remote drift=%v", err)
	}
	if sha, err := gitworktree.ObserveLocalPush(ctx, root, first); err != nil || sha != entries[0].TaskCommitSHA {
		t.Fatalf("concurrent remote update overwritten: %s %v", sha, err)
	}
	m2smGit(t, root, "-c", "core.hooksPath="+os.DevNull, "update-ref", first.TargetRef, first.ExpectedRemoteSHA, entries[0].TaskCommitSHA)
	mergeApproval := m4Approve(t, svc.Approvals, integrationservice.Path(plan.WorkflowID, plan.ID, "apply"), raw)
	if _, err := svc.Push(ctx, plan.WorkflowID, plan.ID, raw, mergeApproval, fence); err == nil {
		t.Fatal("merge approval consumed for push")
	}
	response := f.do(http.MethodPost, strings.TrimPrefix(integrationservice.Path(plan.WorkflowID, plan.ID, "push"), "/api/v1"), string(raw), map[string]string{principal.HeaderOperatorApproval: approval})
	var out controllers.IntegrationOperationResponse
	if response.Status != http.StatusOK || json.Unmarshal([]byte(response.Raw), &out) != nil || out.Operation.State != "applied" {
		t.Fatalf("local HTTP push=%d %s", response.Status, response.Raw)
	}
	op := out.Operation
	for _, p := range req.PushTargets {
		sha, err := gitworktree.ObserveLocalPush(ctx, remotes[p.RemoteKey], p)
		if err != nil || sha != p.CommitSHA {
			t.Fatalf("remote=%s %v", sha, err)
		}
	}
	if replay, err := svc.Push(ctx, plan.WorkflowID, plan.ID, raw, "", fence); err != nil || replay.Revision != op.Revision {
		t.Fatalf("push replay=%+v %v", replay, err)
	}
}
