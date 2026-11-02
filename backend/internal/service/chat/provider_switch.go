package chat

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TerminalProviderSwitcher is late-bound during daemon construction.
type TerminalProviderSwitcher interface {
	SwitchTUIProvider(context.Context, domain.SessionID, domain.ProviderSwitchRequest) error
}

// SetTerminalProviderSwitcher gives the service the terminal side of a switch.
func (s *Service) SetTerminalProviderSwitcher(switcher TerminalProviderSwitcher) {
	s.terminalSwitcher = switcher
}

type providerSwitchActivityDetail struct {
	Event         string              `json:"event"`
	SourceHarness domain.AgentHarness `json:"sourceHarness"`
	TargetHarness domain.AgentHarness `json:"targetHarness"`
	// What AO knows of each side's settings. A side on its agent's default has none.
	SourceModel  string `json:"sourceModel,omitempty"`
	TargetModel  string `json:"targetModel,omitempty"`
	TargetEffort string `json:"targetEffort,omitempty"`
	// TargetAutoCompactWindow is the Claude Code compaction window the target started with.
	TargetAutoCompactWindow string `json:"targetAutoCompactWindow,omitempty"`
	BranchID                string `json:"branchId"`
	// PermissionMode is the mode the target started in. ChatPermissionMode is the
	// chat's own when the target cannot run in it, for a later switch to go back to.
	PermissionMode     ports.PermissionMode `json:"permissionMode,omitempty"`
	ChatPermissionMode ports.PermissionMode `json:"chatPermissionMode,omitempty"`
}

