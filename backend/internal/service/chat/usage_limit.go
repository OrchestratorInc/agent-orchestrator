package chat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrUsageLimitAutoResumeInvalid rejects a source that cannot safely resume.
var ErrUsageLimitAutoResumeInvalid = errors.New("usage-limit auto-resume is unavailable")

// ErrUsageLimitResetUnknown refuses scheduling without a known reset deadline.
var ErrUsageLimitResetUnknown = errors.New("the provider did not report a known usage-limit reset time")

const usageLimitAutoResumeBackoff = 30 * time.Second

// Bound provider processes and wakes without making one session block another.
const usageLimitAutoResumeWorkers = 4

type usageLimitStore interface {
	SetTurnAutoResume(context.Context, string, string, *time.Time, bool, string) error
	PostponeTurnAutoResume(context.Context, string, string, time.Time) (bool, error)
	AutoResumeTurns(context.Context) ([]domain.ConversationTurn, error)
	LatestAutoResumeTurn(context.Context, string) (domain.ConversationTurn, error)
	AppendAutoResumeRetryUserMessage(context.Context, string, domain.SessionID, string, domain.ConversationMessage, string, string, time.Time) (bool, error)
}

type usageLimitAccountReader interface {
	SessionLimits(context.Context, domain.SessionID, domain.AgentHarness, bool) (string, domain.AgentAccountLimits, error)
}

// SetUsageLimitAccounts shares the account service's provider readings with recovery.
func (s *Service) SetUsageLimitAccounts(accounts usageLimitAccountReader) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usageLimitAccounts = accounts
}

// SetTurnAutoResume arms one failed human prompt, rather than a session-wide
// policy. Cancellation needs no live controller and remains available while busy.
func (s *Service) SetTurnAutoResume(ctx context.Context, id domain.SessionID, turnID string, enabled bool) error {
	store, ok := s.store.(usageLimitStore)
	if !ok {
		return ErrUsageLimitAutoResumeInvalid
	}
	record, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return err
	} else if !found {
		return ports.ErrSessionNotFound
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		return domain.ErrNoConversationTurn
	}
	if err != nil {
		return err
	}
	source, err := s.store.TurnByID(ctx, turnID)
	if err != nil {
		return err
	}
	if source.ConversationID != conversation.ID {
		return domain.ErrNoConversationTurn
	}
	if !enabled {
		err = store.SetTurnAutoResume(ctx, conversation.ID, turnID, nil, false, "")
		if err == nil {
			s.autoResumeMu.Lock()
			delete(s.autoResumeRetryAt, turnID)
			s.autoResumeMu.Unlock()
		}
		return err
	}
	if err := validateUsageLimitSession(record); err != nil {
		return err
	}
	if err := validateUsageLimitSource(ctx, s.store, store, source, id); err != nil {
		return err
	}
	err = store.SetTurnAutoResume(ctx, conversation.ID, turnID, source.UsageLimit.ResetsAt, true, "")
	if err == nil {
		s.autoResumeMu.Lock()
		delete(s.autoResumeRetryAt, turnID)
		s.autoResumeMu.Unlock()
	}
	return err
}

func validateUsageLimitSession(record domain.SessionRecord) error {
	if domain.NormalizeSessionMode(record.Mode) != domain.SessionModeChat {
		return ErrNotChatMode
	}
	if record.IsTerminated || record.ProvisionState.WithDefault() != domain.SessionProvisionReady {
		return fmt.Errorf("%w: this session is no longer ready", ErrUsageLimitAutoResumeInvalid)
	}
	if record.Activity.State == domain.ActivityBlocked || record.Activity.State == domain.ActivityWaitingInput {
		return fmt.Errorf("%w: the agent needs input", ErrUsageLimitAutoResumeInvalid)
	}
	return nil
}

