package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AccountHandoffEvent is the thread note event of an automatic usage-limit account move.
const AccountHandoffEvent = "account.limit_handoff"

const (
	accountHandoffWindow  = 10 * time.Minute
	accountHandoffGiveUp  = 10 * time.Minute
	accountHandoffBackoff = 15 * time.Second
	accountHandoffWorkers = 4

	accountHandoffRetrySent    = "sent"
	accountHandoffRetrySkipped = "skipped"
)

type accountHandoffStore interface {
	AccountHandoffCandidateTurns(context.Context, time.Time) ([]domain.ConversationTurn, error)
	GetSessionInterfaceTransition(context.Context, string) (domain.SessionInterfaceTransition, bool, error)
	ListPendingSessionInterfaceTransitionMessages(context.Context, string) ([]domain.SessionInterfaceTransitionMessage, error)
}

// AccountHandoffAccounts reads accounts and their native limits.
type AccountHandoffAccounts interface {
	SessionAccountID(context.Context, domain.SessionID, domain.AgentHarness) (string, error)
	HandoffTarget(context.Context, domain.SessionID, domain.AgentHarness, string) (string, error)
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

type accountHandoff struct {
	turnID         string
	conversationID string
	sessionID      domain.SessionID

	busy    bool
	done    bool
	retryAt time.Time
	moved   atomic.Bool

	targetAccount string
	transitionID  string
	stepAt        time.Time
}

// SetAccountHandoff wires the automatic usage-limit account handoff.
func (s *Service) SetAccountHandoff(enabled func(context.Context) bool, accounts AccountHandoffAccounts, mover AccountHandoffMover) {
	s.handoffMu.Lock()
	defer s.handoffMu.Unlock()
	s.handoff = accountHandoffDeps{enabled: enabled, accounts: accounts, mover: mover}
	s.handoffSince = s.now()
}

// HandoffUsageLimitedTurns advances the automatic usage-limit account handoffs by one step.
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

	var claimed []*accountHandoff
	var err error
	if deps.enabled(ctx) {
		claimed, err = s.claimAccountHandoffs(ctx, store, deps)
	}
	s.handoffMu.Lock()
	s.handoffScanning = false
	if s.handoffs == nil {
		s.handoffs = map[string]*accountHandoff{}
	}
	for _, h := range claimed {
		s.handoffs[h.turnID] = h
	}
	if err != nil || ctx.Err() != nil {
		s.handoffMu.Unlock()
		return errors.Join(err, ctx.Err())
	}
	now := s.now()
	var mine []*accountHandoff
	running := 0
	for id, h := range s.handoffs {
		switch {
		case h.busy:
			running++
		case h.done && now.After(h.stepAt.Add(accountHandoffWindow)):
			delete(s.handoffs, id)
		case !h.done && !h.retryAt.After(now):
			mine = append(mine, h)
		}
	}
	if free := max(accountHandoffWorkers-running, 0); len(mine) > free {
		mine = mine[:free]
	}
	for _, h := range mine {
		h.busy = true
	}
	s.handoffMu.Unlock()

	failures := make(chan error, len(mine))
	var workers sync.WaitGroup
	for _, h := range mine {
		workers.Go(func() {
			done, err := s.stepAccountHandoff(ctx, store, deps, h)
			s.handoffMu.Lock()
			h.busy, h.done = false, done
			h.retryAt = time.Time{}
			if err != nil && ctx.Err() == nil {
				h.retryAt = s.now().Add(accountHandoffBackoff)
			}
			s.handoffMu.Unlock()
			if err != nil {
				failures <- fmt.Errorf("account handoff %s: %w", h.turnID, err)
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

func (s *Service) claimAccountHandoffs(ctx context.Context, store accountHandoffStore, deps accountHandoffDeps) ([]*accountHandoff, error) {
	s.handoffMu.Lock()
	since := s.handoffSince
	s.handoffMu.Unlock()
	if recent := s.now().Add(-accountHandoffWindow); recent.After(since) {
		since = recent
	}
	turns, err := store.AccountHandoffCandidateTurns(ctx, since)
	if err != nil {
		return nil, err
	}
	var claimed []*accountHandoff
	for _, turn := range turns {
		if turn.UsageLimit == nil {
			continue
		}
		s.handoffMu.Lock()
		_, seen := s.handoffs[turn.ID]
		s.handoffMu.Unlock()
		if seen {
			continue
		}
		record, found, err := s.sessions.GetSession(ctx, turn.HandledBySessionID)
		if err != nil {
			return claimed, err
		}
		if _, accounts := domain.AgentAccountProviderFor(record.Harness); !found || !accounts {
			continue
		}
		if account, err := deps.accounts.SessionAccountID(ctx, record.ID, record.Harness); err != nil || account == "" {
			s.log.Debug("account handoff: session account unavailable", "session", record.ID, "error", err)
			continue
		}
		claimed = append(claimed, &accountHandoff{turnID: turn.ID, conversationID: turn.ConversationID, sessionID: record.ID, stepAt: s.now()})
	}
	return claimed, nil
}

func (s *Service) stepAccountHandoff(ctx context.Context, store accountHandoffStore, deps accountHandoffDeps, h *accountHandoff) (bool, error) {
	switch {
	case h.transitionID == "":
		return s.selectAccountHandoff(ctx, store, deps, h)
	case !h.moved.Load():
		return s.settleAccountHandoffMove(ctx, store, deps, h)
	}
	return s.retryAccountHandoff(ctx, store, deps, h)
}

func (s *Service) selectAccountHandoff(ctx context.Context, store accountHandoffStore, deps accountHandoffDeps, h *accountHandoff) (bool, error) {
	source, err := s.store.TurnByID(ctx, h.turnID)
	if errors.Is(err, domain.ErrNoConversationTurn) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	record, err := s.requireChatSession(ctx, h.sessionID)
	if err == nil {
		err = validateUsageLimitSession(record)
	}
	if err == nil {
		err = actionableUsageLimitTurn(ctx, s.store, source)
	}
	if err != nil {
		if permanentUsageLimitResumeError(err) {
			return true, nil
		}
		return s.giveUpAfter(h, err)
	}
	if source.RetryOfTurnID != "" && s.accountHandoffRetried(source.RetryOfTurnID) {
		return s.stayOnAccount(ctx, h, source, "The retry on the new account was refused for a usage limit too.")
	}
	model := ""
	if conversation, err := s.store.ConversationForSession(ctx, h.sessionID); err == nil {
		model = conversation.Settings.Model
	}
	target, err := deps.accounts.HandoffTarget(ctx, h.sessionID, record.Harness, model)
	if errors.Is(err, ports.ErrAgentAccountChanged) {
		return true, nil
	}
	if err != nil {
		return s.giveUpAfter(h, err)
	}
	if target == "" {
		return s.stayOnAccount(ctx, h, source, "No other signed-in account has room for this request.")
	}
	if !deps.enabled(ctx) {
		return true, nil
	}
	transition, err := deps.mover.StartAccountTransition(ctx, h.sessionID, target, domain.SessionInterfaceTransitionDrain)
	if err != nil {
		return s.stayOnAccount(ctx, h, source, "could not start the move: "+err.Error())
	}
	h.targetAccount, h.transitionID, h.stepAt = transition.TargetAccountID, transition.ID, s.now()
	return s.settleAccountHandoffMove(ctx, store, deps, h)
}

func (s *Service) accountHandoffRetried(turnID string) bool {
	s.handoffMu.Lock()
	defer s.handoffMu.Unlock()
	prior, found := s.handoffs[turnID]
	return found && prior.moved.Load()
}

func (s *Service) settleAccountHandoffMove(ctx context.Context, store accountHandoffStore, deps accountHandoffDeps, h *accountHandoff) (bool, error) {
	transition, found, err := store.GetSessionInterfaceTransition(ctx, h.transitionID)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}
	if !transition.Phase.Terminal() {
		return false, nil
	}
	if transition.Phase != domain.SessionInterfaceTransitionCompleted {
		if transition.Phase == domain.SessionInterfaceTransitionFailed {
			source, err := s.store.TurnByID(ctx, h.turnID)
			if err == nil {
				err = s.armUsageLimitFallback(ctx, source)
			}
			if err != nil && !permanentUsageLimitResumeError(err) {
				return s.giveUpAfter(h, err)
			}
		}
		s.log.Info("account handoff: move did not commit", "session", h.sessionID, "turn", h.turnID, "phase", transition.Phase)
		return true, nil
	}
	if on, err := s.onHandoffTarget(ctx, deps, h); err != nil || !on {
		return err == nil, err
	}
	if err := s.noteAccountHandoff(ctx, h, ""); err != nil {
		return false, err
	}
	h.stepAt = s.now()
	h.moved.Store(true)
	return s.retryAccountHandoff(ctx, store, deps, h)
}

func (s *Service) onHandoffTarget(ctx context.Context, deps accountHandoffDeps, h *accountHandoff) (bool, error) {
	record, found, err := s.sessions.GetSession(ctx, h.sessionID)
	if err != nil || !found {
		return false, err
	}
	bound, err := deps.accounts.SessionAccountID(ctx, h.sessionID, record.Harness)
	return err == nil && bound == h.targetAccount, err
}

func (s *Service) noteAccountHandoff(ctx context.Context, h *accountHandoff, retry string) error {
	source, err := s.store.TurnByID(ctx, h.turnID)
	if err != nil {
		return err
	}
	fields := map[string]string{"event": AccountHandoffEvent, "accountId": h.targetAccount}
	if retry != "" {
		fields["retry"] = retry
	}
	detail, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("encode account handoff: %w", err)
	}
	return s.store.UpsertActivity(ctx, h.conversationID, source.ProviderTurnID, domain.ConversationActivity{
		ID:             s.newID(),
		Kind:           domain.ActivityKindSystem,
		Status:         domain.ActivityStatusCompleted,
		Summary:        "Moved to another account",
		Detail:         detail,
		ProviderItemID: "account-handoff:" + h.turnID,
	}, s.now())
}

func (s *Service) recordAccountHandoffRetry(ctx context.Context, h *accountHandoff, retry string) {
	if err := s.noteAccountHandoff(ctx, h, retry); err != nil {
		s.log.Warn("account handoff: note not updated", "session", h.sessionID, "turn", h.turnID, "retry", retry, "error", err)
	}
}

func (s *Service) retryAccountHandoff(ctx context.Context, store accountHandoffStore, deps accountHandoffDeps, h *accountHandoff) (bool, error) {
	if on, err := s.onHandoffTarget(ctx, deps, h); err != nil {
		return false, err
	} else if !on {
		s.recordAccountHandoffRetry(ctx, h, accountHandoffRetrySkipped)
		return true, nil
	}
	if pending, err := store.ListPendingSessionInterfaceTransitionMessages(ctx, h.transitionID); err != nil {
		return false, err
	} else if len(pending) > 0 {
		return s.giveUpRetry(ctx, h, errors.New("messages sent during the move are still being delivered"))
	}
	attemptCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	err := s.retryAccountHandoffOnController(attemptCtx, h)
	cancel()
	if err == nil {
		s.recordAccountHandoffRetry(ctx, h, accountHandoffRetrySent)
		return true, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if permanentUsageLimitResumeError(err) {
		s.log.Info("account handoff: retry withdrawn", "session", h.sessionID, "turn", h.turnID, "error", err)
		s.recordAccountHandoffRetry(ctx, h, accountHandoffRetrySkipped)
		return true, nil
	}
	return s.giveUpRetry(ctx, h, err)
}

func (s *Service) giveUpRetry(ctx context.Context, h *accountHandoff, cause error) (bool, error) {
	done, err := s.giveUpAfter(h, cause)
	if done {
		s.recordAccountHandoffRetry(ctx, h, accountHandoffRetrySkipped)
	}
	return done, err
}

func (s *Service) retryAccountHandoffOnController(ctx context.Context, h *accountHandoff) error {
	controller, release, err := s.workingController(ctx, h.sessionID)
	if err != nil {
		return err
	}
	defer release()
	_, err = controller.retryAccountHandoffTurn(ctx, h.turnID)
	return err
}

func actionableUsageLimitTurn(ctx context.Context, turns Store, source domain.ConversationTurn) error {
	store, ok := turns.(usageLimitStore)
	if !ok {
		return ErrUsageLimitAutoResumeInvalid
	}
	if source.State != domain.TurnStateFailed || source.RolledBackAt != nil || source.UsageLimit == nil {
		return fmt.Errorf("%w: this turn is not an actionable usage-limit failure", ErrUsageLimitAutoResumeInvalid)
	}
	return validateUsageLimitRetry(ctx, turns, store, source)
}

func (c *Controller) retryAccountHandoffTurn(ctx context.Context, turnID string) (domain.ConversationTurn, error) {
	if !c.sendMu.TryLock() {
		return domain.ConversationTurn{}, ErrTurnRunning
	}
	defer c.sendMu.Unlock()
	source, err := c.store.TurnByID(ctx, turnID)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	if source.ConversationID != c.conversation.ID || source.HandledBySessionID != c.sessionID {
		return domain.ConversationTurn{}, ErrUsageLimitAutoResumeInvalid
	}
	if err := actionableUsageLimitTurn(ctx, c.store, source); err != nil {
		return domain.ConversationTurn{}, err
	}
	return c.retryTurnLocked(ctx, turnID, false)
}

func (s *Service) stayOnAccount(ctx context.Context, h *accountHandoff, source domain.ConversationTurn, detail string) (bool, error) {
	if err := s.armUsageLimitFallback(ctx, source); err != nil {
		return s.giveUpAfter(h, err)
	}
	s.log.Info("account handoff: session stays", "session", source.HandledBySessionID, "turn", source.ID, "detail", detail)
	return true, nil
}

func (s *Service) armUsageLimitFallback(ctx context.Context, source domain.ConversationTurn) error {
	if source.UsageLimit == nil || source.UsageLimit.AutoResume {
		return nil
	}
	if err := s.SetTurnAutoResume(ctx, source.HandledBySessionID, source.ID, true); err != nil && !permanentUsageLimitResumeError(err) {
		return err
	}
	return nil
}

func (s *Service) giveUpAfter(h *accountHandoff, cause error) (bool, error) {
	if s.now().Sub(h.stepAt) < accountHandoffGiveUp {
		return false, cause
	}
	s.log.Info("account handoff: gave up", "session", h.sessionID, "turn", h.turnID, "error", cause)
	return true, nil
}
