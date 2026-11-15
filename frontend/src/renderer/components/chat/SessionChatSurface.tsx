import { apiErrorException } from "../../lib/api-error-exception";
import { useSessionAccountSwitch } from "../SessionAccountSummary";
import type { OpenFileAnnotation } from "../../../shared/chat-file-reference";
import { ForkSessionContext } from "../SessionForkControls";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { agentLabel } from "../../lib/agent-options";
import { useImportedAgentBlock } from "../../lib/session-import";
import { useIndependentSideChats } from "./IndependentSideChats";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import { useSideChatsAvailable } from "../../lib/side-chats";

import { AlertTriangle, Loader2 } from "@/components/app-icons";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useChatPermissionModes } from "../../hooks/useChatPermissionModes";
import { useChatMessageFocus, useChatNavigationRequest } from "../../hooks/useChatMessageFocus";
import { useChatModelChoices } from "../../hooks/useChatModelChoices";
import { apiClient, apiErrorCode, apiErrorMessage } from "../../lib/api-client";
import { forgetLaunchSettings, forgetOpeningStart, LaunchSettingsBar, launchSettingsFor, openingStartFor } from "./LaunchSettingsBar";
import { rememberChatSettingsTemplate, rememberChatSkills } from "../../lib/chat-settings-template";
import {
	clearConversationProviderCatalogs,
	conversationQueryKey,
	invalidateConversationProviderCatalogs,
	ProviderSwitchFailed,
	useConversation,
	useConversationCommands,
	useConversationConfigOptions,
	useConversationModels,
	useConversationSkills,
	useProviderSwitchCommand,
	useStageAttachments,
	useWorkspaceFilePaths,
} from "../../hooks/useConversation";
import { useConversationPromptSuggestion } from "../../hooks/useConversationPromptSuggestion";
import { useRememberProjectPermissions } from "../../hooks/useRememberProjectPermissions";
import { useLatestCallback } from "../../hooks/useStable";
import { useSessionBrowserLink } from "../../hooks/useSessionBrowserLink";
import { subscribeSessionSearchReveal, takeSessionSearchReveal, type SessionSearchReveal } from "../../lib/session-search-reveal";
import { sessionUiKey } from "../../lib/hosts";
import { readChatLastModel, rememberChatLastModel } from "../../lib/chat-last-model";
import { clientForSessionHost } from "../../lib/host-clients";
import type { ShellTerminal } from "../../hooks/useShellTerminals";
import { useSessionLinkNavigation } from "../../lib/use-session-link-navigation";
import type { Theme } from "../../stores/ui-store";
import { can } from "../../types/conversation";
import type { ConversationSnapshot, TurnSettings } from "../../types/conversation";
import type { TerminalTarget } from "../../types/terminal";
import { STANDALONE_WORKSPACE_ID, sessionIsActive, type WorkspaceSession } from "../../types/workspace";
import { ComposerErrorNotice, switchEffort, type PendingAgentSwitch } from "./AgentSwitchPicker";
import { AGENT_SWITCH_FAILED } from "./ChatComposer";
import { useUiStore } from "../../stores/ui-store";
import { CONTINUE_STOPPED_TURN_PROMPT, ChatWorkspace, SENT_OUTSIDE_TRANSCRIPT, continuableTurnId, type ChatFindRequest } from "./ChatWorkspace";
import { startingConversationSnapshot } from "./OrchestratorStartingChat";
import { hasProviderPermissionMode, withUltracode } from "./TurnSettingsBar";

export interface ConversationWorkState {
	controllerBusy: boolean;
	hasRunningTurn: boolean;
	queuedTurnCount: number;
}

