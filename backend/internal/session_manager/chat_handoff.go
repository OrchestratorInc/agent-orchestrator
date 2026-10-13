package sessionmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/google/uuid"
)

// A mismatch alone is never proof: only the coordinator's exact native identity
// may introduce an independent context. These lookups do not transfer ownership.
func (m *Manager) chatProviderTransition(ctx context.Context, rec domain.SessionRecord) (*domain.SessionInterfaceTransition, error) {
	store, ok := m.store.(chatProviderOwnershipStore)
	if !ok || rec.Metadata.ProviderConversationID == "" || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return nil, nil
	}
	transition, found, err := store.GetLatestSessionInterfaceTransition(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	if !found || transition.SessionID != rec.ID || transition.SourceMode != domain.SessionModeTUI ||
		transition.TargetMode != domain.SessionModeChat || transition.NativeConversationID != rec.Metadata.ProviderConversationID {
		return nil, nil
	}
	return &transition, nil
}

func (m *Manager) prepareLiveChatProviderHandoff(ctx context.Context, rec domain.SessionRecord) (*domain.ChatProviderHandoff, error) {
	transition, err := m.chatProviderTransition(ctx, rec)
	if err != nil {
		return nil, err
	}
	if transition == nil {
		// No independent context boundary exists for this session. The one
		// stale-branch shape an ordinary orchestrator cold resume can positively
		// prove recovers here; every other shape stays on the strict path below,
		// where an unprovable mismatch is rejected rather than adopted.
		return m.prepareReboundOrchestratorChatProviderHandoff(ctx, rec)
	}
	if transition.Phase != domain.SessionInterfaceTransitionTargetStarting && transition.Phase != domain.SessionInterfaceTransitionActivating {
		return nil, nil
	}
	store, ok := m.store.(chatProviderOwnershipStore)
	if !ok {
		return nil, errors.New("native handoff requires conversation ownership storage")
	}
	conversation, err := store.ConversationForSession(ctx, rec.ID)
	if errors.Is(err, domain.ErrNoConversation) && rec.Kind == domain.KindOrchestrator {
		if projects, ok := m.store.(interface {
			ProjectConversation(context.Context, domain.ProjectID) (domain.ConversationRecord, error)
		}); ok {
			conversation, err = projects.ProjectConversation(ctx, rec.ProjectID)
			if errors.Is(err, domain.ErrNoConversation) {
				return nil, nil // first Chat use in this project
			}
		}
	}
	if errors.Is(err, domain.ErrNoConversation) && rec.Kind != domain.KindOrchestrator {
		return nil, nil // first Chat use for a worker
	}
	if err != nil {
		return nil, err
	}
	if conversation.SessionID != rec.ID {
		previous, found, err := m.store.GetSession(ctx, conversation.SessionID)
		if err != nil {
			return nil, err
		}
		if !found || !previous.IsTerminated || rec.IsTerminated ||
			previous.ProjectID != rec.ProjectID || !rec.CreatedAt.After(previous.CreatedAt) {
			return nil, fmt.Errorf("project conversation %s is owned by another session", conversation.ID)
		}
		current, found, err := m.activeOrchestratorSessionID(ctx, rec.ProjectID)
		if err != nil {
			return nil, err
		}
		if !found || current != rec.ID {
			return nil, errors.New("only the current orchestrator may adopt project history")
		}
	}
	return reserveChatProviderHandoff(ctx, store, rec, *transition, conversation)
}

