package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// executeChatAgentSwitch replaces a structured controller without manufacturing
// a terminal runtime. The durable switch record and session row retain the same
// ownership boundaries as the TUI saga; their generation fence is the Chat
// controller generation instead of a runtime launch id.
func (m *Manager) executeChatAgentSwitch(
	ctx context.Context,
	admitted *admittedAgentSwitch,
) (result domain.AgentSwitch, retErr error) {
	workerCtx := ctx
	result = admitted.record
	store := admitted.store
	cfg := admitted.config
	rec := admitted.session
	id := rec.ID
	project := admitted.project
	targetAgent := admitted.targetAgent
	recorder := newAgentSwitchFlightRecorder(result, domain.SessionModeChat, domain.AgentSwitchExecutionLive)
	// Every step boundary feeds both the flight recorder and the timing log.
	var timer agentSwitchStageTimer
	stage := func(point domain.AgentSwitchFailurePoint) {
		recorder.boundary(point)
		timer.mark(point)
	}
	// Registered first so it runs last, after the outcome below is settled.
	defer func() {
		stages, total := timer.render()
		m.logger.Info("agent switch timing",
			"sessionID", id, "switchID", result.ID, "from", rec.Harness, "to", cfg.TargetHarness,
			"state", result.State, "total", total.Round(time.Millisecond), "stages", stages)
	}()

	sourceStopConclusive := false
	sourceStopped := false
	targetWorkspacePrepared := false
	targetOwnerCommitted := false
	targetOwnershipAmbiguous := false
	skipTerminalization := false
	var panicCause *agentSwitchPanicCause
	targetSetupEnv := m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env)
	m.augmentAgentRuntimeEnv(targetAgent, targetSetupEnv)

	defer func() {
		if recover() != nil {
			panicCause = &agentSwitchPanicCause{
				failurePoint:     domain.AgentSwitchFailureLiveWorkerPanic,
				executionAttempt: uuid.NewString(),
				frames:           sanitizeAgentSwitchPanicStack(debug.Stack()),
			}
			retErr = errors.New("switch Chat agent worker panicked")
		}
		if retErr != nil && !sourceStopped && !sourceStopConclusive {
			m.abortChatAgentSwitchHandoff(rec)
		}
		if retErr != nil && sourceStopConclusive && !sourceStopped {
			// The controller is already gone, but the durable confirmation did not
			// finish. Keep the stopping_source row and mutation gate for restart
			// reconciliation; treating this as pre-stop would reopen a dead source.
			skipTerminalization = true
		}
		rollbackSafe := retErr != nil && sourceStopped && !targetOwnerCommitted && !targetOwnershipAmbiguous
		if retErr != nil && targetWorkspacePrepared && !targetOwnerCommitted && !targetOwnershipAmbiguous {
			stage(domain.AgentSwitchFailureTargetWorkspaceCleanup)
			cleanupCtx, cancel := switchDurableContext(workerCtx)
			cleanupErr := m.cleanupPreparedAgentWorkspaceStrict(
				cleanupCtx, targetAgent, id, rec.Metadata.WorkspacePath, targetSetupEnv)
			cancel()
			if cleanupErr != nil {
				recorder.callOutcome = domain.AgentSwitchCallCleanupFailed
				recorder.compensation = domain.AgentSwitchCompensationFailed
				recorder.retain(false)
				rollbackSafe = false
				skipTerminalization = true
				retErr = errors.Join(retErr, fmt.Errorf("clean Chat target workspace before source rollback: %w", cleanupErr))
			} else {
				targetWorkspacePrepared = false
			}
		}
		if rollbackSafe {
			stage(domain.AgentSwitchFailureSourceControllerRestore)
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(workerCtx), switchPostStopWait)
			rollbackErr := m.rollbackStoppedChatAgentSwitchSource(rollbackCtx, rec, project)
			cancel()
			if rollbackErr != nil {
				recorder.callOutcome = domain.AgentSwitchCallEffectUnknown
				recorder.compensation = domain.AgentSwitchCompensationFailed
				recorder.ownership = domain.AgentSwitchOwnershipNone
				recorder.userImpact = domain.AgentSwitchUserImpactNoLiveOwner
				recorder.retain(false)
				skipTerminalization = true
				retErr = errors.Join(retErr, fmt.Errorf("restore Chat source after failed switch: %w", rollbackErr))
				m.logger.Error("Chat agent switch: automatic source rollback failed",
					"sessionID", id, "switchID", result.ID, "error", rollbackErr)
			} else {
				recorder.compensation = domain.AgentSwitchCompensationSucceeded
				recorder.ownership = domain.AgentSwitchOwnershipSource
				recorder.userImpact = domain.AgentSwitchUserImpactSourceAvailable
				m.logger.Info("Chat agent switch: source restored after target failure",
					"sessionID", id, "switchID", result.ID, "harness", result.FromHarness)
			}
		}
		if retErr != nil && !result.State.Terminal() && skipTerminalization && !result.RequiresRecovery() {
			recorder.retain(targetOwnershipAmbiguous)
			markerCtx, cancel := switchDurableContext(workerCtx)
			if panicCause != nil {
				markerCtx = withAgentSwitchPanicCause(markerCtx, *panicCause)
			}
			marked, markerErr := m.markRetainedSourceRecoveryWithRecorder(markerCtx, store, result, targetOwnershipAmbiguous, recorder)
			cancel()
			if markerErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("persist retained Chat switch recovery marker: %w", markerErr))
			} else {
				result = marked
			}
		}
		if retErr != nil && !result.State.Terminal() && !skipTerminalization {
			settleCtx, cancel := switchDurableContext(workerCtx)
			if panicCause != nil {
				settleCtx = withAgentSwitchPanicCause(settleCtx, *panicCause)
			}
			settled, failErr := m.failAgentSwitchWithRecorder(
				settleCtx, store, result, switchErrorCode(retErr, result.State), recorder)
			cancel()
			if failErr == nil {
				result = settled
				if result.State == domain.AgentSwitchCompleted {
					retErr = nil
				}
			} else {
				m.logger.Error("Chat agent switch: failed to persist terminal failure",
					"sessionID", id, "switchID", result.ID, "state", result.State, "error", failErr)
			}
		}
		if targetWorkspacePrepared && !targetOwnerCommitted && !targetOwnershipAmbiguous {
			cleanupCtx, cancel := switchDurableContext(workerCtx)
			m.cleanupPreparedAgentWorkspace(
				cleanupCtx, targetAgent, id, rec.Metadata.WorkspacePath, targetSetupEnv)
			cancel()
		}
		if result.State.Terminal() && strings.TrimSpace(m.dataDir) != "" {
			cleanupCtx, cancel := switchDurableContext(workerCtx)
			cleanupErr := m.cleanupAgentHandoffArtifacts(cleanupCtx, result)
			cancel()
			if cleanupErr != nil {
				m.observeTerminalAgentSwitchMaintenanceFailure(cleanupCtx, store, result, domain.SessionModeChat, domain.AgentSwitchExecutionLive)
				m.logger.Warn("Chat agent switch: handoff artifact cleanup failed",
					"sessionID", id, "switchID", result.ID, "state", result.State, "error", cleanupErr)
			}
		}
		if result.State.Terminal() {
			m.endAgentSwitch(id)
			return
		}
		m.retainAgentSwitch(id)
	}()

	stage(domain.AgentSwitchFailureTargetPreflight)
	if m.chat == nil {
		return result, fmt.Errorf("switch Chat agent %s: %w", id, ports.ErrChatUnsupported)
	}
	baseAgentConfig := effectiveAgentConfig(cfg.TargetHarness, rec.Kind, project.Config)
	// The target keeps the chat's approval mode, not the project default: a chat
	// someone set to bypass stays in bypass across a switch.
	baseAgentConfig.Permissions = m.chatSwitchPermissions(ctx, rec, baseAgentConfig.Permissions)
	if err := m.chat.PreflightChat(ctx, cfg.TargetHarness, baseAgentConfig.Permissions); err != nil {
		return result, fmt.Errorf("switch Chat agent %s: target preflight: %w", id, err)
	}
	handoff, ok := m.chat.(chatHandoffLauncher)
	if !ok {
		return result, fmt.Errorf("switch Chat agent %s: %w", id, ErrInterfaceHandoffUnsupported)
	}
	agentConfig, err := m.resolveAgentConfig(ctx, ports.SpawnConfig{
		ProjectID: rec.ProjectID,
		Kind:      rec.Kind,
		Harness:   cfg.TargetHarness,
		AgentConfig: ports.AgentConfig{
			Model:  strings.TrimSpace(cfg.Model),
			Effort: strings.TrimSpace(cfg.Effort),
		},
		EffortOverride: strings.TrimSpace(cfg.Effort) != "",
	}, domain.ProjectConfig{AgentConfig: baseAgentConfig})
	if err != nil {
		return result, fmt.Errorf("switch Chat agent %s: target config: %w", id, err)
	}
	// A target the user picked before sending may already be running. Its hook
	// and trust files are in the workspace, so failure cleanup covers them.
	standby := m.takeChatStandby(id, cfg.TargetHarness, agentConfig)
	if standby != nil {
		defer standby.handle.Discard()
		// The standby wrote the target's files before this switch prepared any.
		// A switch that fails before preparing them itself removes them best
		// effort; their removal never decides the switch's outcome.
		defer func() {
			if retErr != nil && !targetWorkspacePrepared && !targetOwnerCommitted {
				cleanupCtx, cancel := switchDurableContext(workerCtx)
				m.cleanupPreparedAgentWorkspace(cleanupCtx, targetAgent, id, rec.Metadata.WorkspacePath, targetSetupEnv)
				cancel()
			}
		}()
	}

	systemPrompt, err := m.buildSystemPrompt(ctx, rec.Kind, rec.ProjectID, rec.ID, rec.RequestedOutput, true)
	if err != nil {
		return result, fmt.Errorf("switch Chat agent %s: system prompt: %w", id, err)
	}
	systemPrompt = appendAgentContinuationProtocol(systemPrompt)
	targetConfigDir, err := nativeConfigDirForMode(ctx, targetAgent, targetSetupEnv, domain.SessionModeChat)
	if err != nil {
		return result, fmt.Errorf("switch Chat agent %s: target config: %w", id, err)
	}
	// The target always starts a fresh native conversation. A resumed one keeps
	// the instructions it was started with and ignores this switch's handoff, so
	// an agent coming back would know only its own earlier turns and none of
	// what the agents in between did. A fresh start gets the whole
	// conversation, its own earlier turns included, through the handoff.
	var resumeCandidate domain.AgentNativeSession
	resumable := false
	targetStartMode := domain.AgentSwitchTargetStartFresh
	providerConversationID := ""
	if resumable {
		targetStartMode = domain.AgentSwitchTargetStartResumed
		providerConversationID = resumeCandidate.NativeSessionID
	}
	additionalDirectories, err := m.restoredWorkspaceProjectDirectories(
		ctx, rec, project, rec.Metadata.WorkspacePath)
	if err != nil {
		return result, fmt.Errorf("switch Chat agent %s: workspace roots: %w", id, err)
	}
	stage(domain.AgentSwitchFailureHandoffDirectoryPrepare)
	if _, _, err := m.prepareAgentHandoffPaths(ctx, id, string(result.ID)); err != nil {
		return result, fmt.Errorf("switch Chat agent %s: prepare handoff directory: %w", id, err)
	}

	stage(domain.AgentSwitchFailureHandoffSettlement)
	result, err = m.settleOptionalAgentHandoff(
		ctx, store, result, domain.AgentHandoffUnavailable)
	if err != nil {
		return result, fmt.Errorf("switch Chat agent %s: close optional source handoff: %w", id, err)
	}
	stage(domain.AgentSwitchFailureChatSourceQuiesce)
	if err := handoff.PrepareChatHandoff(
		ctx, id, domain.SessionInterfaceTransitionInterrupt); err != nil {
		return result, fmt.Errorf("switch Chat agent %s: quiesce source: %w", id, err)
	}

	targetGeneration := domain.AgentGenerationID(strings.TrimSpace(m.newLaunchID()))
	if targetGeneration == "" {
		return result, fmt.Errorf("switch Chat agent %s: target controller generation is empty", id)
	}
	stage(domain.AgentSwitchFailureStoppingSourceCommit)
	if err := m.advanceAgentSwitch(ctx, store, &result, domain.AgentSwitchStoppingSource, func(next *domain.AgentSwitch) {
		next.TargetStartMode = targetStartMode
		next.TargetGenerationID = targetGeneration
	}); err != nil {
		return result, fmt.Errorf("switch Chat agent %s: stop source: %w", id, err)
	}
	recorder.durable(result)

	stage(domain.AgentSwitchFailureSourceControllerStop)
	if err := m.stopSourceControllerConclusive(rec); err != nil {
		recorder.failurePoint = domain.AgentSwitchFailureSourceControllerDrain
		recorder.callOutcome = domain.AgentSwitchCallEffectUnknown
		recorder.retain(false)
		skipTerminalization = true
		return result, fmt.Errorf("switch Chat agent %s: stop source controller: %w", id, err)
	}
	sourceStopConclusive = true
	stoppedAt := m.clock()
	boundaryCtx, cancelBoundary := switchDurableContext(workerCtx)
	stage(domain.AgentSwitchFailureSourceStopCommit)
	confirmed, err := m.lcm.ConfirmAgentSwitchSourceStopped(
		boundaryCtx,
		domain.AgentSwitchSourceStopConfirmation{
			SwitchID:                           result.ID,
			SessionID:                          id,
			SourceMode:                         domain.SessionModeChat,
			SourceHarness:                      rec.Harness,
			SourceGenerationID:                 result.SourceGenerationID,
			ExpectedSourceControllerGeneration: rec.Metadata.ControllerGeneration,
			TargetGenerationID:                 targetGeneration,
			StoppedAt:                          stoppedAt,
		},
	)
	if err != nil {
		cancelBoundary()
		skipTerminalization = true
		return result, fmt.Errorf("switch Chat agent %s: persist source stop: %w", id, err)
	}
	if !confirmed {
		recorder.failurePoint = domain.AgentSwitchFailureSourceStopReadback
		recorder.callOutcome = domain.AgentSwitchCallCommittedResponseLost
		cancelBoundary()
		skipTerminalization = true
		return result, fmt.Errorf("switch Chat agent %s: persist source stop: durable ownership changed concurrently", id)
	}
	sourceStopped = true
	recorder.sourceStopConfirmed = domain.AgentSwitchTriTrue
	recorder.ownership = domain.AgentSwitchOwnershipNone
	recorder.userImpact = domain.AgentSwitchUserImpactNoLiveOwner
	result, err = requireAgentSwitch(boundaryCtx, store, result.ID)
	cancelBoundary()
	if err != nil {
		return result, fmt.Errorf("switch Chat agent %s: reload source stop: %w", id, err)
	}
	recorder.durable(result)
	stoppedSession, ok, err := m.store.GetSession(workerCtx, id)
	if err != nil {
		return result, fmt.Errorf("switch Chat agent %s: reload stopped session: %w", id, err)
	}
	if !ok {
		return result, fmt.Errorf("switch Chat agent %s: reload stopped session: %w", id, ErrNotFound)
	}

	postStopWait := m.switchPostStopWait
	if postStopWait <= 0 {
		postStopWait = switchPostStopWait
	}
	postStopCtx, cancelPostStop := context.WithTimeout(workerCtx, postStopWait)
	defer cancelPostStop()
	ctx = postStopCtx

	stage(domain.AgentSwitchFailureSourceTranscriptCapture)
	// The excerpt comes from AO's own record of the conversation: every agent's
	// turns, in order. A provider's transcript holds only that provider's turns,
	// so after a few switches it would leave out what the others said. The
	// provider transcript, when there is one, stays as a pointer for detail.
	observedTranscript, sourceTranscriptStatus := m.captureSourceTranscriptFact(
		ctx, admitted.sourceAgent, admitted.sourceNative, false)
	finalContext := deterministicSwitchContext{
		OriginalTask:          stoppedSession.Metadata.Prompt,
		LatestUserPrompt:      stoppedSession.Metadata.LatestUserPrompt,
		LatestAssistantUpdate: stoppedSession.Metadata.LatestAssistantUpdate,
		Workspaces:            m.captureWorkspaceFacts(ctx, stoppedSession),
		PullRequests:          m.capturePRFacts(ctx, id),
		CapturedAt:            m.clock(),
		HumanMessageFollows:   cfg.Message != nil,
	}
	if observedTranscript != nil {
		finalContext.SourceTranscriptPath = observedTranscript.Path
	}
	stage(domain.AgentSwitchFailureContinuationBuild)
	// When AO's own record cannot be read, the source's native transcript tail
	// (what the handoff carried before AO kept its own) stands in for it.
	nativeTail := func() *switchTranscriptFact {
		fact, _ := m.captureSourceTranscriptFact(ctx, admitted.sourceAgent, admitted.sourceNative, true)
		return fact
	}
	continuation := m.buildChatSwitchContinuation(ctx, id, result, finalContext, observedTranscript, nativeTail)
	stage(domain.AgentSwitchFailureFinalArtifactPublish)
	writtenFinal, err := m.writeFinalizedHandoffFile(ctx, result, continuation)
	if err != nil {
		return result, fmt.Errorf("switch Chat agent %s: retain finalized handoff: %w", id, err)
	}
	stage(domain.AgentSwitchFailureFinalArtifactCommit)
	result, err = m.finalizeAgentSwitchHandoff(
		ctx, store, result, writtenFinal, false, sourceTranscriptStatus)
	if err != nil {
		if errors.Is(err, errAgentSwitchFinalArtifactVerify) {
			recorder.failurePoint = domain.AgentSwitchFailureFinalArtifactVerify
		}
		return result, fmt.Errorf("switch Chat agent %s: record finalized handoff: %w", id, err)
	}
	finalSystemPrompt := appendAgentSwitchContinuation(systemPrompt, continuation)
	stage(domain.AgentSwitchFailureTargetPromptPrepare)
	systemPromptFile, err := m.prepareSystemPromptFile(rec.ID, cfg.TargetHarness, finalSystemPrompt)
	if err != nil {
		return result, fmt.Errorf("switch Chat agent %s: target system prompt file: %w", id, err)
	}
	stage(domain.AgentSwitchFailureTargetWorkspacePrepare)
	if err := m.prepareWorkspace(
		ctx, targetAgent, rec.ID, rec.Metadata.WorkspacePath,
		finalSystemPrompt, systemPromptFile, agentConfig, targetSetupEnv,
	); err != nil {
		return result, fmt.Errorf("switch Chat agent %s: prepare target workspace: %w", id, err)
	}
	targetWorkspacePrepared = true
	stage(domain.AgentSwitchFailureChatProviderBoundaryCommit)
	if err := m.advanceAgentSwitch(ctx, store, &result, domain.AgentSwitchStartingTarget, nil); err != nil {
		return result, fmt.Errorf("switch Chat agent %s: record target start: %w", id, err)
	}
	recorder.durable(result)
	// Rotate only after the source controller is conclusively stopped. Persisting
	// the target verifier any earlier would revoke the still-live source bearer
	// during the reversible half of the switch.
	credentialRecord, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return result, fmt.Errorf("switch Chat agent %s: reload stopped session for browser capability: %w", id, err)
	}
	if !found || credentialRecord.IsTerminated || credentialRecord.Harness != rec.Harness ||
		domain.NormalizeSessionMode(credentialRecord.Mode) != domain.SessionModeChat ||
		credentialRecord.Activity.State != domain.ActivityExited {
		return result, fmt.Errorf("switch Chat agent %s: source ownership changed before browser capability rotation", id)
	}
	targetLaunchEnv := m.runtimeEnv(id, credentialRecord.ProjectID, credentialRecord.IssueID, project.Config.Env)
	m.augmentAgentRuntimeEnv(targetAgent, targetLaunchEnv)
	releaseCodexAdmission, admissionErr := m.acquireCodexControllerAdmission(ctx, cfg.TargetHarness)
	if admissionErr != nil {
		return result, fmt.Errorf("switch Chat agent %s: %w", id, admissionErr)
	}
	defer releaseCodexAdmission()

	if resumable {
		stage(domain.AgentSwitchFailureChatProviderResume)
	} else {
		stage(domain.AgentSwitchFailureChatProviderStart)
	}
	historyMode := ports.ChatHistoryImport
	if resumable {
		historyMode = ports.ChatHistoryDeferred
	}
	var standbyHandle ports.ChatStandby
	var standbyEnv func() (map[string]string, error)
	if standby != nil {
		standbyHandle = standby.handle
	}
	_, err = m.chat.StartChat(ctx, ChatStart{
		Standby:                 standbyHandle,
		SessionID:               id,
		ProjectID:               rec.ProjectID,
		Kind:                    rec.Kind,
		Harness:                 cfg.TargetHarness,
		DataDir:                 m.dataDir,
		WorkspacePath:           rec.Metadata.WorkspacePath,
		Env:                     targetLaunchEnv,
		Model:                   agentConfig.Model,
		Effort:                  agentConfig.Effort,
		Permissions:             agentConfig.Permissions,
		SystemPrompt:            finalSystemPrompt,
		AdditionalDirectories:   additionalDirectories,
		MCPServers:              m.aoMCPServers(cfg.TargetHarness, targetLaunchEnv),
		ExpectedControllerOwner: credentialRecord.ControllerOwner(),
		PrepareControllerEnv: func(launchCtx context.Context, expected domain.SessionControllerOwner) (map[string]string, error) {
			if standby != nil {
				// The standby already holds a bearer; persisting its verifier now,
				// after the source stopped, is the rotation a cold start does here.
				if standbyEnv == nil {
					standbyEnv = standby.controllerEnv(launchCtx, m, &credentialRecord, expected)
				}
				return standbyEnv()
			}
			prepared, launchEnv, prepareErr := m.prepareChatControllerEnv(
				launchCtx, credentialRecord, project.Config.Env, expected,
			)
			if prepareErr != nil {
				return nil, prepareErr
			}
			credentialRecord = prepared
			m.augmentAgentRuntimeEnv(targetAgent, launchEnv)
			return launchEnv, nil
		},
		ProviderConversationID: providerConversationID,
		ProviderScopeID:        chatSwitchProviderBoundaryID(result.ID),
		ControllerGeneration:   string(targetGeneration),
		HistoryMode:            historyMode,
		ControllerReady: func(started ChatStarted) (ChatControllerCommit, error) {
			emptyCommit := ChatControllerCommit{}
			targetControllerOwner := chatControllerOwner(
				credentialRecord, cfg.TargetHarness,
				started.ProviderConversationID, started.ControllerGeneration,
			)
			targetControllerOwner.AgentSessionID = started.ProviderConversationID
			targetControllerOwner.AgentSessionIDLaunchID = ""
			if strings.TrimSpace(started.ProviderConversationID) == "" ||
				started.ControllerGeneration != string(targetGeneration) {
				return emptyCommit, errors.New("target Chat controller returned incomplete or mismatched identity")
			}
			var stored domain.AgentNativeSession
			if resumable {
				stage(domain.AgentSwitchFailureChatNativeIdentityCommit)
				if started.ProviderConversationID != resumeCandidate.NativeSessionID {
					return emptyCommit, errors.New("resumed target Chat controller changed provider conversation identity")
				}
				expectedGeneration := resumeCandidate.LastGenerationID
				stored = resumeCandidate
				stored.LastGenerationID = targetGeneration
				stored.LastUsedAt = m.clock()
				changed, updateErr := store.UpdateAgentNativeSession(ctx, stored, expectedGeneration)
				if updateErr != nil {
					return emptyCommit, fmt.Errorf("advance resumed target Chat native session: %w", updateErr)
				}
				if !changed {
					return emptyCommit, errors.New("resumed target Chat native session changed concurrently")
				}
			} else {
				stage(domain.AgentSwitchFailureChatNativeIdentityCommit)
				now := m.clock()
				native := domain.AgentNativeSession{
					ID:          domain.AgentNativeSessionID("native-" + uuid.NewString()),
					AOSessionID: id, Harness: cfg.TargetHarness, ConfigDir: targetConfigDir,
					NativeSessionID: started.ProviderConversationID, LastGenerationID: targetGeneration,
					CreatedAt: now, LastUsedAt: now,
				}
				var createErr error
				stored, _, createErr = store.CreateAgentNativeSession(ctx, native)
				if createErr != nil {
					return emptyCommit, fmt.Errorf("persist target Chat native session: %w", createErr)
				}
			}
			stage(domain.AgentSwitchFailureChatProviderBoundaryCommit)
			if err := m.advanceAgentSwitch(ctx, store, &result, domain.AgentSwitchStartingTarget, func(next *domain.AgentSwitch) {
				next.TargetNativeSessionRef = nativeSessionIDPtr(stored.ID)
			}); err != nil {
				return emptyCommit, fmt.Errorf("record target Chat native session: %w", err)
			}
			activatedAt := m.clock()
			activation := domain.AgentSwitchChatTargetActivation{
				SwitchID: result.ID, SessionID: id,
				SourceHarness: rec.Harness, SourceGenerationID: result.SourceGenerationID,
				ExpectedSourceControllerGeneration: rec.Metadata.ControllerGeneration,
				TargetHarness:                      cfg.TargetHarness, TargetNativeSessionRef: stored.ID,
				TargetGenerationID:     targetGeneration,
				ProviderConversationID: started.ProviderConversationID,
				ControllerGeneration:   started.ControllerGeneration,
				ActivatedAt:            activatedAt,
			}
			stage(domain.AgentSwitchFailureChatTargetActivationCommit)
			activated, activationErr := m.lcm.ActivateChatAgentSwitchTarget(
				ctx,
				activation,
			)
			if activationErr != nil || !activated {
				recorder.failurePoint = domain.AgentSwitchFailureChatTargetActivationReadback
				activationCtx, cancelActivation := switchDurableContext(ctx)
				current, committed, sourceStillOwns, resolutionErr := m.resolveChatTargetActivationOutcome(
					activationCtx, store, rec, activation)
				cancelActivation()
				if committed {
					recorder.callOutcome = domain.AgentSwitchCallCommittedResponseLost
					result = current
					targetOwnerCommitted = true
					return ChatControllerCommit{
						Conversation:    committedChatSwitchConversation(started.Conversation, result.ID, activatedAt),
						ControllerOwner: targetControllerOwner,
					}, nil
				}
				if !sourceStillOwns || resolutionErr != nil {
					recorder.callOutcome = domain.AgentSwitchCallEffectUnknown
					targetOwnershipAmbiguous = true
					skipTerminalization = true
					cause := activationErr
					if cause == nil {
						cause = errors.New("activation CAS returned false")
					}
					return emptyCommit, fmt.Errorf("target Chat controller activation outcome is ambiguous: %w", errors.Join(cause, resolutionErr))
				}
				if activationErr != nil {
					return emptyCommit, activationErr
				}
				return emptyCommit, errors.New("target Chat controller ownership changed concurrently")
			}
			targetOwnerCommitted = true
			recorder.targetOwnerCommitted = domain.AgentSwitchTriTrue
			recorder.ownership = domain.AgentSwitchOwnershipTarget
			recorder.userImpact = domain.AgentSwitchUserImpactTargetUnavailable
			result.State = domain.AgentSwitchTargetReady
			result.UpdatedAt = activatedAt
			return ChatControllerCommit{
				Conversation:    committedChatSwitchConversation(started.Conversation, result.ID, activatedAt),
				ControllerOwner: targetControllerOwner,
			}, nil
		},
	})
	if err != nil {
		if targetOwnerCommitted {
			recorder.failurePoint = domain.AgentSwitchFailureChatControllerPublish
			recorder.callOutcome = domain.AgentSwitchCallCommittedResponseLost
			skipTerminalization = true
		}
		return result, fmt.Errorf("switch Chat agent %s: start target controller: %w", id, err)
	}
	if !targetOwnerCommitted {
		skipTerminalization = true
		return result, fmt.Errorf("switch Chat agent %s: target controller started without durable ownership", id)
	}
	result, err = requireAgentSwitch(ctx, store, result.ID)
	if err != nil {
		return result, fmt.Errorf("switch Chat agent %s: reload target activation: %w", id, err)
	}
	stage(domain.AgentSwitchFailureDeliveryOpenCommit)
	if err := m.advanceAgentSwitch(ctx, store, &result, domain.AgentSwitchDelivering, nil); err != nil {
		return result, fmt.Errorf("switch Chat agent %s: begin continuation delivery: %w", id, err)
	}
	recorder.durable(result)
	stage(domain.AgentSwitchFailureChatContinuationRelay)
	if err := m.deliverChatSwitchOpening(ctx, id, result.ID, cfg.Message); err != nil {
		return result, fmt.Errorf("switch Chat agent %s: deliver continuation: %w", id, err)
	}
	acknowledgedAt := m.clock()
	stage(domain.AgentSwitchFailureChatTargetAckCommit)
	result, acknowledged, err := m.acknowledgeAgentSwitchTargetWithReadback(ctx, store, result, targetGeneration, acknowledgedAt)
	if err != nil {
		return result, fmt.Errorf("switch Chat agent %s: acknowledge continuation: %w", id, err)
	}
	if !acknowledged {
		recorder.callOutcome = domain.AgentSwitchCallStale
		return result, nil
	}
	completionCtx, cancelCompletion := switchDurableContext(ctx)
	stage(domain.AgentSwitchFailureCompletionCommit)
	result, err = m.completeAcknowledgedAgentSwitch(completionCtx, store, result)
	cancelCompletion()
	if err != nil {
		return result, fmt.Errorf("switch Chat agent %s: complete: %w", id, err)
	}
	return result, nil
}

