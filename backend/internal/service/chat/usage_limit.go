package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrUsageLimitAutoResumeInvalid rejects a source that cannot safely resume.
var ErrUsageLimitAutoResumeInvalid = errors.New("usage-limit auto-resume is unavailable")

// ErrUsageLimitResetUnknown refuses scheduling without a known reset deadline.
var ErrUsageLimitResetUnknown = errors.New("the provider did not report a known usage-limit reset time")

const usageLimitAutoResumeBackoff = 30 * time.Second

type usageLimitStore interface {
	SetTurnAutoResume(context.Context, string, string, *time.Time, bool, string) error
	AutoResumeTurns(context.Context) ([]domain.ConversationTurn, error)
	LatestAutoResumeTurn(context.Context, string) (domain.ConversationTurn, error)
}

// SetTurnAutoResume arms one failed human prompt, rather than a session-wide
// policy. Cancellation needs no live controller and remains available while busy.
func (s *Service) SetTurnAutoResume(ctx context.Context, id domain.SessionID, turnID string, enabled bool) error {
	s.autoResumeMu.Lock()
	defer s.autoResumeMu.Unlock()
	store, ok := s.store.(usageLimitStore)
	if !ok {
		return ErrUsageLimitAutoResumeInvalid
	}
	if _, found, err := s.sessions.GetSession(ctx, id); err != nil {
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
			delete(s.autoResumeRetryAt, turnID)
		}
		return err
	}
	if err := s.validateUsageLimitSession(ctx, id); err != nil {
		return err
	}
	if err := validateUsageLimitSource(ctx, s.store, store, source, id); err != nil {
		return err
	}
	err = store.SetTurnAutoResume(ctx, conversation.ID, turnID, source.UsageLimit.ResetsAt, true, "")
	if err == nil {
		delete(s.autoResumeRetryAt, turnID)
	}
	return err
}

func (s *Service) validateUsageLimitSession(ctx context.Context, id domain.SessionID) error {
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return err
	}
	if record.IsTerminated || record.ProvisionState.WithDefault() != domain.SessionProvisionReady {
		return fmt.Errorf("%w: this session is no longer ready", ErrUsageLimitAutoResumeInvalid)
	}
	if record.Activity.State == domain.ActivityBlocked || record.Activity.State == domain.ActivityWaitingInput {
		return fmt.Errorf("%w: the agent needs input", ErrUsageLimitAutoResumeInvalid)
	}
	return nil
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
		return domain.ConversationTurn{}, fmt.Errorf("%w: the reset time has not arrived", ErrUsageLimitAutoResumeInvalid)
	}
	return c.retryTurnLocked(ctx, turnID)
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
	sources, err := store.AutoResumeTurns(ctx)
	if err != nil {
		return err
	}
	armed := make(map[string]bool, len(sources))
	for _, source := range sources {
		armed[source.ID] = true
	}
	s.autoResumeMu.Lock()
	for id := range s.autoResumeRetryAt {
		if !armed[id] {
			delete(s.autoResumeRetryAt, id)
		}
	}
	s.autoResumeMu.Unlock()
	var failures []error
	for _, candidate := range sources {
		// Deadline-independent eligibility reads are needed only once work is due.
		if candidate.UsageLimit != nil && candidate.UsageLimit.ResetsAt != nil &&
			candidate.UsageLimit.ResetsAt.After(s.now()) {
			continue
		}
		if err := func() error {
			s.autoResumeMu.Lock()
			defer s.autoResumeMu.Unlock()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if s.autoResumeRetryAt[candidate.ID].After(s.now()) {
				return nil
			}
			// A cancellation or manual retry can consume intent after the scan.
			source, err := s.store.TurnByID(ctx, candidate.ID)
			if err == nil {
				if source.UsageLimit != nil && !source.UsageLimit.AutoResume {
					delete(s.autoResumeRetryAt, candidate.ID)
					return nil
				}
				if source.UsageLimit != nil && source.UsageLimit.ResetsAt != nil && source.UsageLimit.ResetsAt.After(s.now()) {
					return nil
				}
				err = s.validateUsageLimitSession(ctx, source.HandledBySessionID)
			}
			if err == nil {
				err = validateUsageLimitSource(ctx, s.store, store, source, source.HandledBySessionID)
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
			if err == nil {
				delete(s.autoResumeRetryAt, candidate.ID)
				return nil
			}
			if permanentUsageLimitResumeError(err) {
				err = store.SetTurnAutoResume(ctx, candidate.ConversationID, candidate.ID, nil, false,
					"Auto-resume stopped: "+err.Error())
				if err == nil {
					delete(s.autoResumeRetryAt, candidate.ID)
					return nil
				}
			}
			if s.autoResumeRetryAt == nil {
				s.autoResumeRetryAt = make(map[string]time.Time)
			}
			s.autoResumeRetryAt[candidate.ID] = s.now().Add(usageLimitAutoResumeBackoff)
			return err
		}(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
