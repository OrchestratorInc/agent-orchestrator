import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { agentLabel } from "../../lib/agent-options";
import { useIndependentSideChats } from "./IndependentSideChats";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import { useSideChatsAvailable } from "../../lib/side-chats";
/**
 * The central surface for a chat-mode session.
 *
 * Mounted by SessionView when the session's persisted mode is `chat`. It owns the
 * conversation query and command wiring so ChatWorkspace stays a pure view of a
 * snapshot — which is what lets the same component render fixtures in the dev
 * preview and live data here.
 */

import { AlertTriangle, Loader2 } from "lucide-react";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useChatPermissionModes } from "../../hooks/useChatPermissionModes";
import { useChatMessageFocus } from "../../hooks/useChatMessageFocus";
import { useChatModelChoices } from "../../hooks/useChatModelChoices";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
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
import { isWebLink, isWorkspaceHtmlLink } from "../../lib/external-link-policy";
import { sessionUiKey } from "../../lib/hosts";
import { readChatLastModel, rememberChatLastModel } from "../../lib/chat-last-model";
import { clientForSessionHost } from "../../lib/host-clients";
import type { ShellTerminal } from "../../hooks/useShellTerminals";
import { useSessionLinkNavigation } from "../../lib/use-session-link-navigation";
import type { Theme } from "../../stores/ui-store";
import { can } from "../../types/conversation";
import type { ConversationSnapshot, TurnSettings } from "../../types/conversation";
import type { TerminalTarget } from "../../types/terminal";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../../types/workspace";
import { ComposerErrorNotice, switchEffort, type PendingAgentSwitch } from "./AgentSwitchPicker";
import { AGENT_SWITCH_FAILED } from "./ChatComposer";
import { useUiStore } from "../../stores/ui-store";
import { CONTINUE_STOPPED_TURN_PROMPT, ChatWorkspace, continuableTurnId } from "./ChatWorkspace";
import { startingConversationSnapshot } from "./OrchestratorStartingChat";
import { hasProviderPermissionMode, withUltracode } from "./TurnSettingsBar";

export interface ConversationWorkState {
	controllerBusy: boolean;
	hasRunningTurn: boolean;
	queuedTurnCount: number;
}

