package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AccountHandoffEvent marks, on a turn refused for a usage limit, the account
// the session moved to before retrying the prompt there.
const AccountHandoffEvent = "account.limit_handoff"

const (
	// A failure older than this is left to the person: the handoff is for the
	// moment the limit hits, not for history found when the setting turns on.
	accountHandoffWindow = 10 * time.Minute
	// An account left with no known reset stays out for one session window.
	accountHandoffCooldown = 5 * time.Hour
	// A step that keeps failing transiently is given up after this long.
	accountHandoffGiveUp  = 10 * time.Minute
	accountHandoffBackoff = 15 * time.Second
	// Each worker may read every account's limits and wake a provider.
	accountHandoffWorkers = 4
)

// accountHandoffStore is optional so the narrow Store port and its fakes do
// not grow; production's SQLite store implements it.
type accountHandoffStore interface {
	AccountHandoffCandidateTurns(context.Context, time.Time) ([]domain.ConversationTurn, error)
	ClaimChatAccountHandoff(context.Context, domain.ChatAccountHandoff) (bool, error)
	ChatAccountHandoff(context.Context, string) (domain.ChatAccountHandoff, bool, error)
	ActiveChatAccountHandoffs(context.Context) ([]domain.ChatAccountHandoff, error)
	AdvanceChatAccountHandoff(context.Context, domain.ChatAccountHandoff, domain.ChatAccountHandoffPhase) (bool, error)
	RecentlyLeftAccounts(context.Context, domain.SessionID, time.Time, time.Time) ([]string, error)
	SessionAccountPinned(context.Context, domain.SessionID) (bool, error)
	GetSessionInterfaceTransition(context.Context, string) (domain.SessionInterfaceTransition, bool, error)
	GetLatestSessionInterfaceTransition(context.Context, domain.SessionID) (domain.SessionInterfaceTransition, bool, error)
	ListPendingSessionInterfaceTransitionMessages(context.Context, string) ([]domain.SessionInterfaceTransitionMessage, error)
}

type accountHandoffRetryStore interface {
	AppendAccountHandoffRetryUserMessage(context.Context, string, domain.SessionID, string, domain.ConversationMessage, string, string, time.Time) (bool, error)
}

// AccountHandoffAccounts reads accounts and their native limits.
type AccountHandoffAccounts interface {
	SessionAccountID(context.Context, domain.SessionID, domain.AgentHarness) (string, error)
	HandoffTarget(context.Context, domain.SessionID, domain.AgentHarness, string, map[string]bool) (string, error)
}

// AccountHandoffMover starts Session Manager's durable account move.
type AccountHandoffMover interface {
	StartAccountTransition(context.Context, domain.SessionID, string, domain.SessionInterfaceTransitionPolicy) (domain.SessionInterfaceTransition, error)
}

type accountHandoffDeps struct {
	enabled  func(context.Context) bool
	accounts AccountHandoffAccounts
	mover    AccountHandoffMover
}

// SetAccountHandoff wires the setting, the accounts, and the move the
// automatic handoff uses. Without it the handoff never runs.
func (s *Service) SetAccountHandoff(enabled func(context.Context) bool, accounts AccountHandoffAccounts, mover AccountHandoffMover) {
	s.handoffMu.Lock()
	defer s.handoffMu.Unlock()
	s.handoff = accountHandoffDeps{enabled: enabled, accounts: accounts, mover: mover}
}

