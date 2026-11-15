package chat

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/filesnapshot"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionfork"
)

type sessionForkMode int

const (
	forkUnsupported sessionForkMode = iota
	forkIntoHost
	forkIntoWorkspace
)

func sessionForkStrategy(driver ports.ChatDriver) sessionForkMode {
	if _, ok := driver.(ports.ChatWorkspaceForker); ok {
		return forkIntoWorkspace
	}
	if _, ok := driver.(ports.ChatIsolatedForker); ok {
		return forkIntoHost
	}
	return forkUnsupported
}

func (s *Service) SessionForkSource(ctx context.Context, rec domain.SessionRecord, turnID string) (sessionfork.Source, error) {
	driver, err := s.drivers.Driver(rec.Harness)
	if err != nil {
		return sessionfork.Source{Refusal: sessionfork.CodeAgentUnsupported}, nil //nolint:nilerr // reported as a refusal
	}
	mode := sessionForkStrategy(driver)
	if mode == forkUnsupported {
		return s.textHandoffForkSource(ctx, rec, turnID)
	}
	providerID := strings.TrimSpace(rec.Metadata.ProviderConversationID)
	if providerID == "" {
		return s.textHandoffForkSource(ctx, rec, turnID)
	}
	if caps, known := s.knownForkCapabilities(rec); !forkCapable(mode, caps, known) {
		return s.textHandoffForkSource(ctx, rec, turnID)
	}
	conversation, err := s.store.ConversationForSession(ctx, rec.ID)
	if err != nil {
		return sessionfork.Source{}, fmt.Errorf("session fork: load conversation: %w", err)
	}
	anyTurn := forksOlderTurns(rec.Harness)
	plan := sessionForkPlan{
		driver: driver, sourceProviderID: providerID, sourceWorkspace: rec.Metadata.WorkspacePath,
		model: rec.Metadata.Model, effort: rec.Metadata.Effort, permissions: rec.Metadata.Permissions,
	}
	contextMode := domain.ForkContextNativeCurrent
	if mode == forkIntoHost && rec.Harness == domain.HarnessCodex {
		contextMode = domain.ForkContextNativeTurn
	}
	latest, ok, err := s.latestForkTurn(ctx, conversation.ID, mode)
	if err != nil {
		return sessionfork.Source{}, err
	}
	if turnID == "" || (ok && turnID == latest.ID) {
		if !ok {
			return sessionfork.Source{AnyTurn: anyTurn}, nil
		}
		plan.nativeAnchor = codexNativeTurnID(latest.ProviderTurnID)
		if mode == forkIntoWorkspace {
			plan.cutTurnID, plan.conversationID, plan.reader = latest.ID, conversation.ID, s.reader
		}
		return sessionfork.Source{LatestTurnID: latest.ID, ContextMode: contextMode, AnyTurn: anyTurn, PrepareChat: plan.prepare}, nil
	}
	if !anyTurn {
		return sessionfork.Source{Refusal: sessionfork.CodeOlderTurn}, nil
	}
	if s.reader == nil {
		return sessionfork.Source{}, errors.New("session fork: conversation reader unavailable")
	}
	rows, err := s.reader.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		return sessionfork.Source{}, fmt.Errorf("session fork: load turns: %w", err)
	}
	turn, next, ok := olderForkPoint(rows, turnID)
	if !ok {
		return sessionfork.Source{Refusal: sessionfork.CodeNoFinishedTurn}, nil
	}
	if mode == forkIntoWorkspace {
		if plan.upToMessage = lastAssistantAPIMessageID(rows, turn.ID); plan.upToMessage == "" {
			return sessionfork.Source{Refusal: sessionfork.CodeNativeMissing}, nil
		}
	} else if plan.nativeAnchor = codexNativeTurnID(turn.ProviderTurnID); plan.nativeAnchor == "" {
		return sessionfork.Source{Refusal: sessionfork.CodeNativeMissing}, nil
	}
	source := sessionfork.Source{LatestTurnID: turn.ID, ContextMode: domain.ForkContextNativeTurn, AnyTurn: anyTurn, PrepareChat: plan.prepare}
	source.LimitAttachments, source.AttachmentNames = true, attachmentsUpToTurn(rows, turn.ID)
	if next.ID != "" {
		source.FilesSnapshotRef, source.FilesTurnID = filesnapshot.Ref(rec.ID, next.ID), next.ID
		source.FilesAsOf = next.RequestedAt
		if next.StartedAt != nil {
			source.FilesAsOf = *next.StartedAt
		}
	}
	return source, nil
}

