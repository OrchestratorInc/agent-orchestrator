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
	SessionAccountPinned(context.Context, domain.SessionID) (bool, error)
	GetSessionInterfaceTransition(context.Context, string) (domain.SessionInterfaceTransition, bool, error)
	ListPendingSessionInterfaceTransitionMessages(context.Context, string) ([]domain.SessionInterfaceTransitionMessage, error)
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

// accountHandoff is one refused turn's handoff. It lives only in memory: the
// map entry is the once-only claim, and a daemon restart forgets it, which
// leaves the prompt for the person to resend on whatever account the session
// reached. Failures from before this process started are never picked up.
//
// A worker owns the fields below busy while busy is set; handoffMu guards the
// rest, because other workers read them.
type accountHandoff struct {
	turnID         string
	conversationID string
	sessionID      domain.SessionID
	sourceAccount  string
	resetsAt       *time.Time
	claimedAt      time.Time

	busy    bool
	done    bool
	retryAt time.Time
	// moved is set once the session runs on the target account.
	moved bool
	// leftUntil keeps the session from moving back to the source account.
	leftUntil time.Time

	targetAccount string
	transitionID  string
	// stepAt is when the current step began, for giving up on it.
	stepAt time.Time
}

// SetAccountHandoff wires the setting, the accounts, and the move the
// automatic handoff uses. Without it the handoff never runs.
func (s *Service) SetAccountHandoff(enabled func(context.Context) bool, accounts AccountHandoffAccounts, mover AccountHandoffMover) {
	s.handoffMu.Lock()
	defer s.handoffMu.Unlock()
	s.handoff = accountHandoffDeps{enabled: enabled, accounts: accounts, mover: mover}
	s.handoffSince = s.now()
}

// HandoffUsageLimitedTurns advances every automatic account handoff by one
// step, and, while the setting is on, claims recent usage-limit refusals:
//
//  1. Claim the turn, then pick another signed-in account of the same harness
//     whose native limits verify room for the request's model. None, or a
//     session told to stay on its account, keeps the account and falls back
//     to auto-resume at a known reset.
//  2. Session Manager's account move runs; it commits or rolls back.
//  3. A note names the new account, then the prompt is retried once.
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
		case h.done && now.After(h.claimedAt.Add(accountHandoffWindow)) && now.After(h.leftUntil):
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

// claimAccountHandoffs finds recent usage-limit refusals of account harnesses
// this process has not handled yet.
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
		account, err := deps.accounts.SessionAccountID(ctx, record.ID, record.Harness)
		if err != nil || account == "" {
			s.log.Debug("account handoff: session account unavailable", "session", record.ID, "error", err)
			continue
		}
		now := s.now()
		claimed = append(claimed, &accountHandoff{
			turnID: turn.ID, conversationID: turn.ConversationID, sessionID: record.ID,
			sourceAccount: account, resetsAt: turn.UsageLimit.ResetsAt, claimedAt: now, stepAt: now,
		})
	}
	return claimed, nil
}

// stepAccountHandoff runs the handoff's next step and reports whether it is
// over. An error with done unset leaves it for a later tick.
func (s *Service) stepAccountHandoff(ctx context.Context, store accountHandoffStore, deps accountHandoffDeps, h *accountHandoff) (bool, error) {
	switch {
	case h.transitionID == "":
		return s.selectAccountHandoff(ctx, store, deps, h)
	case !s.accountHandoffMoved(h):
		return s.settleAccountHandoffMove(ctx, store, deps, h)
	}
	return s.retryAccountHandoff(ctx, store, h)
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
		err = s.validateAccountHandoffSource(ctx, source)
	}
	if err != nil {
		if permanentUsageLimitResumeError(err) {
			return true, nil
		}
		return s.giveUpAfter(h, err)
	}
	if pinned, err := store.SessionAccountPinned(ctx, h.sessionID); err != nil {
		return false, err
	} else if pinned {
		return s.stayOnAccount(ctx, source, "This session stays on its account.")
	}
	// One move per prompt: a retry the last handoff made, refused again, stays.
	if source.RetryOfTurnID != "" && s.accountHandoffRetried(source.RetryOfTurnID) {
		return s.stayOnAccount(ctx, source, "The retry on the new account was refused for a usage limit too.")
	}
	model := ""
	if conversation, err := s.store.ConversationForSession(ctx, h.sessionID); err == nil {
		model = conversation.Settings.Model
	}
	target, err := deps.accounts.HandoffTarget(ctx, h.sessionID, record.Harness, model, s.accountsLeftBySession(h))
	if errors.Is(err, ports.ErrAgentAccountChanged) {
		return true, nil
	}
	if err != nil {
		return s.giveUpAfter(h, err)
	}
	if target == "" {
		return s.stayOnAccount(ctx, source, "No other signed-in account has room for this request.")
	}
	transition, err := deps.mover.StartAccountTransition(ctx, h.sessionID, target, domain.SessionInterfaceTransitionDrain)
	if err != nil {
		return s.stayOnAccount(ctx, source, "AO could not start the move: "+err.Error())
	}
	h.targetAccount, h.transitionID, h.stepAt = transition.TargetAccountID, transition.ID, s.now()
	leftUntil := s.now().Add(accountHandoffCooldown)
	if h.resetsAt != nil {
		leftUntil = *h.resetsAt
	}
	s.handoffMu.Lock()
	h.leftUntil = leftUntil
	s.handoffMu.Unlock()
	return s.settleAccountHandoffMove(ctx, store, deps, h)
}

