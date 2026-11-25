package automation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	dueScanLimit      = 100
	dispatchScanLimit = 100
	spawnLease        = 2 * time.Minute
	maxRunErrorBytes  = 512
	// pendingPageSize is the page size used to read a pending backlog.
	pendingPageSize   = 100
	skippedRunMessage = "Skipped: an earlier occurrence of this automation was still waiting to run"
)

// SessionSpawner is the existing user-facing session launch boundary.
type SessionSpawner interface {
	Spawn(context.Context, ports.SpawnConfig) (domain.Session, int, int, error)
	Kill(context.Context, domain.SessionID) (bool, error)
}

// schedulerStore is deliberately separate from CRUD Store so focused CRUD
// tests and alternate read adapters need not implement daemon-only operations.
type schedulerStore interface {
	ListDueAutomations(context.Context, time.Time, int64) ([]domain.Automation, error)
	MaterializeAutomationRuns(context.Context, domain.AutomationID, time.Time, []domain.AutomationRun, time.Time, time.Time, time.Time) (bool, error)
	ClaimNextAutomationRun(context.Context, time.Time, time.Time) (domain.AutomationRun, bool, error)
	ListActiveAutomationRuns(context.Context) ([]domain.AutomationRun, error)
	GetSessionByAutomationRunID(context.Context, domain.AutomationRunID) (domain.SessionRecord, bool, error)
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	MarkAutomationRunRunning(context.Context, domain.AutomationRunID, domain.SessionID, time.Time) (bool, error)
	CompleteAutomationRun(context.Context, domain.AutomationRunID, time.Time) (bool, error)
	FailAutomationRun(context.Context, domain.AutomationRunID, string, time.Time) (bool, error)
	ReleaseAutomationRun(context.Context, domain.AutomationRunID, string, time.Time) (bool, error)
	ListAutomationRuns(context.Context, domain.AutomationRunFilter) (domain.AutomationRunPage, error)
}

func (s *Service) schedulerStore() (schedulerStore, error) {
	store, ok := s.store.(schedulerStore)
	if !ok {
		return nil, fmt.Errorf("automation scheduler storage is unavailable")
	}
	return store, nil
}

// Tick performs one bounded materialization, lifecycle projection, claim, and
// dispatch pass. One bad definition is joined into the returned diagnostic but
// does not prevent other definitions from progressing.
func (s *Service) Tick(ctx context.Context, at time.Time) error {
	store, err := s.schedulerStore()
	if err != nil {
		return err
	}
	now := at.UTC()
	var problems []error
	if err := s.completeAndAdoptActive(ctx, store, now); err != nil {
		problems = append(problems, err)
	}
	due, err := store.ListDueAutomations(ctx, now, dueScanLimit)
	if err != nil {
		problems = append(problems, err)
	} else {
		for _, definition := range due {
			if err := s.materializeDefinition(ctx, store, definition, now); err != nil {
				problems = append(problems, fmt.Errorf("automation %s: %w", definition.ID, err))
			}
		}
	}

	// Claim the batch before launching anything. Each spawning claim blocks its
	// siblings, which guarantees at most one dispatch per definition this poll.
	claims := make([]domain.AutomationRun, 0)
	for len(claims) < dispatchScanLimit && ctx.Err() == nil {
		run, ok, claimErr := store.ClaimNextAutomationRun(ctx, now, now.Add(spawnLease))
		if claimErr != nil {
			problems = append(problems, claimErr)
			break
		}
		if !ok {
			break
		}
		claims = append(claims, run)
	}
	for _, run := range claims {
		if err := s.dispatch(ctx, store, run, now); err != nil {
			problems = append(problems, fmt.Errorf("automation run %s: %w", run.ID, err))
		}
	}
	if ctx.Err() != nil {
		problems = append(problems, ctx.Err())
	}
	return errors.Join(problems...)
}

func (s *Service) materializeDefinition(ctx context.Context, store schedulerStore, definition domain.Automation, now time.Time) error {
	if !definition.Enabled || definition.NextRunAt.After(now) {
		return nil
	}
	// However many occurrences fell due, they coalesce into at most one
	// pending run: an automation that is still busy, or whose daemon was
	// down, owes one more run, not one per missed tick. Like a missed alarm,
	// that run is for the most recent due occurrence; older ones are dropped.
	last, err := PreviousOccurrence(definition.RRuleText, definition.Timezone, now)
	if err != nil {
		return err
	}
	if last.Before(definition.NextRunAt) {
		last = definition.NextRunAt.UTC()
	}
	occurrence, err := NextOccurrence(definition.RRuleText, definition.Timezone, now)
	if err != nil {
		return err
	}
	waiting, err := s.coalescePendingRuns(ctx, store, definition.ID, now)
	if err != nil {
		return err
	}
	var runs []domain.AutomationRun
	if !waiting {
		runs = []domain.AutomationRun{{
			ID: domain.AutomationRunID("automation-run-" + s.newID()), AutomationID: definition.ID,
			ScheduledFor: last, Status: domain.AutomationRunPending,
			CreatedAt: now, UpdatedAt: now,
		}}
	}
	_, err = store.MaterializeAutomationRuns(ctx, definition.ID, definition.NextRunAt, runs, last, occurrence, now)
	return err
}