func committedChatSwitchConversation(
	conversation domain.ConversationRecord,
	switchID domain.AgentSwitchID,
	activatedAt time.Time,
) domain.ConversationRecord {
	if conversation.ID == "" {
		return domain.ConversationRecord{}
	}
	conversation.ActiveBranchID = chatSwitchProviderBoundaryID(switchID)
	conversation.Settings.Model = ""
	conversation.Settings.ReasoningEffort = ""
	conversation.UpdatedAt = activatedAt
	return conversation
}

// chatSwitchPermissions is the approval mode a switch target starts with: the
// chat's current one when the conversation records it (someone may have changed
// it mid-chat), else the mode the session was launched with, else fallback.
func (m *Manager) chatSwitchPermissions(ctx context.Context, rec domain.SessionRecord, fallback ports.PermissionMode) ports.PermissionMode {
	if history, ok := m.store.(chatHandoffHistoryStore); ok {
		if conversation, err := history.ConversationForSession(ctx, rec.ID); err == nil && conversation.Settings.ApprovalMode != "" {
			return conversation.Settings.ApprovalMode
		}
	}
	if rec.Metadata.Permissions != "" {
		return rec.Metadata.Permissions
	}
	return fallback
}

// chatMessageSender delivers a human message through the ordinary Chat send
// path, with the same idempotency and attachment handling as the composer.
type chatMessageSender interface {
	SendSessionChatMessage(ctx context.Context, id domain.SessionID, msg ports.ChatUserMessage) (string, error)
}