// HandoffUsageLimitedTurns advances every automatic account handoff by one
// step, and, while the setting is on, claims recent usage-limit refusals.
//
// A handoff is a durable saga keyed by the refused turn, so concurrent ticks
// and a restart resume it rather than repeat it:
//
//  1. selecting: claim the turn, then pick another signed-in account of the
//     same harness whose native limits verify room for the request's model.
//     None, or a session told to stay on its account, keeps the account and
//     falls back to auto-resume at a known reset.
//  2. moving: Session Manager's account move runs; it commits or rolls back.
//  3. moved: a note names the new account, then the prompt is retried once.
//     The retry's transaction moves the record to retried.
func (s *Service) HandoffUsageLimitedTurns(ctx context.Context) error {
	store, ok := s.store.(accountHandoffStore)
	s.handoffMu.Lock()
	deps := s.handoff
	if !ok || deps.accounts == nil || deps.mover == nil || deps.enabled == nil || s.handoffScanning {
		s.handoffMu.Unlock()
		return nil
	}
	s.handoffScanning = true
	s.handoffMu.Unlock()

	records, err := store.ActiveChatAccountHandoffs(ctx)
	if err == nil && deps.enabled(ctx) {
		var claimed []domain.ChatAccountHandoff
		claimed, err = s.claimAccountHandoffs(ctx, store, deps)
		records = append(records, claimed...)
	}
	s.handoffMu.Lock()
	s.handoffScanning = false
	if err != nil || ctx.Err() != nil {
		s.handoffMu.Unlock()
		return errors.Join(err, ctx.Err())
	}
	var mine []domain.ChatAccountHandoff
	for _, rec := range records {
		if len(s.handoffInFlight) >= accountHandoffWorkers {
			break
		}
		if s.handoffInFlight[rec.SourceTurnID] || s.handoffRetryAt[rec.SourceTurnID].After(s.now()) {
			continue
		}
		if s.handoffInFlight == nil {
			s.handoffInFlight = map[string]bool{}
		}
		s.handoffInFlight[rec.SourceTurnID] = true
		mine = append(mine, rec)
	}
	s.handoffMu.Unlock()

	failures := make(chan error, len(mine))
	var workers sync.WaitGroup
	for _, rec := range mine {
		workers.Go(func() {
			err := s.stepAccountHandoff(ctx, store, deps, rec)
			s.handoffMu.Lock()
			delete(s.handoffInFlight, rec.SourceTurnID)
			if err != nil && ctx.Err() == nil {
				if s.handoffRetryAt == nil {
					s.handoffRetryAt = map[string]time.Time{}
				}
				s.handoffRetryAt[rec.SourceTurnID] = s.now().Add(accountHandoffBackoff)
			} else {
				delete(s.handoffRetryAt, rec.SourceTurnID)
			}
			s.handoffMu.Unlock()
			if err != nil {
				failures <- fmt.Errorf("account handoff %s: %w", rec.SourceTurnID, err)
			}
		})
	}
	workers.Wait()
	close(failures)
	var errs []error
	for err := range failures {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// claimAccountHandoffs records recent usage-limit refusals of account
// harnesses. The insert is the claim: a turn gets at most one handoff.
func (s *Service) claimAccountHandoffs(ctx context.Context, store accountHandoffStore, deps accountHandoffDeps) ([]domain.ChatAccountHandoff, error) {
	turns, err := store.AccountHandoffCandidateTurns(ctx, s.now().Add(-accountHandoffWindow))
	if err != nil {
		return nil, err
	}
	var claimed []domain.ChatAccountHandoff
	for _, turn := range turns {
		if turn.UsageLimit == nil {
			continue
		}
		record, found, err := s.sessions.GetSession(ctx, turn.HandledBySessionID)
		if err != nil {
			return claimed, err
		}
		if _, accounts := domain.AgentAccountProviderFor(record.Harness); !found || !accounts {
			continue
		}
		account, err := deps.accounts.SessionAccountID(ctx, record.ID, record.Harness)
		if err != nil || account == "" {
			s.log.Debug("account handoff: session account unavailable", "session", record.ID, "error", err)
			continue
		}
		now := s.now()
		rec := domain.ChatAccountHandoff{
			SourceTurnID: turn.ID, SessionID: record.ID, ConversationID: turn.ConversationID,
			SourceAccountID: account, Phase: domain.ChatAccountHandoffSelecting,
			SourceResetsAt: turn.UsageLimit.ResetsAt, CreatedAt: now, UpdatedAt: now,
		}
		ok, err := store.ClaimChatAccountHandoff(ctx, rec)
		if err != nil {
			return claimed, err
		}
		if ok {
			claimed = append(claimed, rec)
		}
	}
	return claimed, nil
}

func (s *Service) stepAccountHandoff(ctx context.Context, store accountHandoffStore, deps accountHandoffDeps, rec domain.ChatAccountHandoff) error {
	// Another worker or a person may have moved it on since the scan.
	current, found, err := store.ChatAccountHandoff(ctx, rec.SourceTurnID)
	if err != nil || !found {
		return err
	}
	rec = current
	switch rec.Phase {
	case domain.ChatAccountHandoffSelecting:
		return s.selectAccountHandoff(ctx, store, deps, rec)
	case domain.ChatAccountHandoffMoving:
		return s.settleAccountHandoffMove(ctx, store, deps, rec)
	case domain.ChatAccountHandoffMoved:
		return s.retryAccountHandoff(ctx, store, rec)
	}
	return nil
}

func (s *Service) selectAccountHandoff(ctx context.Context, store accountHandoffStore, deps accountHandoffDeps, rec domain.ChatAccountHandoff) error {
	source, err := s.store.TurnByID(ctx, rec.SourceTurnID)
	if errors.Is(err, domain.ErrNoConversationTurn) {
		return s.endAccountHandoff(ctx, store, rec, domain.ChatAccountHandoffSkipped, "The refused turn no longer exists.")
	}
	if err != nil {
		return err
	}
	// A crash between starting the move and recording it leaves the move
	// running with this record still selecting: adopt it, never start another.
	if latest, found, err := store.GetLatestSessionInterfaceTransition(ctx, rec.SessionID); err != nil {
		return err
	} else if found && latest.AccountMove() && latest.SourceAccountID == rec.SourceAccountID && !latest.CreatedAt.Before(rec.CreatedAt) {
		return s.recordAccountHandoffMove(ctx, store, deps, rec, latest)
	}
	record, err := s.requireChatSession(ctx, rec.SessionID)
	if err == nil {
		err = validateUsageLimitSession(record)
	}
	if err == nil {
		err = s.validateAccountHandoffSource(ctx, source)
	}
	if err != nil {
		if permanentUsageLimitResumeError(err) {
			return s.endAccountHandoff(ctx, store, rec, domain.ChatAccountHandoffSkipped, err.Error())
		}
		return s.giveUpAfter(ctx, store, rec, err)
	}
	if pinned, err := store.SessionAccountPinned(ctx, rec.SessionID); err != nil {
		return err
	} else if pinned {
		return s.stayOnAccount(ctx, store, rec, source, "This session stays on its account.")
	}
	// One move per prompt: a retry the last handoff made, refused again, stays.
	if source.RetryOfTurnID != "" {
		prior, found, err := store.ChatAccountHandoff(ctx, source.RetryOfTurnID)
		if err != nil {
			return err
		}
		if found && prior.Phase == domain.ChatAccountHandoffRetried {
			return s.stayOnAccount(ctx, store, rec, source, "The retry on the new account was refused for a usage limit too.")
		}
	}
	left, err := store.RecentlyLeftAccounts(ctx, rec.SessionID, s.now(), s.now().Add(-accountHandoffCooldown))
	if err != nil {
		return err
	}
	exclude := map[string]bool{rec.SourceAccountID: true}
	for _, id := range left {
		exclude[id] = true
	}
	model := ""
	if conversation, err := s.store.ConversationForSession(ctx, rec.SessionID); err == nil {
		model = conversation.Settings.Model
	}
	target, err := deps.accounts.HandoffTarget(ctx, rec.SessionID, record.Harness, model, exclude)
	if errors.Is(err, ports.ErrAgentAccountChanged) {
		return s.endAccountHandoff(ctx, store, rec, domain.ChatAccountHandoffSkipped, "The session's account changed.")
	}
	if err != nil {
		return s.giveUpAfter(ctx, store, rec, err)
	}
	if target == "" {
		return s.stayOnAccount(ctx, store, rec, source, "No other signed-in account has room for this request.")
	}
	transition, err := deps.mover.StartAccountTransition(ctx, rec.SessionID, target, domain.SessionInterfaceTransitionDrain)
	if err != nil {
		return s.stayOnAccount(ctx, store, rec, source, "AO could not start the move: "+err.Error())
	}
	return s.recordAccountHandoffMove(ctx, store, deps, rec, transition)
}

func (s *Service) recordAccountHandoffMove(ctx context.Context, store accountHandoffStore, deps accountHandoffDeps, rec domain.ChatAccountHandoff, transition domain.SessionInterfaceTransition) error {
	next := rec
	next.Phase, next.TargetAccountID, next.TransitionID, next.UpdatedAt = domain.ChatAccountHandoffMoving, transition.TargetAccountID, transition.ID, s.now()
	if ok, err := store.AdvanceChatAccountHandoff(ctx, next, rec.Phase); err != nil || !ok {
		return err
	}
	return s.settleAccountHandoffMove(ctx, store, deps, next)
}

func (s *Service) settleAccountHandoffMove(ctx context.Context, store accountHandoffStore, deps accountHandoffDeps, rec domain.ChatAccountHandoff) error {
	transition, found, err := store.GetSessionInterfaceTransition(ctx, rec.TransitionID)
	if err != nil {
		return err
	}
	if !found {
		return s.endAccountHandoff(ctx, store, rec, domain.ChatAccountHandoffRolledBack, "The account move is missing.")
	}
	if !transition.Phase.Terminal() {
		return nil
	}
	if transition.Phase != domain.SessionInterfaceTransitionCompleted {
		detail := transition.ErrorDetail
		if detail == "" {
			detail = "The account move ended " + string(transition.Phase) + "."
		}
		// Only a move that failed on its own falls back; a cancelled one was a
		// person's call, and one needing recovery shows its own notice.
		if transition.Phase == domain.SessionInterfaceTransitionFailed {
			if source, err := s.store.TurnByID(ctx, rec.SourceTurnID); err == nil {
				s.armUsageLimitFallback(ctx, source)
			}
		}
		return s.endAccountHandoff(ctx, store, rec, domain.ChatAccountHandoffRolledBack, detail)
	}
	record, found, err := s.sessions.GetSession(ctx, rec.SessionID)
	if err != nil {
		return err
	}
	if !found {
		return s.endAccountHandoff(ctx, store, rec, domain.ChatAccountHandoffSkipped, "The session no longer exists.")
	}
	bound, err := deps.accounts.SessionAccountID(ctx, rec.SessionID, record.Harness)
	if err != nil {
		return err
	}
	if bound != rec.TargetAccountID {
		return s.endAccountHandoff(ctx, store, rec, domain.ChatAccountHandoffSkipped, "The session moved to another account since.")
	}
	if err := s.noteAccountHandoff(ctx, rec); err != nil {
		return err
	}
	next := rec
	next.Phase, next.UpdatedAt = domain.ChatAccountHandoffMoved, s.now()
	if ok, err := store.AdvanceChatAccountHandoff(ctx, next, domain.ChatAccountHandoffMoving); err != nil || !ok {
		return err
	}
	return s.retryAccountHandoff(ctx, store, next)
}

// noteAccountHandoff writes the durable thread note on the refused turn. Its
// provider item id makes a repeat after a restart replace, not duplicate, it.
func (s *Service) noteAccountHandoff(ctx context.Context, rec domain.ChatAccountHandoff) error {
	source, err := s.store.TurnByID(ctx, rec.SourceTurnID)
	if err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]string{"event": AccountHandoffEvent, "accountId": rec.TargetAccountID, "fromAccountId": rec.SourceAccountID})
	if err != nil {
		return fmt.Errorf("encode account handoff: %w", err)
	}
	return s.store.UpsertActivity(ctx, rec.ConversationID, source.ProviderTurnID, domain.ConversationActivity{
		ID:             s.newID(),
		Kind:           domain.ActivityKindSystem,
		Status:         domain.ActivityStatusCompleted,
		Summary:        "Moved to another account",
		Detail:         detail,
		ProviderItemID: "account-handoff:" + rec.SourceTurnID,
	}, s.now())
}