// coalescePendingRuns keeps only the oldest pending run of one automation and
// marks any others as skipped. It reports whether a pending run remains, in
// which case no new occurrence needs to be queued.
func (s *Service) coalescePendingRuns(ctx context.Context, store schedulerStore, id domain.AutomationID, now time.Time) (bool, error) {
	// Read every page before changing anything: skipping rows while paging
	// would shift the offsets, and any extra left pending could be claimed
	// before this automation falls due again.
	status := domain.AutomationRunPending
	filter := domain.AutomationRunFilter{AutomationID: id, Status: &status, Limit: pendingPageSize}
	var pending []domain.AutomationRun
	for {
		page, err := store.ListAutomationRuns(ctx, filter)
		if err != nil {
			return false, err
		}
		pending = append(pending, page.Items...)
		filter.Offset += int64(len(page.Items))
		if len(page.Items) == 0 || filter.Offset >= page.Total {
			break
		}
	}
	if len(pending) == 0 {
		return false, nil
	}
	sort.Slice(pending, func(i, j int) bool {
		if !pending[i].ScheduledFor.Equal(pending[j].ScheduledFor) {
			return pending[i].ScheduledFor.Before(pending[j].ScheduledFor)
		}
		return pending[i].ID < pending[j].ID
	})
	var problems []error
	for _, extra := range pending[1:] {
		if _, err := store.FailAutomationRun(ctx, extra.ID, skippedRunMessage, now); err != nil {
			problems = append(problems, err)
		}
	}
	return true, errors.Join(problems...)
}

func (s *Service) dispatch(ctx context.Context, store schedulerStore, run domain.AutomationRun, now time.Time) error {
	definition, err := s.Get(ctx, run.AutomationID)
	if err != nil {
		message := runError(err)
		_, markErr := store.FailAutomationRun(context.WithoutCancel(ctx), run.ID, message, now)
		return errors.Join(err, markErr)
	}
	if s.spawner == nil {
		err := fmt.Errorf("session spawner is unavailable")
		_, releaseErr := store.ReleaseAutomationRun(context.WithoutCancel(ctx), run.ID, runError(err), now)
		return errors.Join(err, releaseErr)
	}
	session, _, _, spawnErr := s.spawner.Spawn(ctx, ports.SpawnConfig{
		ProjectID: definition.ProjectID, Kind: definition.Kind, Harness: definition.Harness,
		AgentConfig:    ports.AgentConfig{Model: definition.Model, Mode: definition.Mode, Effort: definition.Effort},
		EffortOverride: definition.Model != "" || definition.Effort != "",
		Prompt:         definition.Prompt, DisplayName: definition.DisplayName, DisplayNameExplicit: definition.DisplayName != "", AutomationRunID: &run.ID,
	})
	if spawnErr != nil {
		var apiError *apierr.Error
		if session, ok, lookupErr := store.GetSessionByAutomationRunID(context.WithoutCancel(ctx), run.ID); lookupErr != nil {
			return errors.Join(spawnErr, lookupErr)
		} else if ok {
			if session.AutomationLaunchCompleted {
				_, markErr := store.MarkAutomationRunRunning(context.WithoutCancel(ctx), run.ID, session.ID, now)
				return errors.Join(spawnErr, markErr)
			}
			if !session.IsTerminated {
				_, killErr := s.spawner.Kill(context.WithoutCancel(ctx), session.ID)
				if killErr != nil {
					return errors.Join(spawnErr, killErr)
				}
			}
			_, markErr := store.FailAutomationRun(context.WithoutCancel(ctx), run.ID, runError(spawnErr), now)
			return errors.Join(spawnErr, markErr)
		}
		// A paused project fails this occurrence rather than re-queueing it: the
		// pause holds until a person starts a session or changes its settings.
		permanent := errors.As(spawnErr, &apiError) && (apiError.Kind == apierr.KindInvalid || apiError.Kind == apierr.KindNotFound || apiError.Code == "AUTO_SPAWN_PAUSED")
		if permanent {
			_, markErr := store.FailAutomationRun(context.WithoutCancel(ctx), run.ID, runError(spawnErr), now)
			return errors.Join(spawnErr, markErr)
		}
		_, releaseErr := store.ReleaseAutomationRun(context.WithoutCancel(ctx), run.ID, runError(spawnErr), now)
		return errors.Join(spawnErr, releaseErr)
	}
	_, err = store.MarkAutomationRunRunning(context.WithoutCancel(ctx), run.ID, session.ID, now)
	return err
}