// Account readings can extend a known deadline, never shorten it or create consent.
func (s *Service) postponeUsageLimit(ctx context.Context, store usageLimitStore, source domain.ConversationTurn, harness domain.AgentHarness) (bool, error) {
	s.mu.RLock()
	accounts := s.usageLimitAccounts
	s.mu.RUnlock()
	if accounts == nil {
		return false, nil
	}
	// An optional quota probe must not hold up the already-authorized retry.
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	_, limits, err := accounts.SessionLimits(readCtx, source.HandledBySessionID, harness, true)
	cancel()
	if errors.Is(err, ports.ErrAgentAccountChanged) {
		return false, err
	}
	if err != nil {
		s.log.Debug("usage-limit account refresh unavailable; using turn reset", "session", source.HandledBySessionID, "error", err)
		limits.AutoResumeResetsAt = nil
	}
	if limits.AutoResumeResetsAt == nil || !limits.AutoResumeResetsAt.After(s.now()) ||
		!limits.AutoResumeResetsAt.After(*source.UsageLimit.ResetsAt) {
		return false, nil
	}
	// A retry or account move may consume consent during the probe. A zero-row
	// update leaves it consumed and ends this attempt without dispatching.
	_, err = store.PostponeTurnAutoResume(ctx, source.ConversationID, source.ID, *limits.AutoResumeResetsAt)
	return err == nil, err
}

func validateUsageLimitSource(ctx context.Context, turns Store, store usageLimitStore, source domain.ConversationTurn, sessionID domain.SessionID) error {
	if source.State != domain.TurnStateFailed || source.RolledBackAt != nil || source.UsageLimit == nil || source.HandledBySessionID != sessionID {
		return fmt.Errorf("%w: this turn is not an actionable usage-limit failure", ErrUsageLimitAutoResumeInvalid)
	}
	if source.UsageLimit.ResetsAt == nil {
		return ErrUsageLimitResetUnknown
	}
	prompt, err := turns.RetryPrompt(ctx, source.ConversationID, source.ID)
	if err != nil {
		return err
	}
	if !prompt.ActiveLineage {
		return ErrRetryStaleBranch
	}
	if prompt.Origin != domain.MessageOriginHuman {
		return ErrTurnNotRetryable
	}
	if source.ProviderTurnID == "" {
		return ErrRetryDeliveryUncertain
	}
	if _, retried, err := turns.RetryTurnIDForSource(ctx, source.ConversationID, source.ID); err != nil {
		return err
	} else if retried {
		return fmt.Errorf("%w: this turn already has a retry", ErrUsageLimitAutoResumeInvalid)
	}
	latest, err := store.LatestAutoResumeTurn(ctx, source.ConversationID)
	if err != nil {
		return err
	}
	if latest.ID != source.ID {
		return fmt.Errorf("%w: a newer turn superseded this prompt", ErrUsageLimitAutoResumeInvalid)
	}
	return nil
}

// retryUsageLimitTurn uses RetryTurn's durable idempotency and provider dispatch,
// and rechecks intent and lineage under the same lock as sends and queue drains.
func (c *Controller) retryUsageLimitTurn(ctx context.Context, turnID string) (domain.ConversationTurn, error) {
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
	if source.ConversationID != c.conversation.ID || source.UsageLimit == nil || !source.UsageLimit.AutoResume {
		return domain.ConversationTurn{}, ErrUsageLimitAutoResumeInvalid
	}
	if err := validateUsageLimitSource(ctx, c.store, store, source, c.sessionID); err != nil {
		return domain.ConversationTurn{}, err
	}
	if source.UsageLimit.ResetsAt.After(c.now()) {
		return domain.ConversationTurn{}, domain.ErrConversationAutoResumeNotDue
	}
	return c.retryTurnLocked(ctx, turnID, true)
}

func permanentUsageLimitResumeError(err error) bool {
	return errors.Is(err, ErrUsageLimitAutoResumeInvalid) ||
		errors.Is(err, ErrUsageLimitResetUnknown) ||
		errors.Is(err, ports.ErrSessionNotFound) ||
		errors.Is(err, ErrNotChatMode) ||
		errors.Is(err, domain.ErrNoConversation) ||
		errors.Is(err, domain.ErrNoConversationTurn) ||
		errors.Is(err, domain.ErrConversationRebound) ||
		errors.Is(err, ErrTurnNotRetryable) ||
		errors.Is(err, ErrRetryStaleBranch) ||
		errors.Is(err, ErrRetryDeliveryUncertain) ||
		errors.Is(err, ErrRetryContentInvalid) ||
		errors.Is(err, ErrRetryUnsupported) ||
		errors.Is(err, ports.ErrAgentAccountChanged) ||
		errors.Is(err, ports.ErrChatAuthRequired)
}