// SwitchProvider performs an idle-only Chat-to-Chat handoff. It retains the
// canonical AO conversation and publishes a new provider branch only after the
// target has started successfully.
func (s *Service) SwitchProvider(ctx context.Context, id domain.SessionID, request domain.ProviderSwitchRequest) error {
	unlock, err := s.lockProviderSwitch(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	return s.switchProvider(ctx, id, request)
}

// SwitchProviderAndSend switches, then sends msg to the agent it switched to:
// no other switch runs between the two. A message already delivered is not
// switched for again, so its retry cannot take the chat back from a later
// switch. switched says the chat is on the target, so a failure is the send's.
func (s *Service) SwitchProviderAndSend(ctx context.Context, id domain.SessionID, request domain.ProviderSwitchRequest, msg ports.ChatUserMessage) (turn domain.ConversationTurn, switched bool, err error) {
	if err := validateFileReferenceContent(msg, string(id)); err != nil {
		return turn, false, err
	}
	unlock, err := s.lockProviderSwitch(ctx, id)
	if err != nil {
		return turn, false, err
	}
	defer unlock()
	delivered, err := s.messageDelivered(ctx, id, msg.ClientMessageID)
	if err != nil {
		return turn, false, err
	}
	if !delivered {
		// A retry after the switch went through finds the target already active,
		// and so does a window that picked the agent another one already brought
		// in. Either way the message runs with what its sender picked.
		if err := s.switchProvider(ctx, id, request); errors.Is(err, domain.ErrProviderAlreadyActive) {
			if err := s.applyPicksToActiveTarget(ctx, id, request); err != nil {
				return turn, false, err
			}
		} else if err != nil {
			return turn, false, err
		}
	}
	turn, err = s.Send(ctx, id, msg)
	return turn, true, err
}

// applyPicksToActiveTarget gives the agent already running the model, effort
// and permission mode a switch to it named. A pick it cannot take refuses the
// message before anything is sent.
func (s *Service) applyPicksToActiveTarget(ctx context.Context, id domain.SessionID, request domain.ProviderSwitchRequest) error {
	if request.Model == "" && request.Effort == "" && request.PermissionMode == "" {
		return nil
	}
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	current := controller.Settings()
	picked := current
	picked.Model = cmp.Or(request.Model, current.Model)
	picked.ReasoningEffort = cmp.Or(request.Effort, current.ReasoningEffort)
	if request.PermissionMode != "" {
		picked.ApprovalMode = ports.NormalizePermissionMode(request.PermissionMode)
	}
	if picked.Model == current.Model && picked.ReasoningEffort == current.ReasoningEffort && picked.ApprovalMode == current.ApprovalMode {
		return nil
	}
	if _, err := s.setTurnSettings(ctx, controller, domain.SessionConversationOwner(id), picked); err != nil {
		return &domain.ProviderSwitchError{
			Kind:   domain.ProviderSwitchRefusalUnsupported,
			Detail: fmt.Sprintf("%s is already active and did not take the picked settings: %v", request.TargetHarness, err),
			Cause:  err,
		}
	}
	return nil
}

// lockProviderSwitch admits one provider switch per session at a time.
func (s *Service) lockProviderSwitch(ctx context.Context, id domain.SessionID) (func(), error) {
	value, _ := s.providerSwitches.LoadOrStore(id, make(chan struct{}, 1))
	slot, _ := value.(chan struct{})
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) messageDelivered(ctx context.Context, id domain.SessionID, clientMessageID string) (bool, error) {
	if clientMessageID == "" {
		return false, nil
	}
	// A session without a conversation has nothing delivered; the switch says why.
	conversation, err := s.store.ConversationForSession(ctx, id)
	if err != nil {
		return false, nil //nolint:nilerr // The switch reports the session's state itself.
	}
	_, found, err := s.store.ConversationMessageByClientID(ctx, conversation.ID, clientMessageID)
	return found, err
}

func (s *Service) switchProvider(ctx context.Context, id domain.SessionID, request domain.ProviderSwitchRequest) error {
	if request.TargetHarness == "" {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnsupported, Detail: "target provider is required"}
	}
	if record, found, err := s.sessions.GetSession(ctx, id); err != nil {
		return err
	} else if found && domain.NormalizeSessionMode(record.Mode) == domain.SessionModeTUI && s.terminalSwitcher != nil {
		// One builder writes the note for both UIs.
		started := time.Now()
		if request.HandoffNote, err = s.HandoffNote(ctx, record, request.TargetHarness, strings.TrimSpace(request.IdempotencyKey)); err != nil {
			return err
		}
		noteReady := time.Since(started)
		if err := s.terminalSwitcher.SwitchTUIProvider(ctx, id, request); err != nil {
			return err
		}
		s.log.Info("provider switch timing", "session", id, "mode", "terminal",
			"source", record.Harness, "target", request.TargetHarness,
			"handoff_note", noteReady, "total", time.Since(started))
		return nil
	}
	if !s.SupportsChat(request.TargetHarness) {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnsupported, Detail: fmt.Sprintf("%s does not support Chat", request.TargetHarness)}
	}
	// A chat asleep, or still waking, has no source agent to hand over from
	// yet. It is woken first, as a message to it would be.
	if !s.HasLiveChatController(id) {
		if err := s.wakeHibernated(ctx, id); err != nil && !errors.Is(err, ErrNoController) {
			return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalRecoveryRequired, Detail: "the chat's agent could not be woken: " + err.Error(), Cause: err}
		}
	}
	owner := domain.SessionConversationOwner(id)
	gate := s.controllerGate(owner)
	if err := gate.lock(ctx); err != nil {
		return err
	}
	defer gate.unlock()
	// Read under the lock: a switch that ran while this one waited has moved the
	// session to another agent and owner.
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return err
	}
	if record.IsTerminated {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalRecoveryRequired, Detail: "terminated sessions must be restored before switching"}
	}

	source, err := s.Controller(id)
	if err != nil {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalRecoveryRequired, Detail: "the source Chat controller is not ready", Cause: err}
	}
	if source.harness == request.TargetHarness {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalUnsupported, Detail: "target provider is already active", Cause: domain.ErrProviderAlreadyActive}
	}
	if source.busy() {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalBusy, Detail: "the provider is still working"}
	}
	// The target runs under the chat's own permission mode, so an agent that only
	// runs with permissions bypassed (Pi) can take over a chat that has them.
	permissions, chatPermissions := s.switchPermissions(ctx, source, request)
	if err := s.PreflightChat(ctx, request.TargetHarness, permissions); err != nil {
		kind := domain.ProviderSwitchRefusalUnavailable
		if errors.Is(err, ports.ErrChatUnsupported) {
			kind = domain.ProviderSwitchRefusalUnsupported
		}
		return &domain.ProviderSwitchError{Kind: kind, Detail: err.Error(), Cause: err}
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if err != nil {
		return err
	}
	if pending, pendingErr := s.store.HasPendingConversationInteractions(ctx, conversation.ID); pendingErr != nil {
		return pendingErr
	} else if pending {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalPendingInteraction, Detail: "answer the pending provider interaction before switching"}
	}
	started := time.Now()
	noteInput, err := s.handoffNoteInput(ctx, record, request.TargetHarness, strings.TrimSpace(request.IdempotencyKey))
	if err != nil {
		return err
	}
	if err := source.BeginIdleBranchHandoff(ctx); err != nil {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalBusy, Detail: err.Error(), Cause: err}
	}
	abortSource := true
	defer func() {
		if abortSource {
			source.AbortHandoff()
		}
	}()

	startCfg, sourceDriver, err := s.branchLaunchConfig(source)
	if err != nil {
		return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalRecoveryRequired, Detail: "source launch configuration is unavailable", Cause: err}
	}
	// The saved settings can name the owner from before an earlier switch; the
	// target start and source restore check it against the session as it is now.
	startCfg.ExpectedControllerOwner = record.ControllerOwner()
	activeBranch, err := s.store.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil {
		return err
	}
	// The outgoing provider's model, for the handoff marker. Read before it is stopped.
	sourceModel := cmp.Or(source.Settings().Model, record.Metadata.Model)
	operationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nativeEditHandoffLimit)
	defer cancel()
	// The source is idle, so list the changed files while it stops.
	changedFiles := make(chan []string, 1)
	go func() {
		changedFiles <- handoffChangedFiles(operationCtx, record.Metadata.WorkspacePath, record.Metadata.DiffBaseSHA)
	}()
	// restore brings a stopped source back, or the session would sit idle with
	// no provider behind it.
	restore := func(err error) error {
		if restoreErr := s.restoreClosedSourceController(operationCtx, id, source, activeBranch, startCfg, sourceDriver); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		abortSource = false
		return err
	}
	stopStarted := time.Now()
	if err := source.closeForBranchHandoff(operationCtx); err != nil {
		if err = fmt.Errorf("stop source provider: %w", err); source.State() == ports.ChatControllerStopped {
			return restore(err)
		}
		return err
	}
	stopped := time.Since(stopStarted)
	noteInput.ChangedFiles = <-changedFiles
	handoffNote := domain.BuildHandoffNote(noteInput)
	noteReady := time.Since(stopStarted)

	targetCfg := cloneStartConfig(startCfg)
	targetCfg.Harness = request.TargetHarness
	targetCfg.ProviderConversationID = ""
	// The source may have been rejoined after a daemon restart. The target is a
	// new provider with no host to rejoin, so it must be allowed to start one.
	targetCfg.ReconnectOnly = false
	targetCfg.ProviderSwitch = &domain.ChatProviderSwitch{
		ConversationID: conversation.ID, SourceBranchID: activeBranch.ID,
		SourceSequence: conversation.LatestSequence, Bootstrap: handoffNote,
	}
	if request.TargetHarness == domain.HarnessClaudeCode {
		targetCfg.ProviderSwitch.AutoCompactWindow = request.AutoCompactWindow
	}
	targetCfg.Model, targetCfg.Effort = request.Model, request.Effort
	targetCfg.Permissions = permissions
	targetCfg.ControllerReady = func(started StartResult) (ControllerCommit, error) {
		// The target's launch wrote its browser credential to the session, and the
		// user may have renamed or pinned it meanwhile, so the row is read again.
		targetRecord, err := s.requireChatSession(operationCtx, id)
		if err != nil {
			return ControllerCommit{}, err
		}
		targetRecord.Harness = request.TargetHarness
		// The source's native session belongs to the source.
		targetRecord.Metadata.AgentSessionID = ""
		targetRecord.Metadata.NativeTranscriptPath = ""
		targetRecord.Metadata.NativeIdentityObservedAt = time.Time{}
		targetRecord.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: s.now()}
		targetRecord.Metadata.ProviderConversationID = started.ProviderConversationID
		targetRecord.Metadata.ControllerGeneration = started.ControllerGeneration
		targetRecord.Metadata.Model, targetRecord.Metadata.Effort = request.Model, request.Effort
		targetRecord.UpdatedAt = s.now()
		marker := providerSwitchActivityDetail{
			Event: "provider_switch", SourceHarness: source.harness,
			TargetHarness: request.TargetHarness, BranchID: started.ProviderBoundary.ID,
			SourceModel: sourceModel, TargetModel: request.Model, TargetEffort: request.Effort,
			TargetAutoCompactWindow: targetCfg.ProviderSwitch.AutoCompactWindow,
			PermissionMode:          permissions,
		}
		if chatPermissions != permissions {
			marker.ChatPermissionMode = chatPermissions
		}
		detail, _ := json.Marshal(marker)
		switchActivity := domain.ConversationActivity{
			ID: started.ProviderBoundary.ID + ":provider-switch", Kind: domain.ActivityKindSystem,
			Status:  domain.ActivityStatusCompleted,
			Summary: "Switched provider from " + string(source.harness) + " to " + string(request.TargetHarness),
			Detail:  detail, RequestID: strings.TrimSpace(request.IdempotencyKey),
			ProviderItemID: started.ProviderBoundary.ID + ":provider-switch",
		}
		// The target's settings are saved with the switch, so the source never
		// resumes with them, whether the target fails or the daemon dies first.
		if err := s.store.CommitProviderSwitch(operationCtx, targetRecord, *started.ProviderBoundary, started.ControllerGeneration, started.Conversation.Settings, switchActivity); err != nil {
			return ControllerCommit{}, err
		}
		committed := started.Conversation
		committed.ActiveBranchID = started.ProviderBoundary.ID
		committed.SessionID = id
		return ControllerCommit{Conversation: committed, ControllerOwner: domain.SessionControllerOwner{
			Harness: request.TargetHarness, Mode: domain.SessionModeChat,
			ProviderConversationID: started.ProviderConversationID,
			ControllerGeneration:   started.ControllerGeneration,
		}}, nil
	}

	// The context reading is the outgoing model's; the target reports its own.
	if err := s.store.RecordUsage(operationCtx, conversation.ID, domain.ConversationUsage{}); err != nil {
		s.log.Warn("clear source usage before provider switch", "session", id, "error", err)
	}
	targetStarted := time.Now()
	target, err := s.startLocked(operationCtx, targetCfg)
	if err != nil {
		return fmt.Errorf("start target provider: %w", restore(err))
	}
	source.completeBranchHandoff()
	abortSource = false
	s.log.Info("provider switch timing", "session", id, "mode", "chat",
		"source", source.harness, "target", request.TargetHarness,
		"stop_source", stopped, "stop_and_note", noteReady, "start_target", time.Since(targetStarted),
		"total", time.Since(started))
	s.adoptTargetsLiveModel(operationCtx, id, target, request.Model)
	if request.FastMode {
		// Fast mode is a setting of the running controller, not of its launch.
		settings := target.Settings()
		settings.FastMode = true
		if err := target.SetSettings(operationCtx, settings); err != nil {
			s.log.Warn("apply fast mode after provider switch", "session", id, "error", err)
		}
	}
	return nil
}

