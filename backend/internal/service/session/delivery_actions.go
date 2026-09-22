package session

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

type DeliveryActionInput struct {
	Action                   DeliveryAction
	ExpectedWorkspaceVersion string
	CommitMessage            string
}

type DeliveryActionResult struct {
	Delivery    DeliveryStatus
	PullRequest *DeliveryPullRequest
	Committed   bool
	Pushed      bool
}

func (s *Service) deliveryLock(id domain.SessionID) *sync.Mutex {
	s.deliveryLocksMu.Lock()
	defer s.deliveryLocksMu.Unlock()
	lock := s.deliveryLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.deliveryLocks[id] = lock
	}
	return lock
}

func (s *Service) AdvanceDelivery(ctx context.Context, id domain.SessionID, input DeliveryActionInput) (DeliveryActionResult, error) {
	lock := s.deliveryLock(id)
	if !lock.TryLock() {
		return DeliveryActionResult{}, apierr.Conflict("DELIVERY_IN_PROGRESS", "Another delivery action is already running", nil)
	}
	defer lock.Unlock()
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return DeliveryActionResult{}, err
	}
	if !ok {
		return DeliveryActionResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if rec.IsTerminated || rec.Kind == domain.KindOrchestrator {
		return DeliveryActionResult{}, apierr.Conflict("DELIVERY_SESSION_INELIGIBLE", "This session cannot publish workspace changes", nil)
	}
	current, err := s.ListWorkspaceFiles(ctx, id)
	if err != nil {
		return DeliveryActionResult{}, err
	}
	if input.ExpectedWorkspaceVersion == "" || input.ExpectedWorkspaceVersion != current.WorkspaceVersion {
		return DeliveryActionResult{}, apierr.Conflict("DELIVERY_WORKSPACE_STALE", "Workspace changed after it was reviewed", map[string]any{"workspaceVersion": current.WorkspaceVersion})
	}
	if current.Delivery.Action == "" || current.Delivery.Action != input.Action {
		return DeliveryActionResult{}, apierr.Conflict("DELIVERY_ACTION_UNSAFE", firstNonEmpty(current.Delivery.BlockedReason, "The requested delivery action is no longer safe"), nil)
	}
	commitAction := input.Action == DeliveryActionCommitAndPublish || input.Action == DeliveryActionCommitAndPush
	result := DeliveryActionResult{}
	if commitAction {
		message := strings.TrimSpace(input.CommitMessage)
		if message == "" {
			return result, apierr.Invalid("DELIVERY_COMMIT_MESSAGE_REQUIRED", "commitMessage is required", nil)
		}
		if _, err := gitDeliveryOutput(ctx, rec.Metadata.WorkspacePath, "add", "--all"); err != nil {
			return result, deliveryStageError("stage", err)
		}
		if _, err := gitDeliveryOutput(ctx, rec.Metadata.WorkspacePath, "commit", "-m", message); err != nil {
			return result, deliveryStageError("commit", err)
		}
		result.Committed = true
		s.InvalidateWorkspaceCache(id)
		current, err = s.ListWorkspaceFiles(ctx, id)
		if err != nil {
			return result, err
		}
	}
	if input.Action == DeliveryActionPush || input.Action == DeliveryActionCommitAndPush || input.Action == DeliveryActionCommitAndPublish || (input.Action == DeliveryActionPublishPR && (current.Ahead == nil || *current.Ahead > 0)) {
		branch := current.Delivery.Branch
		if branch == "" {
			return result, apierr.Conflict("DELIVERY_BRANCH_REQUIRED", "The current branch cannot be determined", nil)
		}
		if _, err := gitDeliveryOutput(ctx, rec.Metadata.WorkspacePath, "push", "origin", "HEAD:refs/heads/"+branch); err != nil {
			return result, deliveryStageError("push", err)
		}
		result.Pushed = true
		s.InvalidateWorkspaceCache(id)
	}
	if input.Action == DeliveryActionPublishPR || input.Action == DeliveryActionCommitAndPublish {
		if s.scm == nil || s.publisher == nil || s.prClaimer == nil {
			return result, apierr.Unavailable("DELIVERY_PROVIDER_UNAVAILABLE", "Pull request publishing is unavailable")
		}
		remote, err := gitDeliveryOutput(ctx, rec.Metadata.WorkspacePath, "remote", "get-url", "origin")
		if err != nil {
			return result, deliveryStageError("publish", err)
		}
		repo, ok := s.scm.ParseRepository(strings.TrimSpace(remote))
		if !ok {
			return result, apierr.Invalid("DELIVERY_REMOTE_UNSUPPORTED", "The origin remote is not supported", nil)
		}
		head, err := gitDeliveryOutput(ctx, rec.Metadata.WorkspacePath, "rev-parse", "HEAD")
		if err != nil {
			return result, deliveryStageError("publish", err)
		}
		target := strings.TrimPrefix(strings.TrimPrefix(current.CompareBaseRef, "refs/remotes/"), "origin/")
		if target == "" || target == "HEAD" {
			target = "main"
		}
		title := current.Delivery.CommitSubject
		if title == "" {
			title = firstNonEmpty(rec.DisplayName, "Publish session changes")
		}
		published, err := s.publisher.ReconcileOrCreatePullRequest(ctx, ports.SCMPublishRequest{Repo: repo, SourceBranch: current.Delivery.Branch, TargetBranch: target, HeadSHA: strings.TrimSpace(head), Title: title, Body: fmt.Sprintf("Summary\n\n%d files changed (+%d -%d).", current.Summary.Files, current.Summary.Additions, current.Summary.Deletions)})
		if err != nil {
			return result, deliveryStageError("create_pr", err)
		}
		if err := s.claimPublishedPullRequest(ctx, rec, repo, published.PR); err != nil {
			return result, deliveryStageError("associate_pr", err)
		}
		result.PullRequest = &DeliveryPullRequest{URL: published.PR.URL, Number: published.PR.Number}
	}
	s.InvalidateWorkspaceCache(id)
	refreshed, refreshErr := s.ListWorkspaceFiles(ctx, id)
	if refreshErr == nil {
		result.Delivery = refreshed.Delivery
	}
	return result, refreshErr
}

func (s *Service) claimPublishedPullRequest(ctx context.Context, rec domain.SessionRecord, repo ports.SCMRepo, pr ports.SCMPRObservation) error {
	obs := ports.SCMObservation{Fetched: true, Provider: repo.Provider, Host: repo.Host, Repo: repo.Repo, PR: pr}
	now := s.clock().UTC()
	row, checks, reviews, threads, comments := claimRowsFromSCM(rec.ID, obs, ports.ReviewWritePreserve, now, rec)
	_, err := s.prClaimer.ClaimPR(ctx, row, checks, reviews, threads, comments, ports.ReviewWritePreserve, false)
	return err
}

func deliveryStageError(stage string, err error) error {
	_ = err // Native Git/provider details may contain credential-bearing remote URLs.
	return apierr.Conflict("DELIVERY_"+strings.ToUpper(stage)+"_FAILED", "Delivery stopped during "+strings.ReplaceAll(stage, "_", " "), map[string]any{"stage": stage})
}

func gitDeliveryOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := aoprocess.CommandContext(ctx, "git", append([]string{"--no-pager", "--no-optional-locks", "-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