// chatTranscriptExcerpter reads AO's own record of a Chat conversation. Every
// agent has one, whether or not its provider keeps a transcript AO can read.
type chatTranscriptExcerpter interface {
	ChatTranscriptExcerpt(ctx context.Context, id domain.SessionID, maxBytes int, markers []ChatTranscriptMarker) (excerpt string, truncated bool, err error)
}

// ChatTranscriptMarker labels a point in a Chat conversation's history, placed
// among its messages by time.
type ChatTranscriptMarker struct {
	At   time.Time
	Text string
}

// chatSwitchMarkers marks where each earlier completed switch handed the chat
// to another agent, so the next agent can tell which one said what.
func (m *Manager) chatSwitchMarkers(ctx context.Context, id domain.SessionID) []ChatTranscriptMarker {
	switches, err := m.ListAgentSwitches(ctx, id)
	if err != nil {
		return nil
	}
	markers := make([]ChatTranscriptMarker, 0, len(switches))
	for _, sw := range switches {
		if sw.State != domain.AgentSwitchCompleted || sw.RequestedAt.IsZero() {
			continue
		}
		markers = append(markers, ChatTranscriptMarker{
			At:   sw.RequestedAt,
			Text: fmt.Sprintf("chat switched from %s to %s; messages below are from %s", sw.FromHarness, sw.TargetHarness, sw.TargetHarness),
		})
	}
	return markers
}

