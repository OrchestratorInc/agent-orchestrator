import { createPortal } from "react-dom";
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

import { AlertTriangle, CheckCircle2, Loader2, X } from "lucide-react";
import { memo, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
	findActiveAgentSwitch,
	isTerminalAgentSwitch,
	selectDurableAgentSwitch,
	useAgentSwitches,
} from "../../hooks/useAgentSwitches";
import { useObservedAgentSwitchLifecycle } from "../../hooks/useObservedAgentSwitchLifecycle";
import { useAgentSwitchPresentationVisibility, useAgentSwitchRouteVisibility } from "../../hooks/useAgentSwitchVisibility";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { forgetLaunchSettings, forgetOpeningStart, LaunchSettingsBar, launchSettingsFor, openingStartFor } from "./LaunchSettingsBar";
import { rememberChatSettingsTemplate, rememberChatSkills } from "../../lib/chat-settings-template";
import { agentModelsQueryOptions } from "../../hooks/useAgentModelsQuery";
import { useOfferedModels } from "../../hooks/useOfferedModels";
import { isComposerAgentSwitch, useSwitchAgent, useSwitchAgentState } from "../../hooks/useSwitchAgent";
import { restoreChatComposerText } from "../../lib/chat-context-bus";
import { readChatSessionDraft, writeChatComposerText } from "../../lib/chat-drafts";
import { useSessionAgentSwitch } from "./useSessionAgentSwitch";
import {
	releaseSwitchLocalEcho,
	seedHeldLocalEcho,
	type ConversationSendInput,
	useConversation,
	useConversationCommands,
	useConversationConfigOptions,
	useConversationModels,
	useConversationSkills,
	useStageAttachments,
	useWorkspaceFilePaths,
} from "../../hooks/useConversation";
import { useAgentSwitchProviderCatalogs } from "../../hooks/useAgentSwitchProviderCatalogs";
import { useConversationPromptSuggestion } from "../../hooks/useConversationPromptSuggestion";
import { useRememberProjectPermissions } from "../../hooks/useRememberProjectPermissions";
import { useSessionBrowserLink } from "../../hooks/useSessionBrowserLink";
import { claudeChoiceModel } from "../../lib/agent-model-choices";
import { isWebLink, isWorkspaceHtmlLink } from "../../lib/external-link-policy";
import { sessionUiKey } from "../../lib/hosts";
import type { ShellTerminal } from "../../hooks/useShellTerminals";
import {
	deriveAgentSwitchPresentation,
	agentSwitchVisibilityPresentationKind,
	type AgentSwitchPresentation,
} from "../../lib/agent-switch-presentation";
import { cn } from "../../lib/utils";
import { useSessionLinkNavigation } from "../../lib/use-session-link-navigation";
import type { Theme } from "../../stores/ui-store";
import { can } from "../../types/conversation";
import type { ConversationSnapshot } from "../../types/conversation";
import type { TerminalTarget } from "../../types/terminal";
import type { AgentSwitchSummary, WorkspaceSession } from "../../types/workspace";
import { AgentSwitchProgressTrack } from "../AgentSwitchProgressTrack";
import { useUiStore } from "../../stores/ui-store";
import { CONTINUE_STOPPED_TURN_PROMPT, ChatWorkspace, continuableTurnId } from "./ChatWorkspace";
import { startingConversationSnapshot } from "./OrchestratorStartingChat";
import { hasProviderPermissionMode, TurnSettingsBar } from "./TurnSettingsBar";

export interface ConversationWorkState {
	controllerBusy: boolean;
	hasRunningTurn: boolean;
	queuedTurnCount: number;
}