func (s *Service) retryAccountHandoff(ctx context.Context, store accountHandoffStore, rec domain.ChatAccountHandoff) error {
	// A message sent during the move is delivered after it; it comes first,
	// and the latest-turn check then withdraws the retry.
	if pending, err := store.ListPendingSessionInterfaceTransitionMessages(ctx, rec.TransitionID); err != nil {
		return err
	} else if len(pending) > 0 {
		return s.giveUpAfter(ctx, store, rec, errors.New("messages sent during the move are still being delivered"))
	}
	// Hibernation recovery already permits a 60-second native wake.
	attemptCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	_, err := s.retryTurn(attemptCtx, rec.SessionID, rec.SourceTurnID, retryAccountHandoff)
	cancel()
	if err == nil || ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, domain.ErrChatAccountHandoffConsumed) || permanentUsageLimitResumeError(err) {
		// The retry transaction moved it to retried, or a person or a later
		// move withdrew it; a record still moved is withdrawn.
		return s.endAccountHandoff(ctx, store, rec, domain.ChatAccountHandoffSkipped, "The retry was withdrawn: "+err.Error())
	}
	return s.giveUpAfter(ctx, store, rec, err)
}

// validateAccountHandoffSource is the auto-resume check without a deadline:
// the move, not the reset, makes the retry useful.
func (s *Service) validateAccountHandoffSource(ctx context.Context, source domain.ConversationTurn) error {
	store, ok := s.store.(usageLimitStore)
	if !ok {
		return ErrUsageLimitAutoResumeInvalid
	}
	if source.State != domain.TurnStateFailed || source.RolledBackAt != nil || source.UsageLimit == nil {
		return fmt.Errorf("%w: this turn is not an actionable usage-limit failure", ErrUsageLimitAutoResumeInvalid)
	}
	return validateUsageLimitRetry(ctx, s.store, store, source)
}

