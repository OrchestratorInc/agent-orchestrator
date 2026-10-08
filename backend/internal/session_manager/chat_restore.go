package sessionmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type chatRestore struct {
	done   chan struct{}
	result RestoreResult
	err    error
}

// EnsureChatController restores a cold controller before input delivery. All
// callers for one session share the same attempt, including explicit Resume.
// resumeAgentWithMode owns the existing operation and provider controller gates;
// it rereads durable state under the operation gate before opening the provider.
func (m *Manager) EnsureChatController(ctx context.Context, id domain.SessionID) error {
	_, err := m.ensureChatController(ctx, id, false)
	return err
}

func (m *Manager) ensureChatController(ctx context.Context, id domain.SessionID, explicit bool) (RestoreResult, error) {
	m.agentOpMu.Lock()
	if attempt := m.chatRestores[id]; attempt != nil {
		m.agentOpMu.Unlock()
		select {
		case <-attempt.done:
			if attempt.err != nil && !explicit && !errors.Is(attempt.err, ports.ErrChatControllerRestore) {
				return attempt.result, fmt.Errorf("%w: %w", ports.ErrChatControllerRestore, attempt.err)
			}
			return attempt.result, attempt.err
		case <-ctx.Done():
			return RestoreResult{}, ctx.Err()
		}
	}
	attempt := &chatRestore{done: make(chan struct{})}
	m.chatRestores[id] = attempt
	m.agentOpMu.Unlock()
	defer func() {
		m.agentOpMu.Lock()
		delete(m.chatRestores, id)
		close(attempt.done)
		m.agentOpMu.Unlock()
	}()

	rec, found, err := m.store.GetSession(ctx, id)
	switch {
	case err != nil:
	case !found:
		err = ports.ErrSessionNotFound
	case rec.IsTerminated:
		err = ErrTerminated
	case domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat:
		err = ports.ErrChatUnsupported
	case !explicit && (rec.ProvisionState.WithDefault() != domain.SessionProvisionReady || rec.Activity.State == domain.ActivityExited):
		err = ErrAgentExited
	case m.chat == nil:
		err = ports.ErrChatUnsupported
	case m.chat.HasLiveChatController(id):
		if explicit {
			err = ErrAgentNotExited
		} else {
			attempt.result = RestoreResult{Session: rec, Mode: RestoreModeNative}
		}
	default:
		restoreCtx, cancel := context.WithTimeout(ctx, m.statusVerificationLimit)
		defer cancel()
		attempt.result, err = m.resumeAgentWithMode(restoreCtx, id)
	}
	if err != nil {
		attempt.err = err
		if !explicit {
			attempt.err = fmt.Errorf("%w: %w", ports.ErrChatControllerRestore, err)
		}
	}
	return attempt.result, attempt.err
}

// Only work that needs continuity warrants a provider at startup. Empty-prompt
// waiting_input and exited sessions are also cold when they have no unfinished
// durable work. Unknown/legacy activity stays conservative.
func (m *Manager) chatNeedsStartupRestore(ctx context.Context, rec domain.SessionRecord) (bool, error) {
	if rec.Kind == domain.KindOrchestrator {
		return true, nil // Preserve the always-awake coordination boundary.
	}
	if rec.Metadata.WorkspacePath == "" || rec.Metadata.ProviderConversationID == "" {
		return true, nil // Let ordinary recovery handle interrupted/legacy spawns.
	}
	switch rec.Activity.State {
	case domain.ActivityIdle, domain.ActivityWaitingInput, domain.ActivityExited:
	default:
		return true, nil
	}
	if rec.Metadata.ConversationCheckpointUnsettled {
		return true, nil
	}
	if recovery, ok := m.chat.(interface {
		ChatNeedsController(context.Context, domain.SessionID) (bool, error)
	}); ok {
		return recovery.ChatNeedsController(ctx, rec.ID)
	}
	return true, nil // Embedders without durable work reads cannot prove idleness.
}