// Recovery may repair only this session's history, never adopt a successor's.
// A live session with no interface transition of its own takes the ordinary
// cold-resume recovery below; a terminated session keeps the completed
// transition repair.
func (m *Manager) prepareRecoveredChatProviderHandoff(ctx context.Context, rec domain.SessionRecord) (*domain.ChatProviderHandoff, error) {
	transition, err := m.chatProviderTransition(ctx, rec)
	if err != nil {
		return nil, err
	}
	if transition == nil {
		if rec.IsTerminated {
			return nil, nil // recovery repairs a live session's head, never a dead one
		}
		return m.prepareReboundOrchestratorChatProviderHandoff(ctx, rec)
	}
	if !rec.IsTerminated {
		return nil, nil
	}
	if transition.Phase != domain.SessionInterfaceTransitionCompleted {
		return nil, nil
	}
	store, ok := m.store.(chatProviderOwnershipStore)
	if !ok {
		return nil, errors.New("native handoff recovery requires conversation ownership storage")
	}
	conversation, err := store.ConversationForSession(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	if conversation.SessionID != rec.ID {
		return nil, fmt.Errorf("project conversation %s is owned by another session", conversation.ID)
	}
	return reserveChatProviderHandoff(ctx, store, rec, *transition, conversation)
}

// prepareReboundOrchestratorChatProviderHandoff recovers the stale-branch
// identity a replacement orchestrator can inherit: its project conversation was
// already rebound to it, but the active branch still records the terminated
// predecessor's provider handle (or a reserved id the provider never used).
// An ordinary resume cannot prove that shape today, so it is rejected as an
// active-branch handle mismatch and the replacement's own native thread becomes
// unreachable.
//
// The recovery appends a fresh provider boundary through the ordinary Chat
// handoff lifecycle instead of rewriting anything: the predecessor's branch,
// messages, and provider namespace stay immutable, and the replacement's
// thread is published atomically on a child branch. Every gate below must hold
// on positive proof; anything else returns nil so the ordinary strict resume
// keeps rejecting the mismatch exactly as before. A same-session handle
// mismatch is never recovery material: the branch owner must be a retired
// predecessor of the same project, older than this session.
func (m *Manager) prepareReboundOrchestratorChatProviderHandoff(ctx context.Context, rec domain.SessionRecord) (*domain.ChatProviderHandoff, error) {
	if rec.Kind != domain.KindOrchestrator || rec.IsTerminated ||
		rec.Metadata.ProviderConversationID == "" ||
		domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return nil, nil
	}
	store, ok := m.store.(chatProviderOwnershipStore)
	if !ok {
		return nil, nil
	}
	// The project conversation must already be rebound to this session: the
	// recovery never performs or assumes an ownership transfer.
	conversation, err := store.ConversationForSession(ctx, rec.ID)
	if errors.Is(err, domain.ErrNoConversation) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if conversation.Scope != domain.ConversationScopeProject || conversation.SessionID != rec.ID {
		return nil, nil
	}
	// ...and this session must be the project's sole live orchestrator.
	sessions, err := m.store.ListSessions(ctx, rec.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("list orchestrator recovery owners: %w", err)
	}
	currentFound := false
	for _, current := range sessions {
		if current.Kind != domain.KindOrchestrator || current.IsTerminated {
			continue
		}
		if current.ID != rec.ID {
			return nil, nil // a competing live orchestrator cannot be ignored
		}
		currentFound = true
	}
	if !currentFound {
		return nil, nil
	}
	branch, err := store.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		return nil, err
	}
	if branch.SessionID == rec.ID {
		return nil, nil // not a replacement; retain ordinary resume admission
	}
	if branch.SessionID == "" {
		return nil, nil // ownerless history cannot be proven; strict resume rejects it
	}
	previous, found, err := m.store.GetSession(ctx, branch.SessionID)
	if err != nil {
		return nil, err
	}
	if !found || previous.Kind != domain.KindOrchestrator || !previous.IsTerminated ||
		previous.ProjectID != rec.ProjectID || !rec.CreatedAt.After(previous.CreatedAt) {
		return nil, nil // not a positively-owned retired predecessor; strict resume rejects
	}
	if branch.ProviderConversationID == rec.Metadata.ProviderConversationID {
		return nil, nil // the head already holds the requested thread; plain resume applies
	}
	if branch.ProviderConversationID == "" && conversation.LatestSequence != 0 {
		// The ordinary handle check accepts an empty id, so explicitly refuse
		// recorded history whose provider identity cannot be proven.
		return nil, errors.New("cannot recover recorded Chat history without a provider identity")
	}
	if !m.proveNativeConversationPersisted(ctx, rec, rec.Metadata.ProviderConversationID) {
		// Without positive proof that the session's own thread exists, there is
		// nothing to publish: a reserved-but-unpersisted handle stays on the
		// ordinary paths, which reject the mismatch or start fresh in place.
		return nil, nil
	}
	return &domain.ChatProviderHandoff{
		BoundaryID:     uuid.NewString(),
		ConversationID: conversation.ID, PreviousSessionID: conversation.SessionID,
		PreviousBranchID: branch.ID, PreviousSequence: conversation.LatestSequence,
		ExpectedControllerOwner: rec.ControllerOwner(),
		RecoverySource: &domain.ChatProviderHandoffRecoverySource{
			SessionID:              branch.SessionID,
			ProviderConversationID: branch.ProviderConversationID,
		},
	}, nil
}