// switchPermissions is the permission mode a switch target starts with, and
// the chat's own: the one picked for the target, else the chat's current one,
// else the one its agent was launched with, else Auto. The chat's current mode
// is the one it had before the source was brought in, when the source could not
// run in it. A target that cannot run in the chat's mode starts in the strictest
// one it can: Kimi in its default, Pi with permissions bypassed, as the picker
// shows beforehand.
func (s *Service) switchPermissions(ctx context.Context, source *Controller, request domain.ProviderSwitchRequest) (start, chat ports.PermissionMode) {
	mode := s.sourcePermissions(source)
	if marker, ok := s.latestSwitchMarker(ctx, source.conversation.ID); ok &&
		marker.TargetHarness == source.harness && marker.ChatPermissionMode != "" && marker.PermissionMode == mode {
		mode = marker.ChatPermissionMode
	}
	if request.PermissionMode != "" {
		mode = ports.NormalizePermissionMode(request.PermissionMode)
	}
	modes, err := s.PermissionModes(ctx, request.TargetHarness)
	if err != nil || len(modes) == 0 || slices.Contains(modes, mode) {
		// An agent that cannot run at all is refused by the preflight after this.
		return mode, mode
	}
	return modes[0], mode
}

// latestSwitchMarker is the conversation's newest switch marker: the one that
// brought in its current agent.
func (s *Service) latestSwitchMarker(ctx context.Context, conversationID string) (providerSwitchActivityDetail, bool) {
	if s.reader == nil {
		return providerSwitchActivityDetail{}, false
	}
	rows, err := s.reader.LoadConversationSnapshot(ctx, conversationID)
	if err != nil {
		return providerSwitchActivityDetail{}, false
	}
	for index := len(rows.Activities) - 1; index >= 0; index-- {
		var marker providerSwitchActivityDetail
		if json.Unmarshal(rows.Activities[index].Detail, &marker) == nil && marker.Event == "provider_switch" {
			return marker, true
		}
	}
	return providerSwitchActivityDetail{}, false
}

