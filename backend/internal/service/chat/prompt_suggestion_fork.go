package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// RunPromptSuggestionTask uses an unpublished native child, retaining transcript
// reconstruction when the provider cannot safely fork this completed turn.
func (s *Service) RunPromptSuggestionTask(ctx context.Context, id domain.SessionID, turnID string, harness domain.AgentHarness, cfg ports.ChatStartConfig, prompt string) (_ string, err error) {
	driver, err := s.drivers.Driver(harness)
	if err != nil {
		return "", err
	}
	forker, canFork := driver.(ports.ChatIsolatedForker)
	// Native children use the desktop launch's existing crash-cleanup journal.
	// A standalone daemon retains the scratch-conversation fallback.
	runID := s.sides.launchID()
	if !canFork || runID == "" {
		return s.RunBackgroundTask(ctx, harness, cfg, prompt)
	}
	source, release, err := s.workingController(ctx, id)
	if err != nil {
		return "", err
	}
	defer release()
	caps := source.Capabilities()
	if !caps.Has(ports.ChatCapabilityFork) && !caps.Has(ports.ChatCapabilityForkCurrent) {
		return s.RunBackgroundTask(ctx, harness, cfg, prompt)
	}
	if source.harness != harness || source.busy() || source.State() != ports.ChatControllerReady {
		return "", ErrPromptSuggestionStale
	}
	reader, ok := s.store.(promptSuggestionTurnReader)
	if !ok {
		return "", ports.ErrChatCapabilityUnavailable
	}
	latest, err := reader.LatestConversationTurn(ctx, source.conversation.ID)
	if err != nil {
		return "", err
	}
	if latest.ID != turnID || latest.State != domain.TurnStateCompleted || latest.RolledBackAt != nil {
		return "", ErrPromptSuggestionStale
	}
	anchor := latest.ProviderTurnID
	if resolver, ok := source.conv.(ports.ChatNativeTurnID); ok {
		anchor = resolver.NativeTurnID(anchor)
	}
	if !caps.Has(ports.ChatCapabilityFork) {
		anchor = ""
	} else if anchor == "" {
		return "", ErrPromptSuggestionStale
	}
	if strings.TrimSpace(prompt) == "" || s.newID == nil {
		return "", errors.New("background suggestion prompt or id factory is unavailable")
	}
	store, ok := s.sides.store.(*memorySideStore)
	if !ok {
		return "", ErrSideUnavailable
	}
	taskCtx, cancel := context.WithCancel(ctx)
	stopWithService := context.AfterFunc(s.sides.ctx, cancel)
	defer cancel()
	defer stopWithService()
	go func() {
		select {
		case <-source.stopped:
			cancel()
		case <-taskCtx.Done():
		}
	}()
	side := domain.SideConversation{
		ID: "suggestion-" + s.newID(), AppRunID: runID, SessionID: id,
		MainConversationID: source.conversation.ID, Harness: harness,
		SourceProviderID: source.conv.ProviderConversationID(), AnchorTurnID: turnID,
	}
	side.ProviderHostID = "btw-" + runID + "-" + side.ID
	cfg.SessionID = domain.SessionID(side.ProviderHostID)
	cfg.ProviderScopeID, cfg.ProviderIDsScoped = side.ID, true
	// Retain a provider identity that can be deleted after a crash or host stop.
	cfg.Ephemeral, cfg.ReadOnly, cfg.DisableNetwork, cfg.Effort = false, true, true, ""
	if model := source.Settings().Model; model != "" {
		cfg.Model = model
	}
	if anchor == "" {
		cfg.AcquireForkSource = func(ctx context.Context) (context.Context, func(), error) {
			return s.lockCurrentSide(ctx, side)
		}
	}
	cfg.RecordFork = func(recordCtx context.Context, childID string) error {
		if childID == "" || childID == side.SourceProviderID {
			return errors.New("suggestion fork returned no independent child id")
		}
		side.ProviderForkID = childID
		return s.sides.writeForkRecord(recordCtx, side, childID, false)
	}
	var conversation ports.ChatConversation
	defer func() {
		// Only cleanup ownership enters the side store; no visible side, prompt,
		// transcript, or draft is created for an autocomplete request.
		if side.ProviderForkID != "" {
			store.mu.Lock()
			store.hostCleanup[side.ID] = side
			store.cleanup[side.ProviderForkID] = domain.SideProviderCleanup{ForkID: side.ProviderForkID, SessionID: id, Harness: harness}
			store.mu.Unlock()
		}
		var cleanupErr error
		if conversation != nil {
			cleanupErr = cleanupUnpublishedConversation(conversation, true)
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if s.stopProviderHost != nil {
			if hostErr := s.stopProviderHost(cleanupCtx, cfg.SessionID); hostErr != nil {
				if err == nil {
					err = hostErr
				}
				return
			}
		} else if cleanupErr != nil {
			if err == nil {
				err = cleanupErr
			}
			return
		}
		if side.ProviderForkID != "" {
			store.CompleteSideHostCleanup(cleanupCtx, side.ID)
			s.sides.deleteRegisteredFork(cleanupCtx, side, side.ProviderForkID)
		}
	}()
	conversation, err = forker.ForkIntoHost(taskCtx, side.SourceProviderID, anchor, cfg)
	if err != nil {
		return "", fmt.Errorf("fork background %s suggestion: %w", harness, err)
	}
	if conversation == nil {
		return "", errors.New("suggestion fork returned no conversation")
	}
	if side.ProviderForkID == "" {
		if err := cfg.RecordFork(context.WithoutCancel(taskCtx), conversation.ProviderConversationID()); err != nil {
			return "", err
		}
	}
	if conversation.ProviderConversationID() != side.ProviderForkID {
		return "", errors.New("suggestion fork ownership changed before generation")
	}
	return runBackgroundTurn(taskCtx, harness, conversation, prompt)
}