// retryAccountHandoffTurn dispatches the refused prompt once on the new
// account. It rechecks lineage under the send lock, and the retry transaction
// consumes the handoff, so neither a second worker nor a click repeats it.
func (c *Controller) retryAccountHandoffTurn(ctx context.Context, turnID string) (domain.ConversationTurn, error) {
	if !c.sendMu.TryLock() {
		return domain.ConversationTurn{}, ErrTurnRunning
	}
	defer c.sendMu.Unlock()
	store, ok := c.store.(usageLimitStore)
	if !ok {
		return domain.ConversationTurn{}, ErrUsageLimitAutoResumeInvalid
	}
	source, err := c.store.TurnByID(ctx, turnID)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	if source.ConversationID != c.conversation.ID || source.HandledBySessionID != c.sessionID ||
		source.State != domain.TurnStateFailed || source.RolledBackAt != nil || source.UsageLimit == nil {
		return domain.ConversationTurn{}, ErrUsageLimitAutoResumeInvalid
	}
	if err := validateUsageLimitRetry(ctx, c.store, store, source); err != nil {
		return domain.ConversationTurn{}, err
	}
	return c.retryTurnLocked(ctx, turnID, retryAccountHandoff)
}

// stayOnAccount keeps the session where it is and applies the existing
// usage-limit recovery: auto-resume at a known reset, or nothing to do but the
// person's own retry when the reset is unknown.
func (s *Service) stayOnAccount(ctx context.Context, store accountHandoffStore, rec domain.ChatAccountHandoff, source domain.ConversationTurn, detail string) error {
	s.armUsageLimitFallback(ctx, source)
	return s.endAccountHandoff(ctx, store, rec, domain.ChatAccountHandoffStayed, detail)
}

