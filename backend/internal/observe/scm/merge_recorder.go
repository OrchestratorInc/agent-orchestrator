package scm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// observationWriter is the store write persistObservation needs.
type observationWriter interface {
	WriteSCMObservation(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode) error
}

// persistObservation writes one prepared observation and delivers it to
// lifecycle. Lifecycle is allowed to run only after the observed facts are
// durable, but semantic hashes are the observer's acknowledgement cursor. Keep
// changed hashes at their local values until lifecycle succeeds; if the daemon
// restarts after a lifecycle failure, the stale hashes force the same
// observation to be fetched and delivered again.
func persistObservation(ctx context.Context, store observationWriter, lifecycle Lifecycle, logger *slog.Logger, session domain.SessionRecord, local domain.PullRequest, prepared ports.SCMObservation, opts persistenceOptions, reviewMode ports.ReviewWriteMode, now time.Time) error {
	finalPR, finalChecks, finalReviews, finalThreads, finalComments := domainFromObservation(session.ID, session, prepared, local, opts, now)
	pr, checks, reviews, threads, comments := finalPR, finalChecks, finalReviews, finalThreads, finalComments
	if lifecycle != nil {
		pendingOpts := opts
		if prepared.Changed.Metadata {
			pendingOpts.preserveLocalMetadataHash = true
		}
		if prepared.Changed.CI {
			pendingOpts.preserveLocalCIHash = true
		}
		if prepared.Changed.Review {
			pendingOpts.preserveLocalReviewHash = true
		}
		pr, checks, reviews, threads, comments = domainFromObservation(session.ID, session, prepared, local, pendingOpts, now)
	}
	if err := store.WriteSCMObservation(ctx, pr, checks, reviews, threads, comments, reviewMode); err != nil {
		logger.Error("scm observer: DB write failed", "session", session.ID, "pr", pr.URL, "err", err)
		return err
	}
	if lifecycle == nil {
		return nil
	}
	if err := lifecycle.ApplySCMObservation(ctx, session.ID, prepared); err != nil {
		logger.Error("scm observer: lifecycle notification failed", "session", session.ID, "pr", firstNonEmpty(prepared.PR.URL, prepared.PR.HTMLURL, local.URL), "err", err)
		return err
	}
	if err := store.WriteSCMObservation(ctx, finalPR, finalChecks, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		logger.Error("scm observer: DB lifecycle acknowledgement failed", "session", session.ID, "pr", finalPR.URL, "err", err)
		return err
	}
	return nil
}

// MergeStore is the persistence contract MergeRecorder needs.
type MergeStore interface {
	observationWriter
	GetPR(ctx context.Context, url string) (domain.PullRequest, bool, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// MergeRecorder persists a merge AO itself performed. The provider's merge
// response already proves the PR merged, so the tracked row flips to merged
// (and lifecycle reacts) at once instead of waiting for the next SCM poll.
type MergeRecorder struct {
	store     MergeStore
	lifecycle Lifecycle
	clock     func() time.Time
	logger    *slog.Logger
}

// NewMergeRecorder builds a recorder over the same store and lifecycle sink
// the observer writes through. A nil clock uses time.Now.
func NewMergeRecorder(store MergeStore, lifecycle Lifecycle, clock func() time.Time, logger *slog.Logger) *MergeRecorder {
	if clock == nil {
		clock = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &MergeRecorder{store: store, lifecycle: lifecycle, clock: clock, logger: logger}
}

// RecordMerge persists obs, which must describe the merged PR tracked at
// trackedURL, through the observer's normal write and lifecycle path. Review
// rows are preserved: the merge did not change them, and the next poll
// refreshes them as usual.
func (r *MergeRecorder) RecordMerge(ctx context.Context, trackedURL string, obs ports.SCMObservation) error {
	if r == nil || r.store == nil {
		return errors.New("scm merge recorder: not configured")
	}
	if !obs.Fetched || !obs.PR.Merged {
		return errors.New("scm merge recorder: observation is not a merged pull request")
	}
	local, ok, err := r.store.GetPR(ctx, trackedURL)
	if err != nil {
		return fmt.Errorf("load pull request: %w", err)
	}
	if !ok {
		return fmt.Errorf("pull request %s is not tracked", trackedURL)
	}
	session, ok, err := r.store.GetSession(ctx, local.SessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	if !ok {
		return fmt.Errorf("session %s not found", local.SessionID)
	}
	now := r.clock().UTC()
	obs.ObservedAt = now
	opts := persistenceOptions{preserveLocalReviewHash: true, preserveLocalReviewDecision: true}
	prepared := prepareForPersistence(obs, local, opts, now)
	return persistObservation(ctx, r.store, r.lifecycle, r.logger, session, local, prepared, opts, ports.ReviewWritePreserve, now)
}