const HTTP_LINK_PATTERN = /https?:\/\/[^\s<>()\[\]{}"']+/i;
const autoOpenedLinkSessions = new Set<string>();
const ignorePendingAgentChange = () => undefined;

// Messages sent while an agent switch runs, by session, oldest first. They wait
// for the new agent to own the chat. Kept in local storage as well as memory,
// so a reload, quit, or crash mid-switch does not lose what the user sent.
const heldSwitchMessages = new Map<string, ConversationSendInput[]>();
const HELD_SWITCH_STORAGE_PREFIX = "ao.chat.held-switch.";
// Past this a composer switch shows its progress card and any controller error:
// a switch normally lands in a few seconds.
const SLOW_AGENT_SWITCH_MS = 10_000;

function heldSwitchStorage(): Storage | undefined {
	try {
		return window.localStorage;
	} catch {
		return undefined;
	}
}

// A message sent while a switch ran, with the agent that switch was moving
// to and when it was held. Only that switch landing sends it.
type HeldSwitchMessage = ConversationSendInput & { heldFor?: { target: string; at: number } };

function readHeldSwitchMessages(sessionKey: string): HeldSwitchMessage[] {
	const cached = heldSwitchMessages.get(sessionKey);
	if (cached) return cached;
	try {
		const raw = heldSwitchStorage()?.getItem(HELD_SWITCH_STORAGE_PREFIX + sessionKey);
		const parsed: unknown = raw ? JSON.parse(raw) : [];
		// A message stored without its images (over quota) and no text has
		// nothing left to send.
		const held = Array.isArray(parsed)
			? parsed.filter((message): message is HeldSwitchMessage =>
				typeof message?.text === "string" &&
				(message.text.trim() !== "" || Boolean(message.attachments?.length) || Boolean(message.excerpts?.length)))
			: [];
		if (held.length) heldSwitchMessages.set(sessionKey, held);
		return held;
	} catch {
		return [];
	}
}

function writeHeldSwitchMessages(sessionKey: string, held: HeldSwitchMessage[]) {
	if (held.length) heldSwitchMessages.set(sessionKey, held);
	else heldSwitchMessages.delete(sessionKey);
	const storage = heldSwitchStorage();
	if (!storage) return;
	const key = HELD_SWITCH_STORAGE_PREFIX + sessionKey;
	try {
		if (held.length) storage.setItem(key, JSON.stringify(held));
		else storage.removeItem(key);
	} catch {
		// Over quota with images: keep the words.
		try {
			storage.setItem(key, JSON.stringify(held.map(({ attachments: _attachments, resources: _resources, ...rest }) => rest)));
		} catch {
			// The in-memory copy still serves this window.
		}
	}
}

function holdSwitchMessage(sessionKey: string, message: HeldSwitchMessage) {
	writeHeldSwitchMessages(sessionKey, [...readHeldSwitchMessages(sessionKey), message]);
}

function takeHeldSwitchMessages(sessionKey: string): HeldSwitchMessage[] {
	const held = readHeldSwitchMessages(sessionKey);
	if (held.length) writeHeldSwitchMessages(sessionKey, []);
	return held;
}

function dropHeldSwitchMessage(sessionKey: string, clientMessageId: string | undefined) {
	const held = readHeldSwitchMessages(sessionKey);
	writeHeldSwitchMessages(sessionKey, held.filter((message) => message.clientMessageId !== clientMessageId));
}

// Sessions whose held messages are being sent right now.
const flushingHeldSwitch = new Set<string>();

// Hands text back after whatever the user has typed since: to the mounted
// composer, or to the draft it opens with.
function returnTextToComposer(sessionKey: string, incarnation: string, text: string) {
	if (!text || restoreChatComposerText(sessionKey, text)) return;
	const scope = { sessionId: sessionKey, incarnation };
	const draft = readChatSessionDraft(scope).composer.text;
	writeChatComposerText(scope, draft.trim() ? `${draft}\n\n${text}` : text);
}

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
	handoffDialogOpen = false,
	workspaceTabs,
	workspaceTabActions,
	workspaceActiveTabKey,
	workspaceFileActive,
	auxiliaryTabOrder,
	onAuxiliaryTabOrderChange,
	controllerTransitioning,
	agentResuming,
	controllerResumeError,
	newWorkDisabled,
	arriving,
	onConversationWorkChange,
}: {
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
	handoffDialogOpen?: boolean;
	workspaceTabs?: Array<{ key: string; content: ReactNode; onSelect: () => void }>;
	workspaceTabActions?: ReactNode;
	workspaceActiveTabKey?: string;
	/** A file overlay hides the chat surface, so it must not acknowledge switch UI. */
	workspaceFileActive?: boolean;
	/** Session-owned order shared with the terminal UI surface. */
	auxiliaryTabOrder?: string[];
	onAuxiliaryTabOrderChange?: (keys: string[]) => void;
	/** The target controller is being installed by an interface handoff. */
	controllerTransitioning?: boolean;
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
	const projectPermissions = useRememberProjectPermissions(session.workspaceId, snapshot?.harness, hostId);
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
		const durableHumanClientIds = new Set(
			snapshot.items.flatMap((item) =>
				item.kind === "message" && item.role === "user" && item.origin === "human" && item.clientMessageId
					? [item.clientMessageId]
					: [],
			),
		);
		for (const echo of localEchos) {
			if (echo.switching) {
				if (durableHumanClientIds.has(echo.clientMessageId)) acknowledgeLocalEcho?.(undefined, echo.clientMessageId);
			} else if (echo.opening && durableHumanTurnIds.size > 0) acknowledgeLocalEcho?.(undefined, echo.clientMessageId);
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
	const controllerCatalogsEnabled = (targetChatControllerReady || agentResuming) && !controllerTransitioning && !newWorkDisabled;
	// Agent-switch presentation for the chat surface progress track and input locks.
	const switchMutation = useSwitchAgentState(uiSessionId);
	const agentSwitchesQuery = useAgentSwitches(session.id, hostId);
	const agentSwitches = agentSwitchesQuery.data ?? [];
	const activeHistorySwitch = findActiveAgentSwitch(agentSwitches);
	const selectedDurableAgentSwitch = selectDurableAgentSwitch(
		session.activeAgentSwitch,
		agentSwitches,
	);
	const admissionAgentSwitch: AgentSwitchSummary | undefined =
		switchMutation.isPending && switchMutation.input
			? {
				agentHandoffStatus: "not_attempted",
				fromHarness: switchMutation.input.session.provider,
				id: `admission:${switchMutation.input.idempotencyKey}`,
				state: "preparing_handoff",
				targetHarness: switchMutation.input.targetHarness,
			}
			: undefined;
	const {
		dismissFailure: dismissAgentSwitchFailure,
		dismissedFailureSwitchId,
		isObserved: isAgentSwitchObserved,
		isRetired: isAgentSwitchRetired,
		observedTerminalSwitch,
		settle: settleAgentSwitch,
	} = useObservedAgentSwitchLifecycle({
		sessionId: uiSessionId,
		agentSwitches,
		nonterminalCandidates: [
			session.activeAgentSwitch,
			activeHistorySwitch,
			selectedDurableAgentSwitch,
			admissionAgentSwitch,
		],
	});
	const durableAgentSwitch =
		selectedDurableAgentSwitch && !isAgentSwitchRetired(selectedDurableAgentSwitch.id)
			? selectedDurableAgentSwitch
			: undefined;
	const agentSwitch = durableAgentSwitch ?? admissionAgentSwitch ?? observedTerminalSwitch;
	useAgentSwitchRouteVisibility(`session/${uiSessionId}`, agentSwitch && agentSwitch.state !== "completed" && agentSwitch.state !== "failed" ? "active" : "history", undefined, false);
	const switchPresentation = agentSwitch
		? deriveAgentSwitchPresentation({
				agentSwitch,
				activityState: session.activity?.state,
				currentHarness: session.provider,
				isTerminated: Boolean(session.isTerminated),
				// The shared presentation uses a live terminal handle as its TUI
				// takeover proof. Chat has no terminal runtime, so its equivalent is
				// the structured controller reaching a dispatchable state.
				terminalHandleId: targetChatControllerReady ? "chat-controller" : undefined,
			})
		: undefined;
	const agentSwitching = Boolean(
		switchMutation.isPending ||
			(switchPresentation?.outcome === "in_progress" ||
				switchPresentation?.outcome === "recovery"),
	);
	const observedSettledSwitchId =
		agentSwitch &&
		(switchPresentation?.outcome === "success" || switchPresentation?.outcome === "failure") &&
		isAgentSwitchObserved(agentSwitch.id)
			? agentSwitch.id
			: undefined;
	const latestTerminalSwitch = agentSwitches.find(isTerminalAgentSwitch);
	const controllerOwnedTerminalSwitch =
		targetChatControllerReady &&
		latestTerminalSwitch &&
		((latestTerminalSwitch.state === "completed" &&
			latestTerminalSwitch.targetHarness === session.provider) ||
			(latestTerminalSwitch.state === "failed" &&
				latestTerminalSwitch.fromHarness === session.provider))
			? latestTerminalSwitch
			: undefined;
	// Catalog ownership follows the live controller epoch, not whether this mount
	// happened to observe the switch in progress. A sub-second switch can arrive
	// first as terminal history and still needs its outgoing cache reconciled.
	const providerCatalogSettledSwitchId =
		observedSettledSwitchId ?? controllerOwnedTerminalSwitch?.id;
	const catalogsEnabled = useAgentSwitchProviderCatalogs({
		sessionId: session.id,
		hostId,
		agentSwitching,
		settledSwitchId: providerCatalogSettledSwitchId,
	});
	const configOptions = useConversationConfigOptions(
		session.id,
		Boolean(controllerCatalogsEnabled && catalogsEnabled && snapshot && can(snapshot, "config_options")),
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
	const { models: controllerModels } = useConversationModels(
		session.id,
		Boolean(controllerCatalogsEnabled && catalogsEnabled && snapshot) && !hasProviderModel,
		hostId,
	);
	// Claude's live list is family aliases; the new-task picker's catalog carries the versions.
	const isClaude = snapshot?.harness === "claude-code";
	const claudeCatalog = useQuery({ ...agentModelsQueryOptions("claude-code", "", hostId), enabled: isClaude }).data;
	const catalogModels = useMemo(() => {
		if (!isClaude || !claudeCatalog?.models.length) return controllerModels;
		return claudeCatalog.models
			.filter((model) => model.id.toLowerCase() !== "default")
			.map((model) => ({
				id: model.id,
				displayName: model.label || model.id,
				default: Boolean(model.isDefault),
			}));
	}, [isClaude, claudeCatalog, controllerModels]);
	// A provider's own model option (ACP config) replaces the native list above,
	// so the same setting narrows its choices; other options pass through as is.
	const providerModelOption = configOptions.options?.find(
		(option) => option.type === "select" && (option.category === "model" || option.id === "model"),
	);
	const providerModelChoices = useMemo(
		() => providerModelOption?.choices.map((choice) => ({ id: choice.value })),
		[providerModelOption],
	);
	// Resolve current Claude aliases before filtering so their labels cannot move
	// to an older enabled model when the current concrete entry is switched off.
	const resolveClaudeChoice = useMemo(
		() => isClaude && claudeCatalog ? claudeChoiceModel(claudeCatalog.models) : undefined,
		[isClaude, claudeCatalog],
	);
	const currentModels = [snapshot?.settings?.model, providerModelOption?.currentValue];
	const { models } = useOfferedModels(snapshot?.harness, catalogModels, {
		hostId,
		keep: [...currentModels, ...currentModels.map((id) => id ? resolveClaudeChoice?.(id)?.id : undefined)],
	});
	// Claude's choices are family aliases ("sonnet"); Settings names catalog ids.
	const claudeChoiceID = useMemo(
		() => resolveClaudeChoice && ((choice: { id: string }) => resolveClaudeChoice(choice.id)?.id ?? choice.id),
		[resolveClaudeChoice],
	);
	const { models: offeredProviderModels } = useOfferedModels(snapshot?.harness, providerModelChoices, { hostId, keep: [providerModelOption?.currentValue], modelID: claudeChoiceID });
	const offeredConfigOptions = useMemo(() => {
		if (!providerModelOption) return configOptions.options;
		const offered = new Set(offeredProviderModels.map((choice) => choice.id));
		return configOptions.options?.map((option) => option === providerModelOption
			? { ...option, choices: option.choices.filter((choice) => offered.has(choice.value)) }
			: option);
	}, [configOptions.options, offeredProviderModels, providerModelOption]);
	const { skills } = useConversationSkills(
		session.id,
		Boolean(controllerCatalogsEnabled && catalogsEnabled && snapshot),
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
		// The first answer after an agent switch recaps known state, PR links
		// included; opening those would pop the browser open on every switch.
		const switchStarts = agentSwitches
			.filter((entry) => entry.state !== "failed" && entry.requestedAt)
			.map((entry) => Date.parse(entry.requestedAt as string) - 5_000);
		const switchOpeningTurnIds = new Set<string>();
		for (const startedAt of switchStarts) {
			// Only a message sent with the switch, not the user's next one hours later.
			const opening = snapshot.items.find((item) =>
				item.kind === "message" && item.role === "user" && item.origin === "human" &&
				Boolean(item.turnId) && Date.parse(item.createdAt) >= startedAt &&
				Date.parse(item.createdAt) <= startedAt + 125_000);
			if (opening?.turnId) switchOpeningTurnIds.add(opening.turnId);
		}
		for (const item of snapshot.items) {
			if (
				item.kind !== "message" ||
				item.role !== "assistant" ||
				item.streaming ||
				pendingCompleted.get(item.id) !== item.revision
			) continue;
			if (item.turnId && switchOpeningTurnIds.has(item.turnId)) {
				pendingCompleted.delete(item.id);
				continue;
			}
			const url = firstBrowserLink(item.text, paths);
			if (url) {
				autoOpenedLinkSessions.add(uiSessionId);
				pendingCompleted.delete(item.id);
				autoOpenLinkInBrowser(url);
				break;
			}
		}
	}, [agentSwitches, autoOpenLinkInBrowser, isLoading, paths, snapshot, uiSessionId]);
	const observedSuccessfulSwitch = Boolean(
		agentSwitch &&
			observedSettledSwitchId === agentSwitch.id &&
			switchPresentation?.outcome === "success",
	);
	useEffect(() => {
		if (!observedSuccessfulSwitch || !agentSwitch || !switchPresentation) return;
		settleAgentSwitch(agentSwitch, switchPresentation);
	}, [agentSwitch, observedSuccessfulSwitch, settleAgentSwitch, switchPresentation]);
	// A switch sent from the composer reads as its message working, not as the
	// switch dialog's progress card: the bar already names the new agent.
	const composerSwitch = Boolean(
		(switchMutation.isPending && switchMutation.input?.message) || isComposerAgentSwitch(agentSwitch?.id),
	);
	const composerSwitchInProgress = composerSwitch && (switchMutation.isPending || switchPresentation?.outcome === "in_progress");
	const switchEcho = localEchos.find((echo) => echo.switching);
	// A composer switch that has not landed after a while shows its progress
	// card, so a stuck switch is visible.
	const switchStartedAt = switchEcho ? Date.parse(switchEcho.createdAt) : undefined;
	const [switchSlow, setSwitchSlow] = useState(false);
	useEffect(() => {
		if (!composerSwitchInProgress) {
			setSwitchSlow(false);
			return;
		}
		const remaining = SLOW_AGENT_SWITCH_MS - (Date.now() - (switchStartedAt ?? Date.now()));
		if (remaining <= 0) {
			setSwitchSlow(true);
			return;
		}
		const timer = setTimeout(() => setSwitchSlow(true), remaining);
		return () => clearTimeout(timer);
	}, [composerSwitchInProgress, switchStartedAt]);
	// Chat shows a switch in the transcript ("Switched from X to Y") and the
	// working message, not as a card. A failure, a switch awaiting recovery, or
	// one that is taking too long still gets one.
	const shownSwitchPresentation =
		(switchPresentation?.outcome === "failure" && dismissedFailureSwitchId !== agentSwitch?.id) ||
		switchPresentation?.outcome === "recovery" ||
		// A switch from the dialog, or one this window did not start, has no
		// message in the transcript to show it; its card explains the locked chat.
		((switchSlow || !composerSwitch) && switchPresentation?.outcome === "in_progress")
			? switchPresentation
			: undefined;
	// The switch a composer send is making right now, before or after the daemon accepts it.
	const liveComposerSwitch = switchMutation.isPending && switchMutation.input?.message
		? { from: switchMutation.input.session.provider, to: switchMutation.input.targetHarness }
		: composerSwitch && agentSwitch && agentSwitch.state !== "failed"
			? { from: agentSwitch.fromHarness, to: agentSwitch.targetHarness }
			: undefined;
	const agentSwitchMarkers = useMemo(() => {
		const markers = agentSwitches
			// A failed switch, or one awaiting recovery, has not handed the chat over.
			.filter((entry) =>
				entry.state !== "failed" && !(entry.errorCode && entry.state !== "completed") &&
				Boolean(entry.requestedAt ?? entry.updatedAt))
			.map((entry) => ({
				id: entry.id,
				from: entry.fromHarness,
				to: entry.targetHarness,
				at: (entry.requestedAt ?? entry.updatedAt) as string,
			}))
			.sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
		if (!switchEcho) return markers;
		// The row arrives with the message that made the switch, and stays above it
		// while the daemon's copy of the message replaces the local one.
		const echoAt = Date.parse(switchEcho.createdAt);
		let anchored = false;
		const pinned = markers.map((marker) => {
			if (Date.parse(marker.at) < echoAt - 5_000) return marker;
			anchored = true;
			return { ...marker, at: switchEcho.createdAt };
		});
		if (!anchored && liveComposerSwitch) {
			pinned.push({ id: `switching:${switchEcho.clientMessageId}`, ...liveComposerSwitch, at: switchEcho.createdAt });
		}
		return pinned;
	}, [agentSwitches, liveComposerSwitch?.from, liveComposerSwitch?.to, switchEcho]);
	const switchLocksChat = !composerSwitch && Boolean(
		switchPresentation?.lockAgentTerminal && !switchPresentation.allowSourceInput,
	);
	// A failed switch never delivered its message: take the echo down and give
	// the text back to an empty composer. Messages held across a reload count:
	// the switch they waited on is the one that failed.
	const heldMessages = readHeldSwitchMessages(uiSessionId);
	const heldPending = heldMessages.length > 0;
	const heldFor = heldMessages[0]?.heldFor;
	// After a reload the dock starts empty; held messages show there again.
	useEffect(() => {
		for (const message of readHeldSwitchMessages(uiSessionId)) {
			if (!message.clientMessageId || localEchos.some((echo) => echo.clientMessageId === message.clientMessageId)) continue;
			seedHeldLocalEcho(queryClient, session.id, hostId, {
				clientMessageId: message.clientMessageId,
				text: message.text,
				excerpts: message.excerpts,
				createdAt: new Date(message.heldFor?.at ?? Date.now()).toISOString(),
			});
		}
		// Once per session view; later holds seed their own echo.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [hostId, queryClient, session.id, uiSessionId]);
	// The newest switch decides where held messages go. After a reload nothing
	// marks the switch as the composer's, so this is the only proof it landed.
	const newestSwitch = useMemo(() => {
		// The switch just created has no timestamp only if the daemon left it out; it is the newest.
		const at = (entry: (typeof agentSwitches)[number]) =>
			Date.parse(entry.requestedAt ?? entry.updatedAt ?? "") || Number.MAX_SAFE_INTEGER;
		return agentSwitches.reduce<(typeof agentSwitches)[number] | undefined>(
			(newest, entry) => (!newest || at(entry) >= at(newest) ? entry : newest),
			undefined,
		);
	}, [agentSwitches]);
	// Held messages wait on one switch: toward the agent they were held for,
	// asked for around when they were held (clocks may differ on a remote host).
	const newestRequestedAt = Date.parse(newestSwitch?.requestedAt ?? "");
	const newestIsHeldSwitch = Boolean(newestSwitch) && (!heldFor || (
		newestSwitch?.targetHarness === heldFor.target &&
		(Number.isNaN(newestRequestedAt) || Math.abs(newestRequestedAt - heldFor.at) <= 30 * 60_000)
	));
	const heldSwitchLanded = !agentSwitching && newestIsHeldSwitch && newestSwitch?.state === "completed" &&
		newestSwitch.targetHarness === session.provider;
	// What was held waited on a switch that failed, even one whose card is gone,
	// or one the daemon never recorded.
	const heldSwitchLost = heldPending && !agentSwitching && !switchEcho && agentSwitchesQuery.isSuccess &&
		(newestSwitch?.state === "failed" || !newestIsHeldSwitch);
	const switchFailed = ((composerSwitch || heldPending) && switchPresentation?.outcome === "failure") || heldSwitchLost;
	useEffect(() => {
		if (!switchFailed) return;
		if (!switchEcho && readHeldSwitchMessages(uiSessionId).length === 0) return;
		if (flushingHeldSwitch.has(uiSessionId)) return;
		const held = takeHeldSwitchMessages(uiSessionId);
		for (const echo of [switchEcho, ...held]) {
			if (echo?.clientMessageId) commands.acknowledgeLocalEcho?.(undefined, echo.clientMessageId);
		}
		const text = [switchEcho?.text, ...held.map((message) => message.text)].filter(Boolean).join("\n\n");
		returnTextToComposer(uiSessionId, session.createdAt ?? uiSessionId, text);
	}, [commands, session.createdAt, switchEcho, switchFailed, uiSessionId]);
	// Once the new agent owns the chat, what was held goes to it in order. It
	// queues behind the switch's own turn like any send made while one runs.
	const heldReady = !composerSwitchInProgress && !switchFailed && heldSwitchLanded && targetChatControllerReady && !switchEcho;
	useEffect(() => {
		if (!heldReady || flushingHeldSwitch.has(uiSessionId)) return;
		const held = readHeldSwitchMessages(uiSessionId);
		if (!held.length) return;
		flushingHeldSwitch.add(uiSessionId);
		void (async () => {
			const unsent: string[] = [];
			try {
				for (const { heldFor: _heldFor, ...message } of held) {
					// The send shows its own echo in the dock; drop the held one in the same tick.
					if (message.clientMessageId) releaseSwitchLocalEcho(queryClient, session.id, hostId, message.clientMessageId);
					await commands.send(message).catch(() => {
						if (message.text.trim()) unsent.push(message.text);
					});
					// Stored until sent, so quitting midway loses nothing; a resend
					// reuses the message's id.
					dropHeldSwitchMessage(uiSessionId, message.clientMessageId);
				}
			} finally {
				flushingHeldSwitch.delete(uiSessionId);
			}
			// What the new agent did not take goes back to the composer.
			returnTextToComposer(uiSessionId, session.createdAt ?? uiSessionId, unsent.join("\n\n"));
		})();
	}, [commands, heldReady, hostId, queryClient, session.createdAt, session.id, uiSessionId]);
	const promptSuggestion = useConversationPromptSuggestion(snapshot, Boolean(
		targetChatControllerReady && !controllerBusy && !localEchos.length &&
		!commands.rollbackPending && !commands.editMessagePending && !commands.activateBranchPending && !commands.compacting &&
		!session.isTerminated && session.provisionState !== "failed" && session.provisionState !== "provisioning" &&
		!isLoading && !error && !refreshError && !unavailable &&
		!agentSwitching && !switchLocksChat && !handoffDialogOpen &&
		!controllerTransitioning && !agentResuming && !newWorkDisabled && !arriving &&
		!workspaceFileActive && !workspaceActiveTabKey && !reviewerTarget && !reviewerChatSelected && !shellTarget,
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
	const visibilityPresentationKind = agentSwitchVisibilityPresentationKind(shownSwitchPresentation);
	useAgentSwitchPresentationVisibility({
		localRouteKey: `session/${uiSessionId}`,
		agentSwitch,
		presentationKind: visibilityPresentationKind,
		visible: Boolean(
			shownSwitchPresentation &&
				(!isLoading || renderShellFallback) &&
				(!unavailable || renderShellFallback) &&
				!error &&
				renderSnapshot &&
				!workspaceFileActive,
		),
	});

	const settingsReady = Boolean(renderSnapshot) && !optimisticChat && (targetChatControllerReady || agentResuming) &&
		(!renderSnapshot || !can(renderSnapshot, "config_options") || configOptions.loaded);
	// The session's own settings have taken over; a later restart must not bring the launch chips back.
	useEffect(() => {
		if (settingsReady && launchSettings) forgetLaunchSettings(queryClient, session.id, hostId);
	}, [hostId, launchSettings, queryClient, session.id, settingsReady]);
	// Once the opening brief's turn has settled the chat is an ordinary one; a later
	// Undo of that turn must not bring the start-up state back.
	const openingSettled = Boolean(snapshot?.turns[0] && snapshot.turns[0].state !== "queued" && snapshot.turns[0].state !== "running");
	useEffect(() => {
		if (openingStartedAt && openingSettled) forgetOpeningStart(queryClient, session.id, hostId);
	}, [hostId, openingSettled, openingStartedAt, queryClient, session.id]);

	// The agent row in the model menu. Picking another agent only changes the bar;
	// the next send carries it.
	const switchAgent = useSwitchAgent();
	const sessionAgent = useSessionAgentSwitch({
		session,
		hostId,
		enabled: session.kind === "worker" && !session.cloud && !session.isTerminated &&
			session.provisionState !== "provisioning" && session.provisionState !== "failed",
	});
	const pendingAgent = sessionAgent.pending;
	const clearPendingAgent = sessionAgent.clear;
	// Once the new agent's own settings are up, its live bar takes over.
	const pendingAgentLanded = Boolean(
		pendingAgent && pendingAgent.harness === session.provider && settingsReady && !composerSwitchInProgress && !switchEcho,
	);
	useEffect(() => {
		if (pendingAgentLanded) clearPendingAgent();
	}, [clearPendingAgent, pendingAgentLanded]);
	// After a failed switch the chat stays with its agent; a picked target left
	// in place would make the next send quietly try the switch again.
	const failedSwitchId = switchFailed ? (agentSwitch?.id ?? newestSwitch?.id ?? "failed") : undefined;
	const clearedForSwitch = useRef<string | undefined>(undefined);
	useEffect(() => {
		if (!failedSwitchId || clearedForSwitch.current === failedSwitchId) return;
		clearedForSwitch.current = failedSwitchId;
		clearPendingAgent();
	}, [clearPendingAgent, failedSwitchId]);
	// Held still while the switch runs, as the new chat page holds its launch bar.
	const pendingAgentLocked = composerSwitchInProgress || Boolean(switchEcho) || pendingAgent?.harness === session.provider;
	const pendingAgentBar = pendingAgent ? (
		<TurnSettingsBar
			models={pendingAgent.models}
			settings={pendingAgent.settings}
			harness={pendingAgent.harness}
			// Locked, it still shows the agent and model the switch is starting.
			onChange={pendingAgentLocked ? ignorePendingAgentChange : pendingAgent.choose}
			disabled={pendingAgentLocked}
			showApprovalMode={false}
			agentMenu={pendingAgentLocked ? undefined : sessionAgent.agentMenu}
			agentIcon
		/>
	) : undefined;

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
				agentInputDisabled={switchLocksChat || handoffDialogOpen}
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
				// The switch stops one agent before the next starts; that gap is not a crash.
				agentSwitching={composerSwitchInProgress}
				loadingQuietly={optimisticChat && !optimisticArrival && session.provisionState !== "provisioning"}
				agentResuming={agentResuming}
				hasOlder={hasOlder}
				loadingOlder={isLoadingOlder}
				onLoadOlder={loadOlder}
				busy={commands.busy}
				agentMenu={pendingAgent ? undefined : sessionAgent.agentMenu}
				agentSwitchMarkers={agentSwitchMarkers}
				switchingEcho={switchEcho ? { clientMessageId: switchEcho.clientMessageId, startedAt: switchEcho.createdAt } : undefined}
				excerptsEnabled
				onSend={async (text, attachments, clientMessageId, excerpts) => {
					// Only claim /btw and /side where side chats can open. Elsewhere the
					// text goes to the agent as typed (it may be the harness's own skill),
					// so a failed side launch can never strand the main draft.
					const btw = sideCreationEnabled ? /^\/(?:btw|side)(?:\s+|$)/i.exec(text) : null;
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
					const message = {
						text,
						attachments,
						clientMessageId: clientMessageId ?? crypto.randomUUID(),
						excerpts: excerpts?.map((excerpt) => ({
							conversationId: excerpt.conversationId,
							messageId: excerpt.messageId,
							revision: excerpt.revision,
							text: excerpt.text,
						})),
					};
					// A switch is under way: hold this in the queue dock and send it to
					// the new agent once it owns the chat.
					if (composerSwitchInProgress || switchEcho) {
						holdSwitchMessage(uiSessionId, {
							...message,
							heldFor: liveComposerSwitch ? { target: liveComposerSwitch.to, at: Date.now() } : undefined,
						});
						seedHeldLocalEcho(queryClient, session.id, hostId, {
							clientMessageId: message.clientMessageId,
							text,
							excerpts: message.excerpts,
							createdAt: new Date().toISOString(),
						});
						return;
					}
					// Another agent is picked: this message rides the switch and is the
					// first thing the new agent answers.
					if (pendingAgent && pendingAgent.harness !== session.provider) {
						try {
							await switchAgent.mutateAsync({
								session,
								targetHarness: pendingAgent.harness,
								model: pendingAgent.model,
								effort: pendingAgent.effort,
								// The message's own id: a retried send cannot start a second switch.
								idempotencyKey: message.clientMessageId,
								message,
							});
						} catch (error) {
							// Messages held behind a switch that was not made follow this
							// one back to the composer, once the composer has it again.
							window.setTimeout(() => {
								const held = takeHeldSwitchMessages(uiSessionId);
								for (const entry of held) {
									if (entry.clientMessageId) releaseSwitchLocalEcho(queryClient, session.id, hostId, entry.clientMessageId);
								}
								returnTextToComposer(uiSessionId, session.createdAt ?? uiSessionId,
									held.map((entry) => entry.text).filter((text) => text.trim()).join("\n\n"));
							}, 0);
							throw error;
						}
						return;
					}
					return commands.send(message); }}
				commandError={commands.error}
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
				openingStartedAt={openingStartedAt}
				// A task started from the new chat page keeps its chips until these are ready.
				draftSettings={pendingAgentBar ?? (!settingsReady && launchSettings ? <LaunchSettingsBar {...launchSettings} /> : undefined)}
				models={models}
				onChooseSettings={hasProviderMode ? undefined : commands.chooseSettings}
				onRememberPermissions={can(renderSnapshot, "config_options") && !configOptions.loaded
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
			{shownSwitchPresentation ? (
				<ChatAgentSwitchStatus
					auxiliaryActive={Boolean(reviewerTarget || shellTarget)}
					onDismiss={
						shownSwitchPresentation.outcome === "failure" && agentSwitch
							? () => dismissAgentSwitchFailure(agentSwitch.id)
							: undefined
					}
					presentation={shownSwitchPresentation}
				/>
			) : null}
		</div>
	);
});

function ChatAgentSwitchStatus({
	auxiliaryActive,
	onDismiss,
	presentation,
}: {
	auxiliaryActive: boolean;
	onDismiss?: () => void;
	presentation: AgentSwitchPresentation;
}) {
	const { t } = useTranslation();
	const fullOverlay = presentation.lockAgentTerminal && !presentation.allowSourceInput && !auxiliaryActive;
	const warning = presentation.outcome === "failure" || presentation.outcome === "recovery";
	const success = presentation.outcome === "success";
	const inProgress = presentation.outcome === "in_progress";
	return (
		<div
			aria-busy={inProgress && presentation.animate ? true : undefined}
			aria-live="polite"
			className={cn(
				"pointer-events-none z-20 flex",
				fullOverlay
					? "absolute inset-0 items-center justify-center bg-background/75 backdrop-blur-[1px]"
					: "absolute inset-x-3 top-3 justify-center",
			)}
			data-outcome={presentation.outcome}
			data-testid="chat-agent-switch-status"
			role="status"
		>
			<div
				className={cn(
					"pointer-events-auto relative flex w-full max-w-lg items-start gap-3 rounded-lg border bg-surface/95 px-4 py-3 text-left shadow-lg",
					onDismiss && "pr-11",
					success
						? "border-success/40"
						: warning
							? presentation.tone === "danger"
								? "border-danger/40"
								: "border-warning/40"
							: "border-border",
				)}
			>
				{success ? (
					<CheckCircle2 aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-success" />
				) : warning ? (
					<AlertTriangle
						aria-hidden="true"
						className={cn(
							"mt-0.5 size-4 shrink-0",
							presentation.tone === "danger" ? "text-danger" : "text-warning",
						)}
					/>
				) : (
					<Loader2 aria-hidden="true" className="mt-0.5 size-4 shrink-0 animate-spin text-status-working" />
				)}
				<div className="min-w-0 flex-1">
					<strong className="block text-sm text-foreground">
						{t(presentation.titleKey, presentation.values)}
					</strong>
					<p className="mt-0.5 text-pretty text-xs leading-relaxed text-muted-foreground">
						{t(presentation.descriptionKey, presentation.values)}
					</p>
					{inProgress ? <AgentSwitchProgressTrack stage={presentation.stage} /> : null}
				</div>
				{onDismiss ? (
					<button
						aria-label={t("common.close")}
						className="absolute right-2 top-2 grid size-7 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent/50"
						onClick={onDismiss}
						type="button"
					>
						<X aria-hidden="true" className="size-icon-sm" />
					</button>
				) : null}
			</div>
		</div>
	);
}

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
