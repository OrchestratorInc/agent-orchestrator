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
	// forkIntoHost forks in a temporary provider host in the fork's worktree.
	forkIntoHost
	// forkIntoWorkspace copies the provider's session files without a host.
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

// SessionForkSource reports how a Chat session can be forked at turnID, or at
// its latest finished turn when turnID is empty. It never wakes or changes the
// source.
func (s *Service) SessionForkSource(ctx context.Context, rec domain.SessionRecord, turnID string) (sessionfork.Source, error) {
	driver, err := s.drivers.Driver(rec.Harness)
	if err != nil {
		// No driver for this agent: a refusal the desktop shows, not a failure.
		return sessionfork.Source{Refusal: sessionfork.CodeAgentUnsupported}, nil //nolint:nilerr // reported as a refusal
	}
	// An agent that can't copy its own conversation is forked with AO's copy,
	// as text.
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
	// Attachments sent after this turn aren't the fork's.
	source.LimitAttachments, source.AttachmentNames = true, attachmentsUpToTurn(rows, turn.ID)
	if next.ID != "" {
		// The files as this turn left them are the ones saved before the
		// next turn started, for rollback.
		source.FilesSnapshotRef, source.FilesTurnID = filesnapshot.Ref(rec.ID, next.ID), next.ID
		// The snapshot is taken as the next turn starts.
		source.FilesAsOf = next.RequestedAt
		if next.StartedAt != nil {
			source.FilesAsOf = *next.StartedAt
		}
	}
	return source, nil
}

// olderForkPoint finds turnID if it is a finished turn that wasn't rolled
// back, and the turn after it, whose saved snapshot holds turnID's files.
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

// lastAssistantAPIMessageID is the API message id of turnID's last assistant
// message, which names where to cut a Claude session.
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

// latestForkTurn finds the turn a fork copies through. It reads only the
// latest turn when it can: the agent's copy always includes it, so it must be
// settled. A latest turn the agent never accepted carries no provider id for
// Codex to anchor on, so the last finished one is used instead.
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

// latestFinishedTurn is the last finished turn that wasn't rolled back. A
// recovered turn is one imported from the agent's own history, as in a fork.
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

// knownForkCapabilities is what the session's agent is known to support: the
// live controller's, else the last probe for its agent. It never starts one.
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

// forkCapable is false only when the agent is known to lack a native fork.
// Claude's workspace fork copies files and needs no ACP capability.
func forkCapable(mode sessionForkMode, caps ports.ChatCapabilities, known bool) bool {
	if mode == forkIntoWorkspace || !known {
		return true
	}
	return caps.Has(ports.ChatCapabilityFork) || caps.Has(ports.ChatCapabilityForkCurrent)
}

// codexNativeTurnID strips the "codex:<len>:<scope>" prefix AO adds to Codex
// turn ids, giving the id Codex knows.
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
	// upToMessage is the provider's id for the last message to keep when a
	// workspace fork copies an older turn; empty copies everything.
	upToMessage string
	// cutTurnID, for a workspace fork of the latest turn, is the turn to stop
	// the copy after: the original may start another turn while the fork is
	// being made. The cut is read only when the fork is made.
	cutTurnID      string
	conversationID string
	reader         SnapshotReader
	model, effort  string
	permissions    ports.PermissionMode
}

// prepare makes the agent's native copy of the conversation for the fork's
// worktree and returns its id. The fork's controller then starts from it.
func (p sessionForkPlan) prepare(ctx context.Context, target ports.ForkTarget) (string, error) {
	cfg := ports.ChatStartConfig{
		// A separate host id, so the temporary fork host never collides with
		// the fork's own controller.
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
			// Without an id for the turn's last reply, the whole session is
			// copied, as before.
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
		// ACP agents copy current context only. The source is idle at its
		// latest turn, so current context is that turn.
		conv, err = forker.ForkIntoHost(ctx, p.sourceProviderID, "", cfg)
	}
	if err != nil {
		return "", err
	}
	id := conv.ProviderConversationID()
	// Stop the temporary host; Close alone may only detach from it. The
	// native copy stays on disk for the fork's controller to resume.
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

// forksOlderTurns reports whether a session fork of this agent can start
// from an older turn: Claude cuts its copy at any message and Codex at any
// turn, while other agents copy their current context only. Only such a fork
// reads the snapshot saved as each turn starts, so only these agents' turns
// take one for forks.
func forksOlderTurns(harness domain.AgentHarness) bool {
	return harness == domain.HarnessClaudeCode || harness == domain.HarnessCodex
}

// textHandoffForkSource forks a session whose agent can't copy its own
// conversation: a fresh agent starts with AO's record of it, as text. Only
// the latest turn can be forked, since the record isn't cut at a turn.
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
	// The note stops at this turn: the original may finish another while the
	// fork is being made.
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

// attachmentPath matches a message's reference to one of its session's
// attachments, as the worktree path agents read it from.
var attachmentPath = regexp.MustCompile(`\.ao/attachments/([A-Za-z0-9][A-Za-z0-9._-]*)`)

// attachmentsUpToTurn names the attachments referenced by messages of turnID
// and the turns before it, sorted. A rolled-back turn's messages don't count.
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