func olderForkPoint(rows ConversationRows, turnID string) (turn, next domain.ConversationTurn, ok bool) {
	for i, candidate := range rows.Turns {
		if candidate.ID != turnID {
			continue
		}
		finished := candidate.State == domain.TurnStateCompleted || candidate.State == domain.TurnStateRecovered
		if !finished || candidate.RolledBackAt != nil {
			return domain.ConversationTurn{}, domain.ConversationTurn{}, false
		}
		for _, later := range rows.Turns[i+1:] {
			if later.RolledBackAt == nil {
				return candidate, later, true
			}
		}
		return candidate, domain.ConversationTurn{}, true
	}
	return domain.ConversationTurn{}, domain.ConversationTurn{}, false
}

var anthropicMessageID = regexp.MustCompile(`msg_[A-Za-z0-9]+`)

func lastAssistantAPIMessageID(rows ConversationRows, turnID string) string {
	id := ""
	for _, message := range rows.Messages {
		if message.TurnID != turnID || message.Role != domain.MessageRoleAssistant {
			continue
		}
		if matches := anthropicMessageID.FindAllString(message.ProviderItemID, -1); len(matches) > 0 {
			id = matches[len(matches)-1]
		}
	}
	return id
}

func (s *Service) latestForkTurn(ctx context.Context, conversationID string, mode sessionForkMode) (domain.ConversationTurn, bool, error) {
	if reader, ok := s.store.(promptSuggestionTurnReader); ok {
		latest, err := reader.LatestConversationTurn(ctx, conversationID)
		if errors.Is(err, domain.ErrNoConversationTurn) {
			return domain.ConversationTurn{}, false, nil
		}
		if err != nil {
			return domain.ConversationTurn{}, false, fmt.Errorf("session fork: load latest turn: %w", err)
		}
		if !settledForkTurn(latest) {
			return domain.ConversationTurn{}, false, nil
		}
		if mode == forkIntoWorkspace || latest.ProviderTurnID != "" {
			return latest, true, nil
		}
	}
	if s.reader == nil {
		return domain.ConversationTurn{}, false, errors.New("session fork: conversation reader unavailable")
	}
	rows, err := s.reader.LoadConversationSnapshot(ctx, conversationID)
	if err != nil {
		return domain.ConversationTurn{}, false, fmt.Errorf("session fork: load turns: %w", err)
	}
	turn, ok := latestFinishedTurn(rows)
	return turn, ok, nil
}

func settledForkTurn(turn domain.ConversationTurn) bool {
	switch turn.State {
	case domain.TurnStateCompleted, domain.TurnStateRecovered, domain.TurnStateInterrupted, domain.TurnStateFailed:
		return true
	}
	return false
}

func latestFinishedTurn(rows ConversationRows) (domain.ConversationTurn, bool) {
	for i := len(rows.Turns) - 1; i >= 0; i-- {
		turn := rows.Turns[i]
		finished := turn.State == domain.TurnStateCompleted || turn.State == domain.TurnStateRecovered
		if finished && turn.RolledBackAt == nil {
			return turn, true
		}
	}
	return domain.ConversationTurn{}, false
}

func (s *Service) knownForkCapabilities(rec domain.SessionRecord) (ports.ChatCapabilities, bool) {
	if controller, err := s.Controller(rec.ID); err == nil {
		if caps := controller.Capabilities(); caps != nil {
			return caps, true
		}
	}
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	caps, ok := s.probed[rec.Harness]
	return caps, ok
}

func forkCapable(mode sessionForkMode, caps ports.ChatCapabilities, known bool) bool {
	if mode == forkIntoWorkspace || !known {
		return true
	}
	return caps.Has(ports.ChatCapabilityFork) || caps.Has(ports.ChatCapabilityForkCurrent)
}

func codexNativeTurnID(id string) string {
	rest, ok := strings.CutPrefix(id, "codex:")
	if !ok {
		return id
	}
	lengthText, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return id
	}
	length, err := strconv.Atoi(lengthText)
	if err != nil || length < 0 || length >= len(rest) {
		return id
	}
	return rest[length:]
}

