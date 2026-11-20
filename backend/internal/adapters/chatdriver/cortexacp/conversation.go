package cortexacp

import (
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// managedConversation is the shared ACP lifecycle contract this candidate
// preserves. Deliberately omit ChatConfigOptionController: its direct setters
// bypass ValidateTurnSettings and could enable a native bypass/plan mode during
// a running turn. Models are selected through validated turn settings only.
// Optional compaction, hibernation and other unqualified surfaces stay hidden.
type managedConversation interface {
	ports.ChatConversation
	ports.ChatDeferredTurnStarter
	ports.ChatProviderPreserver
	ports.ChatProviderTerminator
	ports.ChatLiveReconnector
	ports.ChatLiveReconnectActivator
	ports.ChatProviderEventAcknowledger
	ports.ChatHistoryReader
}

type guardedConversation struct{ managedConversation }
type guardedHistoryConversation struct {
	*guardedConversation
	ports.ChatHistoryRefresher
}

func guardConversation(conv ports.ChatConversation, err error) (ports.ChatConversation, error) {
	if err != nil {
		return nil, err
	}
	managed, ok := conv.(managedConversation)
	if !ok {
		if terminator, canTerminate := conv.(ports.ChatProviderTerminator); canTerminate {
			_ = terminator.Terminate()
		} else if conv != nil {
			_ = conv.Close()
		}
		return nil, fmt.Errorf("%w: Cortex candidate requires the shared ACP lifecycle", ports.ErrChatDriverIncompatible)
	}
	guarded := &guardedConversation{managedConversation: managed}
	if history, ok := conv.(ports.ChatHistoryRefresher); ok {
		return &guardedHistoryConversation{guardedConversation: guarded, ChatHistoryRefresher: history}, nil
	}
	return guarded, nil
}