// Reconcile repairs crash-interrupted claims, projects durable completion, and
// materializes a bounded startup catch-up. Dispatch begins in the observer's
// immediate Tick after reconciliation.
func (s *Service) Reconcile(ctx context.Context) error {
	store, err := s.schedulerStore()
	if err != nil {
		return err
	}
	now := s.clock().UTC()
	var problems []error
	if err := s.completeAndAdoptActive(ctx, store, now); err != nil {
		problems = append(problems, err)
	}
	due, err := store.ListDueAutomations(ctx, now, dueScanLimit)
	if err != nil {
		problems = append(problems, err)
	} else {
		for _, definition := range due {
			if err := s.materializeDefinition(ctx, store, definition, now); err != nil {
				problems = append(problems, err)
			}
		}
	}
	return errors.Join(problems...)
}

func (s *Service) completeAndAdoptActive(ctx context.Context, store schedulerStore, now time.Time) error {
	runs, err := store.ListActiveAutomationRuns(ctx)
	if err != nil {
		return err
	}
	var problems []error
	for _, run := range runs {
		if run.Status == domain.AutomationRunSpawning {
			session, ok, lookupErr := store.GetSessionByAutomationRunID(ctx, run.ID)
			if lookupErr != nil {
				problems = append(problems, lookupErr)
			} else if ok && session.AutomationLaunchCompleted {
				_, lookupErr = store.MarkAutomationRunRunning(ctx, run.ID, session.ID, now)
				if lookupErr != nil {
					problems = append(problems, lookupErr)
				} else if automationSessionSettled(session) {
					if _, lookupErr = store.CompleteAutomationRun(ctx, run.ID, now); lookupErr != nil {
						problems = append(problems, lookupErr)
					}
				}
			} else if ok && run.LeaseExpiresAt != nil && !run.LeaseExpiresAt.After(now) {
				if lookupErr = s.rollbackIncompleteLaunch(ctx, store, run, session, now); lookupErr != nil {
					problems = append(problems, lookupErr)
				}
			} else if !ok && run.LeaseExpiresAt != nil && !run.LeaseExpiresAt.After(now) {
				// The daemon died between claiming and session creation, and
				// startup reconciliation ran before the lease expired. Release
				// the claim so later occurrences are not blocked forever.
				if _, lookupErr = store.ReleaseAutomationRun(ctx, run.ID, "Recovered expired spawn claim", now); lookupErr != nil {
					problems = append(problems, lookupErr)
				}
			}
			continue
		}
		var session domain.SessionRecord
		var ok bool
		if run.SessionID != nil {
			session, ok, err = store.GetSession(ctx, *run.SessionID)
		} else {
			session, ok, err = store.GetSessionByAutomationRunID(ctx, run.ID)
		}
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !ok {
			_, failErr := store.FailAutomationRun(ctx, run.ID, "Linked session is missing", now)
			if failErr != nil {
				problems = append(problems, failErr)
			}
			continue
		}
		if run.SessionID == nil {
			if _, err := store.MarkAutomationRunRunning(ctx, run.ID, session.ID, now); err != nil {
				problems = append(problems, err)
			}
		}
		if automationSessionSettled(session) {
			if _, err := store.CompleteAutomationRun(ctx, run.ID, now); err != nil {
				problems = append(problems, err)
			}
		}
	}
	return errors.Join(problems...)
}

// automationSessionSettled reports whether a scheduled session has finished
// the work it was launched for, which ends its run and lets the next
// occurrence start. Terminating the session still counts, but so does the
// agent settling on its own: idle, waiting for its next instruction, or
// exited. The session itself stays open so its output can be read.
//
// A spawn is seeded idle before the agent reports anything, and an agent may
// report idle once as it starts. Requiring a later reading than the first
// signal means the agent has reported at least twice, so a run is not
// completed before its prompt was ever worked on. A blocked agent, stopped on
// a permission or approval prompt, is still mid-task and keeps its run.
func automationSessionSettled(session domain.SessionRecord) bool {
	if session.IsTerminated {
		return true
	}
	if session.ProvisionState.IsProvisioning() || session.FirstSignalAt.IsZero() ||
		!session.Activity.LastActivityAt.After(session.FirstSignalAt) {
		return false
	}
	switch session.Activity.State {
	case domain.ActivityIdle, domain.ActivityWaitingInput, domain.ActivityExited:
		return true
	default:
		return false
	}
}

func (s *Service) rollbackIncompleteLaunch(ctx context.Context, store schedulerStore, run domain.AutomationRun, session domain.SessionRecord, now time.Time) error {
	if s.spawner == nil {
		return fmt.Errorf("cannot roll back incomplete automation session %s: session spawner is unavailable", session.ID)
	}
	if _, err := s.spawner.Kill(ctx, session.ID); err != nil {
		return fmt.Errorf("roll back incomplete automation session %s: %w", session.ID, err)
	}
	_, err := store.FailAutomationRun(ctx, run.ID, "Recovered incomplete session launch", now)
	return err
}

func runError(err error) string {
	message := strings.Join(strings.Fields(err.Error()), " ")
	if len(message) > maxRunErrorBytes {
		message = message[:maxRunErrorBytes]
	}
	return message
}