// ResumeUsageLimitedTurns preserves consent through temporary failures. Only a
// permanent refusal or the transaction recording a retry consumes it.
func (s *Service) ResumeUsageLimitedTurns(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store, ok := s.store.(usageLimitStore)
	if !ok {
		return nil
	}
	s.autoResumeMu.Lock()
	if s.autoResumeScanning || len(s.autoResumeInFlight) >= usageLimitAutoResumeWorkers {
		s.autoResumeMu.Unlock()
		return nil
	}
	s.autoResumeScanning = true
	s.autoResumeMu.Unlock()
	sources, err := store.AutoResumeTurns(ctx)
	armed := make(map[string]bool, len(sources))
	for _, source := range sources {
		armed[source.ID] = true
	}
	s.autoResumeMu.Lock()
	s.autoResumeScanning = false
	if err != nil || ctx.Err() != nil {
		s.autoResumeMu.Unlock()
		return errors.Join(err, ctx.Err())
	}
	for id := range s.autoResumeRetryAt {
		if !armed[id] {
			delete(s.autoResumeRetryAt, id)
		}
	}
	var claimed []domain.ConversationTurn
	for _, candidate := range sources {
		if len(s.autoResumeInFlight) >= usageLimitAutoResumeWorkers {
			break
		}
		if s.autoResumeInFlight[candidate.ID] || s.autoResumeRetryAt[candidate.ID].After(s.now()) {
			continue
		}
		// Eligibility reads are needed only once the durable deadline arrives.
		if candidate.UsageLimit != nil && candidate.UsageLimit.ResetsAt != nil && candidate.UsageLimit.ResetsAt.After(s.now()) {
			continue
		}
		if s.autoResumeInFlight == nil {
			s.autoResumeInFlight = make(map[string]bool)
		}
		s.autoResumeInFlight[candidate.ID] = true
		claimed = append(claimed, candidate)
	}
	s.autoResumeMu.Unlock()

	failures := make(chan error, len(claimed))
	var workers sync.WaitGroup
	for _, candidate := range claimed {
		workers.Go(func() {
			defer func() {
				s.autoResumeMu.Lock()
				delete(s.autoResumeInFlight, candidate.ID)
				s.autoResumeMu.Unlock()
			}()
			if err := s.resumeUsageLimitedTurn(ctx, store, candidate); err != nil {
				failures <- err
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

func (s *Service) resumeUsageLimitedTurn(ctx context.Context, store usageLimitStore, candidate domain.ConversationTurn) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A cancellation or manual retry can consume intent after the scan.
	source, err := s.store.TurnByID(ctx, candidate.ID)
	var record domain.SessionRecord
	if err == nil {
		if source.UsageLimit != nil && !source.UsageLimit.AutoResume {
			return nil
		}
		if source.UsageLimit != nil && source.UsageLimit.ResetsAt != nil && source.UsageLimit.ResetsAt.After(s.now()) {
			return nil
		}
		record, err = s.requireChatSession(ctx, source.HandledBySessionID)
		if err == nil {
			err = validateUsageLimitSession(record)
		}
	}
	if err == nil {
		err = validateUsageLimitSource(ctx, s.store, store, source, source.HandledBySessionID)
	}
	if err == nil {
		var postponed bool
		postponed, err = s.postponeUsageLimit(ctx, store, source, record.Harness)
		if postponed {
			return nil
		}
	}
	if err == nil {
		// Hibernation recovery already permits a 60-second native wake.
		attemptCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		_, err = s.retryTurn(attemptCtx, source.HandledBySessionID, source.ID, true)
		cancel()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, domain.ErrConversationAutoResumeNotDue) {
		return nil
	}
	if permanentUsageLimitResumeError(err) {
		err = store.SetTurnAutoResume(ctx, candidate.ConversationID, candidate.ID, nil, false,
			"Auto-resume stopped: "+err.Error())
	}
	s.autoResumeMu.Lock()
	defer s.autoResumeMu.Unlock()
	if err == nil {
		delete(s.autoResumeRetryAt, candidate.ID)
	} else {
		if s.autoResumeRetryAt == nil {
			s.autoResumeRetryAt = make(map[string]time.Time)
		}
		s.autoResumeRetryAt[candidate.ID] = s.now().Add(usageLimitAutoResumeBackoff)
	}
	return err
}