// accountsLeftBySession lists the source account and every account the session
// moved away from that has not reset yet.
func (s *Service) accountsLeftBySession(h *accountHandoff) map[string]bool {
	exclude := map[string]bool{h.sourceAccount: true}
	now := s.now()
	s.handoffMu.Lock()
	defer s.handoffMu.Unlock()
	for _, other := range s.handoffs {
		if other.sessionID == h.sessionID && other.leftUntil.After(now) {
			exclude[other.sourceAccount] = true
		}
	}
	return exclude
}

func (s *Service) accountHandoffMoved(h *accountHandoff) bool {
	s.handoffMu.Lock()
	defer s.handoffMu.Unlock()
	return h.moved
}

func (s *Service) accountHandoffRetried(turnID string) bool {
	s.handoffMu.Lock()
	defer s.handoffMu.Unlock()
	prior, found := s.handoffs[turnID]
	return found && prior.moved
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
		// Only a move that failed on its own falls back; a cancelled one was a
		// person's call, and one needing recovery shows its own notice.
		if transition.Phase == domain.SessionInterfaceTransitionFailed {
			if source, err := s.store.TurnByID(ctx, h.turnID); err == nil {
				s.armUsageLimitFallback(ctx, source)
			}
		}
		s.log.Info("account handoff: move did not commit", "session", h.sessionID, "turn", h.turnID, "phase", transition.Phase)
		return true, nil
	}
	record, found, err := s.sessions.GetSession(ctx, h.sessionID)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}
	bound, err := deps.accounts.SessionAccountID(ctx, h.sessionID, record.Harness)
	if err != nil {
		return false, err
	}
	if bound != h.targetAccount {
		return true, nil
	}
	if err := s.noteAccountHandoff(ctx, h); err != nil {
		return false, err
	}
	h.stepAt = s.now()
	s.handoffMu.Lock()
	h.moved = true
	s.handoffMu.Unlock()
	return s.retryAccountHandoff(ctx, store, h)
}

// noteAccountHandoff writes the durable thread note on the refused turn. Its
// provider item id makes a repeat replace, not duplicate, it.
func (s *Service) noteAccountHandoff(ctx context.Context, h *accountHandoff) error {
	source, err := s.store.TurnByID(ctx, h.turnID)
	if err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]string{"event": AccountHandoffEvent, "accountId": h.targetAccount, "fromAccountId": h.sourceAccount})
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

func (s *Service) retryAccountHandoff(ctx context.Context, store accountHandoffStore, h *accountHandoff) (bool, error) {
	// A message sent during the move is delivered after it; it comes first,
	// and the latest-turn check then withdraws the retry.
	if pending, err := store.ListPendingSessionInterfaceTransitionMessages(ctx, h.transitionID); err != nil {
		return false, err
	} else if len(pending) > 0 {
		return s.giveUpAfter(h, errors.New("messages sent during the move are still being delivered"))
	}
	// Hibernation recovery already permits a 60-second native wake.
	attemptCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	err := s.retryAccountHandoffOnController(attemptCtx, h)
	cancel()
	if err == nil {
		return true, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if permanentUsageLimitResumeError(err) {
		s.log.Info("account handoff: retry withdrawn", "session", h.sessionID, "turn", h.turnID, "error", err)
		return true, nil
	}
	return s.giveUpAfter(h, err)
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

// retryAccountHandoffTurn dispatches the refused prompt on the new account. It
// rechecks lineage under the send lock, so a person's own retry or newer
// message withdraws it.
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
	return c.retryTurnLocked(ctx, turnID, false)
}

// stayOnAccount keeps the session where it is and applies the existing
// usage-limit recovery: auto-resume at a known reset, or nothing to do but the
// person's own retry when the reset is unknown.
func (s *Service) stayOnAccount(ctx context.Context, source domain.ConversationTurn, detail string) (bool, error) {
	s.armUsageLimitFallback(ctx, source)
	s.log.Info("account handoff: session stays", "session", source.HandledBySessionID, "turn", source.ID, "detail", detail)
	return true, nil
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

// giveUpAfter keeps a transient failure for the next tick, until the current
// step has been stuck too long; then recovery is the person's.
func (s *Service) giveUpAfter(h *accountHandoff, cause error) (bool, error) {
	if s.now().Sub(h.stepAt) < accountHandoffGiveUp {
		return false, cause
	}
	s.log.Info("account handoff: gave up", "session", h.sessionID, "turn", h.turnID, "error", cause)
	return true, nil
}