// deliverChatSwitchOpening opens the target's first turn. A switch made from
// the composer carries the user's message, which the target answers directly;
// otherwise AO's brief activation prompt starts it.
func (m *Manager) deliverChatSwitchOpening(
	ctx context.Context,
	id domain.SessionID,
	switchID domain.AgentSwitchID,
	message *ports.ChatUserMessage,
) error {
	if message == nil {
		_, err := m.chat.RelaySessionChatTurn(
			ctx, id, aoTargetActivationPrompt, chatSwitchActivationMessageID(switchID), ports.MessageDeliveryOptions{})
		return err
	}
	sender, ok := m.chat.(chatMessageSender)
	if !ok {
		return errors.New("chat launcher cannot deliver a human message")
	}
	_, err := sender.SendSessionChatMessage(ctx, id, *message)
	return err
}

// buildChatSwitchContinuation fills the handoff with as much of the
// conversation as fits under the handoff cap. The excerpt comes from AO's own
// record, every agent's turns and actions in order; a provider's transcript
// holds only its own, so after a few switches it would leave out what the
// others said. The provider transcript, when there is one, stays as a pointer.
//
// Over the cap, the generic builder falls back to a few KiB of history, so the
// excerpt is first sized to the whole cap, then shrunk by each overflow.
func (m *Manager) buildChatSwitchContinuation(
	ctx context.Context,
	id domain.SessionID,
	sw domain.AgentSwitch,
	facts deterministicSwitchContext,
	native *switchTranscriptFact,
	nativeTail func() *switchTranscriptFact,
) string {
	transcript := native
	budget := handoffContinuationMaxBytes
	for attempt := 0; attempt < 4 && budget > 0; attempt++ {
		conversation := m.chatTranscriptFact(ctx, id, budget)
		if conversation == nil {
			if attempt == 0 && nativeTail != nil {
				if fallback := nativeTail(); fallback != nil {
					transcript = fallback
				}
			}
			break
		}
		if native != nil {
			conversation.Path = native.Path
		}
		transcript = conversation
		over := len(buildTargetContinuationMessageBody(sw, facts, transcript)) - handoffContinuationMaxBytes
		if over <= 0 {
			break
		}
		budget -= over + 4<<10
	}
	return buildTargetContinuationMessageWithLimit(sw, facts, transcript, handoffContinuationMaxBytes)
}