const HTTP_LINK_PATTERN = /https?:\/\/[^\s<>()\[\]{}"']+/i;
const autoOpenedLinkSessions = new Set<string>();

interface AssistantLinkState {
	revision: number;
	sequence: number;
	streaming: boolean;
}

interface ConversationLinkBaseline {
	latestSequence: number;
	messages: Map<string, AssistantLinkState>;
	pendingCompleted: Map<string, number>;
}

function cleanExtractedLink(value: string): string {
	return value.replace(/[.,!?;:`\\]+$/, "");
}

function firstBrowserLink(text: string, workspacePaths: string[]): string | undefined {
	const candidates: Array<{ index: number; value: string }> = [];
	const webMatch = HTTP_LINK_PATTERN.exec(text);
	if (webMatch) candidates.push({ index: webMatch.index, value: cleanExtractedLink(webMatch[0]) });
	const markdownLink = /\[[^\]]+\]\(([^)\s]+)\)/.exec(text);
	if (markdownLink?.[1]) candidates.push({ index: markdownLink.index, value: cleanExtractedLink(markdownLink[1]) });
	for (const candidate of candidates.sort((a, b) => a.index - b.index)) {
		if (isWebLink(candidate.value) || isWorkspaceHtmlLink(candidate.value, workspacePaths)) return candidate.value;
	}
	return undefined;
}

export const SessionChatSurface = memo(function SessionChatSurface({
	sideChatContainer,
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
	onOpenLinkInBrowser,
	headerActions,
	sessionTabAction,
	sessionTabActionWide = false,
	tabStripAction,
	workspaceTabs,
	workspaceTabActions,
	workspaceActiveTabKey,
	workspaceFileActive,
	auxiliaryTabOrder,
	onAuxiliaryTabOrderChange,
	controllerTransitioning,
	accountMoving,
	agentResuming,
	controllerResumeError,
	newWorkDisabled,
	arriving,
	onConversationWorkChange,
	interfaceSwitch,
}: {
	/** The switch to the terminal view, for states without the tab strip and its menu, and its error. */
	interfaceSwitch?: { control: ReactNode; errorNotice?: ReactNode };
	sideChatContainer?: HTMLDivElement | null;
	session: WorkspaceSession;
	/** Owning daemon for a remote session; omitted for the local daemon. */
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
	/** This session's standalone shells, rendered as tabs in the chat header. */
	shellTerminals?: ShellTerminal[];
	/** The selected shell pane, if any. Mirrors reviewerTarget. */
	shellTarget?: Extract<TerminalTarget, { kind: "shell" }>;
	onSelectShellTerminal?: (handleId: string) => void;
	onCloseShellTerminal?: (handleId: string) => void;
	onRenameShellTerminal?: (handleId: string, title: string) => void;
	daemonReady?: boolean;
	theme?: Theme;
	onOpenShell?: () => void;
	openingShell?: boolean;
	shellError?: string;
	/** Opens the Files inspector from a turn's changed-files Review control. */
	onOpenFiles?: () => void;
	/** Opens the Files inspector focused on one changed path. */
	onOpenFile?: (path: string, line?: number) => void;
	/** Opens a chat link in the active blank tab or a new tab in this session's AO Browser. */
	onOpenLinkInBrowser?: (uri: string) => Promise<void>;
	headerActions?: ReactNode;
	sessionTabAction?: ReactNode;
	sessionTabActionWide?: boolean;
	tabStripAction?: ReactNode;
	workspaceTabs?: Array<{ key: string; content: ReactNode; onSelect: () => void }>;
	workspaceTabActions?: ReactNode;
	workspaceActiveTabKey?: string;
	/** A file overlay hides the chat surface, so it must not offer a suggestion. */
	workspaceFileActive?: boolean;
	/** Session-owned order shared with the terminal UI surface. */
	auxiliaryTabOrder?: string[];
	onAuxiliaryTabOrderChange?: (keys: string[]) => void;
	/** The target controller is being installed by an interface handoff. */
	controllerTransitioning?: boolean;
	/** The handoff is an account move; see ChatWorkspace. */
	accountMoving?: boolean;
	/** A stopped agent is being resumed in the background after the chat opened. */
	agentResuming?: boolean;
	controllerResumeError?: string;
	/** An interface handoff fences new agent work while current-turn decisions remain available. */
	newWorkDisabled?: boolean;
	/** A switch from the terminal is under way and the conversation is not readable yet. */
	arriving?: boolean;
	/** Reports accepted Chat work that must inform an interface-switch policy choice. */
	onConversationWorkChange?: (state: ConversationWorkState) => void;
}) {
	const { t } = useTranslation();
	const uiSessionId = sessionUiKey(session.id, hostId);
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
	// Route props can move to the destination before the old query observer drops
	// its data. Treat that snapshot as unknown everywhere, especially at the work
	// boundary that decides whether switching to Terminal needs user consent.
	const snapshot = queriedSnapshot?.sessionId === session.id ? queriedSnapshot : undefined;
	const commands = useConversationCommands(session.id, hostId);
	// Set only when this task was started from the new chat page.
	const queryClient = useQueryClient();
	// Whether the project has a setup script, so a start lists that step only when it
	// runs something. Remote and cloud projects are not read here and keep the step.
	const plansSetup = Boolean(session.provisionSteps?.some((step) => step.id === "setup"));
	const projectScripts = useQuery({
		queryKey: ["project", session.workspaceId],
		enabled: plansSetup && !hostId && !session.cloud,
		staleTime: 30_000,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}", { params: { path: { id: session.workspaceId } } });
			if (error) throw new Error(apiErrorMessage(error));
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
	// Choosing another agent only records the choice; the switch runs with the next message.
	const providerSwitch = useProviderSwitchCommand(session.id, hostId);
	const [chosenSwitch, setChosenSwitch] = useState<PendingAgentSwitch>();
	// The chat is the tab in front: not a file, terminal or reviewer tab.
	const chatInFront = !workspaceFileActive && !workspaceActiveTabKey && !reviewerTarget && !reviewerChatSelected && !shellTarget;
	// Sending is the switch, so a pick is dropped once another tab comes forward: a message
	// typed on coming back could otherwise go to an agent no longer meant.
	useEffect(() => {
		if (!chatInFront) setChosenSwitch(undefined);
	}, [chatInFront]);
	// After a daemon restart the daemon reattaches surviving Chats on its own, and
	// that reattach emits no change event. The session's recovery check settling
	// on "ready" is the signal to refresh the controller state this view shows.
	const statusReadiness = session.statusReadiness;
	const lastReadiness = useRef({ id: session.id, readiness: statusReadiness });
	useEffect(() => {
		const previous = lastReadiness.current;
		lastReadiness.current = { id: session.id, readiness: statusReadiness };
		if (hostId || previous.id !== session.id || statusReadiness !== "ready" ||
			!previous.readiness || previous.readiness === "ready") return;
		void queryClient.invalidateQueries({ queryKey: conversationQueryKey(session.id) });
	}, [hostId, queryClient, session.id, statusReadiness]);
	// A switch refused inside a send; the separate switch request keeps its own.
	const [sendSwitchError, setSendSwitchError] = useState<unknown>();
	const switchError = providerSwitch.error ?? sendSwitchError;
	const clearSwitchError = () => { setSendSwitchError(undefined); providerSwitch.reset(); };
	const currentHarness = snapshot?.harness;
	const pendingSwitch = currentHarness && chosenSwitch?.harness !== currentHarness ? chosenSwitch : undefined;
	// Catalogs are cached by session; a switch made elsewhere (another window or device)
	// leaves the outgoing agent's, so they follow the agent the chat reports.
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
	// A pick whose agent the chat now runs is done, however the switch's answer went.
	useEffect(() => {
		if (chosenSwitch && chosenSwitch.harness === currentHarness) setChosenSwitch(undefined);
	}, [chosenSwitch, currentHarness]);
	const pendingHarness = pendingSwitch?.harness;
	// The daemon's first-start chat check can take a second; run it now so the send does not wait.
	useEffect(() => {
		if (!pendingHarness) return;
		void clientForSessionHost(hostId)
			.POST("/api/v1/agents/{agent}/chat/prewarm", { params: { path: { agent: pendingHarness } } })
			.catch(() => undefined);
	}, [hostId, pendingHarness]);
	// Ultracode is asked for in each message, so it is kept here, not on the agent.
	const [ultracodeOn, setUltracodeOn] = useState(false);
	const ultracode = useMemo(() => ({ on: ultracodeOn, onChange: setUltracodeOn }), [ultracodeOn]);
	const autoCompactWindow = snapshot?.settings.autoCompactWindow;
	const [applyingAutoCompact, setApplyingAutoCompact] = useState(false);
	// Claude Code reads this when it starts, so saving it restarts the agent.
	const saveAutoCompact = useLatestCallback((value: TurnSettings["autoCompactWindow"]) => {
		if (!snapshot) return;
		setApplyingAutoCompact(true);
		void commands.chooseStartSettings({ ...snapshot.settings, autoCompactWindow: value })
			.catch((error) => useUiStore.getState().showGlobalToast("Auto-compact change failed", apiErrorMessage(error), "error"))
			.finally(() => setApplyingAutoCompact(false));
	});
	const agentBusy = controllerBusy || hasRunningTurn || queuedTurnCount > 0;
	// The restart is only safe while the agent is not working.
	const autoCompact = useMemo(
		() => ({ value: autoCompactWindow, disabled: applyingAutoCompact || agentBusy, onChange: saveAutoCompact }),
		[agentBusy, applyingAutoCompact, autoCompactWindow, saveAutoCompact],
	);
	const switchBlocked = agentBusy || providerSwitch.isPending;
	// A new pick for the same agent keeps the permission mode picked for it.
	const choosePendingSwitch = useCallback((next: PendingAgentSwitch | undefined) => {
		setChosenSwitch((current) => next && current?.approvalMode && current.harness === next.harness && !next.approvalMode
			? { ...next, approvalMode: current.approvalMode }
			: next);
	}, []);
	// While the agent runs in a mode it needed (Pi only bypassed), a switch away goes back
	// to the chat's own, which its switch marker keeps.
	const chatPermissionMode = useMemo(() => {
		const marker = snapshot?.items.findLast((item) => item.kind === "activity" && item.detail?.event === "provider_switch");
		const detail = marker?.kind === "activity" ? marker.detail : undefined;
		return detail?.targetHarness === currentHarness && detail?.permissionMode === snapshot?.settings.approvalMode
			? detail?.chatPermissionMode
			: undefined;
	}, [currentHarness, snapshot?.items, snapshot?.settings.approvalMode]);
	const permissionModes = useChatPermissionModes(hostId, pendingSwitch?.harness);
	const pendingPermissionModes = pendingSwitch ? permissionModes?.get(pendingSwitch.harness) : undefined;
	const agentSwitch = useMemo(
		() => currentHarness && !session.cloud && !newWorkDisabled
			? { projectId: session.workspaceId, hostId, pending: pendingSwitch, pendingPermissionModes, chatPermissionMode, onPendingChange: choosePendingSwitch, disabled: switchBlocked, ultracode, autoCompact }
			: undefined,
		[autoCompact, chatPermissionMode, choosePendingSwitch, currentHarness, hostId, newWorkDisabled, pendingPermissionModes, pendingSwitch, session.cloud, session.workspaceId, switchBlocked, ultracode],
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
	// Mode commits before the target controller starts. A cached ready snapshot
	// can also outlive the source, so wait for the handoff's final snapshot refresh.
	// A background resume keeps the last-known catalogs visible so the model
	// picker doesn't blank out while the agent spins up.
	// A sleeping, waking or reconnecting agent cannot report its settings yet; the bar shows the saved ones.
	const waking = snapshot?.harness === session.provider &&
		(snapshot.controller?.state === "hibernated" || snapshot.controller?.state === "connecting" || snapshot.controller?.state === "recovering");
	const controllerCatalogsEnabled = (targetChatControllerReady || agentResuming) && !controllerTransitioning && !newWorkDisabled;
	const configOptions = useConversationConfigOptions(
		session.id,
		Boolean(controllerCatalogsEnabled && snapshot && can(snapshot, "config_options")),
		hostId,
	);
	// A provider config catalog may cover only model, only mode, or both.
	// Suppress native controls only for dimensions the provider catalog replaces;
	// a model-only catalog must not hide the Approvals control.
	const providerOptions = configOptions.options ?? [];
	// Keep this agent's settings so the new chat page can draw the very bar a
	// session of it will show, before that session exists.
	// Keyed by the raw harness: the provider label folds unknown harnesses into one.
	const templateHarness = snapshot?.harness;
	useEffect(() => {
		if (session.kind !== "worker" || !templateHarness || !configOptions.loaded || providerOptions.length === 0) return;
		rememberChatSettingsTemplate(templateHarness, hostId, { options: providerOptions });
	}, [configOptions.loaded, hostId, providerOptions, session.kind, templateHarness]);
	const hasProviderMode = hasProviderPermissionMode(providerOptions);
	const hasProviderModel = providerOptions.some(
		(option) => option.category === "model" || option.id === "model",
	);
	// Only asked for once the conversation is actually readable: the catalog comes
	// from the live controller, so there is nothing to fetch before then.
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
	// A new chat of this agent offers these "/" skills before its own session runs.
	const skillsHarness = snapshot?.harness;
	useEffect(() => {
		if (session.kind !== "worker" || !skillsHarness || skills.length === 0) return;
		rememberChatSkills(skillsHarness, hostId, skills);
	}, [hostId, session.kind, skills, skillsHarness]);
	const { paths, truncated } = useWorkspaceFilePaths(session.id, Boolean(snapshot), hostId);
	const stageAttachments = useStageAttachments(session.id, hostId);
	const [annotationNavigationRequest, setAnnotationNavigationRequest] = useState<{ id: string; text: string; messageId?: string; revision?: number }>();
	useChatMessageFocus(uiSessionId, undefined, Boolean(snapshot), setAnnotationNavigationRequest);
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
			setAnnotationNavigationRequest({ ...annotation, id: crypto.randomUUID() });
		},
		() => useUiStore.getState().setInspectorOpen(uiSessionId, false),
		sideCreationEnabled,
	);
	useEffect(() => { if (sideChatContainer) sideChats.show(); }, [sideChatContainer]);
	useEffect(() => setSideWork(sideChats.sides.some((side) => side.hasWork)), [sideChats.sides]);
	const attachToSide = async (excerpt: ChatDraftExcerptReference) => {
		openSideInspector();
		if (sideChats.activeId) {
			sideChats.addReference(sideChats.activeId, excerpt);
			sideChats.show(sideChats.activeId);
		} else {
			await sideChats.create(excerpt);
		}
	};

	const openLinkInBrowser = useSessionBrowserLink(session, onOpenLinkInBrowser, paths, onOpenFile);
	// Links opened on the user's behalf stay quiet about a file the session does
	// not own; only a click earns the explanation in the file pane.
	const autoOpenLinkInBrowser = useSessionBrowserLink(session, onOpenLinkInBrowser, paths);
	const openSessionLink = useSessionLinkNavigation(hostId);
	const conversationLinkBaselines = useRef(new Map<string, ConversationLinkBaseline>());
	useEffect(() => {
		if (!snapshot || isLoading) return;
		const previous = conversationLinkBaselines.current.get(uiSessionId);
		const isInitialSnapshot = !previous;
		const latestUserMessage = snapshot.items
			.filter((item) => item.kind === "message" && item.role === "user")
			.at(-1);
		const messages = new Map<string, AssistantLinkState>();
		const pendingCompleted = new Map(previous?.pendingCompleted);
		let latestSequence = Math.max(previous?.latestSequence ?? -1, snapshot.latestSequence);
		for (const item of snapshot.items) {
			latestSequence = Math.max(latestSequence, item.sequence);
			if (item.kind !== "message" || item.role !== "assistant") continue;
			const prior = previous?.messages.get(item.id);
			messages.set(item.id, {
				revision: item.revision,
				sequence: item.sequence,
				streaming: item.streaming,
			});
			if (item.streaming) {
				pendingCompleted.delete(item.id);
				continue;
			}
			const completedCurrentTurnOnMount =
				isInitialSnapshot && latestUserMessage && item.sequence > latestUserMessage.sequence;
			const newlyCompleted = previous
				? prior
					? prior.streaming && item.revision >= prior.revision
					: item.sequence > previous.latestSequence
				: completedCurrentTurnOnMount;
			if (newlyCompleted) pendingCompleted.set(item.id, item.revision);
			else if (pendingCompleted.get(item.id) !== item.revision) pendingCompleted.delete(item.id);
		}
		conversationLinkBaselines.current.set(uiSessionId, { latestSequence, messages, pendingCompleted });
		if (autoOpenedLinkSessions.has(uiSessionId)) return;
		// Do not surprise users by opening links from history when a session is first
		// mounted. The exception is the current turn: a fast agent can finish before
		// the first conversation request resolves, so its response is already present
		// in the initial snapshot and must not be mistaken for old history.
		for (const item of snapshot.items) {
			if (
				item.kind !== "message" ||
				item.role !== "assistant" ||
				item.streaming ||
				pendingCompleted.get(item.id) !== item.revision
			) continue;
			const url = firstBrowserLink(item.text, paths);
			if (url) {
				autoOpenedLinkSessions.add(uiSessionId);
				pendingCompleted.delete(item.id);
				autoOpenLinkInBrowser(url);
				break;
			}
		}
	}, [autoOpenLinkInBrowser, isLoading, paths, snapshot, uiSessionId]);
	const promptSuggestion = useConversationPromptSuggestion(snapshot, Boolean(
		targetChatControllerReady && !controllerBusy && !localEchos.length &&
		!commands.rollbackPending && !commands.editMessagePending && !commands.activateBranchPending && !commands.compacting &&
		!session.isTerminated && session.provisionState !== "failed" && session.provisionState !== "provisioning" &&
		!isLoading && !error && !refreshError && !unavailable &&
		!controllerTransitioning && !agentResuming && !newWorkDisabled && !arriving &&
		chatInFront,
	), hostId);
	const renderShellFallback = Boolean(shellTarget && session);
	// A switch from the terminal shows the chat before the daemon has a
	// conversation to read. The starting snapshot stands in for the whole
	// arrival, even over a cached transcript, so history appears once and fades
	// in instead of flashing, hiding, and reappearing as the controller restarts.
	const optimisticArrival = Boolean(arriving) && !renderShellFallback;
	// A task started from the new chat page already shows its brief, so its first
	// read keeps that frame instead of flashing a loading state.
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
	const settingsLoading = waking || Boolean(renderSnapshot && can(renderSnapshot, "config_options") && !configOptions.loaded && !configOptions.error);
	// The model the running agent reports, kept so this chat names it at once the next time it loads.
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
		// Keep an ambiguous request's identity until durable progress confirms
		// that intent ended. An undo can later make the same stop eligible again.
		if (!stoppedTurnId && renderSnapshot && continueIntent.current &&
			renderSnapshot.latestSequence > continueIntent.current.sequence) {
			continueIntent.current = null;
		}
	}, [stoppedTurnId, renderSnapshot?.latestSequence]);
	// Keep the project marked as starting until its controller is ready, so
	// sidebar actions cannot launch a duplicate orchestrator.
	useEffect(() => {
		if (session.kind !== "orchestrator" || session.provisionState === "provisioning") return;
		if (isLoading && !renderShellFallback) return;
		if (session.provisionState !== "failed" && !targetChatControllerReady && !renderShellFallback && !error && !unavailable) return;
		useUiStore.getState().setProjectProvisioning(session.workspaceId, false, hostId);
	}, [error, hostId, isLoading, renderShellFallback, session.kind, session.provisionState, session.workspaceId, targetChatControllerReady, unavailable]);

	// Until the agent's own settings arrive the bar shows the saved ones, disabled. With
	// none saved yet it waits, so defaults never pass for the chat's own.
	const settingsReady = Boolean(renderSnapshot) && !optimisticChat && (targetChatControllerReady || agentResuming || waking) &&
		(!settingsLoading || Boolean(renderSnapshot?.settings?.model || renderSnapshot?.settings?.approvalMode));
	// The session's own settings have taken over; a later restart must not bring the launch chips back.
	useEffect(() => {
		if (settingsReady && !settingsLoading && launchSettings) forgetLaunchSettings(queryClient, session.id, hostId);
	}, [hostId, launchSettings, queryClient, session.id, settingsLoading, settingsReady]);
	// Once the opening brief's turn has settled the chat is an ordinary one; a later
	// Undo of that turn must not bring the start-up state back.
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

	// A chat session whose controller has not started yet, or whose agent cannot
	// run Chat is a state to explain rather than an error to spin on. A compatible
	// session may switch interfaces, but retrying this failed controller by itself
	// cannot change the answer.
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
					{error ?? "Could not load this conversation."}
				</p>
				{/* The terminal can still carry on from here. */}
				{interfaceSwitch?.control}
			</Centered>
		);
	}

	return (
		<div className="relative h-full min-h-0">
			{refreshError ? <p role="alert" className="px-4 py-2 text-xs text-destructive">{refreshError}</p> : null}
			{sideChatContainer ? createPortal(sideChats.panel, sideChatContainer) : null}
			<ChatWorkspace
				showSetupStep={showSetupStep}
				onAddToSideChat={sideEnabled && (sideChats.activeId || sideCreationEnabled) ? attachToSide : undefined}
				annotationNavigationRequest={annotationNavigationRequest}
				key={uiSessionId}
				uiSessionId={uiSessionId}
				assetBaseUrl={assetBaseUrl}
				remoteHostId={hostId}
				snapshot={renderSnapshot}
				promptSuggestion={promptSuggestion}
				newWorkDisabled={newWorkDisabled}
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
				sessionTabActionWide={sessionTabActionWide}
				tabStripAction={tabStripAction}
				workspaceTabs={workspaceTabs}
				workspaceTabActions={workspaceTabActions}
				workspaceActiveTabKey={workspaceActiveTabKey}
				auxiliaryTabOrder={auxiliaryTabOrder}
				onAuxiliaryTabOrderChange={onAuxiliaryTabOrderChange}
				controllerTransitioning={controllerTransitioning}
				accountMoving={accountMoving}
				loadingQuietly={optimisticChat && !optimisticArrival && session.provisionState !== "provisioning"}
				agentResuming={agentResuming}
				hasOlder={hasOlder}
				loadingOlder={isLoadingOlder}
				onLoadOlder={loadOlder}
				busy={commands.busy}
				excerptsEnabled
				fileReferencesEnabled={Boolean(renderSnapshot.supportsFileReferences)}
				onSend={async (text, attachments, clientMessageId, excerpts, fileReferences) => {
					// Only claim /btw and /side where side chats can open. Elsewhere the
					// text goes to the agent as typed (it may be the harness's own skill),
					// so a failed side launch can never strand the main draft.
					const btw = sideCreationEnabled ? /^\/(?:btw|side)(?:\s+|$)/i.exec(text) : null;
     if (btw && fileReferences?.length) {
      throw Object.assign(new Error("File annotations cannot be sent to side chats yet. Send in the main chat or remove the file annotations."), { code: "CHAT_FILE_REFERENCES_SIDE_UNSUPPORTED" });
     }
     if (btw) {
						openSideInspector();
						const side = await sideChats.create(undefined, true, clientMessageId);
						const question = text.slice(btw[0].length).trim();
						if (question || excerpts?.length || attachments?.length) {
							// Retain the main draft if handing the request to the side fails.
							await sideChats.send(side.id, question, attachments, excerpts, true, clientMessageId);
						}
						return;
					}
					const viaClaude = (pendingSwitch?.harness ?? currentHarness) === "claude-code";
					const message = {
						text: viaClaude && (pendingSwitch ? pendingSwitch.ultracode : ultracode.on) ? withUltracode(text) : text,
						attachments,
						clientMessageId,
						fileReferences,
						excerpts: excerpts?.map((excerpt) => ({
							id: excerpt.id, order: excerpt.order,
							conversationId: excerpt.conversationId,
							messageId: excerpt.messageId,
							revision: excerpt.revision,
							text: excerpt.text,
						})),
					};
					// A new message replaces an earlier switch's failure.
					clearSwitchError();
					if (!pendingSwitch) return commands.send(message);
					// The bar shows the mode the agent will start in only once its modes are known
					// (Pi runs only with permissions bypassed); nothing goes before then.
					if (!pendingPermissionModes) {
						const reason = { message: t("agentSwitch.checkingPermissionModes", { agent: agentLabel(pendingSwitch.harness) }) };
						setSendSwitchError(reason);
						throw Object.assign(new Error(reason.message), { code: AGENT_SWITCH_FAILED });
					}
					const { harness: targetHarness, model, fastMode, autoCompactWindow } = pendingSwitch;
					const effort = switchEffort(pendingSwitch);
					const switchRequest = { targetHarness, model, effort, fastMode, autoCompactWindow, permissionMode: pendingSwitch.approvalMode };
					// The transcript draws the "Switched" line from this while the daemon works.
					const switchDisplay = currentHarness
						? { sourceHarness: currentHarness, sourceModel: snapshot?.settings?.model, targetModel: model, targetEffort: effort }
						: undefined;
					// Nothing was sent if the switch fails, so the draft stays editable. A refused
					// switch drops the pick; with no clear answer it stays until the chat shows its agent.
					const switchFailed = (error: unknown) => {
						if (error instanceof ProviderSwitchFailed) setChosenSwitch(undefined);
						void queryClient.invalidateQueries({ queryKey: conversationQueryKey(session.id, hostId) });
						return Object.assign(new Error(apiErrorMessage(error)), { code: AGENT_SWITCH_FAILED });
					};
					const switched = () => {
						setChosenSwitch(undefined);
						// Ultracode picked with the switch stays on for the messages after it.
						ultracode.onChange(Boolean(pendingSwitch.ultracode));
					};
					// The daemon switches and sends in one request, attachments included, so no other
					// switch can take the message; it shows at once.
					try {
						const sent = await commands.send({ ...message, providerSwitch: switchRequest, switchDisplay });
						switched();
						return sent;
					} catch (error) {
						if (error instanceof ProviderSwitchFailed) {
							setSendSwitchError(error.reason);
							throw switchFailed(error);
						}
						// The switch went through and only the send failed, or no answer came. The chat is
						// read back; once it runs the picked agent the pick drops, so a retry is an ordinary send.
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
				onOpenShell={onOpenShell}
				openingShell={openingShell}
				shellError={shellError}
				settingsReady={settingsReady}
				settingsLoading={settingsLoading}
				lastModel={lastModel}
				openingStartedAt={openingStartedAt}
				// A task started from the new chat page keeps its chips until the agent's own settings load.
				draftSettings={launchSettings && (!settingsReady || settingsLoading) ? <LaunchSettingsBar {...launchSettings} /> : undefined}
				models={models}
				onChooseSettings={hasProviderMode ? undefined : commands.chooseSettings}
				// A Scratchpad session has no project to remember a mode for.
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
				rollbackPending={commands.rollbackPending}
				rollbackError={commands.rollbackError}
				onOpenFiles={onOpenFiles}
				onOpenFile={onOpenFile}
				retryControl={commands.retryControl}
				onEditMessage={commands.editMessage}
				editMessagePending={commands.editMessagePending}
				editMessageError={commands.editMessageError}
				onActivateBranch={commands.activateBranch}
				activateBranchPending={commands.activateBranchPending}
				activateBranchError={commands.activateBranchError}
				skills={sideCreationEnabled ? [...(["btw", "side"].map((name) => ({ name, displayName: name, description: "Open a new side chat", source: "AO" }))), ...skills.filter((skill) => skill.name !== "btw" && skill.name !== "side")] : skills}
				filePaths={paths}
				filePathsTruncated={truncated}
				localEchos={localEchos}
				onStageAttachments={stageAttachments}
				nativeImages={can(renderSnapshot, "images")}
				// Gated on what the daemon advertises, so the control is never drawn for a
				// harness that cannot steer. The refusal check stays as a backstop: it
				// covers the window before the controller reports, and it is the last word
				// afterwards, since the capability is a property of the driver.
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