func (s *Service) sourcePermissions(source *Controller) ports.PermissionMode {
	s.mu.RLock()
	launched := s.startConfigs[source.owner()].Permissions
	s.mu.RUnlock()
	return ports.NormalizePermissionMode(cmp.Or(source.Settings().ApprovalMode, launched, ports.PermissionModeAuto))
}

// adoptTargetsLiveModel saves the model the target runs when its chat does not
// offer the picked one (Claude Code's lists the newest of each family by alias),
// or the chat would show one model and save another.
func (s *Service) adoptTargetsLiveModel(ctx context.Context, id domain.SessionID, target *Controller, picked string) {
	configurer, ok := target.conv.(ports.ChatConfigOptionController)
	if picked == "" || !ok {
		return
	}
	options, err := configurer.ListConfigOptions(ctx)
	if err != nil {
		return
	}
	for _, option := range options {
		if option.Category != "model" && option.ID != "model" {
			continue
		}
		live := option.Current.Select
		if live == "" || live == picked || slices.ContainsFunc(option.Choices, func(choice ports.ChatConfigOptionChoice) bool { return choice.Value == picked }) {
			return
		}
		settings := target.Settings()
		settings.Model = live
		if err := target.SetSettings(ctx, settings); err != nil {
			s.log.Warn("save the switch target's own model", "session", id, "error", err)
		}
		return
	}
}