// proveNativeConversationPersisted requires a positive
// AgentInterfaceHandoffHistoryProbe answer. Without the probe capability, or
// without a definite yes, there is no proof: this is deliberately not the
// conservative treat-as-existing fallback used elsewhere, because opening a
// provider boundary here must be earned, never assumed.
func (m *Manager) proveNativeConversationPersisted(ctx context.Context, rec domain.SessionRecord, nativeConversationID string) bool {
	if m.agents == nil {
		return false
	}
	registered, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return false
	}
	probe, ok := registered.(ports.AgentInterfaceHandoffHistoryProbe)
	if !ok {
		return false
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return false
	}
	env := m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env)
	exists, err := probe.NativeConversationExists(ctx, ports.SessionRef{
		ID:            string(rec.ID),
		WorkspacePath: rec.Metadata.WorkspacePath,
		Metadata: map[string]string{
			ports.MetadataKeyAgentSessionID: rec.Metadata.AgentSessionID,
		},
	}, nativeConversationID, env)
	return err == nil && exists
}

func reserveChatProviderHandoff(ctx context.Context, store chatProviderOwnershipStore, rec domain.SessionRecord, transition domain.SessionInterfaceTransition, conversation domain.ConversationRecord) (*domain.ChatProviderHandoff, error) {
	branch, err := store.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		return nil, err
	}
	// A project root's session_id records the orchestrator that created it; after
	// a rebind the conversation's current session owns that root.
	owned := branch.SessionID == rec.ID || (conversation.Scope == domain.ConversationScopeProject &&
		branch.ParentBranchID == "" && conversation.SessionID == rec.ID)
	if owned && branch.ProviderConversationID == rec.Metadata.ProviderConversationID {
		return nil, nil // also makes retry after a committed boundary idempotent
	}
	if branch.SessionID == "" || branch.ProviderConversationID == "" {
		if conversation.LatestSequence > 0 || conversation.SessionID != rec.ID {
			return nil, errors.New("cannot prove ownership of the previous Chat history")
		}
		return nil, nil // positively unused root needs no context boundary
	}
	handoff := &domain.ChatProviderHandoff{
		BoundaryID:     interfaceTransitionProviderBoundaryID(transition.ID),
		ConversationID: conversation.ID, PreviousSessionID: conversation.SessionID,
		PreviousBranchID: branch.ID, PreviousSequence: conversation.LatestSequence,
		ExpectedControllerOwner: rec.ControllerOwner(),
	}
	if branch.SessionID != rec.ID {
		// The previous branch still records another session's provider identity.
		// The publication transaction re-proves it so a competing boundary or
		// identity change during provider I/O fails closed.
		handoff.RecoverySource = &domain.ChatProviderHandoffRecoverySource{
			SessionID:              branch.SessionID,
			ProviderConversationID: branch.ProviderConversationID,
		}
	}
	return handoff, nil
}
