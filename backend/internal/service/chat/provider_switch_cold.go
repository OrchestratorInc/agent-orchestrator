package chat

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ColdSwitchLauncher builds the target agent's launch for a Chat session whose
// own agent has exited and cannot start, and runs start with it. Session Manager
// holds the session's agent operation while start runs, so a Resume click cannot
// bring the old agent back halfway through the handoff.
type ColdSwitchLauncher func(
	ctx context.Context,
	id domain.SessionID,
	target domain.AgentHarness,
	start func(context.Context, StartConfig) error,
) error

// SetColdSwitchLauncher lets a provider switch hand over from the stored
// transcript when the chat's own agent cannot be started.
func (s *Service) SetColdSwitchLauncher(launch ColdSwitchLauncher) {
	s.coldSwitchLaunch = launch
}

// providerSwitchSource is what a switch records about the agent it replaces.
type providerSwitchSource struct {
	harness           domain.AgentHarness
	model             string
	autoCompactWindow string
	permissions       ports.PermissionMode
	chatPermissions   ports.PermissionMode
}

// commitProviderSwitch is the target launch's ControllerReady: it moves the
// session to the target and records the switch marker in one transaction.
func (s *Service) commitProviderSwitch(
	ctx context.Context,
	id domain.SessionID,
	request domain.ProviderSwitchRequest,
	source providerSwitchSource,
) func(StartResult) (ControllerCommit, error) {
	return func(started StartResult) (ControllerCommit, error) {
		// The target's launch wrote its browser credential to the session, and the
		// user may have renamed or pinned it meanwhile, so the row is read again.
		targetRecord, err := s.requireChatSession(ctx, id)
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
			SourceModel: source.model, TargetModel: request.Model, TargetEffort: request.Effort,
			TargetAutoCompactWindow: source.autoCompactWindow,
			PermissionMode:          source.permissions,
		}
		if source.chatPermissions != source.permissions {
			marker.ChatPermissionMode = source.chatPermissions
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
		if err := s.store.CommitProviderSwitch(ctx, targetRecord, *started.ProviderBoundary, started.ControllerGeneration, started.Conversation.Settings, switchActivity); err != nil {
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
}

// coldSwitchProvider hands a chat to the target agent when the chat's own agent
// has exited and could not be started (resumeErr). The handoff note comes from
// AO's stored transcript, as it always does; there is just no running source to
// stop first. Anything the dead agent left in flight is settled by the target's
// start, as on any controller start after an exit.
func (s *Service) coldSwitchProvider(
	ctx context.Context,
	id domain.SessionID,
	request domain.ProviderSwitchRequest,
	result *domain.ProviderSwitchResult,
	resumeErr error,
) error {
	refuse := func(err error) error {
		var refusal *domain.ProviderSwitchError
		if errors.As(err, &refusal) {
			return err
		}
		return &domain.ProviderSwitchError{
			Kind:   domain.ProviderSwitchRefusalRecoveryRequired,
			Detail: fmt.Sprintf("the chat's agent has exited and could not be started (%v), and the switch could not start the new agent: %v", resumeErr, err),
			Cause:  errors.Join(resumeErr, err),
		}
	}
	err := s.coldSwitchLaunch(ctx, id, request.TargetHarness, func(ctx context.Context, base StartConfig) error {
		owner := domain.SessionConversationOwner(id)
		gate := s.controllerGate(owner)
		if err := gate.lock(ctx); err != nil {
			return err
		}
		defer gate.unlock()
		if s.HasLiveChatController(id) {
			// The old agent came back after all. Its own handoff is the safe one.
			return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalBusy, Detail: "the chat's agent started again; try the switch once more"}
		}
		record, err := s.requireChatSession(ctx, id)
		if err != nil {
			return err
		}
		if record.IsTerminated {
			return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalRecoveryRequired, Detail: "terminated sessions must be restored before switching"}
		}
		if record.Harness == request.TargetHarness {
			return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalAlreadyActive, Detail: "target provider is already active", Cause: domain.ErrProviderAlreadyActive}
		}
		conversation, err := s.store.ConversationForSession(ctx, id)
		if err != nil {
			return err
		}
		pending, err := s.store.HasPendingConversationInteractions(ctx, conversation.ID)
		if err != nil {
			return err
		} else if pending && !request.Force {
			return &domain.ProviderSwitchError{Kind: domain.ProviderSwitchRefusalPendingInteraction, Detail: "the agent is waiting on an approval or question"}
		}
		mode := ports.NormalizePermissionMode(cmp.Or(conversation.Settings.ApprovalMode, base.Permissions, ports.PermissionModeAuto))
		permissions, chatPermissions := s.switchPermissionsFrom(ctx, conversation.ID, record.Harness, mode, request)
		if err := s.PreflightChat(ctx, request.TargetHarness, permissions); err != nil {
			kind := domain.ProviderSwitchRefusalUnavailable
			if errors.Is(err, ports.ErrChatUnsupported) {
				kind = domain.ProviderSwitchRefusalUnsupported
			}
			return &domain.ProviderSwitchError{Kind: kind, Detail: err.Error(), Cause: err}
		}
		activeBranch, err := s.store.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
		if err != nil {
			return err
		}
		started := time.Now()
		noteInput, err := s.handoffNoteInput(ctx, record, request.TargetHarness, strings.TrimSpace(request.IdempotencyKey))
		if err != nil {
			return err
		}
		noteInput.ChangedFiles = handoffChangedFiles(ctx, record.Metadata.WorkspacePath, record.Metadata.DiffBaseSHA)

		targetCfg := cloneStartConfig(base)
		targetCfg.Harness = request.TargetHarness
		targetCfg.ProviderConversationID = ""
		targetCfg.ReconnectOnly = false
		targetCfg.ProviderSwitch = &domain.ChatProviderSwitch{
			ConversationID: conversation.ID, SourceBranchID: activeBranch.ID,
			SourceSequence: conversation.LatestSequence, Bootstrap: domain.BuildHandoffNote(noteInput),
		}
		if request.TargetHarness == domain.HarnessClaudeCode || domain.CanTurnOffAutoCompact(request.TargetHarness) {
			targetCfg.ProviderSwitch.AutoCompactWindow = request.AutoCompactWindow
		}
		targetCfg.Model, targetCfg.Effort = request.Model, request.Effort
		targetCfg.Permissions = permissions
		targetCfg.ControllerReady = s.commitProviderSwitch(ctx, id, request, providerSwitchSource{
			harness: record.Harness, model: cmp.Or(conversation.Settings.Model, record.Metadata.Model),
			autoCompactWindow: targetCfg.ProviderSwitch.AutoCompactWindow,
			permissions:       permissions, chatPermissions: chatPermissions,
		})
		// The context reading is the outgoing model's; the target reports its own.
		if err := s.store.RecordUsage(ctx, conversation.ID, domain.ConversationUsage{}); err != nil {
			s.log.Warn("clear source usage before provider switch", "session", id, "error", err)
		}
		target, err := s.startLocked(ctx, targetCfg)
		if err != nil {
			return fmt.Errorf("start target provider: %w", err)
		}
		result.FailedApprovals = request.Force && pending
		s.log.Info("provider switch timing", "session", id, "mode", "chat-cold",
			"source", record.Harness, "target", request.TargetHarness, "total", time.Since(started))
		s.adoptTargetsLiveModel(ctx, id, target, request.Model)
		if request.FastMode {
			settings := target.Settings()
			settings.FastMode = true
			if err := target.SetSettings(ctx, settings); err != nil {
				s.log.Warn("apply fast mode after provider switch", "session", id, "error", err)
			}
		}
		return nil
	})
	if err != nil {
		return refuse(err)
	}
	return nil
}