// chatTranscriptFact is the newest maxBytes of AO's record of this
// conversation, or nil when there is none to read.
func (m *Manager) chatTranscriptFact(ctx context.Context, id domain.SessionID, maxBytes int) *switchTranscriptFact {
	excerpter, ok := m.chat.(chatTranscriptExcerpter)
	if !ok {
		return nil
	}
	excerpt, truncated, err := excerpter.ChatTranscriptExcerpt(ctx, id, maxBytes, m.chatSwitchMarkers(ctx, id))
	if err != nil {
		m.logger.Warn("Chat agent switch: AO transcript excerpt unavailable", "sessionID", id, "error", err)
		return nil
	}
	if strings.TrimSpace(excerpt) == "" {
		return nil
	}
	return &switchTranscriptFact{Tail: excerpt, Truncated: truncated, Conversation: true}
}

func chatSwitchProviderBoundaryID(switchID domain.AgentSwitchID) string {
	return string(switchID) + ":provider"
}

func chatSwitchActivationMessageID(switchID domain.AgentSwitchID) string {
	return "agent-switch:" + string(switchID) + ":activation"
}

func (m *Manager) abortChatAgentSwitchHandoff(rec domain.SessionRecord) {
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return
	}
	if handoff, ok := m.chat.(chatHandoffLauncher); ok {
		handoff.AbortChatHandoff(rec.ID)
	}
}