type sessionForkPlan struct {
	driver           ports.ChatDriver
	sourceProviderID string
	sourceWorkspace  string
	nativeAnchor     string
	upToMessage      string
	cutTurnID        string
	conversationID   string
	reader           SnapshotReader
	model, effort    string
	permissions      ports.PermissionMode
}

func (p sessionForkPlan) prepare(ctx context.Context, target ports.ForkTarget) (string, error) {
	cfg := ports.ChatStartConfig{
		SessionID: domain.SessionID("fork-" + string(target.SessionID)),
		DataDir:   target.DataDir, WorkspacePath: target.WorkspacePath, Env: target.Env,
		Model: p.model, Effort: p.effort, Permissions: p.permissions,
	}
	if forker, ok := p.driver.(ports.ChatWorkspaceForker); ok {
		upTo := p.upToMessage
		if upTo == "" && p.cutTurnID != "" && p.reader != nil {
			rows, err := p.reader.LoadConversationSnapshot(ctx, p.conversationID)
			if err != nil {
				return "", fmt.Errorf("session fork: load turns: %w", err)
			}
			upTo = lastAssistantAPIMessageID(rows, p.cutTurnID)
		}
		return forker.ForkIntoWorkspace(ctx, p.sourceProviderID, p.sourceWorkspace, upTo, cfg)
	}
	forker, ok := p.driver.(ports.ChatIsolatedForker)
	if !ok {
		return "", ports.ErrChatCapabilityUnavailable
	}
	conv, err := forker.ForkIntoHost(ctx, p.sourceProviderID, p.nativeAnchor, cfg)
	if errors.Is(err, ports.ErrChatCapabilityUnavailable) && p.nativeAnchor != "" {
		conv, err = forker.ForkIntoHost(ctx, p.sourceProviderID, "", cfg)
	}
	if err != nil {
		return "", err
	}
	id := conv.ProviderConversationID()
	if terminator, ok := conv.(ports.ChatProviderTerminator); ok {
		_ = terminator.Terminate()
	} else {
		_ = conv.Close()
	}
	if id == "" || id == p.sourceProviderID {
		return "", errors.New("session fork: agent returned no independent conversation")
	}
	return id, nil
}

func forksOlderTurns(harness domain.AgentHarness) bool {
	return harness == domain.HarnessClaudeCode || harness == domain.HarnessCodex
}

func (s *Service) textHandoffForkSource(ctx context.Context, rec domain.SessionRecord, turnID string) (sessionfork.Source, error) {
	conversation, err := s.store.ConversationForSession(ctx, rec.ID)
	if errors.Is(err, domain.ErrNoConversation) {
		return sessionfork.Source{}, nil
	}
	if err != nil {
		return sessionfork.Source{}, fmt.Errorf("session fork: load conversation: %w", err)
	}
	latest, ok, err := s.latestForkTurn(ctx, conversation.ID, forkIntoWorkspace)
	if err != nil {
		return sessionfork.Source{}, err
	}
	if !ok {
		return sessionfork.Source{}, nil
	}
	if turnID != "" && turnID != latest.ID {
		return sessionfork.Source{Refusal: sessionfork.CodeOlderTurn}, nil
	}
	source := rec
	upTo := latest.RequestedAt
	if latest.CompletedAt != nil {
		upTo = *latest.CompletedAt
	}
	return sessionfork.Source{
		LatestTurnID: latest.ID, ContextMode: domain.ForkContextTextHandoff,
		Handoff: func(ctx context.Context, target ports.ForkTarget) (string, error) {
			return s.ForkHandoffNote(ctx, source, target, "", upTo)
		},
	}, nil
}

var attachmentPath = regexp.MustCompile(`\.ao/attachments/([A-Za-z0-9][A-Za-z0-9._-]*)`)

func attachmentsUpToTurn(rows ConversationRows, turnID string) []string {
	inPrefix := map[string]bool{}
	for _, turn := range rows.Turns {
		if turn.RolledBackAt == nil {
			inPrefix[turn.ID] = true
		}
		if turn.ID == turnID {
			break
		}
	}
	seen := map[string]bool{}
	var names []string
	for _, message := range rows.Messages {
		if !inPrefix[message.TurnID] {
			continue
		}
		for _, text := range []string{message.Text, message.DeliveryContentJSON} {
			for _, match := range attachmentPath.FindAllStringSubmatch(text, -1) {
				if name := match[1]; !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
	}
	sort.Strings(names)
	return names
}