export const SessionChatSurface = memo(function SessionChatSurface({
	sideChatContainer,
	subagentContainer,
	onRevealSubagents,
	session,
	hostId,
	assetBaseUrl,
	reviewerTerminal,
	onOpenReviewerTerminal,
	reviewerChat,
	onOpenReviewerChat,
	reviewerChatSelected,
	onSessionRenamed,
	reviewerTarget,
	onSelectChat,
	shellTerminals,
	shellTarget,
	onSelectShellTerminal,
	onCloseShellTerminal,
	onRenameShellTerminal,
	daemonReady,
	theme,
	onOpenShell,
	openingShell,
	shellError,
	onOpenFiles,
	onOpenFile,
	onOpenArtifact,
	onOpenFileAnnotation,
	onOpenLinkInBrowser,
	headerActions,
	sessionTabAction,
	sessionTabActionPinned = false,
	tabStripAction,
	workspaceTabs,
	workspaceTabActions,
	workspaceActiveTabKey,
	workspaceFileActive,
	auxiliaryTabOrder,
	onAuxiliaryTabOrderChange,
	controllerTransitioning,
	accountMoving,
	accountMoveQueues,
	agentResuming,
	controllerResumeError,
	controllerResumeOpenElsewhere,
	newWorkDisabled,
	arriving,
	onConversationWorkChange,
	interfaceSwitch,
}: {
	interfaceSwitch?: { control: ReactNode; errorNotice?: ReactNode };
	sideChatContainer?: HTMLDivElement | null;
	subagentContainer?: HTMLDivElement | null;
	onRevealSubagents?: () => void;
	session: WorkspaceSession;
	hostId?: string;
	assetBaseUrl?: string;
	reviewerTerminal?: { handleId: string; harness: string };
	onOpenReviewerTerminal?: (target: { handleId: string; harness: string }) => void;
	reviewerChat?: { reviewId: string; harness: string };
	onOpenReviewerChat?: (target: { reviewId: string; harness: string }) => void;
	reviewerChatSelected?: boolean;
	onSessionRenamed?: () => void | Promise<void>;
	reviewerTarget?: Extract<TerminalTarget, { kind: "reviewer" }>;
	onSelectChat?: () => void;
	shellTerminals?: ShellTerminal[];
	shellTarget?: Extract<TerminalTarget, { kind: "shell" }>;
	onSelectShellTerminal?: (handleId: string) => void;
	onCloseShellTerminal?: (handleId: string) => void;
	onRenameShellTerminal?: (handleId: string, title: string) => void;
	daemonReady?: boolean;
	theme?: Theme;
	onOpenShell?: () => void;
	openingShell?: boolean;
	shellError?: string;
	onOpenFiles?: () => void;
	onOpenFile?: (path: string, line?: number) => void;
	onOpenArtifact?: (target: { path: string }) => void;
	onOpenFileAnnotation?: OpenFileAnnotation;
	onOpenLinkInBrowser?: (uri: string) => Promise<void>;
	headerActions?: ReactNode;
	sessionTabAction?: ReactNode;
	sessionTabActionPinned?: boolean;
	tabStripAction?: ReactNode;
	workspaceTabs?: Array<{ key: string; content: ReactNode; onSelect: () => void }>;
	workspaceTabActions?: ReactNode;
	workspaceActiveTabKey?: string;
	workspaceFileActive?: boolean;
	auxiliaryTabOrder?: string[];
	onAuxiliaryTabOrderChange?: (keys: string[]) => void;
	controllerTransitioning?: boolean;
	accountMoving?: boolean;
	accountMoveQueues?: boolean;
	agentResuming?: boolean;
	controllerResumeError?: string;
	controllerResumeOpenElsewhere?: boolean;
	newWorkDisabled?: boolean;
	arriving?: boolean;
	onConversationWorkChange?: (state: ConversationWorkState) => void;
}) {
	const { t } = useTranslation();
	const uiSessionId = sessionUiKey(session.id, hostId);
	const hostUnreachable = Boolean(hostId) && !daemonReady;
	const {
		snapshot: queriedSnapshot,
		isLoading,
		unavailable,
		error,
		refreshError,
		hasOlder,
		isLoadingOlder,
		loadOlder,
	} = useConversation(session.id, hostId);
	const snapshot = queriedSnapshot?.sessionId === session.id ? queriedSnapshot : undefined;
	const commands = useConversationCommands(session.id, hostId, !sessionIsActive(session));
	const importBlock = useImportedAgentBlock(session, hostId ?? session.hostId).block;
	const queryClient = useQueryClient();
	const plansSetup = Boolean(session.provisionSteps?.some((step) => step.id === "setup"));
	const projectScripts = useQuery({
		queryKey: ["project", session.workspaceId],
		enabled: plansSetup && !hostId && !session.cloud,
		staleTime: 30_000,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}", { params: { path: { id: session.workspaceId } } });
			if (error) throw apiErrorException(error);
			return data?.status === "ok" ? data.project : undefined;
		},
	});
	const projectConfig = projectScripts.data && "config" in projectScripts.data ? projectScripts.data.config : undefined;
	const showSetupStep = Boolean(hostId || session.cloud) || !projectScripts.isSuccess ||
		(projectConfig?.postCreate ?? []).some((command: string) => command.trim() !== "");
	const launchSettings = launchSettingsFor(queryClient, session.id, hostId);
	const openingStartedAt = openingStartFor(queryClient, session.id, hostId);
	const projectPermissions = useRememberProjectPermissions(session.workspaceId, snapshot?.harness, hostId, session.kind);
	const {
		acknowledgeAcceptedTurn,
		acknowledgeLocalEcho,
		localEchos = [],
		pendingAcceptedTurnId,
	} = commands;
	const conversationWorkKnown = Boolean(snapshot);
	const [sideWork, setSideWork] = useState(false);
	const acceptedLocalTurnObserved = Boolean(
		pendingAcceptedTurnId && snapshot?.turns.some((turn) => turn.id === pendingAcceptedTurnId),
	);
	const acceptedLocalWorkPending = Boolean(pendingAcceptedTurnId && !acceptedLocalTurnObserved);
	const controllerBusy =
		snapshot?.controller?.state === "busy" || commands.busy || acceptedLocalWorkPending;
	const hasRunningTurn = Boolean(snapshot?.turns.some((turn) => turn.state === "running"));
	const queuedTurnCount = snapshot?.turns.filter((turn) => turn.state === "queued").length ?? 0;
	const providerSwitch = useProviderSwitchCommand(session.id, hostId);
	const [chosenSwitch, setChosenSwitch] = useState<PendingAgentSwitch>();
	const chatInFront = !workspaceFileActive && !workspaceActiveTabKey && !reviewerTarget && !reviewerChatSelected && !shellTarget;
	useEffect(() => {
		if (!chatInFront) setChosenSwitch(undefined);
	}, [chatInFront]);
	const statusReadiness = session.statusReadiness;
	const lastReadiness = useRef({ id: session.id, readiness: statusReadiness });
	useEffect(() => {
		const previous = lastReadiness.current;
		lastReadiness.current = { id: session.id, readiness: statusReadiness };
		if (hostId || previous.id !== session.id || statusReadiness !== "ready" ||
			!previous.readiness || previous.readiness === "ready") return;
		void queryClient.invalidateQueries({ queryKey: conversationQueryKey(session.id) });
	}, [hostId, queryClient, session.id, statusReadiness]);
	const [sendSwitchError, setSendSwitchError] = useState<unknown>();
	const switchError = providerSwitch.error ?? sendSwitchError;
	const clearSwitchError = () => { setSendSwitchError(undefined); providerSwitch.reset(); };
	const currentHarness = snapshot?.harness;
	const pendingSwitch = currentHarness && chosenSwitch?.harness !== currentHarness ? chosenSwitch : undefined;
	useEffect(() => {
		if (!currentHarness) return;
		const key = ["conversation-catalog-harness", hostId ?? "", session.id];
		const seen = queryClient.getQueryData<string>(key);
		queryClient.setQueryData(key, currentHarness);
		if (seen && seen !== currentHarness) {
			clearConversationProviderCatalogs(queryClient, session.id, hostId);
			invalidateConversationProviderCatalogs(queryClient, session.id, hostId);
		}
	}, [currentHarness, hostId, queryClient, session.id]);
	useEffect(() => {
		if (chosenSwitch && chosenSwitch.harness === currentHarness) setChosenSwitch(undefined);
	}, [chosenSwitch, currentHarness]);
	const pendingHarness = pendingSwitch?.harness;
	useEffect(() => {
		if (!pendingHarness) return;
		void clientForSessionHost(hostId)
			.POST("/api/v1/agents/{agent}/chat/prewarm", { params: { path: { agent: pendingHarness } } })
			.catch(() => undefined);
	}, [hostId, pendingHarness]);
	const [ultracodeOn, setUltracodeOn] = useState(false);
	const ultracode = useMemo(() => ({ on: ultracodeOn, onChange: setUltracodeOn }), [ultracodeOn]);
	const autoCompactWindow = snapshot?.settings.autoCompactWindow;
	const [applyingAutoCompact, setApplyingAutoCompact] = useState(false);
	const saveAutoCompact = useLatestCallback((value: TurnSettings["autoCompactWindow"]) => {
		if (!snapshot) return;
		setApplyingAutoCompact(true);
		void commands.chooseStartSettings({ ...snapshot.settings, autoCompactWindow: value })
			.catch((error) => useUiStore.getState().showGlobalToast("Auto-compact change failed", apiErrorMessage(error), "error"))
			.finally(() => setApplyingAutoCompact(false));
	});
	const agentBusy = controllerBusy || hasRunningTurn || queuedTurnCount > 0;
	const autoCompact = useMemo(
		() => ({ value: autoCompactWindow, disabled: applyingAutoCompact || agentBusy, onChange: saveAutoCompact }),
		[agentBusy, applyingAutoCompact, autoCompactWindow, saveAutoCompact],
	);
	const switchBlocked = agentBusy || providerSwitch.isPending;
	const choosePendingSwitch = useCallback((next: PendingAgentSwitch | undefined) => {
		setChosenSwitch((current) => next && current?.approvalMode && current.harness === next.harness && !next.approvalMode
			? { ...next, approvalMode: current.approvalMode }
			: next);
	}, []);
	const chatPermissionMode = useMemo(() => {
		const marker = snapshot?.items.findLast((item) => item.kind === "activity" && item.detail?.event === "provider_switch");
		const detail = marker?.kind === "activity" ? marker.detail : undefined;
		return detail?.targetHarness === currentHarness && detail?.permissionMode === snapshot?.settings.approvalMode
			? detail?.chatPermissionMode
			: undefined;
	}, [currentHarness, snapshot?.items, snapshot?.settings.approvalMode]);
	const permissionModes = useChatPermissionModes(hostId, pendingSwitch?.harness);
	const pendingPermissionModes = pendingSwitch ? permissionModes?.get(pendingSwitch.harness) : undefined;
	const accountSwitch = useSessionAccountSwitch(session, hostId ?? session.hostId);
	const account = accountSwitch.choice;
	const agentSwitch = useMemo(
		() => currentHarness && !session.cloud && !newWorkDisabled
			? { projectId: session.workspaceId, hostId, pending: pendingSwitch, pendingPermissionModes, chatPermissionMode, onPendingChange: choosePendingSwitch, disabled: switchBlocked, ultracode, autoCompact, account }
			: undefined,
		[account, autoCompact, chatPermissionMode, choosePendingSwitch, currentHarness, hostId, newWorkDisabled, pendingPermissionModes, pendingSwitch, session.cloud, session.workspaceId, switchBlocked, ultracode],
	);
	useEffect(() => {
		if (acceptedLocalTurnObserved && pendingAcceptedTurnId) {
			acknowledgeAcceptedTurn(pendingAcceptedTurnId);
		}
	}, [acceptedLocalTurnObserved, acknowledgeAcceptedTurn, pendingAcceptedTurnId]);
	useEffect(() => {
		if (!snapshot) return;
		const durableHumanTurnIds = new Set(
			snapshot.items.flatMap((item) =>
				item.kind === "message" && item.role === "user" && item.origin === "human" && item.turnId
					? [item.turnId]
					: [],
			),
		);
		const queuedTurnIds = new Set(snapshot.turns.filter((turn) => turn.state === "queued").map((turn) => turn.id));
		for (const echo of localEchos) {
			if (echo.opening && durableHumanTurnIds.size > 0) acknowledgeLocalEcho?.(undefined, echo.clientMessageId);
			else if (
				echo.turnId && durableHumanTurnIds.has(echo.turnId) &&
				!(echo.backgroundWake && queuedTurnIds.has(echo.turnId))
			) acknowledgeLocalEcho?.(echo.turnId);
		}
	}, [acknowledgeLocalEcho, localEchos, snapshot]);
	useEffect(() => {
		if (!conversationWorkKnown) return;
		onConversationWorkChange?.({ controllerBusy: controllerBusy || sideWork, hasRunningTurn: hasRunningTurn || sideWork, queuedTurnCount });
	}, [controllerBusy, conversationWorkKnown, hasRunningTurn, onConversationWorkChange, queuedTurnCount, sideWork]);
	const targetChatControllerReady =
		snapshot?.harness === session.provider &&
		(snapshot.controller?.state === "ready" || snapshot.controller?.state === "busy");
	const waking = snapshot?.harness === session.provider &&
		(snapshot.controller?.state === "hibernated" || snapshot.controller?.state === "connecting" || snapshot.controller?.state === "recovering");
	const controllerCatalogsEnabled = !hostUnreachable && targetChatControllerReady && !controllerTransitioning && !newWorkDisabled;
	const configOptions = useConversationConfigOptions(
		session.id,
		Boolean(controllerCatalogsEnabled && snapshot && can(snapshot, "config_options")),
		hostId,
	);
	const providerOptions = configOptions.options ?? [];
	const templateHarness = snapshot?.harness;
	useEffect(() => {
		if (session.kind !== "worker" || !templateHarness || !configOptions.loaded || providerOptions.length === 0) return;
		rememberChatSettingsTemplate(templateHarness, hostId, { options: providerOptions });
	}, [configOptions.loaded, hostId, providerOptions, session.kind, templateHarness]);
	const hasProviderMode = hasProviderPermissionMode(providerOptions);
	const hasProviderModel = providerOptions.some(
		(option) => option.category === "model" || option.id === "model",
	);
	const { models: controllerModels, loaded: controllerModelsLoaded } = useConversationModels(
		session.id,
		Boolean(controllerCatalogsEnabled && snapshot) && !hasProviderModel,
		hostId,
	);
	const { models, configOptions: offeredConfigOptions } = useChatModelChoices(snapshot?.harness, controllerModels, configOptions.options, snapshot?.settings?.model, hostId, controllerModelsLoaded);
	const { skills } = useConversationSkills(
		session.id,
		Boolean(controllerCatalogsEnabled && snapshot),
		hostId,
	);
	const skillsHarness = snapshot?.harness;
	useEffect(() => {
		if (session.kind !== "worker" || !skillsHarness || skills.length === 0) return;
		rememberChatSkills(skillsHarness, hostId, skills);
	}, [hostId, session.kind, skills, skillsHarness]);
	const { paths, truncated } = useWorkspaceFilePaths(session.id, Boolean(snapshot) && !session.needsResume, hostId);
	const stageAttachments = useStageAttachments(session.id, hostId);
	const annotationNavigation = useChatNavigationRequest<{ id: string; text: string; snapshot?: import("../../../shared/chat-excerpt").ChatExcerptSnapshot; messageId?: string; revision?: number; activitySequence?: number }>(uiSessionId);
	const navigateToAnnotation = annotationNavigation.navigate;
	useChatMessageFocus(uiSessionId, undefined, Boolean(snapshot), navigateToAnnotation);
	const findNavigation = useChatNavigationRequest<ChatFindRequest>(uiSessionId);
	const openFind = findNavigation.navigate;
	const [heldReveal, setHeldReveal] = useState<{ owner: string; reveal: SessionSearchReveal }>();
	useEffect(() => {
		const revealPending = () => {
			const reveal = takeSessionSearchReveal(session.id);
			if (!reveal) return;
			onSelectChat?.();
			setHeldReveal({ owner: session.id, reveal });
		};
		revealPending();
		return subscribeSessionSearchReveal(session.id, revealPending);
	}, [onSelectChat, session.id]);
	const sideEnabled = useSideChatsAvailable(session, hostId) && !unavailable;
	const sideCreationEnabled = sideEnabled && Boolean(snapshot && can(snapshot, "resume"));
	const openSideInspector = () => {
		const ui = useUiStore.getState();
		ui.setInspectorView(uiSessionId, "sideChat");
		ui.setInspectorOpen(uiSessionId, true);
	};
	const sideChats = useIndependentSideChats(
		session.id, models, skills, stageAttachments, Boolean(snapshot && can(snapshot, "images")), sideEnabled,
		(annotation) => {
			onSelectChat?.();
			navigateToAnnotation({ ...annotation, id: crypto.randomUUID() });
		},
		() => useUiStore.getState().setInspectorOpen(uiSessionId, false),
		sideCreationEnabled,
	);
	useEffect(() => { if (sideChatContainer) sideChats.show(); }, [sideChatContainer]);
	useEffect(() => setSideWork(sideChats.sides.some((side) => side.hasWork)), [sideChats.sides]);
	const reportSubagents = useCallback(
		(available: boolean) => useUiStore.getState().setSubagentsAvailable(uiSessionId, available),
		[uiSessionId],
	);
	const subagentPanel = useMemo(
		() => subagentContainer === undefined || !onRevealSubagents
			? undefined
			: { container: subagentContainer, reveal: onRevealSubagents, onAvailableChange: reportSubagents, models },
		[models, onRevealSubagents, reportSubagents, subagentContainer],
	);
	const selectionSideScope = useMemo(() => ({ active: true,
		processing: new Map<string, Promise<void>>(),
		receipts: new Map<string, { creationKey: string; excerpt: ChatDraftExcerptReference }>(),
	}), [uiSessionId, snapshot?.conversationId]);
	const selectionSideScopeRef = useRef(selectionSideScope);
	selectionSideScopeRef.current = selectionSideScope;
	const selectionSideChatsRef = useRef(sideChats);
	selectionSideChatsRef.current = sideChats;
	useEffect(() => {
		selectionSideScope.active = true;
		return () => {
			selectionSideScope.active = false;
			selectionSideScope.processing.clear(); selectionSideScope.receipts.clear();
		};
	}, [selectionSideScope]);
	const attachToSide = async (excerpt: ChatDraftExcerptReference) => {
		const current = () => selectionSideScope.active && selectionSideScopeRef.current === selectionSideScope;
		if (!current()) throw new Error("Side chat is no longer active.");
		const identity = JSON.stringify([uiSessionId, excerpt.conversationId, excerpt.messageId, excerpt.revision, excerpt.text, excerpt.snapshot?.sourceRange, excerpt.snapshot?.renderedRange]);
		const pending = selectionSideScope.processing.get(identity);
		if (pending) return pending;
		const destination = selectionSideChatsRef.current;
		const destinationId = destination.activeId;
		const action = Promise.resolve().then(async () => {
			if (!current()) throw new Error("Side chat is no longer active.");
			openSideInspector();
			if (destinationId) {
				if (!selectionSideChatsRef.current.sides.some(side => side.id === destinationId)) throw new Error("Side chat is no longer active.");
				await destination.addReference(destinationId, excerpt);
				destination.show(destinationId);
			} else {
				let receipt = selectionSideScope.receipts.get(identity);
				if (!receipt) {
					receipt = { creationKey: crypto.randomUUID(), excerpt: { ...excerpt } };
					selectionSideScope.receipts.set(identity, receipt);
				}
				try {
					await destination.create(receipt.excerpt, false, receipt.creationKey, {
						isCurrent: current,
						shouldActivate: () => current() && selectionSideChatsRef.current.activeId === destinationId,
					});
				} catch (error) {
					if (apiErrorCode(error) === "CHAT_SIDE_CLOSED") selectionSideScope.receipts.delete(identity);
					throw error;
				}
				if (!current()) throw new Error("Side chat is no longer active.");
				selectionSideScope.receipts.delete(identity);
			}
		});
		selectionSideScope.processing.set(identity, action);
		try {
			await action;
		} finally {
			selectionSideScope.processing.delete(identity);
		}
	};

	const openLinkInBrowser = useSessionBrowserLink(session, onOpenLinkInBrowser, paths, onOpenFile);
	const openSessionLink = useSessionLinkNavigation(hostId);
	const promptSuggestion = useConversationPromptSuggestion(snapshot, Boolean(
		controllerCatalogsEnabled && !controllerBusy && !localEchos.length &&
		!commands.rollbackPending && !commands.editMessagePending && !commands.activateBranchPending && !commands.compacting &&
		!session.isTerminated && session.provisionState !== "failed" && session.provisionState !== "provisioning" &&
		!isLoading && !error && !refreshError && !unavailable &&
		!controllerTransitioning && !agentResuming && !newWorkDisabled && !arriving &&
		chatInFront,
	), hostId);
	const renderShellFallback = Boolean(shellTarget && session);
	const optimisticArrival = Boolean(arriving) && !renderShellFallback;
	const openingSend = localEchos.some((echo) => echo.opening);
	const optimisticChat = ((session.kind === "orchestrator" || openingSend) && isLoading && !renderShellFallback) || optimisticArrival;
	const continueIntent = useRef<{ turnId: string; clientMessageId: string; sequence: number } | null>(null);
	const renderSnapshot =
		(optimisticArrival
			? { ...startingConversationSnapshot(session.id, session.provider), controller: { state: "stopped" as const } }
			: snapshot) ??
		(optimisticChat ? startingConversationSnapshot(session.id, session.provider) : undefined) ??
		(renderShellFallback
			? unavailableConversationSnapshot(session)
			: undefined);
	const conversationShown = Boolean(snapshot) && renderSnapshot === snapshot;
	const appliedReveal = useRef<SessionSearchReveal | undefined>(undefined);
	useEffect(() => {
		if (!heldReveal || !conversationShown) return;
		const { owner, reveal } = heldReveal;
		setHeldReveal(undefined);
		if (owner !== session.id || appliedReveal.current === reveal) return;
		appliedReveal.current = reveal;
		navigateToAnnotation({ id: crypto.randomUUID(), text: reveal.text, messageId: reveal.messageId, activitySequence: reveal.activitySequence });
		const word = reveal.text.trim();
		if (word && reveal.find) openFind({ id: crypto.randomUUID(), query: word, messageId: reveal.messageId });
	}, [conversationShown, heldReveal, navigateToAnnotation, openFind, session.id]);
	const settingsLoading = waking || Boolean(renderSnapshot && can(renderSnapshot, "config_options") && !configOptions.loaded && !configOptions.error);
	const liveModel = settingsLoading || !targetChatControllerReady || controllerTransitioning ? undefined
		: providerOptions.find((option) => option.category === "model" || option.id === "model")?.currentValue ?? snapshot?.settings?.model;
	useEffect(() => {
		if (liveModel && snapshot?.harness) rememberChatLastModel(uiSessionId, snapshot.harness, liveModel);
	}, [liveModel, snapshot?.harness, uiSessionId]);
	const lastModel = useMemo(
		() => settingsLoading ? readChatLastModel(uiSessionId, renderSnapshot?.harness) : undefined,
		[renderSnapshot?.harness, settingsLoading, uiSessionId],
	);
	const stoppedTurnId = renderSnapshot ? continuableTurnId(renderSnapshot) : "";
	useEffect(() => {
		if (!stoppedTurnId && renderSnapshot && continueIntent.current &&
			renderSnapshot.latestSequence > continueIntent.current.sequence) {
			continueIntent.current = null;
		}
	}, [stoppedTurnId, renderSnapshot?.latestSequence]);
	useEffect(() => {
		if (session.kind !== "orchestrator" || session.provisionState === "provisioning") return;
		if (isLoading && !renderShellFallback) return;
		if (session.provisionState !== "failed" && !targetChatControllerReady && !renderShellFallback && !error && !unavailable) return;
		useUiStore.getState().setProjectProvisioning(session.workspaceId, false, hostId);
	}, [error, hostId, isLoading, renderShellFallback, session.kind, session.provisionState, session.workspaceId, targetChatControllerReady, unavailable]);

	const settingsReady = Boolean(renderSnapshot) && !optimisticChat && (targetChatControllerReady || agentResuming || waking) &&
		(!settingsLoading || Boolean(renderSnapshot?.settings?.model || renderSnapshot?.settings?.approvalMode));
	useEffect(() => {
		if (settingsReady && !settingsLoading && launchSettings) forgetLaunchSettings(queryClient, session.id, hostId);
	}, [hostId, launchSettings, queryClient, session.id, settingsLoading, settingsReady]);
	const openingSettled = Boolean(snapshot?.turns[0] && snapshot.turns[0].state !== "queued" && snapshot.turns[0].state !== "running");
	useEffect(() => {
		if (openingStartedAt && openingSettled) forgetOpeningStart(queryClient, session.id, hostId);
	}, [hostId, openingSettled, openingStartedAt, queryClient, session.id]);

	if (isLoading && !renderShellFallback && !optimisticChat) {
		return (
			<Centered>
				<Loader2 aria-hidden="true" className="size-4 animate-spin text-muted-foreground" />
				<span className="text-xs text-muted-foreground">Loading conversation…</span>
			</Centered>
		);
	}

	if (unavailable && !renderShellFallback && !optimisticArrival) {
		return (
			<Centered>
				<AlertTriangle aria-hidden="true" className="size-4 text-warning" />
				<strong className="text-xs text-foreground">Conversation unavailable</strong>
				<p className="max-w-sm text-center text-xs leading-relaxed text-muted-foreground">
					{unavailable.message}
				</p>
				<p className="max-w-sm text-center text-xs leading-relaxed text-muted-foreground">
					The worktree is untouched. Open a shell from the inspector to work in it directly.
				</p>
				{interfaceSwitch?.control}
			</Centered>
		);
	}

	if ((error && !optimisticArrival) || !renderSnapshot) {
		return (
			<Centered>
				<AlertTriangle aria-hidden="true" className="size-4 text-destructive" />
				<p className="max-w-sm text-center text-xs leading-relaxed text-muted-foreground">
					{hostUnreachable && error ? t("remote.hostOffline") : error ?? "Could not load this conversation."}
				</p>
				{interfaceSwitch?.control}
			</Centered>
		);
	}

	return (
		<div className="relative h-full min-h-0">
			{refreshError && !hostUnreachable ? <p role="alert" className="px-4 py-2 text-xs text-destructive">{refreshError}</p> : null}
			{sideChatContainer ? createPortal(sideChats.panel, sideChatContainer) : null}
			{accountSwitch.dialog}
			<ForkSessionContext.Provider value={session}>
			<ChatWorkspace
				showSetupStep={showSetupStep}
				onAddToSideChat={sideEnabled && (sideChats.activeId || sideCreationEnabled) ? attachToSide : undefined}
				subagentPanel={subagentPanel}
				checkingSideChatAvailability={sideEnabled && !sideChats.activeId && (!snapshot || snapshot.capabilities === undefined)}
				annotationNavigationRequest={annotationNavigation.request}
				onAnnotationNavigationHandled={annotationNavigation.handled}
				findRequest={findNavigation.request}
				onFindRequestHandled={findNavigation.handled}
				key={uiSessionId}
				uiSessionId={uiSessionId}
				assetBaseUrl={assetBaseUrl}
				remoteHostId={hostId}
				snapshot={renderSnapshot}
				promptSuggestion={promptSuggestion}
				newWorkDisabled={newWorkDisabled || hostUnreachable}
				composerLockedPlaceholder={hostUnreachable && newWorkDisabled ? t("remote.offlineCopy") : undefined}
				sendBlockedReason={hostUnreachable && !newWorkDisabled ? t("remote.sendWhenConnected") : undefined}
				savedCopy={hostUnreachable}
				importBlock={importBlock}
				onLinkOpen={openLinkInBrowser}
				onSessionLinkOpen={openSessionLink}
				sessionTitle={session.title}
				sessionRole={session.kind}
				session={session}
				onSessionRenamed={onSessionRenamed}
				reviewerTerminal={reviewerTerminal}
				onOpenReviewerTerminal={onOpenReviewerTerminal}
				reviewerChat={reviewerChat}
				onOpenReviewerChat={onOpenReviewerChat}
				reviewerChatSelected={reviewerChatSelected}
				reviewerTarget={reviewerTarget}
				onSelectChat={onSelectChat}
				shellTerminals={shellTerminals}
				shellTarget={shellTarget}
				onSelectShellTerminal={onSelectShellTerminal}
				onCloseShellTerminal={onCloseShellTerminal}
				onRenameShellTerminal={onRenameShellTerminal}
				daemonReady={daemonReady}
				theme={theme}
				headerActions={headerActions}
				sessionTabAction={sessionTabAction}
				sessionTabActionPinned={sessionTabActionPinned}
				tabStripAction={tabStripAction}
				workspaceTabs={workspaceTabs}
				workspaceTabActions={workspaceTabActions}
				workspaceActiveTabKey={workspaceActiveTabKey}
				auxiliaryTabOrder={auxiliaryTabOrder}
				onAuxiliaryTabOrderChange={onAuxiliaryTabOrderChange}
				controllerTransitioning={controllerTransitioning}
				accountMoving={accountMoving}
				accountMoveQueues={accountMoveQueues}
				loadingQuietly={optimisticChat && !optimisticArrival && session.provisionState !== "provisioning"}
				historyUnread={!conversationShown && !renderShellFallback && session.provisionState !== "provisioning"}
				agentResuming={agentResuming}
				hasOlder={hasOlder}
				loadingOlder={isLoadingOlder}
				onLoadOlder={loadOlder}
				busy={commands.busy}
				excerptsEnabled
				fileReferencesEnabled={Boolean(renderSnapshot.supportsFileReferences)}
				onSend={async (text, attachments, clientMessageId, excerpts, fileReferences) => {
					const btw = sideCreationEnabled ? /^\/btw(?:\s+|$)/i.exec(text) : null;
     if (btw && fileReferences?.length) {
      throw Object.assign(new Error("File annotations cannot be sent to side chats yet. Send in the main chat or remove the file annotations."), { code: "CHAT_FILE_REFERENCES_SIDE_UNSUPPORTED" });
     }
     if (btw) {
						openSideInspector();
						const side = await sideChats.create(undefined, true, clientMessageId);
						const question = text.slice(btw[0].length).trim();
						if (question || excerpts?.length || attachments?.length) {
							await sideChats.send(side.id, question, attachments, excerpts, true, clientMessageId);
						}
						return SENT_OUTSIDE_TRANSCRIPT;
					}
					const viaClaude = (pendingSwitch?.harness ?? currentHarness) === "claude-code";
					const message = {
						text: viaClaude && (pendingSwitch ? pendingSwitch.ultracode : ultracode.on) ? withUltracode(text) : text,
						attachments,
						clientMessageId,
						fileReferences,
						excerpts: excerpts?.map((excerpt) => ({
							id: excerpt.id, order: excerpt.order, snapshot: excerpt.snapshot,
							conversationId: excerpt.conversationId,
							messageId: excerpt.messageId,
							revision: excerpt.revision,
							text: excerpt.text,
							comment: excerpt.comment,
						})),
					};
					clearSwitchError();
					if (!pendingSwitch) return commands.send(message);
					if (!pendingPermissionModes) {
						const reason = { message: t("agentSwitch.checkingPermissionModes", { agent: agentLabel(pendingSwitch.harness) }) };
						setSendSwitchError(reason);
						throw Object.assign(new Error(reason.message), { code: AGENT_SWITCH_FAILED });
					}
					const { harness: targetHarness, model, fastMode, autoCompactWindow } = pendingSwitch;
					const effort = switchEffort(pendingSwitch);
					const switchRequest = { targetHarness, model, effort, fastMode, autoCompactWindow, permissionMode: pendingSwitch.approvalMode };
					const switchDisplay = currentHarness
						? { sourceHarness: currentHarness, sourceModel: snapshot?.settings?.model, targetModel: model, targetEffort: effort }
						: undefined;
					const switchFailed = (error: unknown) => {
						if (error instanceof ProviderSwitchFailed) setChosenSwitch(undefined);
						void queryClient.invalidateQueries({ queryKey: conversationQueryKey(session.id, hostId) });
						return Object.assign(apiErrorException(error), { code: AGENT_SWITCH_FAILED });
					};
					const switched = () => {
						setChosenSwitch(undefined);
						ultracode.onChange(Boolean(pendingSwitch.ultracode));
					};
					try {
						const sent = await commands.send({ ...message, providerSwitch: switchRequest, switchDisplay });
						switched();
						return sent;
					} catch (error) {
						if (error instanceof ProviderSwitchFailed) {
							setSendSwitchError(error.reason);
							throw switchFailed(error);
						}
						void queryClient.invalidateQueries({ queryKey: conversationQueryKey(session.id, hostId) });
						throw error;
					}
				}}
				commandError={commands.error}
				agentSwitch={agentSwitch}
				composerNotice={interfaceSwitch?.errorNotice ?? (switchError ? (
					<ComposerErrorNotice
						title={t("agentSwitch.couldNotSwitch")}
						message={apiErrorMessage(switchError)}
						onDismiss={clearSwitchError}
					/>
				) : accountSwitch.error ? (
					<ComposerErrorNotice
						title={t("settings.agentAccounts.switchFailed")}
						message={accountSwitch.error}
						onDismiss={accountSwitch.clearError}
					/>
				) : undefined)}
				onDecide={commands.resolve}
				onResolveInput={commands.resolveInput}
				onInterrupt={commands.interrupt}
				onContinueTurn={() => {
					const turnId = `${renderSnapshot.conversationId}:${renderSnapshot.activeBranchId ?? ""}:${stoppedTurnId}`;
					if (continueIntent.current?.turnId !== turnId) {
						continueIntent.current = { turnId, clientMessageId: crypto.randomUUID(), sequence: renderSnapshot.latestSequence };
					}
					return commands.send({
						text: CONTINUE_STOPPED_TURN_PROMPT,
						continuation: true,
						clientMessageId: continueIntent.current.clientMessageId,
					});
				}}
				onResumeAgent={() => {
					void commands.resumeAgent().catch(() => {});
				}}
				resumingAgent={commands.resumingAgent || (renderSnapshot.controller.state === "hibernated" && controllerBusy)}
				resumeError={commands.resumeError ?? controllerResumeError}
				resumeOpenElsewhere={commands.resumeError ? commands.resumeOpenElsewhere : controllerResumeOpenElsewhere}
				onOpenShell={onOpenShell}
				openingShell={openingShell}
				shellError={shellError}
				settingsReady={settingsReady}
				settingsLoading={settingsLoading}
				lastModel={lastModel}
				openingStartedAt={openingStartedAt}
				draftSettings={launchSettings && (!settingsReady || settingsLoading) ? <LaunchSettingsBar {...launchSettings} /> : undefined}
				models={models}
				onChooseSettings={hasProviderMode ? undefined : commands.chooseSettings}
				onRememberPermissions={session.workspaceId === STANDALONE_WORKSPACE_ID || (can(renderSnapshot, "config_options") && !configOptions.loaded)
					? undefined : projectPermissions.remember}
				rememberPermissionsPending={projectPermissions.pending}
				rememberPermissionsError={projectPermissions.error}
				rememberedPermissionMode={projectPermissions.savedMode}
				configOptions={offeredConfigOptions}
				onChooseConfigOption={configOptions.setOption}
				configOptionPending={configOptions.pending || commands.choosingSettings}
				configOptionError={controllerCatalogsEnabled ? configOptions.error : undefined}
				onCompact={commands.compact}
				compacting={commands.compacting}
				compactUnavailable={commands.compactUnavailable}
				onRollback={commands.rollback}
				onPreviewRollback={commands.previewRollback}
				rollbackPending={commands.rollbackPending}
				rollbackError={commands.rollbackError}
				onOpenFiles={onOpenFiles}
				onOpenFile={onOpenFile}
				onOpenArtifact={onOpenArtifact}
				onOpenFileAnnotation={onOpenFileAnnotation}
				retryControl={commands.retryControl}
				onEditMessage={commands.editMessage}
				editMessagePending={commands.editMessagePending}
				editMessageError={commands.editMessageError}
				onActivateBranch={commands.activateBranch}
				activateBranchPending={commands.activateBranchPending}
				activateBranchError={commands.activateBranchError}
				skills={sideCreationEnabled ? [...(["btw"].map((name) => ({ name, displayName: name, description: "Open a new side chat", source: "AO" }))), ...skills.filter((skill) => skill.name !== "btw")] : skills}
				filePaths={paths}
				filePathsTruncated={truncated}
				localEchos={localEchos}
				onStageAttachments={stageAttachments}
				nativeImages={can(renderSnapshot, "images")}
				onSteer={can(renderSnapshot, "steer") && !commands.steerUnsupported ? commands.steer : undefined}
				sendPending={commands.sendPending}
				steerPending={commands.steerPending}
				steerRefusal={commands.steerRefusal}
				onPromoteQueuedTurn={
					can(renderSnapshot, "steer") && !commands.steerUnsupported
						? commands.promoteQueuedTurn
						: undefined
				}
				onEditQueuedTurn={commands.editQueuedTurn}
				onCancelQueuedTurn={commands.cancelQueuedTurn}
				onReorderQueuedTurns={commands.reorderQueuedTurns}
				promoteQueuedTurnPendingTurnId={commands.promoteQueuedTurnPendingTurnId}
				cancelQueuedTurnPendingTurnId={commands.cancelQueuedTurnPendingTurnId}
				editQueuedTurnPendingTurnId={commands.editQueuedTurnPendingTurnId}
			/>
			</ForkSessionContext.Provider>
		</div>
	);
});

function unavailableConversationSnapshot(session: WorkspaceSession): ConversationSnapshot {
	return {
		...startingConversationSnapshot(session.id, session.provider),
		controller: { state: "stopped", error: "Conversation unavailable" },
	};
}

function Centered({ children }: { children: React.ReactNode }) {
	return (
		<div className="flex h-full flex-col items-center justify-center gap-2 bg-background px-6">
			{children}
		</div>
	);
}