func (m *Manager) rollbackStoppedChatAgentSwitchSource(
	ctx context.Context,
	rec domain.SessionRecord,
	project domain.ProjectRecord,
) error {
	current, found, err := m.store.GetSession(ctx, rec.ID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if current.IsTerminated || current.Harness != rec.Harness ||
		domain.NormalizeSessionMode(current.Mode) != domain.SessionModeChat {
		return errors.New("chat source no longer owns the session")
	}
	sourceAgent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return ErrUnknownHarness
	}
	systemPrompt, err := m.buildSystemPrompt(ctx, rec.Kind, rec.ProjectID, rec.ID, rec.RequestedOutput, true)
	if err != nil {
		return err
	}
	systemPromptFile, err := m.prepareSystemPromptFile(rec.ID, rec.Harness, systemPrompt)
	if err != nil {
		return err
	}
	agentConfig := effectiveAgentConfig(rec.Harness, rec.Kind, project.Config)
	env := m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env)
	m.augmentAgentRuntimeEnv(sourceAgent, env)
	if err := m.prepareWorkspace(
		ctx, sourceAgent, rec.ID, rec.Metadata.WorkspacePath,
		systemPrompt, systemPromptFile, agentConfig, env,
	); err != nil {
		return err
	}
	// A target that failed to start can leave its provider host behind. The
	// session has one host, so the source cannot reopen while the target's
	// host still owns it under another protocol. Stop it first.
	if err := m.stopSourceControllerConclusive(current); err != nil {
		return fmt.Errorf("stop failed target controller: %w", err)
	}
	_, err = m.resumeChatController(
		ctx, "restore failed agent switch", current, project,
		workspaceInfo(current), false, false, "", domain.SessionInterfaceTransitionHistoryStrict,
	)
	return err
}

