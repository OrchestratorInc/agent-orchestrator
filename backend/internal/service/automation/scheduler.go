package automation

import (
	"context"
	"errors"
	"fmt"
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
)

// SessionSpawner is the existing user-facing session launch boundary.
type SessionSpawner interface {
	Spawn(context.Context, ports.SpawnConfig) (domain.Session, int, int, error)
	Kill(context.Context, domain.SessionID) (bool, error)
	// HibernateChatForAutomation stops a finished Chat session's provider while
	// keeping it resumable. False without an error means it still has work.
	HibernateChatForAutomation(context.Context, domain.SessionID) (bool, error)
}

// schedulerStore is deliberately separate from CRUD Store so focused CRUD
// tests and alternate read adapters need not implement daemon-only operations.
type schedulerStore interface {
	ListDueAutomations(context.Context, time.Time, int64) ([]domain.Automation, error)
	MaterializeAutomationRun(context.Context, domain.AutomationID, time.Time, domain.AutomationRun, time.Time, time.Time) (bool, error)
	ClaimNextAutomationRun(context.Context, time.Time, time.Time) (domain.AutomationRun, bool, error)
	ListActiveAutomationRuns(context.Context) ([]domain.AutomationRun, error)
	GetSessionByAutomationRunID(context.Context, domain.AutomationRunID) (domain.SessionRecord, bool, error)
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	MarkAutomationRunRunning(context.Context, domain.AutomationRunID, domain.SessionID, time.Time, time.Time) (bool, error)
	CompleteAutomationRun(context.Context, domain.AutomationRunID, time.Time) (bool, error)
	FailAutomationRun(context.Context, domain.AutomationRunID, string, time.Time) (bool, error)
	ReleaseAutomationRun(context.Context, domain.AutomationRunID, string, time.Time) (bool, error)
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
	// However many occurrences fell due, the automation owes one run, for the
	// most recent of them; older ones are dropped. The store inserts that run,
	// or moves a run already waiting to this time and skips any other waiting
	// runs, in the same transaction that advances the schedule.
	last, next, err := occurrencesAround(definition.RRuleText, definition.Timezone, now)
	if err != nil {
		return err
	}
	if last.Before(definition.NextRunAt) {
		last = definition.NextRunAt.UTC()
	}
	run := domain.AutomationRun{
		ID: domain.AutomationRunID("automation-run-" + s.newID()), AutomationID: definition.ID,
		ScheduledFor: last, Status: domain.AutomationRunPending,
		CreatedAt: now, UpdatedAt: now,
	}
	_, err = store.MaterializeAutomationRun(ctx, definition.ID, definition.NextRunAt, run, next, now)
	return err
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
				started := s.clock().UTC()
				_, markErr := store.MarkAutomationRunRunning(context.WithoutCancel(ctx), run.ID, session.ID, started, started)
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
	// Record the start after Spawn returns: by then the launch, including any
	// after-start prompt delivery, has completed, so activity the agent reported
	// before its prompt arrived cannot settle the run.
	started := s.clock().UTC()
	_, err = store.MarkAutomationRunRunning(context.WithoutCancel(ctx), run.ID, session.ID, started, started)
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
				started := recoveredStart(session)
				_, lookupErr = store.MarkAutomationRunRunning(ctx, run.ID, session.ID, started, now)
				if lookupErr != nil {
					problems = append(problems, lookupErr)
				} else if automationSessionSettled(session, started) {
					if lookupErr = s.finishSettledRun(ctx, store, run.ID, session, now); lookupErr != nil {
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
		var startedAt time.Time
		if run.StartedAt != nil {
			startedAt = *run.StartedAt
		}
		if run.SessionID == nil {
			if startedAt.IsZero() {
				startedAt = recoveredStart(session)
			}
			if _, err := store.MarkAutomationRunRunning(ctx, run.ID, session.ID, startedAt, now); err != nil {
				problems = append(problems, err)
			}
		}
		if automationSessionSettled(session, startedAt) {
			if err := s.finishSettledRun(ctx, store, run.ID, session, now); err != nil {
				problems = append(problems, err)
			}
		}
	}
	return errors.Join(problems...)
}

// automationSessionSettled reports whether a scheduled session has finished
// the work it was launched for, which ends its run and lets the next
// occurrence start. A terminated session has; otherwise the agent must be idle
// after a turn, or exited, on a reading taken after the run started.
//
// startedAt is when the launch, and its prompt delivery, completed. Readings
// from before it come from the agent starting up (a seeded idle, a
// SessionStart, an empty Stop before the prompt was pasted), not from working
// on the prompt. Rows without a start time fall back to requiring a reading
// later than the first signal.
//
// waiting_input and blocked keep the run: harnesses report tool-approval and
// input requests as either (the chat controller and several terminal adapters
// use waiting_input for approvals), so the agent is still mid-task.
func automationSessionSettled(session domain.SessionRecord, startedAt time.Time) bool {
	if session.IsTerminated {
		return true
	}
	if session.ProvisionState.IsProvisioning() || session.FirstSignalAt.IsZero() {
		return false
	}
	if state := session.Activity.State; state != domain.ActivityIdle && state != domain.ActivityExited {
		return false
	}
	reading := session.Activity.LastActivityAt
	if startedAt.IsZero() {
		return reading.After(session.FirstSignalAt)
	}
	return reading.After(startedAt)
}

// recoveredStart is the start recorded for a run adopted after a restart,
// whose launch completed without the scheduler seeing Spawn return. The
// recovery time would hide a turn that finished while the daemon was down, so
// use the session's latest prompt time instead: the launch sets it when it
// builds the prompt and the agent's prompt-submit hook moves it forward. A
// session without one falls back to its creation time.
func recoveredStart(session domain.SessionRecord) time.Time {
	if at := session.Metadata.LatestUserPromptAt; !at.IsZero() {
		return at.UTC()
	}
	return session.CreatedAt.UTC()
}

// finishSettledRun stops a settled session's agent and then completes its run.
// Stopping it first keeps the non-overlap guarantee: a finished session left
// running could start working again (a nudge, a queued message) beside the
// next occurrence. When the agent cannot be stopped yet, the run stays running
// and the next tick retries.
func (s *Service) finishSettledRun(ctx context.Context, store schedulerStore, id domain.AutomationRunID, session domain.SessionRecord, now time.Time) error {
	if !session.IsTerminated {
		stopped, err := s.stopSettledSession(ctx, session)
		if err != nil || !stopped {
			return err
		}
	}
	_, err := store.CompleteAutomationRun(ctx, id, now)
	return err
}

// stopSettledSession hibernates a Chat session, keeping its conversation
// resumable, and kills a terminal one or a Chat session whose provider cannot
// resume. Killing keeps the transcript and any uncommitted work; only a clean
// worktree is removed. A Chat session already marked hibernated still goes
// through hibernation: its provider is gone unless an earlier shutdown failed,
// and that call finishes such a shutdown.
func (s *Service) stopSettledSession(ctx context.Context, session domain.SessionRecord) (bool, error) {
	chat := domain.NormalizeSessionMode(session.Mode) == domain.SessionModeChat
	if !chat && session.HibernatedAt != nil {
		return true, nil
	}
	if s.spawner == nil {
		return false, fmt.Errorf("cannot stop settled automation session %s: session spawner is unavailable", session.ID)
	}
	if chat && (session.HibernatedAt != nil || session.Activity.State != domain.ActivityExited) {
		hibernated, err := s.spawner.HibernateChatForAutomation(ctx, session.ID)
		if !errors.Is(err, ports.ErrChatHibernationUnsupported) {
			if err != nil {
				return false, fmt.Errorf("hibernate settled automation session %s: %w", session.ID, err)
			}
			return hibernated, nil
		}
	}
	if _, err := s.spawner.Kill(ctx, session.ID); err != nil {
		return false, fmt.Errorf("kill settled automation session %s: %w", session.ID, err)
	}
	return true, nil
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