func (s *Service) armUsageLimitFallback(ctx context.Context, source domain.ConversationTurn) {
	if source.UsageLimit == nil || source.UsageLimit.AutoResume {
		return
	}
	err := s.SetTurnAutoResume(ctx, source.HandledBySessionID, source.ID, true)
	if err != nil && !errors.Is(err, ErrUsageLimitResetUnknown) {
		s.log.Info("account handoff: auto-resume fallback unavailable", "turn", source.ID, "error", err)
	}
}

// giveUpAfter keeps a transient failure for the next tick, until the handoff
// has been stuck too long; then the session stays and recovery is manual.
func (s *Service) giveUpAfter(ctx context.Context, store accountHandoffStore, rec domain.ChatAccountHandoff, cause error) error {
	if s.now().Sub(rec.UpdatedAt) < accountHandoffGiveUp {
		return cause
	}
	return s.endAccountHandoff(ctx, store, rec, domain.ChatAccountHandoffSkipped, "Gave up: "+cause.Error())
}

func (s *Service) endAccountHandoff(ctx context.Context, store accountHandoffStore, rec domain.ChatAccountHandoff, phase domain.ChatAccountHandoffPhase, detail string) error {
	next := rec
	next.Phase, next.Detail, next.UpdatedAt = phase, detail, s.now()
	_, err := store.AdvanceChatAccountHandoff(ctx, next, rec.Phase)
	if err == nil {
		s.log.Info("account handoff ended", "session", rec.SessionID, "turn", rec.SourceTurnID, "phase", phase, "detail", detail)
	}
	return err
}