// reconcileChatAgentSwitch settles a switch interrupted with its structured
// controller in memory. No Chat controller survives a daemon restart, so the
// durable controller generation replaces runtime liveness as the ownership
// proof: a source-owned row is resumed, while an activated target is restored
// with its finalized continuation before the switch gate is released.
func (m *Manager) reconcileChatAgentSwitch(
	ctx context.Context,
	store ports.AgentSwitchStore,
	rec domain.SessionRecord,
	sw domain.AgentSwitch,
	execution domain.AgentSwitchExecution,
) (bool, error) {
	recorder := newAgentSwitchFlightRecorder(sw, domain.SessionModeChat, execution)
	fail := func(code domain.AgentSwitchErrorCode) (bool, error) {
		recorder.boundary(domain.AgentSwitchFailureRecoverySettlement)
		_, err := m.failAgentSwitchWithRecorder(ctx, store, sw, code, recorder)
		return err == nil, err
	}
	switch sw.State {
	case domain.AgentSwitchPreparingHandoff:
		return fail(domain.AgentSwitchErrorDaemonRestartPreStop)
	case domain.AgentSwitchStoppingSource:
		if rec.IsTerminated {
			return fail(domain.AgentSwitchErrorSourceSessionTerminated)
		}
		confirmed, err := m.lcm.ConfirmAgentSwitchSourceStopped(ctx, domain.AgentSwitchSourceStopConfirmation{
			SwitchID:                           sw.ID,
			SessionID:                          rec.ID,
			SourceMode:                         domain.SessionModeChat,
			SourceHarness:                      sw.FromHarness,
			SourceGenerationID:                 sw.SourceGenerationID,
			ExpectedSourceControllerGeneration: string(sw.SourceGenerationID),
			TargetGenerationID:                 sw.TargetGenerationID,
			StoppedAt:                          m.clock(),
		})
		if err != nil {
			return false, err
		}
		if !confirmed {
			return false, fmt.Errorf("reconcile Chat agent switch %s: source-stop ownership changed concurrently", sw.ID)
		}
		current, err := requireAgentSwitch(ctx, store, sw.ID)
		if err != nil {
			return false, err
		}
		return m.failRecoveredSwitchWithSourceRollback(ctx, store, rec, current, execution)
	case domain.AgentSwitchSourceStopped:
		if err := m.cleanupRecoveredTargetWorkspace(ctx, rec, sw); err != nil {
			return false, err
		}
		return m.failRecoveredSwitchWithSourceRollback(ctx, store, rec, sw, execution)
	case domain.AgentSwitchStartingTarget:
		if rec.Harness != sw.FromHarness {
			return false, fmt.Errorf("reconcile Chat agent switch %s: starting target has unexpected durable owner %q", sw.ID, rec.Harness)
		}
		if err := m.cleanupRecoveredTargetWorkspace(ctx, rec, sw); err != nil {
			return false, err
		}
		return m.failRecoveredSwitchWithSourceRollback(ctx, store, rec, sw, execution)
	case domain.AgentSwitchTargetReady, domain.AgentSwitchDelivering:
		recoverable, err := m.chatTargetNativeIdentityRecoverable(ctx, store, rec, sw)
		if err != nil {
			return false, err
		}
		if !recoverable {
			return false, fmt.Errorf("reconcile Chat agent switch %s: activated target identity is not durable", sw.ID)
		}
		return m.recoverActivatedChatAgentSwitch(ctx, store, rec, sw, execution)
	default:
		if sw.State.Terminal() {
			return true, nil
		}
		return false, fmt.Errorf("reconcile Chat agent switch %s: unknown state %q", sw.ID, sw.State)
	}
}

// recoverActivatedChatAgentSwitch reopens the exact provider conversation with
// its verified finalized continuation before the switch gate can be released.
// target_ready is safe to deliver because no turn was attempted yet. A
// delivering row is ambiguous, so it is never replayed; restoring its hidden
// context is enough to leave the durable target usable before terminalization.
func (m *Manager) recoverActivatedChatAgentSwitch(
	ctx context.Context,
	store ports.AgentSwitchStore,
	rec domain.SessionRecord,
	sw domain.AgentSwitch,
	execution domain.AgentSwitchExecution,
) (bool, error) {
	recorder := newAgentSwitchFlightRecorder(sw, domain.SessionModeChat, execution)
	if m.chat == nil {
		return false, fmt.Errorf("reconcile Chat agent switch %s: %w", sw.ID, ports.ErrChatUnsupported)
	}
	recorder.boundary(domain.AgentSwitchFailureRecoveryArtifactVerify)
	if _, valid := m.readVerifiedFinalizedHandoff(ctx, sw); !valid {
		return false, fmt.Errorf("reconcile Chat agent switch %s: finalized handoff failed verification", sw.ID)
	}
	if !m.chat.HasLiveChatController(rec.ID) {
		recorder.boundary(domain.AgentSwitchFailureRecoveryActivation)
		project, err := m.loadProject(ctx, rec.ProjectID)
		if err != nil {
			return false, fmt.Errorf("reconcile Chat agent switch %s: target project: %w", sw.ID, err)
		}
		if _, err := m.resumeChatController(
			ctx, "recover Chat agent switch", rec, project, workspaceInfo(rec), false, execution == domain.AgentSwitchExecutionStartupReconcile,
			string(sw.TargetGenerationID), domain.SessionInterfaceTransitionHistoryStrict,
		); err != nil {
			return false, err
		}
	}

	current, err := requireAgentSwitch(ctx, store, sw.ID)
	if err != nil {
		return false, err
	}
	if current.State == domain.AgentSwitchDelivering {
		if current.TargetAcknowledgedAt != nil {
			if _, err := m.completeAcknowledgedAgentSwitch(ctx, store, current); err != nil {
				return false, err
			}
			return true, nil
		}
		recorder.boundary(domain.AgentSwitchFailureRecoverySettlement)
		recorder.userImpact = domain.AgentSwitchUserImpactDeliveryUnknown
		_, err := m.failAgentSwitchWithRecorder(ctx, store, current, domain.AgentSwitchErrorDeliveryUnconfirmed, recorder)
		return err == nil, err
	}
	if current.State != domain.AgentSwitchTargetReady {
		return false, fmt.Errorf("reconcile Chat agent switch %s: target state changed to %q", sw.ID, current.State)
	}
	recorder.boundary(domain.AgentSwitchFailureDeliveryOpenCommit)
	if err := m.advanceAgentSwitch(ctx, store, &current, domain.AgentSwitchDelivering, nil); err != nil {
		return false, err
	}
	recorder.boundary(domain.AgentSwitchFailureChatContinuationRelay)
	// The human message a composer switch carried lived only in the request
	// AO was serving when it restarted, so it may never arrive.
	if _, err := m.chat.RelaySessionChatTurn(
		ctx, rec.ID, aoTargetRecoveryPrompt, chatSwitchActivationMessageID(current.ID), ports.MessageDeliveryOptions{}); err != nil {
		recorder.userImpact = domain.AgentSwitchUserImpactDeliveryUnknown
		_, failErr := m.failAgentSwitchWithRecorder(ctx, store, current, domain.AgentSwitchErrorDeliveryUnconfirmed, recorder)
		return failErr == nil, failErr
	}
	recorder.boundary(domain.AgentSwitchFailureChatTargetAckCommit)
	current, acknowledged, err := m.acknowledgeAgentSwitchTargetWithReadback(
		ctx, store, current, current.TargetGenerationID, m.clock())
	if err != nil {
		return false, err
	}
	if !acknowledged {
		return current.State.Terminal(), nil
	}
	if _, err := m.completeAcknowledgedAgentSwitch(ctx, store, current); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) chatTargetNativeIdentityRecoverable(
	ctx context.Context,
	store ports.AgentSwitchStore,
	rec domain.SessionRecord,
	sw domain.AgentSwitch,
) (bool, error) {
	if rec.Harness != sw.TargetHarness ||
		strings.TrimSpace(rec.Metadata.ControllerGeneration) != string(sw.TargetGenerationID) {
		return false, nil
	}
	recoverable, err := m.targetNativeIdentityRecoverable(ctx, store, rec, sw)
	if err != nil || !recoverable || sw.TargetNativeSessionRef == nil {
		return recoverable, err
	}
	native, found, err := store.GetAgentNativeSession(ctx, *sw.TargetNativeSessionRef)
	if err != nil {
		return false, err
	}
	return found && strings.TrimSpace(rec.Metadata.ProviderConversationID) != "" &&
		rec.Metadata.ProviderConversationID == native.NativeSessionID, nil
}

func (m *Manager) resolveChatTargetActivationOutcome(
	ctx context.Context,
	store ports.AgentSwitchStore,
	source domain.SessionRecord,
	activation domain.AgentSwitchChatTargetActivation,
) (domain.AgentSwitch, bool, bool, error) {
	current, found, err := store.GetAgentSwitch(ctx, activation.SwitchID)
	if err != nil {
		return domain.AgentSwitch{}, false, false, err
	}
	if !found {
		return domain.AgentSwitch{}, false, false, errors.New("chat agent switch disappeared while resolving target activation")
	}
	session, found, err := m.store.GetSession(ctx, activation.SessionID)
	if err != nil {
		return current, false, false, err
	}
	if !found {
		return current, false, false, ErrNotFound
	}
	targetRefMatches := current.TargetNativeSessionRef != nil &&
		*current.TargetNativeSessionRef == activation.TargetNativeSessionRef
	committed := current.State == domain.AgentSwitchTargetReady &&
		current.SourceGenerationID == activation.SourceGenerationID &&
		current.TargetGenerationID == activation.TargetGenerationID && targetRefMatches &&
		domain.NormalizeSessionMode(session.Mode) == domain.SessionModeChat &&
		session.Harness == activation.TargetHarness &&
		session.Activity.State == domain.ActivityIdle &&
		session.Metadata.ProviderConversationID == activation.ProviderConversationID &&
		session.Metadata.ControllerGeneration == activation.ControllerGeneration
	if committed {
		targetNative, nativeFound, nativeErr := store.GetAgentNativeSession(ctx, activation.TargetNativeSessionRef)
		if nativeErr != nil {
			return current, false, false, nativeErr
		}
		if !nativeFound || targetNative.AOSessionID != activation.SessionID ||
			targetNative.Harness != activation.TargetHarness ||
			targetNative.LastGenerationID != activation.TargetGenerationID ||
			targetNative.NativeSessionID != activation.ProviderConversationID ||
			session.Metadata.AgentSessionID != targetNative.NativeSessionID {
			return current, false, false, nil
		}
		conversationStore, ok := m.store.(interface {
			ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error)
			ConversationBranch(context.Context, string, string) (domain.ConversationBranch, error)
		})
		if !ok {
			return current, false, false, errors.New("chat activation outcome requires conversation store")
		}
		conversation, conversationErr := conversationStore.ConversationForSession(ctx, activation.SessionID)
		if conversationErr != nil {
			return current, false, false, conversationErr
		}
		boundaryID := chatSwitchProviderBoundaryID(activation.SwitchID)
		boundary, boundaryErr := conversationStore.ConversationBranch(ctx, conversation.ID, boundaryID)
		if boundaryErr != nil {
			return current, false, false, boundaryErr
		}
		committed = conversation.ActiveBranchID == boundaryID && boundary.Active &&
			boundary.SessionID == activation.SessionID &&
			boundary.ProviderConversationID == activation.ProviderConversationID &&
			boundary.ProviderScopeID == boundaryID
	}
	if committed {
		return current, true, false, nil
	}
	sourceStillOwns := current.State == domain.AgentSwitchStartingTarget &&
		current.SourceGenerationID == activation.SourceGenerationID &&
		current.TargetGenerationID == activation.TargetGenerationID && targetRefMatches &&
		domain.NormalizeSessionMode(session.Mode) == domain.SessionModeChat &&
		session.Harness == activation.SourceHarness &&
		session.Activity.State == domain.ActivityExited &&
		session.Metadata.ProviderConversationID == source.Metadata.ProviderConversationID &&
		session.Metadata.ControllerGeneration == activation.ExpectedSourceControllerGeneration
	return current, false, sourceStillOwns, nil
}
