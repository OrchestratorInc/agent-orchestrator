import type { ChatExcerptSnapshot } from "../../../shared/chat-excerpt";
import { excerptSizeError } from "../../../shared/chat-excerpt";
import { captureExcerptSnapshot, captureExcerptTurn } from "../../lib/chat-excerpt-snapshot";
import { FileAnnotationContext } from "./FileAnnotationPreview";
import { useChatFind } from "../../hooks/useChatFind";
import { occurrenceKey, type FindOccurrence } from "../../lib/chat-find";
import "./chat-find.css";
import type { ChatFileReference, OpenFileAnnotation } from "../../../shared/chat-file-reference";
import { TurnWorkSummary } from "./TurnWorkSummary";
import { TurnSettleSnapshot } from "./TurnSettleSnapshot";
import { useChatDraftTranslation } from "../../lib/chat-draft-messages";
import { cloudChatStartingInPlace } from "../../lib/cloud-lifecycle";
import { isSettingsPageOpen } from "../../hooks/useAppShortcut";
import { useLatestCallback, useStableSet } from "../../hooks/useStable";
import { useEdgeFadeRef } from "../../hooks/useEdgeFade";
import { useTabListTabStop } from "../../hooks/useTabListTabStop";
/**
 * The Chat surface for a session whose persisted mode is `chat`.
 *
 * Rendered from a durable snapshot the daemon serves. The renderer stays thin:
 * items arrive already ordered by sequence and are never re-sorted here, turn
 * state comes from the daemon rather than being inferred from the last message,
 * and a decision is a typed action carrying the provider's request id.
 *
 * State this surface must be able to show, because each one happens: empty,
 * streaming, waiting on an approval, a command still running with no completion,
 * a turn the user stopped, a controller that died mid-turn, and history the user
 * has scrolled away from.
 */

import {
	memo,
	useContext,
	useCallback,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	useSyncExternalStore,
	type ComponentProps,
	type CSSProperties,
	type MutableRefObject,
	type KeyboardEvent as ReactKeyboardEvent,
	type MouseEvent as ReactMouseEvent,
	type PointerEvent as ReactPointerEvent,
	type ReactNode,
	type WheelEvent as ReactWheelEvent,
} from "react";
import { Archive, ArrowDown, Columns2, FlaskConical, ListTodo, Loader2, NotebookPen, Presentation, Route, ScanSearch, TriangleAlert, Undo2, WandSparkles } from "@/components/app-icons";
import { AnimatePresence, motion, Reorder, useReducedMotion } from "motion/react";
import { useTranslation } from "react-i18next";
import { ChatSelectionActions } from "./ChatSelectionActions";
import { selectionRangesEqual } from "../../lib/selection-toolbar";
import { useScrollFollow } from "../../hooks/useScrollFollow";
import {
	defaultRangeExtractor,
	measureElement,
	observeElementRect,
	useVirtualizer,
	type Virtualizer,
} from "@tanstack/react-virtual";
import { isShownAORender } from "../../lib/mcp-tool";
import { isOrchestratorPlumbing } from "./activity-step";
import { cn } from "../../lib/utils";
import { sessionBranchPresentation } from "../../lib/session-branch";
import { annotationBody, annotationTextMatches, annotationReferenceRange, annotationDiagramBlock, highlightChatAnnotation } from "../../lib/chat-annotation-navigation";
import {
	acknowledgeChatInlineEditMutation,
	beginChatInlineEditMutation,
	cancelChatInlineEditMutation,
	clearAcceptedChatInlineEdit,
	clearRejectedChatInlineEditDelivery,
	clearUncertainChatInlineEditDelivery,
	finishChatInlineEditMutation,
	getChatInlineEditMutation,
	markChatInlineEditDeliveryAccepted,
	prepareChatInlineEditDelivery,
	activateChatDraftScope,
	chatDraftScopeKey,
	chatQueuedAttachmentScopeKey,
	readChatSessionDraft,
	subscribeChatDraftRuntime,
	writeChatInlineEdit,
	writeChatExcerptReferences,
	writeChatQueuedEdit,
	type ChatDraftQueuedEdit,
	type ChatDraftAttachment,
	type ChatDraftExcerptReference,
	type ChatDraftRetainedAttachment,
	type DraftClearResult,
	type ChatDraftInlineEdit,
	type ChatDraftScope,
	type ChatInlineEditDelivery,
} from "../../lib/chat-drafts";
import { purgeFileAttachments, purgeFileAttachmentsForSession } from "../../hooks/useFileAttachments";
import { setChatDraftBoundary } from "../../lib/chat-draft-boundary";
import { sameContent, useStableList } from "../../lib/stable-list";
import { useTabScrollEdges } from "../../hooks/useTabScrollEdges";
import { useSidebarChromeClearanceRef } from "../../hooks/useSidebarChromeGeometry";
import { apiErrorCode, getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { isDialogOrMenuOpen } from "../../lib/dom-selectors";
import { clampTerminalFontSize, initialTerminalFontSize, terminalFontSizeStorageKey } from "../../lib/terminal-font-size";
import { createTerminalMux, muxUrlFromApiBase } from "../../lib/terminal-mux";
import { isLinuxPlatform, isMacPlatform } from "../../lib/platform";
import { handleTerminalTabListKeyDown } from "../../lib/terminal-tabs";
import { agentLabel } from "../../lib/agent-options";
import type { ApprovalMode } from "../../types/conversation";
import type { ConversationLocalEcho } from "../../hooks/useConversation";
import type { ShellTerminal } from "../../hooks/useShellTerminals";
import { inspectorIsOpen, sidebarOccupiesLayout, useUiStore } from "../../stores/ui-store";
import type { TerminalTarget } from "../../types/terminal";
import {
	isOrchestratorSession,
	sessionIsActive,
	type SessionKind,
	type WorkspaceSession,
	type SessionProvisionStep,
} from "../../types/workspace";
import { AgentAvatar } from "../AgentAvatar";
import { DraggableWorkspaceTab, SessionPaneTab, SessionTabSlot } from "../CenterPane";
import { useSplitCloseFocused, useSplitHiddenTabKeys } from "../split-pane-context";
import { TabContextMenu, type TabMenuSpec } from "../TabContextMenu";
import { Button } from "../ui/button";
import { ConfirmDialog } from "../ConfirmDialog";
import { SessionTopbarPortal } from "../SessionTopbarPortal";
import { SessionTabsTopbar } from "../PageTopbar";
import { ShellTerminalTab } from "../ShellTerminalTab";
import { TerminalPane } from "../TerminalPane";
import {
	ActivityRow,
	ApprovalCard,
	AssistantMessage,
	CompactionMarker,
	ProviderSwitchMarker,
	ContinuedMarker,
	HumanMessage,
	humanBubbleClass,
	humanVisibleText,
	OriginMessage,
	SteerMessage,
	TurnActions,
	TurnResults,
	TurnOutcome,
	LiveResponseStatus,
	LiveStepChoices,
	isLiveStepKind,
	isModelReroute,
	liveTaskLabel,
	reconnectCopy,
	stepInProgress,
	type TurnOutcomeRetryControl,
	ChatHostId,
} from "./ChatTimelineItems";
import {
	FromAgent,
	isTaskTitleUpdate,
	isWorkerLead,
	isDuplicateSpawnAction,
	relayedVia,
	renamedTitle,
	SpawnedAgents,
	spawnedSessionIds,
	TaskNamedLine,
	useWorkerUpdateIndex,
	WorkerUpdateIndexContext,
	BatchedReportsContext,
	type BatchedReports,
	placeBatchedReports,
	ReportsLine,
	type WorkerUpdate,
	WorkerUpdateRows,
	workerUpdatesOf,
} from "./WorkerUpdates";
import { AutomationMessage, SteeredBrowserFeedback, steeredBrowserFeedback } from "./AutomationMessage";
import { HumanMessageEditor } from "./HumanMessageEditor";
import { ChatLinkProvider, ChatOpeningRef } from "./ChatMarkdown";
import { admitArrivals, ChatEntrance, createChatEntranceGate } from "./chat-entrance";
import { pageHidden } from "../../lib/page-hidden";
import { ChatImageSourceProvider } from "./chat-image-source";
import type { AgentSwitchProps } from "./AgentSwitchPicker";
import type { ComposerPromptSuggestion } from "./ComposerEditor";
import { ChatTokenSkills } from "./sentComposerTokens";
import { ChatComposer, type ChatComposerHandle, type StoredComposerAttachment } from "./ChatComposer";
import { ContextMeter } from "./ContextMeter";
import { stagedAttachmentParts, attachmentName } from "./messageAttachments";
import type { QueuedMessageEditOptions } from "../../types/conversation";
import { QueuedMessageDock, type QueuedMessage } from "./QueuedMessageDock";
import { SessionStartup } from "./SessionStartup";
import { ActivityRun } from "./ActivityRun";
import { isOrdinaryActivity } from "./activity-action";
import { isFailedCall } from "./activity-command";
import { TurnPlan } from "./TurnPlan";
import { ComposerModeItems, TurnSettingsBar, type ApprovalModeLimits } from "./TurnSettingsBar";
import { ElicitationDock, ElicitationHistory } from "./ElicitationDock";
import { agentAccountProviderFor } from "../../hooks/useAgentAccounts";
import { ImportedAgentBlocked, McpServerBanner, ReauthBanner, ThreadStateBanner, VersionSwitchBanner } from "./ChatStatusBanners";
import type { AgentBlock } from "../../lib/session-import";
import { UsageLimitNotice } from "./UsageLimitNotice";
import {
	activeTurn,
	activityPlan,
	brokenMcpServers,
	can,
	isCompaction,
	isSteer,
	fullQueuedTurnOrder,
	hiddenTimelineTurnIds,
	isUserQueuePrompt,
	needsReauth,
	queuedTurnIds,
	type BatchedDelivery,
	type ConversationPlan,
	type ConversationSnapshot,
	type ControllerState,
	type ChatConfigOption,
	type ChatConfigOptionValue,
	type ChatModel,
	type ChatSkill,
	type ChatSteerOutcome,
	type ChatEditOutcome,
	type ConversationActivity,
	type ConversationBranchPoint,
	type ConversationItem,
	type ConversationMessage,
	type ConversationTurn,
	type ConversationUsageLimit,
	type TurnArtifactChange,
	type TurnDiff,
	type TurnSettings,
} from "../../types/conversation";

const NO_SKILLS: ChatSkill[] = [];

const EMPTY_CHAT_PLACEHOLDERS = [
	"Fix a failing test in this project",
	"Explain how this project is structured",
	"Plan the next step for this feature",
	"Find and fix a bug in this project",
] as const;

/**
 * The newest pending approval or question the live turn is waiting on.
 *
 * A request with no turn id belongs to the session rather than a turn, so a
 * turn-scoped one always wins; between equals the later sequence is the live one.
 */
function latestPendingInteraction(
	items: ConversationItem[],
	activityKind: "approval" | "user_input",
	turn: ConversationTurn | undefined,
): ConversationActivity | undefined {
	return items.reduce<ConversationActivity | undefined>((latest, item) => {
		if (
			item.kind !== "activity" ||
			item.activityKind !== activityKind ||
			item.status !== "pending" ||
			(item.turnId ? item.turnId !== turn?.id : !turn)
		) {
			return latest;
		}
		if (latest?.turnId && !item.turnId) return latest;
		if (item.turnId && !latest?.turnId) return item;
		return !latest || item.sequence > latest.sequence ? item : latest;
	}, undefined);
}

const CHAT_FONT_SIZE_DEFAULT = 14;

const WHEEL_ZOOM_THRESHOLD = 80;
const WHEEL_ZOOM_RESET_MS = 250;

// The daemon enriches this request with the interrupted task and response tail.
// Continue starts another turn in the existing conversation; it is not an exact
// execution checkpoint.
export const CONTINUE_STOPPED_TURN_PROMPT = "Continue from where you stopped.";

/** The latest turn that still counts is one the user stopped. */
export function continuableTurnId(snapshot: ConversationSnapshot): string {
	return snapshot.continueTurnId ?? "";
}

export interface ChatRetryControl {
	retry: (turnId: string) => void | Promise<unknown>;
	pending?: boolean;
	error?: string;
	turnId?: string;
	setAutoResume?: (turnId: string, enabled: boolean) => void | Promise<unknown>;
	autoResumePending?: boolean;
	autoResumeError?: string;
	autoResumeTurnId?: string;
}

type ReviewerTerminalTarget = Extract<TerminalTarget, { kind: "reviewer" }>;
type ShellTerminalTarget = Extract<TerminalTarget, { kind: "shell" }>;
type WorkspaceTab = { key: string; content: ReactNode; onSelect: () => void; onClose?: () => void; menu?: TabMenuSpec };
type ChatAuxiliaryTab =
	| { key: string; kind: "reviewer"; terminal: { handleId: string; harness: string } }
	| { key: string; kind: "reviewer-chat"; terminal: { reviewId: string; harness: string } }
	| { key: string; kind: "shell"; terminal: ShellTerminal }
	| { key: string; kind: "workspace"; tab: WorkspaceTab };

// The tab strip re-renders with every session view update. A shell tab only
// changes with its shell or selection, so it skips the rest; with a dozen
// tabs this is most of the strip's render work.
const ShellTerminalTabEntry = memo(function ShellTerminalTabEntry({
	tabKey,
	shell,
	isActive,
	onSelect,
	onClose,
	onRename,
	stripShellHandleIds,
}: {
	tabKey: string;
	shell: ShellTerminal;
	stripShellHandleIds: readonly string[];
	isActive: boolean;
	onSelect: (handleId: string) => void;
	onClose: (handleId: string) => void;
	onRename?: (handleId: string, title: string) => void;
}) {
	return (
		<DraggableWorkspaceTab value={tabKey}>
			<ShellTerminalTab
				appearance="connected"
				isActive={isActive}
				onClose={() => onClose(shell.handleId)}
				onRename={onRename ? (title) => onRename(shell.handleId, title) : undefined}
				onSelect={() => onSelect(shell.handleId)}
				shell={shell}
				stripShellHandleIds={stripShellHandleIds}
			/>
		</DraggableWorkspaceTab>
	);
});

const isMac = isMacPlatform();
const isLinux = isLinuxPlatform();

type MessageEditDraft = ChatDraftInlineEdit;

/**
 * A stream refresh replaces the complete snapshot object. Queue prompts do not
 * change while a running turn emits output, so retain the previous list when its
 * contents are equal and avoid redrawing the dock and composer subtree.
 */
function useQueuedMessages(snapshot: ConversationSnapshot, excludeTurnId?: string): QueuedMessage[] {
	const previous = useRef<QueuedMessage[]>([]);
	return useMemo(() => {
		const messagesByTurn = new Map(
			snapshot.items
				.filter((item): item is ConversationMessage => isUserQueuePrompt(item) && Boolean(item.turnId))
				.map((message) => [message.turnId as string, message]),
		);
		const next = snapshot.turns.flatMap((queuedTurn) => {
			if (queuedTurn.state !== "queued" || queuedTurn.id === excludeTurnId) return [];
			const message = messagesByTurn.get(queuedTurn.id);
			return message ? [{ turnId: queuedTurn.id, message }] : [];
		});
		const current = previous.current;
		if (
			current.length === next.length &&
			current.every(
				(entry, index) =>
					entry.turnId === next[index]?.turnId && sameContent(entry.message, next[index]?.message),
			)
		) {
			return current;
		}
		previous.current = next;
		return next;
	}, [excludeTurnId, snapshot.items, snapshot.turns]);
}

export interface ChatWorkspaceProps {
	onAddToSideChat?: (excerpt: ChatDraftExcerptReference) => void | Promise<void>;
	checkingSideChatAvailability?: boolean;
	annotationNavigationRequest?: { id: string; text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number };
	/** Called once the chat applied annotationNavigationRequest, so its owner drops it rather than have a later mount replay it. */
	onAnnotationNavigationHandled?: (id: string) => void;
	snapshot: ConversationSnapshot;
	promptSuggestion?: ComposerPromptSuggestion;
	/** Renderer-owned state identity; the snapshot's sessionId remains the daemon wire ID. */
	uiSessionId?: string;
	/** Durable draft owner for a conversation that is not a worker session. */
	draftOwner?: ChatDraftScope;
	/** The session title from the sidebar (matches what users see in the left sidebar) */
	sessionTitle?: string;
	/** The AO role using this shared conversation surface. */
	sessionRole?: SessionKind;
	/** Host-specific proxy origin for images and staged attachments. */
	assetBaseUrl?: string;
	/** Selected remote host, even while its connection has no proxy origin. */
	remoteHostId?: string;
	/** Session-level actions owned above the conversation surface. */
	headerActions?: ReactNode;
	/** Agent-session actions on the primary chat tab (interface switch, handoff). */
	sessionTabAction?: ReactNode;
	/** Widen the primary tab action slot only while an interface switch spinner is showing. */
	sessionTabActionPinned?: boolean;
	/** Pinned beside the tab strip, before the workspace topbar actions. */
	tabStripAction?: ReactNode;
	/** File tabs coordinated by SessionView, appended to the native chat tab strip. */
	workspaceTabs?: WorkspaceTab[];
	workspaceTabActions?: ReactNode;
	workspaceActiveTabKey?: string;
	/** Session-owned order shared with the terminal UI surface. */
	auxiliaryTabOrder?: string[];
	onAuxiliaryTabOrderChange?: (keys: string[]) => void;
	/** Suppress a transient stopped snapshot while a mode handoff installs Chat. */
	controllerTransitioning?: boolean;
	/** The handoff is an account move: the chat stays as it is, with no arrival chrome. */
	accountMoving?: boolean;
	accountMoveQueues?: boolean;
	/** The conversation is still being read on a plain open or navigation; hold the composer without the startup shimmer. */
	loadingQuietly?: boolean;
	/** The history hasn't been read yet and may hold messages: arrive as in a chat that has some, never greet it as a new one. */
	historyPending?: boolean;
	/**
	 * A stopped agent is being resumed after the chat opened. Unlike a mode
	 * handoff, the history is final, so it stays readable; only sending waits.
	 */
	agentResuming?: boolean;
	startingSteps?: readonly SessionProvisionStep[];
	settingsReady?: boolean;
	/** The agent's own settings have not arrived; the bar shows the saved ones, disabled. */
	settingsLoading?: boolean;
	/** The model this chat's agent last reported running, named while its settings load. */
	lastModel?: string;
	/** Freeze agent-owned Chat controls while a durable session mutation owns input. */
	agentInputDisabled?: boolean;
	/** Fence new agent work without blocking decisions required by the current turn. */
	newWorkDisabled?: boolean;
	/**
	 * A Freestyle cloud orchestrator being created, before its session exists:
	 * drawn as that session's in-place start (prompt suggestions and the queue
	 * placeholder) so the handoff to it changes nothing on screen.
	 */
	pendingInPlaceStart?: boolean;
	reviewerTerminal?: { handleId: string; harness: string };
	onOpenReviewerTerminal?: (target: { handleId: string; harness: string }) => void;
	reviewerChat?: { reviewId: string; harness: string };
	onOpenReviewerChat?: (target: { reviewId: string; harness: string }) => void;
	/** A typed reviewer owns the body while this worker surface remains mounted. */
	reviewerChatSelected?: boolean;
	/** The parent surface owns the shared session tab strip. */
	hideHeader?: boolean;
	inlineHeader?: boolean;
	/** Older durable history is available but not loaded into the DOM yet. */
	hasOlder?: boolean;
	loadingOlder?: boolean;
	onLoadOlder?: () => void;
	/** The surface's onSend forwards transcript excerpts. Without it, "Add to
	 * chat" is hidden so a selection cannot be cleared from the draft unsent. */
	excerptsEnabled?: boolean;
	fileReferencesEnabled?: boolean;
	fileReferenceDelivery?: import("../../../shared/chat-file-reference").FileReferenceDelivery;
	messageByteLimit?: number;
	onSend?: (
		text: string,
		attachments?: { mimeType: string; data: string }[],
		clientMessageId?: string,
		excerpts?: ChatDraftExcerptReference[],
		fileReferences?: ChatFileReference[],
		/** With text delivery, the message as typed, before its file captures were inlined into `text`. */
		displayText?: string,
	) => void | Promise<unknown>;
	onDecide?: (requestId: string, decisionId: string) => void;
	onResolveInput?: (
		requestId: string,
		action: "accept" | "decline" | "cancel",
		content?: Record<string, unknown>,
	) => Promise<unknown> | void;
	onInterrupt?: () => void;
	/**
	 * Continues the latest turn after the user stopped it, sending
	 * CONTINUE_STOPPED_TURN_PROMPT as a continuation. Offered on the composer's
	 * button while that turn is the latest and nothing is running.
	 */
	onContinueTurn?: () => Promise<unknown> | void;
	commandError?: string;
	onResumeAgent?: () => void | Promise<unknown>;
	resumingAgent?: boolean;
	resumeError?: string;
	/** resumeError says another app still holds this chat. */
	resumeOpenElsewhere?: boolean;
	onOpenShell?: () => void;
	openingShell?: boolean;
	shellError?: string;
	/** Open an HTTP(S) link in this session's AO Browser panel. */
	onLinkOpen?: (url: string) => void;
	onSessionLinkOpen?: (url: string) => void;
	/** A send or decision is in flight. */
	busy?: boolean;
	/** The provider's model catalog. Empty hides the model control. */
	models?: ChatModel[];
	/** Makes the model control one picker for agent, model and effort. */
	agentSwitch?: AgentSwitchProps;
	/** A line above the chat box, such as a switch waiting for the next message. */
	composerNotice?: ReactNode;
	/**
	 * Locks the chat box with this placeholder, as while someone else in a
	 * shared session is writing to the agent. Decisions the current turn needs
	 * stay available.
	 */
	composerLockedPlaceholder?: string;
	/** The snapshot is a saved copy, so its controller state says nothing about the agent now. */
	savedCopy?: boolean;
	importBlock?: AgentBlock;
	/** Every edit of the chat box's draft text or move of its caret. */
	onComposerInput?: (text: string, caret?: number) => void;
	/**
	 * Shows the turn settings (model, effort, mode) read-only, as for someone
	 * in a shared session whose owner sets them.
	 */
	settingsLocked?: boolean;
	/** The AO session this surface renders for. Used to attach the reviewer pane. */
	session?: WorkspaceSession;
	/** Refresh the owning workspace cache after the shared primary tab is renamed. */
	onSessionRenamed?: () => void | Promise<void>;
	/** The selected reviewer pane. Kept even while its tab is temporarily unavailable. */
	reviewerTarget?: ReviewerTerminalTarget;
	/** Switch the active tab back to the chat timeline. */
	onSelectChat?: () => void;
	/** This session's standalone shells, rendered as tabs after the reviewer. */
	shellTerminals?: ShellTerminal[];
	/** The selected shell pane, if any. Mirrors reviewerTarget. */
	shellTarget?: ShellTerminalTarget;
	onSelectShellTerminal?: (handleId: string) => void;
	onCloseShellTerminal?: (handleId: string) => void;
	onRenameShellTerminal?: (handleId: string, title: string) => void;
	/** Daemon readiness for the reviewer terminal pane. */
	daemonReady?: boolean;
	/** Resolved color theme for the reviewer terminal pane. */
	theme?: "light" | "dark";
	/** `picked` names the setting the user chose from its own menu; other changes echo the shown settings. */
	onChooseSettings?: (settings: TurnSettings, picked?: keyof TurnSettings) => void;
	/** `null` forgets the project's remembered mode. */
	onRememberPermissions?: (mode: ApprovalMode | null) => Promise<unknown> | void;
	showApprovalMode?: boolean;
	approvalModes?: ApprovalMode[];
	/** Approval modes the session allows that the selected model cannot run. */
	approvalModeLimits?: ApprovalModeLimits;
	rememberPermissionsPending?: boolean;
	rememberPermissionsError?: string;
	rememberedPermissionMode?: ApprovalMode;
	/** Live provider-owned options, such as ACP model, effort, mode, and fast mode. */
	configOptions?: ChatConfigOption[];
	onChooseConfigOption?: (
		optionId: string,
		value: ChatConfigOptionValue,
	) => Promise<unknown> | void;
	configOptionPending?: boolean;
	configOptionError?: string;
	/** Summarize earlier history to reclaim context. */
	onCompact?: () => void;
	/** A compaction is running provider-side. It takes seconds, not milliseconds. */
	compacting?: boolean;
	/** Why compaction is not available right now, from the daemon's typed refusal. */
	compactUnavailable?: string;
	/**
	 * Discard a turn and everything after it. Absent means the agent cannot undo,
	 * and the affordance is not drawn at all rather than shown and then refused.
	 */
	onRollback?: (turnId: string) => void | Promise<unknown>;
	rollbackPending?: boolean;
	rollbackError?: string;
	/** Opens the session Files inspector from a turn's changed-files Review control. */
	onOpenFiles?: () => void;
	/** Opens the Files inspector focused on one changed path. */
	onOpenFile?: (path: string, line?: number) => void;
	/** Opens one of the session's artifacts in the inspector's Artifacts tab, by its artifact path. */
	onOpenArtifact?: (target: { path: string }) => void;
	onOpenFileAnnotation?: OpenFileAnnotation;
	/**
	 * Re-dispatch a failed turn's durable prompt as a new turn. Offered only for
	 * eligible failed human turns, so the affordance is drawn on the failed-turn
	 * boundary and never for a turn that is running or already succeeded.
	 */
	retryControl?: ChatRetryControl;
	/** Create a conversation branch by replacing a prior human prompt. */
	onEditMessage?: (
		turnId: string,
		text: string,
		clientMessageId?: string,
	) => void | ChatEditOutcome | Promise<ChatEditOutcome | void>;
	editMessagePending?: boolean;
	editMessageError?: string;
	/** Switch the visible conversation to another branch. */
	onActivateBranch?: (branchId: string) => void | Promise<unknown>;
	activateBranchPending?: boolean;
	activateBranchError?: string;
	/** The provider's skills. Empty leaves `/` an ordinary character. */
	skills?: ChatSkill[];
	/** Worktree paths offered for `@`. */
	filePaths?: string[];
	/** Told what follows "@" in the composer, for files that are searched rather than listed (see ChatComposer). */
	onFileQuery?: (query: string) => void;
	/** Something the chat will run in is still starting (a Cloud sandbox started ahead of the brief): the composer shows it. */
	composerStarting?: boolean;
	/** The path list was capped by the daemon rather than being all of them. */
	filePathsTruncated?: boolean;
	/** Renderer-only human messages awaiting their exact durable counterpart. */
	localEchos?: ConversationLocalEcho[];
	/**
	 * Writes staged images into the worktree and answers with the paths the agent
	 * can open. Absent means no attach control is offered — the fixture preview has
	 * no worktree to write into.
	 */
	onStageAttachments?: (attachments: { mimeType: string; data: string; name?: string }[], signal?: AbortSignal) => Promise<string[]>;
	/** Staged files are uploaded to AO Cloud, up to maxBytes each; see ChatComposer. */
	cloudAttachments?: { maxBytes: number };
	/** The provider negotiated native image prompt blocks. */
	nativeImages?: boolean;
	/**
	 * Deliver guidance into the turn already running, instead of queueing a message
	 * behind it. Absent means this harness cannot steer and no control is drawn —
	 * Claude answers `CHAT_STEER_UNSUPPORTED`, and an affordance that only ever fails
	 * is worse than none.
	 */
	onSteer?: (
		text: string,
		attachments?: { mimeType: string; data: string }[],
		clientMessageId?: string,
		recoverOnly?: boolean,
	) => Promise<ChatSteerOutcome | void>;
	showSteerButton?: boolean;
	sendPending?: boolean;
	steerPending?: boolean;
	/** Why the last steer was refused, from the daemon's typed answer. */
	steerRefusal?: string;
	onPromoteQueuedTurn?: (turnId: string) => Promise<unknown>;
	onEditQueuedTurn?: (turnId: string, text: string, options?: QueuedMessageEditOptions) => Promise<unknown>;
	onCancelQueuedTurn?: (turnId: string) => Promise<unknown>;
	onReorderQueuedTurns?: (turnIds: string[]) => Promise<unknown>;
	promoteQueuedTurnPendingTurnId?: string;
	cancelQueuedTurnPendingTurnId?: string;
	editQueuedTurnPendingTurnId?: string;
	/** Replaces the turn settings bar, for a chat whose session does not exist yet. */
	draftSettings?: ReactNode;
	/** Shown under the composer, for a chat whose session does not exist yet. */
	draftNotice?: ReactNode;
	/**
	 * When the user sent the brief that started this chat. Until the first turn
	 * settles the chat reads as working from that moment, with no connecting banner.
	 */
	openingStartedAt?: string;
	/** A tray tucked under the composer while the chat is empty, e.g. the new chat page's project picker. */
	composerTray?: ReactNode;
	/** Off where another flow owns an unanswered send, such as the new chat page's pending launch. */
	parkUnsentSends?: boolean;
	/** An empty header strip sits above: center the empty chat in the window, not below the strip. */
	centerInWindow?: boolean;
	/** The project has a setup script, so the start-up checklist lists that step. */
	showSetupStep?: boolean;
	/** Keep files the workspace cannot take yet in the composer, sent with the message. */
	holdAttachments?: boolean;
	onHeldAttachmentsChange?: ComponentProps<typeof ChatComposer>["onHeldAttachmentsChange"];
}

type ChatWorkspaceActivation =
	| { key: string; state: "active" }
	| { key: string; state: "failed"; reason: "obsolete" | "storage" };

function OfflineRemoteTerminal() {
	const { t } = useTranslation();
	return <div className="flex h-full items-center justify-center text-sm text-muted-foreground" role="status">{t("remote.hostOffline")}</div>;
}

/**
 * Do not mount any renderer draft owner until the daemon incarnation has
 * authoritatively claimed its storage scope. The activation transition itself
 * owns cleanup of the predecessor; callbacks from that obsolete surface can
 * then only fail closed against the successor lease. If storage alone fails,
 * Chat still mounts, but with an in-memory composer that owns no saved drafts.
 */
export function ChatWorkspace(props: ChatWorkspaceProps) {
	const { snapshot, session, draftOwner, uiSessionId = draftOwner?.sessionId ?? snapshot.sessionId } = props;
	const draftScope = useMemo<ChatDraftScope>(
		() => draftOwner ?? ({
			sessionId: uiSessionId,
			// Live surfaces carry the daemon-created session timestamp. Snapshot-only
			// fixtures retain the legacy logical scope for deterministic previews.
			incarnation: session?.createdAt ?? uiSessionId,
		}),
		[draftOwner, session?.createdAt, uiSessionId],
	);
	const scopeKey = chatDraftScopeKey(draftScope);
	const [activation, setActivation] = useState<ChatWorkspaceActivation>();
	const [activationAttempt, setActivationAttempt] = useState(0);
	const retryDraftPersistence = useCallback(() => setActivationAttempt((attempt) => attempt + 1), []);

	useLayoutEffect(() => {
		// Snapshot-only previews have no daemon session incarnation to arbitrate.
		// Their legacy logical scope remains isolated to fixture/demo surfaces.
		if (!session?.createdAt && !draftOwner) {
			setActivation({ key: scopeKey, state: "active" });
			return;
		}
		const result = activateChatDraftScope(draftScope);
		if (!result.ok) {
			setActivation({ key: scopeKey, state: "failed", reason: result.reason });
			return;
		}
		if (result.replaced) purgeFileAttachmentsForSession(draftScope.sessionId);
		setActivation({ key: scopeKey, state: "active" });
	}, [activationAttempt, draftOwner, draftScope, scopeKey, session?.createdAt]);

	const obsolete = activation?.key === scopeKey && activation.state === "failed" && activation.reason === "obsolete";
	if (activation?.key !== scopeKey || obsolete) {
		return (
			<section
				aria-label="Chat"
				className="cursor-chat-surface flex h-full min-h-0 flex-col items-center justify-center px-6 [font-size:var(--chat-font-size)]"
				data-session-mode={snapshot.mode}
				style={{ "--chat-font-size": `${CHAT_FONT_SIZE_DEFAULT}px` } as CSSProperties}
			>
				{obsolete ? (
					<div className="max-w-lg rounded-lg border border-border bg-card p-4">
						<p className="text-xs text-foreground" role="alert">
							This Chat view belongs to an older session incarnation. Reopen the current session to continue.
						</p>
					</div>
				) : null}
			</section>
		);
	}

	const draftPersistenceAvailable = activation.state === "active";
	return (
		<ChatWorkspaceContent
			key={scopeKey}
			{...props}
			draftScope={draftScope}
			draftPersistenceAvailable={draftPersistenceAvailable}
			onRetryDraftPersistence={draftPersistenceAvailable ? undefined : retryDraftPersistence}
		/>
	);
}

function ChatWorkspaceContent({
	onAddToSideChat,
	checkingSideChatAvailability,
	annotationNavigationRequest,
	onAnnotationNavigationHandled,
	snapshot,
	promptSuggestion,
	sessionTitle,
	sessionRole = "worker",
	assetBaseUrl,
	remoteHostId,
	headerActions,
	sessionTabAction,
	sessionTabActionPinned = false,
	tabStripAction,
	workspaceTabs,
	workspaceTabActions,
	workspaceActiveTabKey,
	auxiliaryTabOrder,
	onAuxiliaryTabOrderChange,
	controllerTransitioning,
	accountMoving = false,
	accountMoveQueues = false,
	loadingQuietly,
	historyPending = false,
	agentResuming = false,
	startingSteps,
	settingsReady = true,
	settingsLoading = false,
	lastModel,
	agentInputDisabled = false,
	newWorkDisabled = false,
	pendingInPlaceStart = false,
	reviewerTerminal,
	onOpenReviewerTerminal,
	reviewerChat,
	onOpenReviewerChat,
	reviewerChatSelected = false,
	hideHeader = false,
	inlineHeader = false,
	session,
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
	hasOlder,
	loadingOlder,
	onLoadOlder,
	excerptsEnabled = false,
	fileReferencesEnabled = false,
	fileReferenceDelivery,
	messageByteLimit,
	onSend,
	onDecide,
	onResolveInput,
	onInterrupt,
	onContinueTurn,
	commandError,
	onResumeAgent,
	resumingAgent,
	resumeError,
	resumeOpenElsewhere,
	onOpenShell,
	openingShell,
	shellError,
	onLinkOpen,
	onSessionLinkOpen,
	busy,
	models,
	agentSwitch,
	composerNotice,
	composerLockedPlaceholder,
	savedCopy = false,
	importBlock,
	settingsLocked = false,
	onComposerInput,
	onChooseSettings,
	onRememberPermissions,
	showApprovalMode,
	approvalModes,
	approvalModeLimits,
	rememberPermissionsPending,
	rememberPermissionsError,
	rememberedPermissionMode,
	configOptions,
	onChooseConfigOption,
	configOptionPending,
	configOptionError,
	onCompact,
	compacting,
	compactUnavailable,
	onRollback,
	rollbackPending,
	rollbackError,
	onOpenFiles,
	onOpenFile,
	onOpenArtifact,
	onOpenFileAnnotation,
	retryControl,
	onEditMessage,
	editMessagePending,
	editMessageError,
	onActivateBranch,
	activateBranchPending,
	activateBranchError,
	skills,
	filePaths,
	filePathsTruncated,
	onFileQuery,
	composerStarting = false,
	localEchos,
	onStageAttachments,
	cloudAttachments,
	nativeImages,
	onSteer,
	showSteerButton,
	sendPending,
	steerPending,
	steerRefusal,
	onPromoteQueuedTurn,
	onEditQueuedTurn,
	onCancelQueuedTurn,
	onReorderQueuedTurns,
	promoteQueuedTurnPendingTurnId,
	cancelQueuedTurnPendingTurnId,
	editQueuedTurnPendingTurnId,
	draftSettings,
	draftNotice,
	openingStartedAt,
	composerTray,
	parkUnsentSends = true,
	centerInWindow = false,
	showSetupStep = true,
	holdAttachments,
	onHeldAttachmentsChange,
	draftScope,
	draftPersistenceAvailable,
	onRetryDraftPersistence,
}: ChatWorkspaceProps & {
	draftScope: ChatDraftScope;
	draftPersistenceAvailable: boolean;
	onRetryDraftPersistence?: () => void;
}) {
	const draftScopeKey = chatDraftScopeKey(draftScope);
	const uiSessionId = draftScope.sessionId;
	const activeRemoteHostId = remoteHostId ?? session?.hostId;
 const fileNavigationGeneration=useRef(0);
 useEffect(()=>{fileNavigationGeneration.current++;return ()=>{fileNavigationGeneration.current++}},[snapshot.sessionId,activeRemoteHostId,workspaceActiveTabKey]);
	const remoteCreateMux = useMemo(
		() => assetBaseUrl ? () => createTerminalMux(muxUrlFromApiBase(assetBaseUrl)) : undefined,
		[assetBaseUrl],
	);
	// The turn the daemon still holds open, whatever the controller is doing. It
	// gates history mutations (continue, rollback, edit, retry): the daemon refuses
	// them while a turn is open, even one settled on screen for a stopped controller.
	const durableTurn = activeTurn(snapshot);
	const canContinue = Boolean(onContinueTurn) && !durableTurn && !newWorkDisabled && Boolean(continuableTurnId(snapshot));
	const latestSettledTurn = snapshot.turns.findLast((entry) =>
		!entry.rolledBack && entry.state !== "queued" && entry.state !== "cancelled",
	);
	const limitedTurn = latestSettledTurn?.state === "failed" && latestSettledTurn.usageLimit &&
		latestSettledTurn.providerTurnId && !latestSettledTurn.hasRetryAttempt &&
		!snapshot.turns.some((entry) => entry.retryOfTurnId === latestSettledTurn.id) &&
		latestSettledTurn.hasHumanPrompt
		? latestSettledTurn : undefined;
	// The primary Chat view wakes a sleeping provider in the background. Its
	// marker clears before the new controller is ready, so an intermediate
	// "stopped" snapshot is still part of that wake, not a crashed agent.
	const wakingFromHibernate = useRef(snapshot.controller.state === "hibernated");
	if (snapshot.controller.state === "hibernated") wakingFromHibernate.current = true;
	if (snapshot.controller.state === "ready" || snapshot.controller.state === "busy") wakingFromHibernate.current = false;
	if (snapshot.controller.state === "stopped" && !resumingAgent) wakingFromHibernate.current = false;
	const suppressStopped = wakingFromHibernate.current && snapshot.controller.state === "stopped";
	// A terminated session has no agent to resume: only a restore brings it back.
	const terminated = Boolean(session && !sessionIsActive(session));
	// After a restart the daemon is still verifying this session and reconnects
	// its surviving agent itself. Until it settles, "stopped" is not a state to act on.
	const startupReconnecting = snapshot.controller.state === "stopped" && !resumingAgent && !terminated &&
		session?.statusReadiness === "checking";
	// The controller is really gone: not a handoff, a wake, a resume or a restart
	// the daemon is still reconnecting. The stopped banner shows on exactly this.
	const controllerStopped = snapshot.controller.state === "stopped" &&
		!(controllerTransitioning || agentResuming || suppressStopped || resumingAgent || startupReconnecting);
	// A running turn cannot be running without a controller: it settles instead of
	// spinning forever (#6064).
	const turn = activeTurn(snapshot, { controllerStopped });
	const signInRequired = needsReauth(snapshot) && agentAccountProviderFor(snapshot.harness) !== null && !activeRemoteHostId && !session?.cloud;
	const startupState =
		session?.provisionState === "provisioning" || session?.provisionState === "failed"
			? session.provisionState
			: undefined;
	// While a session starts, its opening brief reads as sent: the setup checklist
	// under it explains why nothing has answered yet. Later messages still queue.
	const plannedSteps = session?.provisionSteps ?? startingSteps;
	// Named tasks keep a setup checkpoint even when the project has no setup script;
	// the checklist names that step only when there is a script to run, or it failed
	// (the step still running when the start failed).
	const provisionSteps = useMemo(
		() => plannedSteps?.filter((step) => step.id !== "setup" || showSetupStep || (startupState === "failed" && step.status === "running")),
		[plannedSteps, showSetupStep, startupState],
	);
	// Ready can arrive before the first turn does. Keep its setup slot through
	// that gap; selecting the first turn also never promotes a queued follow-up.
	const hasStartup = Boolean(
		startupState || (provisionSteps?.length &&
			(snapshot.turns[0]?.state === "queued" || snapshot.turns[0]?.state === "running")),
	);
	const openingTurnId = hasStartup
		? snapshot.turns[0]?.id
		: undefined;
	const provisionError = session?.provisionError;
	const { branch: sessionBranch, naming: namingBranch } = sessionBranchPresentation(session);
	const startup = useMemo(
		() =>
			hasStartup
				? {
						failed: startupState === "failed",
						steps: provisionSteps ?? [],
						error: provisionError,
						agentName: agentLabel(snapshot.harness),
						branch: sessionBranch,
						namingBranch,
						openingTurnId,
						onRetry: newWorkDisabled ? undefined : onResumeAgent,
						retrying: resumingAgent,
						retryError: resumeError,
					}
				: undefined,
		[
			hasStartup,
			namingBranch,
			newWorkDisabled,
			onResumeAgent,
			openingTurnId,
			provisionError,
			provisionSteps,
			resumeError,
			resumingAgent,
			sessionBranch,
			snapshot.harness,
			startupState,
		],
	);
	const hasPendingInteraction = snapshot.items.some(
		(item) =>
			item.kind === "activity" &&
			(item.activityKind === "approval" || item.activityKind === "user_input") &&
			item.status === "pending" &&
			(!item.turnId || item.turnId === turn?.id),
	);
	const handleChatKeyDown = useCallback(
		(event: ReactKeyboardEvent<HTMLElement>) => {
			if (
				newWorkDisabled ||
				event.key !== "Escape" ||
				event.defaultPrevented ||
				isDialogOrMenuOpen() ||
				event.altKey ||
				event.ctrlKey ||
				event.metaKey ||
				turn?.state !== "running" ||
				hasPendingInteraction ||
				!onInterrupt
			)
				return;
			event.preventDefault();
			onInterrupt();
		},
		[hasPendingInteraction, newWorkDisabled, onInterrupt, turn],
	);
	const handleChatSurfaceClick = useCallback((event: ReactMouseEvent<HTMLElement>) => {
		const target = event.target;
		if (!(target instanceof Element)) return;
		// Portal menus (the agent picker) still bubble clicks here; focusing the composer would close them.
		if (!event.currentTarget.contains(target)) return;
		// A click fires after a drag selection ends. Focusing the composer here would
		// collapse the range the user just selected in the transcript.
		if (window.getSelection()?.isCollapsed === false) return;
		// The focusable context tooltip must not redirect focus to the composer.
		if (
			target.closest(
				"button, a, input, textarea, select, [contenteditable='true'], [role='button'], [role='option'], [data-context-meter], [role='menuitem'], [role='dialog'], [data-testid='session-terminal'], .xterm, .terminal-surface",
			)
		)
			return;

		const composer = surfaceRef.current?.querySelector<HTMLElement>(
			'[aria-label="Message the agent"]',
		);
		if (composer?.getAttribute("aria-disabled") !== "true") composerFocusRef.current?.focus();
	}, []);
	// Selection is durable UI state; availability only controls whether the tab is
	// offered. Keeping these separate preserves a selected reviewer while an active
	// session temporarily becomes terminated and later returns.
	const reviewerActive = reviewerChatSelected || Boolean(reviewerTarget && session);
	const shellActive = Boolean(shellTarget && session);
	// When the conversation panel was last shown again after a terminal or reviewer
	// tab covered it. The transcript is held while covered (HoldWhileHidden), so rows
	// written meanwhile reach it only on return, and must not read as arrivals the
	// reader watched. Adjusted during render, so the held transcript sees it in the
	// same render that catches it up; only a tab change pays for the extra pass.
	const panelCovered = reviewerActive || shellActive;
	const [panelWasCovered, setPanelWasCovered] = useState(panelCovered);
	const [panelShownAt, setPanelShownAt] = useState<number>();
	if (panelCovered !== panelWasCovered) {
		setPanelWasCovered(panelCovered);
		if (!panelCovered) setPanelShownAt(Date.now());
	}
	const splitHiddenTabKeys = useSplitHiddenTabKeys();
	const closeFocusedSplitPane = useSplitCloseFocused();
	const auxiliaryTabs = useMemo<ChatAuxiliaryTab[]>(
		() => [
			...(reviewerTerminal
				? [{ key: `reviewer:${reviewerTerminal.handleId}`, kind: "reviewer" as const, terminal: reviewerTerminal }]
				: []),
			...(!reviewerTerminal && reviewerChat
				? [{ key: `reviewer-chat:${reviewerChat.reviewId}`, kind: "reviewer-chat" as const, terminal: reviewerChat }]
				: []),
			...(shellTerminals ?? []).map((terminal) => ({ key: terminal.handleId, kind: "shell" as const, terminal })),
			...(workspaceTabs ?? []).map((tab) => ({ key: tab.key, kind: "workspace" as const, tab })),
		].filter((tab) => !splitHiddenTabKeys.has(tab.key)),
		[reviewerChat, reviewerTerminal, shellTerminals, splitHiddenTabKeys, workspaceTabs],
	);
	const availableTabKeys = useMemo(() => auxiliaryTabs.map((tab) => tab.key), [auxiliaryTabs]);
	const [tabOrderBySession, setTabOrderBySession] = useState<Record<string, string[]>>({});
	const orderedAuxiliaryTabs = useMemo(() => {
		const preferred = auxiliaryTabOrder ?? tabOrderBySession[uiSessionId] ?? [];
		const byKey = new Map(auxiliaryTabs.map((tab) => [tab.key, tab]));
		const ordered = preferred.flatMap((key) => {
			const tab = byKey.get(key);
			if (!tab) return [];
			byKey.delete(key);
			return [tab];
		});
		return [...ordered, ...byKey.values()];
	}, [auxiliaryTabOrder, auxiliaryTabs, uiSessionId, tabOrderBySession]);
	const activeWorkspaceTab = workspaceActiveTabKey
		? workspaceTabs?.find((tab) => tab.key === workspaceActiveTabKey)
		: undefined;
	const reorderAuxiliaryTabs = useCallback(
		(nextKeys: string[]) => {
			const available = new Set(availableTabKeys);
			const next = nextKeys.filter((key, index) => available.has(key) && nextKeys.indexOf(key) === index);
			for (const key of availableTabKeys) {
				if (!next.includes(key)) next.push(key);
			}
			if (onAuxiliaryTabOrderChange) onAuxiliaryTabOrderChange(next);
			else setTabOrderBySession((current) => ({ ...current, [uiSessionId]: next }));
		},
		[availableTabKeys, onAuxiliaryTabOrderChange, uiSessionId],
	);
	useEffect(() => {
		if (auxiliaryTabOrder) {
			const available = new Set(availableTabKeys);
			const next = auxiliaryTabOrder.filter((key) => available.has(key));
			for (const key of availableTabKeys) {
				if (!next.includes(key)) next.push(key);
			}
			if (!next.every((key, index) => key === auxiliaryTabOrder[index]) || next.length !== auxiliaryTabOrder.length) {
				onAuxiliaryTabOrderChange?.(next);
			}
			return;
		}
		setTabOrderBySession((current) => {
			const currentOrder = current[uiSessionId] ?? [];
			const available = new Set(availableTabKeys);
			const next = currentOrder.filter((key) => available.has(key));
			for (const key of availableTabKeys) {
				if (!next.includes(key)) next.push(key);
			}
			if (next.length === currentOrder.length && next.every((key, index) => key === currentOrder[index])) return current;
			if (next.length === 0) {
				const { [uiSessionId]: _removed, ...rest } = current;
				return rest;
			}
			return { ...current, [uiSessionId]: next };
		});
	}, [auxiliaryTabOrder, availableTabKeys, onAuxiliaryTabOrderChange, uiSessionId]);
	const queuedMessages = useQueuedMessages(snapshot, openingTurnId);
	const [steerStartedAt, setSteerStartedAt] = useState<ReadonlyMap<string, string>>(() => new Map());
	const [steerErrors, setSteerErrors] = useState<Readonly<Record<string, string>>>({});
	const [steerEchoes, setSteerEchoes] = useState<readonly SteerEcho[]>([]);
	const backgroundWakeQueuedTurnIds = useMemo(
		() => new Set(
			(localEchos ?? [])
				.filter((echo) => echo.backgroundWake && echo.turnId)
				.map((echo) => echo.turnId as string)
				.filter((turnId) => snapshot.turns.some((turn) => turn.id === turnId && turn.state === "queued")),
		),
		[localEchos, snapshot.turns],
	);
	// A send made while a turn was active is shown in the dock the moment it is sent,
	// not as a chat bubble that jumps to the dock when the daemon answers. It stays
	// until the daemon's own row for it arrives.
	const pendingQueuedMessages = useMemo((): QueuedMessage[] =>
		(localEchos ?? [])
			.filter((echo) => echo.queued && !snapshot.items.some((item) => echoMatchesItem(echo, item, backgroundWakeQueuedTurnIds)))
			.map((echo) => ({
				turnId: echo.turnId ?? `local:${echo.clientMessageId}`,
				pending: true,
				message: {
					kind: "message",
					id: `local:${echo.clientMessageId}`,
					turnId: echo.turnId ?? `local:${echo.clientMessageId}`,
					sequence: snapshot.latestSequence + 0.01,
					revision: 0,
					role: "user",
					origin: "human",
					text: echo.text,
				continuation: echo.continuation,
					streaming: false,
					delivery: "queued",
					clientMessageId: echo.clientMessageId,
					createdAt: echo.createdAt,
				},
			})),
		[backgroundWakeQueuedTurnIds, localEchos, snapshot.items, snapshot.latestSequence],
	);
	const visibleQueuedMessages = useMemo(
		() => [
			...queuedMessages.filter(
				(message) => !backgroundWakeQueuedTurnIds.has(message.turnId) && !steerStartedAt.has(message.turnId),
			),
			...pendingQueuedMessages,
		],
		[backgroundWakeQueuedTurnIds, pendingQueuedMessages, queuedMessages, steerStartedAt],
	);
	// A steer sent from the composer shows in its turn at once, as a send's echo does.
	// The daemon places its row when the agent accepts the steer, so until then the echo
	// stays last, and from then on it keeps the place it had, with later output below it.
	// The daemon's own row, matched by its client id, replaces it.
	const pendingSteerEchoes = useMemo(
		() => turn?.state === "running"
			? steerEchoes.filter((echo) => echo.turnId === turn.id && !snapshot.items.some((item) => steerEchoArrived(item, echo)))
			: [],
		[snapshot.items, steerEchoes, turn],
	);
	useEffect(() => {
		setSteerEchoes((current) => {
			const next = current.filter((echo) => pendingSteerEchoes.includes(echo));
			return next.length === current.length ? current : next;
		});
	}, [pendingSteerEchoes]);
	const timelineSnapshot = useMemo(() => {
		const steering = queuedMessages.filter(({ turnId }) => steerStartedAt.has(turnId));
		if (turn?.state !== "running" || (steering.length === 0 && pendingSteerEchoes.length === 0)) return snapshot;
		const items = [...snapshot.items];
		pendingSteerEchoes.forEach((echo, index) => {
			const sequence = (echo.acceptedAfterSequence ?? snapshot.latestSequence) + 0.4 + index / 1000;
			const at = items.findIndex((item) => item.sequence > sequence);
			items.splice(at < 0 ? items.length : at, 0, {
				kind: "activity",
				id: `steer-echo:${echo.clientMessageId}`,
				turnId: turn.id,
				sequence,
				revision: 0,
				activityKind: "system",
				status: "completed",
				summary: echo.text,
				detail: { event: "steer", text: echo.text, origin: "human", clientMessageId: echo.clientMessageId },
				createdAt: echo.createdAt,
			});
		});
		return {
			...snapshot,
			items: [
				...items,
				...steering.map(({ turnId, message }, index): ConversationActivity => ({
					kind: "activity",
					id: `steering:${turnId}`,
					turnId: turn.id,
					sequence: snapshot.latestSequence + 0.5 + index / 100,
					revision: 0,
					activityKind: "system",
					status: "completed",
					summary: message.text,
					detail: { event: "steer", text: message.text, origin: message.origin, clientMessageId: message.clientMessageId },
					createdAt: steerStartedAt.get(turnId)!,
				})),
			],
		};
	}, [pendingSteerEchoes, queuedMessages, snapshot, steerStartedAt, turn]);
	// The dock omits queued turns that are not the user's own; the daemon needs
	// the whole queue, with those turns left where they were.
	const reorderQueuedTurns = useCallback(
		(dockOrder: string[]) => onReorderQueuedTurns?.(fullQueuedTurnOrder(snapshot, dockOrder)) ?? Promise.resolve(),
		[onReorderQueuedTurns, snapshot],
	);
	const stablePromoteQueuedTurn = useStableCallback(onPromoteQueuedTurn);
	const stableCancelQueuedTurn = useStableCallback(onCancelQueuedTurn);
	const [queueEdit, setQueueEdit] = useState<ChatDraftQueuedEdit | undefined>(
		() => (draftPersistenceAvailable ? readChatSessionDraft(draftScope).queuedEdit : undefined),
	);
	const queueEditRef = useRef(queueEdit);
	const [queueDraftError, setQueueDraftError] = useState<string>();
	const unprovenQueueWrite = useRef<{ expectedRevision?: string; written: { revisions: (string | undefined)[] } } | undefined>(undefined);
	const updateQueueDraft = useCallback((edit: Omit<ChatDraftQueuedEdit, "revision"> | undefined, expectedRevision?: string) => {
		const previous = unprovenQueueWrite.current;
		const result = writeChatQueuedEdit(draftScope, edit, expectedRevision, undefined,
			previous?.expectedRevision === expectedRevision ? previous?.written : undefined);
		setQueueDraftError(result.ok ? undefined : "chat.draft.queueSaveLocalFailed");
		if (result.ok) {
			const retired = queueEditRef.current;
			unprovenQueueWrite.current = undefined;
			queueEditRef.current = result.draft.queuedEdit;
			setQueueEdit(result.draft.queuedEdit);
			if (retired && (!result.draft.queuedEdit || retired.ownerId !== result.draft.queuedEdit.ownerId)) {
				purgeFileAttachments(chatQueuedAttachmentScopeKey(draftScope,
					`${retired.turnId}:${retired.ownerId ?? retired.expectedRevision ?? "legacy"}`));
			}
		} else if (result.attempted) {
			unprovenQueueWrite.current = { expectedRevision, written: result.attempted };
		}
		return result;
	}, [draftScope]);
	useEffect(() => {
		setChatDraftBoundary(draftScope.sessionId, "queued-edit", queueDraftError ? "persistence-failed" : undefined);
		return () => setChatDraftBoundary(draftScope.sessionId, "queued-edit", undefined);
	}, [queueDraftError, draftScope.sessionId]);
	// Text equality cannot prove an attachment-only edit was accepted. Keep an
	// uncertain edit and its original daemon revision until a save is acknowledged.
	const changeQueuedDraft = useCallback((text: string) => {
		const current = queueEditRef.current;
		if (!current || current.clientMessageId || current.turnId !== queueEdit?.turnId || current.ownerId !== queueEdit.ownerId || current.text === text) return;
		const result = updateQueueDraft({ ...current, text, saving: undefined, clientMessageId: undefined }, current.revision);
		if (!result.ok) {
			queueEditRef.current = { ...current, text };
			setQueueEdit(queueEditRef.current);
		}
	}, [queueEdit?.turnId, queueEdit?.ownerId, updateQueueDraft]);
	const changeQueuedAttachments = useCallback((field: "attachments" | "stagedAttachments", attachments: ChatDraftRetainedAttachment[] | ChatDraftAttachment[]) => {
		const current = queueEditRef.current;
		if (!current || current.clientMessageId || current.turnId !== queueEdit?.turnId || current.ownerId !== queueEdit.ownerId) return;
		if (JSON.stringify(current[field] ?? []) === JSON.stringify(attachments)) return;
		const next = { ...current, [field]: attachments, saving: undefined, clientMessageId: undefined };
		const result = updateQueueDraft(next, current.revision);
		if (!result.ok) {
			queueEditRef.current = next;
			setQueueEdit(next);
		}
	}, [queueEdit?.turnId, queueEdit?.ownerId, updateQueueDraft]);
	const changeQueuedStagedAttachments = useCallback((attachments: ChatDraftAttachment[]) => {
		changeQueuedAttachments("stagedAttachments", attachments);
	}, [changeQueuedAttachments]);
	const changeQueuedRetainedAttachments = useCallback((attachments: ChatDraftRetainedAttachment[]) => {
		changeQueuedAttachments("attachments", attachments);
	}, [changeQueuedAttachments]);
	const handleCancelQueuedTurn = useCallback(
		async (turnId: string) => {
			if (!onCancelQueuedTurn) return;
			if (queueEditRef.current?.turnId === turnId && queueEditRef.current.clientMessageId) return;
			await stableCancelQueuedTurn(turnId);
			const current = queueEditRef.current;
			if (current?.turnId === turnId && !current.clientMessageId) updateQueueDraft(undefined, current.revision);
		},
		[queueEdit, stableCancelQueuedTurn, updateQueueDraft],
	);
	// Keep the dispatch target with this composer instance while attachment staging
	// awaits. A newer queue editor must not redirect an older ordinary send.
	const handleComposerSend = useCallback(
		async (text: string, attachments?: Parameters<NonNullable<typeof onSend>>[1], clientMessageId?: string, retainedContent?: number[], excerpts?: ChatDraftExcerptReference[], fileReferences?: ChatFileReference[], displayText?: string) => {
			if (queueEdit) {
				if (!onEditQueuedTurn) {
					throw new Error("chat.draft.queueUnavailable");
				}
				if (!queueEdit.clientMessageId && !queuedMessages.some((entry) => entry.turnId === queueEdit.turnId)) {
					throw new Error("chat.draft.queueMissing");
				}
				const currentEdit = queueEditRef.current;
				if (!currentEdit || currentEdit.turnId !== queueEdit.turnId || currentEdit.ownerId !== queueEdit.ownerId) {
					throw new Error("chat.draft.queueReplaced");
				}
				const prepared = updateQueueDraft({
					...currentEdit,
					saving: true,
					clientMessageId: currentEdit.clientMessageId ?? crypto.randomUUID(),
					nativeImages: currentEdit.nativeImages ?? Boolean(nativeImages),
				}, currentEdit.revision);
				if (!prepared.ok || !prepared.draft.queuedEdit) throw new Error("chat.draft.queuePrepareFailed");
				try {
					await onEditQueuedTurn(queueEdit.turnId, text, {
						clientMessageId: prepared.draft.queuedEdit.clientMessageId,
						...(attachments?.length ? { attachments } : {}),
						retainedContent: queueEdit.attachments === undefined ? undefined : retainedContent,
						expectedRevision: queueEdit.expectedRevision,
					});
				} catch (error) {
					// These outcomes follow the daemon's atomic receipt lookup, so even
					// a recovered retry proves this exact edit was never accepted.
					if ([
						"CHAT_TURN_NOT_QUEUED",
						"CHAT_QUEUED_EDIT_CONFLICT",
						"CHAT_QUEUED_CONTENT_INVALID",
						"CHAT_QUEUED_TEXT_REQUIRED",
					].includes(apiErrorCode(error) ?? "")) {
						updateQueueDraft({
							...prepared.draft.queuedEdit,
							clientMessageId: undefined, saving: undefined, nativeImages: undefined,
						}, prepared.draft.queuedEdit.revision);
					}
					throw error;
				}
				if (queueEditRef.current?.ownerId === queueEdit.ownerId) {
					if (!updateQueueDraft(undefined, prepared.draft.queuedEdit.revision).ok) throw new Error("chat.draft.queueClearFailed");
				}
				return;
			}
			// Where the composer stands now is where the docking slide starts from.
			if (wasConversationEmpty.current && composerBoxRef.current) {
				composerCenteredTopRef.current = composerBoxRef.current.getBoundingClientRect().top;
			}
			return onSend?.(text, attachments, clientMessageId, excerpts, fileReferences, displayText);
		},
		[draftScope, onEditQueuedTurn, onSend, nativeImages, queueEdit, queuedMessages, updateQueueDraft],
	);
	const stableInterrupt = useStableCallback(onInterrupt);
	const stableSteer = useStableCallback(onSteer);
	const beginQueuedEdit = useCallback(
		(turnId: string, text: string) => {
			if (newWorkDisabled || queueEditRef.current?.clientMessageId) return;
			const message = queuedMessages.find((queued) => queued.turnId === turnId)?.message;
			if (!message) return;
			const parts = stagedAttachmentParts(text);
			// Paths and native blocks have no shared persisted identity.
			const attachments: StoredComposerAttachment[] = parts.attachments.map((path) => ({
				id: path, name: attachmentName(path), path,
			}));
			(message.content ?? []).forEach((item, index) => {
				attachments.push({ id: `content-${index}`, contentIndex: index, contentType: item.type,
					name: item.name || (item.type === "image" ? `Image ${index + 1}` : item.uri || "Attachment") });
			});
			updateQueueDraft({ turnId, ownerId: crypto.randomUUID(), text: parts.body, expectedRevision: message.revision, attachments, stagedAttachments: [] });
		},
		[newWorkDisabled, queuedMessages, updateQueueDraft],
	);
	const cancelQueuedEdit = useCallback(() => {
		const current = queueEditRef.current;
		if (current && !current.clientMessageId) updateQueueDraft(undefined, current.revision);
	}, [updateQueueDraft]);
	const promoteQueuedTurn = useCallback(
		async (turnId: string) => {
			setSteerErrors(({ [turnId]: _retried, ...rest }) => rest);
			setSteerStartedAt((current) => new Map(current).set(turnId, new Date().toISOString()));
			try {
				await stablePromoteQueuedTurn(turnId);
			} catch (error) {
				setSteerStartedAt((current) => {
					const next = new Map(current);
					next.delete(turnId);
					return next;
				});
				setSteerErrors((current) => ({
					...current,
					[turnId]: error instanceof Error ? error.message : "That action failed.",
				}));
			}
		},
		[stablePromoteQueuedTurn],
	);
	const steerEchoTarget = useStableCallback(() =>
		turn?.state === "running" ? { turnId: turn.id, sentAfterSequence: snapshot.latestSequence } : undefined);
	const latestSequence = useStableCallback(() => snapshot.latestSequence);
	const steer = useCallback(
		async (
			text: string,
			attachments?: { mimeType: string; data: string }[],
			clientMessageId?: string,
			recoverOnly?: boolean,
		) => {
			const id = clientMessageId ?? crypto.randomUUID();
			// A recovery re-asks about a steer sent before; the daemon may already have it.
			const target = recoverOnly ? undefined : steerEchoTarget();
			if (target) {
				setSteerEchoes((current) => [
					...current.filter((echo) => echo.clientMessageId !== id),
					{ clientMessageId: id, text, createdAt: new Date().toISOString(), ...target },
				]);
			}
			const dropEcho = () => setSteerEchoes((current) => current.filter((echo) => echo.clientMessageId !== id));
			try {
				const outcome = await stableSteer(text, attachments, id, recoverOnly);
				// A refused steer stays in the composer, so its echo goes.
				if (outcome?.status === "not-accepted") dropEcho();
				else {
					const acceptedAfterSequence = latestSequence() ?? 0;
					setSteerEchoes((current) => current.map((echo) =>
						echo.clientMessageId === id && echo.acceptedAfterSequence === undefined ? { ...echo, acceptedAfterSequence } : echo));
				}
				return outcome;
			} catch (error) {
				dropEcho();
				throw error;
			}
		},
		[latestSequence, stableSteer, steerEchoTarget],
	);
	const acceptedClientMessageIds = useMemo(
		() =>
			new Set(
				snapshot.items.flatMap((item) => {
					// A sent message's own row proves it arrived, as a steer's activity does.
					if (item.kind === "message") {
						return item.role === "user" && item.origin === "human" && item.clientMessageId ? [item.clientMessageId] : [];
					}
					if (
						item.kind !== "activity" ||
						item.detail?.event !== "steer" ||
						!item.detail.clientMessageId
					) return [];
					return [item.detail.clientMessageId];
				}),
			),
		[snapshot.items],
	);
	// The turn a confirmation is open for. Undo is not reversible and it changes what
	// the agent knows, so it is never one click.
	const [confirming, setConfirming] = useState<string | undefined>(undefined);
	const surfaceRef = useRef<HTMLElement | null>(null);
	const composerFocusRef = useRef<ChatComposerHandle>(null);
	const artifactSession = session?.requestedOutput === "artifact";
	// The sample prompts show only while the composer is empty: picking one hides
	// them, and clearing the draft brings them back. A saved draft hides them from
	// the first frame, so a remount (such as a project switch) never flashes them.
	const [composerEmpty, setComposerEmpty] = useState(() => {
		const { composer } = readChatSessionDraft(draftScope);
		return !composer.text.trim() && composer.attachments.length === 0 && !composer.excerpts?.length;
	});
	const annotationNavigationRef = useRef<((annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number }) => void) | null>(null);
	const navigateToAnnotation = useCallback((annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number }) => {
		annotationNavigationRef.current?.(annotation);
	}, []);
	// This content remounts per session (it is keyed by the draft scope), so a
	// request still held after it applied would apply again to the next chat.
	useEffect(() => {
		if (!annotationNavigationRequest) return;
		navigateToAnnotation(annotationNavigationRequest);
		onAnnotationNavigationHandled?.(annotationNavigationRequest.id);
	}, [annotationNavigationRequest, navigateToAnnotation, onAnnotationNavigationHandled]);
	// Storage failures can be transient, so try again when the user comes back. Only while
	// the composer is empty: recovering remounts it, which would drop unsaved text.
	useEffect(() => {
		if (!onRetryDraftPersistence) return;
		const retry = () => {
			if (composerFocusRef.current?.isEmpty() !== false) onRetryDraftPersistence();
		};
		window.addEventListener("focus", retry);
		return () => window.removeEventListener("focus", retry);
	}, [onRetryDraftPersistence]);
	const lastWheelZoomAtRef = useRef(0);
	const wheelZoomRemainderRef = useRef(0);
	const [terminalFontSize, setTerminalFontSize] = useState(initialTerminalFontSize);
	const [isFullscreen, setIsFullscreen] = useState(false);
	useEffect(() => {
		const handleFullscreenChange = () => {
			setIsFullscreen(document.fullscreenElement === surfaceRef.current);
		};
		document.addEventListener("fullscreenchange", handleFullscreenChange);
		return () => document.removeEventListener("fullscreenchange", handleFullscreenChange);
	}, []);

	const updateTerminalFontSize = useCallback((delta: number) => {
		setTerminalFontSize((current) => {
			const next = clampTerminalFontSize(current + delta);
			window.localStorage?.setItem(terminalFontSizeStorageKey, String(next));
			return next;
		});
	}, []);

	const handleWheelZoom = useCallback(
		(event: ReactWheelEvent<HTMLDivElement>) => {
			if (!event.ctrlKey && !event.metaKey) return;
			event.preventDefault();
			event.stopPropagation();

			if (event.timeStamp - lastWheelZoomAtRef.current > WHEEL_ZOOM_RESET_MS) {
				wheelZoomRemainderRef.current = 0;
			}
			lastWheelZoomAtRef.current = event.timeStamp;
			wheelZoomRemainderRef.current += event.deltaY;

			const steps = Math.floor(Math.abs(wheelZoomRemainderRef.current) / WHEEL_ZOOM_THRESHOLD);
			if (steps === 0) return;

			const direction = wheelZoomRemainderRef.current > 0 ? -1 : 1;
			updateTerminalFontSize(direction * steps);
			wheelZoomRemainderRef.current -=
				Math.sign(wheelZoomRemainderRef.current) * steps * WHEEL_ZOOM_THRESHOLD;
		},
		[updateTerminalFontSize],
	);

	const toggleFullscreen = useCallback(async () => {
		const surface = surfaceRef.current;
		if (!surface) return;
		try {
			if (document.fullscreenElement === surface) {
				await document.exitFullscreen();
				return;
			}
			await surface.requestFullscreen();
		} catch (error) {
			console.warn("Unable to toggle chat reviewer fullscreen", error);
		}
	}, []);

	// Cycle chat → reviewer → shells in strip order, wrapping. With no auxiliary
	// tabs there is nothing to cycle to and the shortcut is a no-op.
	const selectAdjacentTab = useCallback(
		(direction: -1 | 1) => {
			const tabs = [{ key: "chat", kind: "chat" as const }, ...orderedAuxiliaryTabs];
			if (tabs.length <= 1) return;
			const activeKey = workspaceActiveTabKey ?? (shellActive
				? shellTarget?.handleId
				: reviewerActive
					? reviewerTerminal
						? `reviewer:${reviewerTerminal.handleId}`
						: reviewerChat
							? `reviewer-chat:${reviewerChat.reviewId}`
							: "chat"
					: "chat");
			const activeIndex = tabs.findIndex((tab) => tab.key === activeKey);
			const currentIndex = activeIndex >= 0 ? activeIndex : 0;
			const next = tabs[(currentIndex + direction + tabs.length) % tabs.length];
			if (!next) return;
			if (next.kind === "chat") {
				onSelectChat?.();
				return;
			}
			if (next.kind === "reviewer") {
				onOpenReviewerTerminal?.(next.terminal);
				return;
			}
			if (next.kind === "reviewer-chat") {
				onOpenReviewerChat?.(next.terminal);
				return;
			}
			if (next.kind === "shell") {
				onSelectShellTerminal?.(next.terminal.handleId);
				return;
			}
			next.tab.onSelect();
		},
		[
			onOpenReviewerTerminal,
			onOpenReviewerChat,
			onSelectChat,
			onSelectShellTerminal,
			reviewerActive,
			reviewerChat,
			reviewerTerminal,
			orderedAuxiliaryTabs,
			shellActive,
			shellTarget,
			workspaceActiveTabKey,
		],
	);
	const handleChatTabsKeyDown = useCallback(
		(event: ReactKeyboardEvent<HTMLDivElement>) => {
			if (
				event.target instanceof HTMLElement &&
				event.target.getAttribute("role") === "tab" &&
				event.key === "Tab" &&
				event.ctrlKey &&
				!event.altKey &&
				!event.metaKey
			) {
				event.preventDefault();
				selectAdjacentTab(event.shiftKey ? -1 : 1);
				return;
			}
			handleTerminalTabListKeyDown(event);
		},
		[selectAdjacentTab],
	);

	useEffect(
		() =>
			!hideHeader ? aoBridge.app.onCloseShellTerminalShortcut(() => {
				if (closeFocusedSplitPane) closeFocusedSplitPane();
				else if (activeWorkspaceTab?.onClose) activeWorkspaceTab.onClose();
				else if (shellTarget) onCloseShellTerminal?.(shellTarget.handleId);
			}) : undefined,
		[activeWorkspaceTab, closeFocusedSplitPane, hideHeader, onCloseShellTerminal, shellTarget],
	);

	useEffect(() => {
		if (hideHeader) return;
		const disposePrevious = aoBridge.app.onPreviousTabShortcut(() => !isSettingsPageOpen() && selectAdjacentTab(-1));
		const disposeNext = aoBridge.app.onNextTabShortcut(() => !isSettingsPageOpen() && selectAdjacentTab(1));
		return () => {
			disposePrevious();
			disposeNext();
		};
	}, [hideHeader, selectAdjacentTab]);

	useEffect(() => {
		if (hideHeader) return;
		aoBridge.app.setCloseShellTerminalShortcutEnabled(
			Boolean(closeFocusedSplitPane) || Boolean(activeWorkspaceTab?.onClose) || Boolean(shellTarget && onCloseShellTerminal),
		);
		return () => aoBridge.app.setCloseShellTerminalShortcutEnabled(false);
	}, [activeWorkspaceTab, closeFocusedSplitPane, hideHeader, onCloseShellTerminal, shellTarget]);

	// Keep the rollback affordance mounted while a new turn runs; disable it until
	// the daemon can safely accept it so the action row never shifts.
	const rollbackTarget =
		onRollback && !newWorkDisabled && snapshot.controller.state !== "hibernated"
			? (id: string) => setConfirming(id)
			: undefined;
	const discarded = snapshot.turns.filter((t) => t.rolledBack).length;

	const brokenServers = useMemo(() => brokenMcpServers(snapshot), [snapshot]);
	const editHumanMessage = onEditMessage;
	const pendingApproval = useMemo(
		() => latestPendingInteraction(snapshot.items, "approval", turn),
		[snapshot.items, turn],
	);
	const pendingUserInput = useMemo(
		() => latestPendingInteraction(snapshot.items, "user_input", turn),
		[snapshot.items, turn],
	);
	const stableSettings = useStableValue(snapshot.settings);
	// Claude Code compacts at the window picked for it, well before its model's full context.
	const compactsAt = snapshot.harness === "claude-code" ? snapshot.settings?.autoCompactWindow : undefined;
	const stableUsage = useStableValue(snapshot.usage);
	// The account's quota, for the meter's warning pill from 75%.
	const stableRateLimits = useStableValue(snapshot.rateLimits);
	// A reroute describes the model it was recorded against; once the user picks a
	// different model it no longer says anything about the next turn.
	const currentModelReroute =
		snapshot.modelReroute &&
		(!snapshot.modelReroute.fromModel ||
			!snapshot.settings.model ||
			snapshot.modelReroute.fromModel === snapshot.settings.model)
			? snapshot.modelReroute
			: undefined;
	const stableModelReroute = useStableValue(currentModelReroute);
	const stablePendingApproval = useStableValue(pendingApproval);
	const stablePendingUserInput = useStableValue(pendingUserInput);
	const composerSettings = useMemo(
		() =>
			settingsReady && (onChooseSettings || onChooseConfigOption) ? (
				<TurnSettingsBar
					agentSwitch={agentSwitch}
					beforePicker={agentSwitch ? <ContextMeter usage={stableUsage} rateLimits={stableRateLimits} compactsAt={compactsAt} /> : undefined}
					// While the agent wakes or starts; a chat reads "hibernated" until ready.
					modelsLoading={settingsLoading}
					lastModel={lastModel}
					models={models ?? []}
					settings={stableSettings}
					onRememberPermissions={onRememberPermissions}
					rememberPermissionsPending={rememberPermissionsPending}
					rememberPermissionsError={rememberPermissionsError}
					rememberedPermissionMode={rememberedPermissionMode}
					harness={snapshot.harness}
					showApprovalMode={showApprovalMode ?? !session?.cloud}
					approvalModes={approvalModes}
					approvalModeLimits={approvalModeLimits}
					reroute={stableModelReroute}
					onChange={newWorkDisabled || settingsLocked ? undefined : onChooseSettings}
					configOptions={configOptions ?? []}
					onChangeConfigOption={newWorkDisabled || settingsLocked ? undefined : onChooseConfigOption}
					configPending={configOptionPending}
					autoSelectEffortOnOpen={snapshot.items.length === 0 && !turn}
					error={configOptionError}
					// Turn settings require a live controller even while messages can queue.
				// A background resume keeps the controls visible with their last values.
					disabled={
							(!agentResuming && (
								snapshot.controller.state === "connecting" ||
								snapshot.controller.state === "stopped"
							)) ||
							settingsLoading ||
							session?.provisionState === "provisioning" ||
							// A save in flight is not here: the bar holds back what cannot overlap one.
							controllerTransitioning || newWorkDisabled || settingsLocked
						}
				/>
			) : null,
		[
			settingsReady,
			settingsLocked,
			settingsLoading,
			lastModel,
			configOptionError,
			configOptionPending,
			configOptions,
			controllerTransitioning,
			models,
			agentSwitch,
			stableUsage,
			stableRateLimits,
			newWorkDisabled,
			session?.provisionState,
			onChooseConfigOption,
			onChooseSettings,
			onRememberPermissions,
			rememberPermissionsPending,
			rememberPermissionsError,
			rememberedPermissionMode,
			showApprovalMode,
			approvalModes,
			approvalModeLimits,
			session?.cloud,
			snapshot.controller.state,
			snapshot.items.length,
			turn,
			stableModelReroute,
			stableSettings,
			compactsAt,
		],
	);
	const composerApproval = useMemo(
		() =>
			stablePendingApproval ? (
				<ApprovalCard
					activity={stablePendingApproval}
					onDecide={onDecide}
					busy={busy}
					embedded
				/>
			) : undefined,
		[busy, onDecide, stablePendingApproval],
	);
	const composerElicitation = useMemo(
		() =>
			stablePendingUserInput ? (
				<ElicitationDock key={stablePendingUserInput.id} activity={stablePendingUserInput} onResolve={onResolveInput} />
			) : undefined,
		[onResolveInput, stablePendingUserInput],
	);
	const canSteerQueuedMessage =
		Boolean(onSteer) && can(snapshot, "steer") && turn?.state === "running";
	const composerQueuedDock = useMemo(
		() =>
			visibleQueuedMessages.length > 0 ? (
				<QueuedMessageDock
					messages={visibleQueuedMessages}
					editingTurnId={queueEdit?.turnId}
					disabled={Boolean(queueEdit?.clientMessageId)}
					canSteer={canSteerQueuedMessage}
					onPromoteQueuedTurn={newWorkDisabled ? undefined : promoteQueuedTurn}
					onBeginQueuedEdit={
						newWorkDisabled || !draftPersistenceAvailable || !onEditQueuedTurn ? undefined : beginQueuedEdit
					}
					onCancelQueuedTurn={newWorkDisabled ? undefined : handleCancelQueuedTurn}
					onReorderQueuedTurns={newWorkDisabled || !onReorderQueuedTurns ? undefined : reorderQueuedTurns}
					promotePendingTurnId={promoteQueuedTurnPendingTurnId}
					cancelPendingTurnId={cancelQueuedTurnPendingTurnId}
					steerErrors={steerErrors}
				/>
			) : null,
		[
			beginQueuedEdit,
			canSteerQueuedMessage,
			cancelQueuedTurnPendingTurnId,
			draftPersistenceAvailable,
			handleCancelQueuedTurn,
			newWorkDisabled,
			onEditQueuedTurn,
			onReorderQueuedTurns,
			reorderQueuedTurns,
			promoteQueuedTurn,
			promoteQueuedTurnPendingTurnId,
			queueEdit?.clientMessageId,
			queueEdit?.turnId,
			steerErrors,
			visibleQueuedMessages,
		],
	);
	const composerDraftSeed = useMemo(
		() => (queueEdit ? { id: `${queueEdit.turnId}:${queueEdit.ownerId ?? queueEdit.expectedRevision ?? "legacy"}`, text: queueEdit.text, attachments: queueEdit.attachments, stagedAttachments: queueEdit.stagedAttachments } : undefined),
		[queueEdit],
	);
	const sentMessageList = useMemo(
		() => sentMessageHistory(snapshot, localEchos ?? [], openingTurnId, backgroundWakeQueuedTurnIds),
		[backgroundWakeQueuedTurnIds, localEchos, openingTurnId, snapshot],
	);
	// Stable across streaming deltas, so the memoized composer only renders for a new prompt.
	const sentMessages = useStableValue(sentMessageList);
	// Empty chats center the prompt; once a turn or item exists the composer docks
	// at the bottom and stays there for the rest of the session.
	const orchestratorStarting = sessionRole === "orchestrator" && startupState !== "failed" && (
		startupState === "provisioning" || agentResuming ||
		(!loadingQuietly && (snapshot.controller.state === "connecting" || snapshot.controller.state === "recovering"))
	);
	// A Freestyle Chat session starting or waking stays on its conversation:
	// the composer shimmers and its messages wait in the control plane's turn
	// queue until the worker connects (cloudChatStartingInPlace).
	const cloudStarting = cloudChatStartingInPlace(session);
	// A local or other-provider orchestrator opens its composer once it is up.
	const startupQueues = cloudStarting || (session?.provisionState === "provisioning" && sessionRole !== "orchestrator");
	const orchestratorBlocksComposer = orchestratorStarting && !startupQueues;
	// One steady line for the whole start. A local start runs its steps in a
	// second or two, and naming each one flickered through the placeholder.
	const setupPlaceholder = "Starting your orchestrator";
	// Arriving from the terminal reuses the startup composer: the shimmer plus a
	// placeholder that follows the controller. Leaving for the terminal
	// (newWorkDisabled) stays quiet, since that screen is about to go away.
	const arrivingInChat = (Boolean(controllerTransitioning) || historyPending) && !accountMoving && !newWorkDisabled && startupState !== "provisioning";
	// A new orchestrator has no conversation to restore; keep the start's words.
	const arrivingPlaceholder = sessionRole === "orchestrator" && !historyPending && !session?.lastUserMessageAt
		? setupPlaceholder
		: historyPending || snapshot.controller.state === "connecting" || snapshot.controller.state === "recovering"
		? "Restoring your conversation"
		: "Starting the chat agent";
	const compactStartup = sessionRole === "orchestrator" && startupState === "failed" ? startup : undefined;
	// Arriving in a chat that already has messages docks the composer at the
	// bottom while they load; a chat with none opens centered like any new chat.
	const arrivingWithHistory = arrivingInChat && (historyPending || Boolean(session?.lastUserMessageAt));
	// A chat started from the new chat page holds the start-up shimmer from the
	// send until the agent picks the brief up, on that page and on the session's.
	// Arriving from the new chat page, whose header strip is empty, the session's
	// header slides into that strip. Only on that arrival, not on a later visit.
	const [headerSlideIn] = useState(() => {
		const slideKey = uiSessionId ?? snapshot.sessionId;
		if (!openingStartedAt || Date.now() - Date.parse(openingStartedAt) > 15_000 || headerSlidSessions.has(slideKey)) return false;
		headerSlidSessions.add(slideKey);
		// Past the arrival window the start time alone rules out a replay.
		setTimeout(() => headerSlidSessions.delete(slideKey), 15_000);
		return true;
	});
	const openingStarting = Boolean(openingStartedAt) && startupState !== "failed" && !openingTurnSettled(snapshot, controllerStopped) &&
		!(turn?.state === "running" && snapshot.controller.state !== "connecting");
	const conversationEmpty = !arrivingWithHistory && snapshot.items.length === 0 && !turn && (localEchos?.length ?? 0) === 0 && (!hasStartup || sessionRole === "orchestrator");
	const { t } = useTranslation();
	const [emptyChatPlaceholder] = useState(
		() => sessionRole === "orchestrator"
			? "Ask anything about this project"
			: EMPTY_CHAT_PLACEHOLDERS[Math.floor(Math.random() * EMPTY_CHAT_PLACEHOLDERS.length)],
	);
	const composerDockRef = useRef<HTMLDivElement>(null);
	// The composer itself, not the dock: the dock also holds the welcome heading, which
	// disappears on the first send, and sliding from the dock's top would make the
	// composer jump up by the heading's height before it moved.
	const composerBoxRef = useRef<HTMLDivElement>(null);
	const wasConversationEmpty = useRef(conversationEmpty);
	const [welcomeLeaving, setWelcomeLeaving] = useState(false);
	const composerCenteredTopRef = useRef<number | null>(null);
	const composerFlipDyRef = useRef<number | null>(null);
	const composerSessionRef = useRef(uiSessionId);

	useLayoutEffect(() => {
		const dock = composerDockRef.current;
		if (!dock) return;

		const sessionChanged = composerSessionRef.current !== uiSessionId;
		if (sessionChanged) {
			composerSessionRef.current = uiSessionId;
			composerCenteredTopRef.current = null;
			composerFlipDyRef.current = null;
			dock.style.transition = "";
			dock.style.transform = "";
			dock.removeAttribute("data-composer-motion");
		}

		const composerTop = () => (composerBoxRef.current ?? dock).getBoundingClientRect().top;
		if (conversationEmpty) {
			composerCenteredTopRef.current ??= composerTop();
			composerFlipDyRef.current = null;
			return;
		}

		// Capture the centered→docked delta once. Keep it across Strict Mode's
		// setup→cleanup→setup so the docking motion still plays.
		if (composerFlipDyRef.current == null && composerCenteredTopRef.current != null) {
			composerFlipDyRef.current = composerCenteredTopRef.current - composerTop();
			composerCenteredTopRef.current = null;
		}

		const dy = composerFlipDyRef.current;
		const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
		if (dy == null || reduceMotion || Math.abs(dy) < 1) {
			composerFlipDyRef.current = null;
			return;
		}

		dock.dataset.composerMotion = "animating";
		dock.style.transition = "none";
		dock.style.transform = `translateY(${dy}px)`;
		void dock.offsetHeight;
		dock.style.transition = "transform 500ms cubic-bezier(0.22, 1, 0.36, 1)";
		dock.style.transform = "translateY(0)";

		const onEnd = (event: TransitionEvent) => {
			if (event.target !== dock || event.propertyName !== "transform") return;
			composerFlipDyRef.current = null;
			dock.style.transition = "";
			dock.style.transform = "";
			dock.removeAttribute("data-composer-motion");
		};
		dock.addEventListener("transitionend", onEnd);
		return () => {
			dock.removeEventListener("transitionend", onEnd);
		};
	}, [conversationEmpty, uiSessionId]);

	// The welcome heading fades out where it stood instead of vanishing, so the first
	// send does not pop it off the screen.
	useLayoutEffect(() => {
		const leaving = wasConversationEmpty.current && !conversationEmpty;
		wasConversationEmpty.current = conversationEmpty;
		if (!leaving) {
			if (conversationEmpty) setWelcomeLeaving(false);
			return;
		}
		setWelcomeLeaving(true);
		const timer = window.setTimeout(() => setWelcomeLeaving(false), WELCOME_FADE_MS);
		return () => window.clearTimeout(timer);
	}, [conversationEmpty]);

	return (
 <FileAnnotationContext.Provider value={fileReferenceDelivery === "text" ? undefined : { sessionId: snapshot.sessionId, hostId: activeRemoteHostId, onOpen: onOpenFileAnnotation, generation: fileNavigationGeneration }}>
		<section
			ref={surfaceRef}
			data-chat-find-scope=""
			onKeyDown={handleChatKeyDown}
			onClick={handleChatSurfaceClick}
			aria-label="Chat"
			className="cursor-chat-surface flex h-full min-h-0 min-w-0 w-full flex-col overflow-hidden [font-size:var(--chat-font-size)]"
			data-session-mode={snapshot.mode}
			data-session-role={sessionRole}
			style={
				{
					"--chat-font-size": `${CHAT_FONT_SIZE_DEFAULT}px`,
				} as CSSProperties
			}
		>
			{hideHeader ? null : <ChatHeader
				snapshot={snapshot}
				sessionTitle={sessionTitle}
				sessionRole={sessionRole}
				onOpenReviewerTerminal={onOpenReviewerTerminal}
				onOpenReviewerChat={onOpenReviewerChat}
				reviewerActive={reviewerActive}
				onSelectChat={onSelectChat}
				shellActiveHandleId={shellActive ? shellTarget?.handleId : undefined}
				onSelectShellTerminal={onSelectShellTerminal}
				onCloseShellTerminal={onCloseShellTerminal}
				onRenameShellTerminal={onRenameShellTerminal}
				onTabsKeyDown={handleChatTabsKeyDown}
				headerActions={headerActions}
				session={session}
				onSessionRenamed={onSessionRenamed}
				sessionTabAction={sessionTabAction}
				sessionTabActionPinned={sessionTabActionPinned}
				tabStripAction={tabStripAction}
				workspaceTabActions={workspaceTabActions}
				workspaceActiveTabKey={workspaceActiveTabKey}
				orderedAuxiliaryTabs={orderedAuxiliaryTabs}
				onReorderAuxiliaryTabs={reorderAuxiliaryTabs}
				inline={isFullscreen || inlineHeader}
				slideIn={headerSlideIn}
				fullscreen={isFullscreen}
			/>}
			<div className="relative flex min-h-0 flex-1 flex-col">
				{reviewerTarget && session ? (
					<div
						aria-label="Reviewer terminal"
						className="relative min-h-0 flex-1"
						data-testid="chat-reviewer-panel"
						onWheelCapture={handleWheelZoom}
						role="tabpanel"
					>
						<div className="h-full min-h-0" data-testid="chat-reviewer-terminal">
							{activeRemoteHostId && !assetBaseUrl ? <OfflineRemoteTerminal /> : <TerminalPane
								key={activeRemoteHostId ? `${activeRemoteHostId}:${assetBaseUrl}` : undefined}
								createMux={activeRemoteHostId ? remoteCreateMux : undefined}
								daemonReady={activeRemoteHostId ? Boolean(assetBaseUrl) : Boolean(daemonReady)}
								fontSize={terminalFontSize}
								isFullscreen={isFullscreen}
								onChangeFontSize={updateTerminalFontSize}
								onToggleFullscreen={toggleFullscreen}
								session={session}
								terminalTarget={reviewerTarget}
								theme={theme ?? "dark"}
							/>}
						</div>
					</div>
				) : null}
				{shellTarget && session ? (
					<div
						aria-label="Shell terminal"
						className="relative min-h-0 flex-1"
						data-testid="chat-shell-panel"
						onWheelCapture={handleWheelZoom}
						role="tabpanel"
					>
						<div className="h-full min-h-0" data-testid="chat-shell-terminal">
							{activeRemoteHostId && !assetBaseUrl ? <OfflineRemoteTerminal /> : <TerminalPane
								key={activeRemoteHostId ? `${activeRemoteHostId}:${assetBaseUrl}` : undefined}
								createMux={activeRemoteHostId ? remoteCreateMux : undefined}
								daemonReady={activeRemoteHostId ? Boolean(assetBaseUrl) : Boolean(daemonReady)}
								fontSize={terminalFontSize}
								focusRequested
								isFullscreen={isFullscreen}
								onChangeFontSize={updateTerminalFontSize}
								onToggleFullscreen={toggleFullscreen}
								session={session}
								terminalTarget={shellTarget}
								theme={theme ?? "dark"}
							/>}
						</div>
					</div>
				) : null}
				<div
					aria-hidden={reviewerActive || shellActive}
					aria-label="Chat conversation"
					className="flex min-h-0 flex-1 flex-col"
					data-testid="chat-conversation-panel"
					hidden={reviewerActive || shellActive}
					inert={
						reviewerActive || shellActive || agentInputDisabled || (controllerTransitioning && !accountMoveQueues)
							? true
							: undefined
					}
					role="tabpanel"
				>
				<HoldWhileHidden hidden={reviewerActive || shellActive}>
					{snapshot.account ? (
						<ReauthBanner key={`${snapshot.sessionId}:${snapshot.conversationId}`} account={snapshot.account} harness={snapshot.harness} sessionId={snapshot.sessionId}
							hostId={activeRemoteHostId} cloud={Boolean(session?.cloud)} />
					) : null}
					{!draftPersistenceAvailable ? (
						<div role="status" className="flex shrink-0 items-center gap-2 border-b border-border bg-surface px-4 py-2 text-xs text-muted-foreground">
							<TriangleAlert aria-hidden="true" className="size-3.5 shrink-0 text-warning" />
							{t("chat.draft.storageUnavailable")}
						</div>
					) : null}
					{savedCopy || importBlock ? null : (
						<ControllerBanner
							controller={snapshot.controller}
							quietConnect={Boolean(openingStartedAt) && !firstTurnSettled(snapshot)}
							provisionState={orchestratorStarting ? "provisioning" : session?.provisionState}
							needsResume={session?.needsResume}
							transitioning={controllerTransitioning || agentResuming}
							automaticWakePending={suppressStopped}
							reconnecting={startupReconnecting}
							terminated={terminated}
							onResume={newWorkDisabled ? undefined : onResumeAgent}
							resuming={resumingAgent}
							resumeError={resumeError}
							resumeOpenElsewhere={resumeOpenElsewhere}
							harness={snapshot.harness}
							onOpenShell={onOpenShell}
							openingShell={openingShell}
							shellError={shellError}
							signInRequired={signInRequired}
						/>
					)}
					{snapshot.threadState ? <ThreadStateBanner threadState={snapshot.threadState} /> : null}
					<VersionSwitchBanner
						staleBinary={session?.staleBinary}
						hostId={activeRemoteHostId}
						// Only a resume starts a stopped agent, on the new release.
						agentStopped={controllerStopped}
					/>
					<div
						className={cn("flex min-h-0 flex-1 flex-col", conversationEmpty && "justify-center", conversationEmpty && centerInWindow && "pb-inspector-tabs", arrivingWithHistory && snapshot.items.length === 0 && "justify-end")}
						data-composer-placement={conversationEmpty ? "center" : "dock"}
					>
						<ChatLinkProvider
							sessionId={snapshot.sessionId}
							onLinkOpen={onLinkOpen}
							onFileOpen={onOpenFile}
							onSessionLinkOpen={onSessionLinkOpen}
							remoteHost={Boolean(activeRemoteHostId)}
							sessionLinkHostId={activeRemoteHostId}
							sessionLinkSourceKind={session?.cloud ? "cloud" : undefined}
							workspacePaths={filePaths}
						>
							<ChatImageSourceProvider sessionId={snapshot.sessionId} assetBaseUrl={assetBaseUrl} remoteHost={Boolean(activeRemoteHostId)} artifacts={session?.artifactFiles} cloud={session?.cloud}>
							<ChatHostId.Provider value={activeRemoteHostId}>
							<ChatTokenSkills.Provider value={skills ?? NO_SKILLS}>

								<Timeline
									shownAt={panelShownAt}
							onAddToSideChat={onAddToSideChat}
									checkingSideChatAvailability={checkingSideChatAvailability}
									workerUpdates={sessionRole === "orchestrator"}
									annotationNavigationRef={annotationNavigationRef}
									key={draftScopeKey}
									snapshot={timelineSnapshot}
									controllerStopped={controllerStopped}
									assetBaseUrl={assetBaseUrl}
									remoteHost={Boolean(activeRemoteHostId)}
									findUnavailable={Boolean(session?.cloud)}
									draftScope={draftScope}
									draftPersistenceAvailable={draftPersistenceAvailable}
									hasOlder={hasOlder}
									loadingOlder={loadingOlder}
									onLoadOlder={onLoadOlder}
									excerptsEnabled={excerptsEnabled}
									onDecide={onDecide}
									busy={busy}
									onRollback={rollbackTarget}
									onOpenFiles={onOpenFiles}
									onOpenFile={onOpenFile}
									onOpenArtifact={onOpenArtifact}
									retryControl={retryControl}
									composerRecoveryTurnId={retryControl ? limitedTurn?.id : undefined}
									onEditHumanMessage={draftPersistenceAvailable ? editHumanMessage : undefined}
									editPending={editMessagePending}
									editBusy={Boolean(durableTurn)}
									editError={editMessageError}
									onActivateBranch={onActivateBranch}
									activateBranchPending={activateBranchPending}
									activateBranchError={activateBranchError}
									newWorkDisabled={newWorkDisabled}
									rollbackDisabled={Boolean(durableTurn || rollbackPending || newWorkDisabled)}
									localEchos={localEchos}
									startup={sessionRole === "orchestrator" ? undefined : startup}
									openingStartedAt={openingStartedAt}
									// A Freestyle Chat session started from this device: the row under its
									// brief says the sandbox is still coming up until its worker connects.
									openingStartupLabel={cloudStarting && openingStartedAt ? t("chat.startup.sandbox") : undefined}
									arriving={arrivingInChat}
								/>
							</ChatTokenSkills.Provider>
							</ChatHostId.Provider>
							</ChatImageSourceProvider>
						</ChatLinkProvider>

						<div ref={composerDockRef} className="cursor-chat-composer-dock shrink-0 px-4 pb-3">
							<div
								data-empty={conversationEmpty || undefined}
								className="mx-auto flex w-full max-w-3xl flex-col gap-2 transition-[max-width] duration-500 ease-out data-[empty]:max-w-2xl"
							>
								{discarded > 0 ? <RolledBackNotice count={discarded} /> : null}
								{limitedTurn?.usageLimit && retryControl ? (
									<UsageLimitNotice
										key={limitedTurn.id}
										className="justify-end"
										usageLimit={limitedTurn.usageLimit}
										onResume={() => { void Promise.resolve(retryControl.retry(limitedTurn.id)).catch(() => undefined); }}
										onAutoResumeChange={retryControl.setAutoResume
											? (enabled) => { void Promise.resolve(retryControl.setAutoResume?.(limitedTurn.id, enabled)).catch(() => undefined); }
											: undefined}
										pending={retryControl.pending || retryControl.autoResumePending}
										disabled={Boolean(durableTurn || busy || newWorkDisabled || agentInputDisabled || controllerTransitioning || agentResuming)}
										error={(retryControl.turnId === limitedTurn.id ? retryControl.error : undefined) ??
											(retryControl.autoResumeTurnId === limitedTurn.id ? retryControl.autoResumeError : undefined)}
									/>
								) : null}
								{conversationEmpty ? (
									<h1 className="mb-5 text-center text-2xl font-normal tracking-tight text-foreground sm:text-3xl">
										{t(artifactSession ? "chat.welcome.artifactHeading" : "chat.welcome.heading")}
									</h1>
								) : null}
								<WelcomePromptSuggestions
									// While an orchestrator starts the prompts stay put, disabled, so they don't
									// pop in at the handoff. Other fenced chats keep them hidden.
									open={conversationEmpty && composerEmpty && (!newWorkDisabled || pendingInPlaceStart || orchestratorStarting)}
									artifact={artifactSession}
									unavailable={newWorkDisabled || pendingInPlaceStart}
									onPick={(text) => { if (!newWorkDisabled && !pendingInPlaceStart) composerFocusRef.current?.setDraft(text); }}
								/>
								<div ref={composerBoxRef} className="relative z-10" data-composer-tray={conversationEmpty && composerTray ? "" : undefined}>
									{welcomeLeaving ? (
										<h1
											aria-hidden="true"
											className="chat-welcome-leaving pointer-events-none absolute inset-x-0 bottom-full mb-5 text-center text-2xl font-normal tracking-tight text-foreground sm:text-3xl"
										>
											{t(artifactSession ? "chat.welcome.artifactHeading" : "chat.welcome.heading")}
										</h1>
									) : null}
									<McpServerBanner
										key={draftScopeKey}
										sessionId={uiSessionId}
										incarnation={draftScope.incarnation}
										servers={brokenServers}
										placement={conversationEmpty ? "below" : "above"}
										active={!workspaceActiveTabKey && !reviewerActive && !shellActive}
									/>
									{importBlock && session ? (
										<ImportedAgentBlocked agent={session.provider} block={importBlock} />
									) : session?.mode === "tui" && session.needsResume ? (
										<p className="py-3 text-sm text-muted-foreground" role="status">{t("sessionImport.terminalArchive")}</p>
									) : <ChatComposer
										notice={composerNotice}
										onSelectAnnotation={navigateToAnnotation}
										focusRef={composerFocusRef}
										key={`${draftScopeKey}:${draftPersistenceAvailable ? "saved" : "memory"}:${queueEdit ? `${queueEdit.turnId}:${queueEdit.ownerId ?? queueEdit.expectedRevision ?? "legacy"}` : "composer"}`}
										queuedDock={composerQueuedDock}
										approval={composerApproval}
										elicitation={composerElicitation}
										onSend={handleComposerSend}
						fileReferencesEnabled={fileReferencesEnabled && Boolean(snapshot.supportsFileReferences)}
						fileReferenceDelivery={fileReferenceDelivery}
						messageByteLimit={messageByteLimit}
										sentMessages={sentMessages}
										sentMessagesScope={`${snapshot.conversationId}:${snapshot.activeBranchId ?? ""}`}
										draftSeed={composerDraftSeed}
										promptSuggestion={!queueEdit && !composerApproval && !composerElicitation && !turn &&
											!busy && !sendPending && !steerPending && !agentInputDisabled && !newWorkDisabled &&
											!controllerTransitioning && !loadingQuietly && !agentResuming &&
											!workspaceActiveTabKey && !reviewerActive && !shellActive && snapshot.controller.state === "ready"
											? promptSuggestion : undefined}
										editingQueuedTurnId={queueEdit?.turnId}
										queuedEditRecovery={Boolean(queueEdit?.clientMessageId)}
										savingQueuedEditPending={Boolean(
											queueEdit?.turnId &&
												editQueuedTurnPendingTurnId === queueEdit.turnId,
										)}
										onCancelQueuedEdit={cancelQueuedEdit}
										onQueuedDraftChange={queueEdit ? changeQueuedDraft : undefined}
										onDraftEmptyChange={setComposerEmpty}
										onDraftInput={onComposerInput}
										queuedDraftScope={queueEdit ? draftScope : undefined}
										onQueuedAttachmentsChange={changeQueuedStagedAttachments}
										onQueuedRetainedAttachmentsChange={changeQueuedRetainedAttachments}
										onInterrupt={turn && !newWorkDisabled && !openingStarting && !accountMoveQueues ? stableInterrupt : undefined}
										onContinue={canContinue && !queueEdit && !openingStarting ? onContinueTurn : undefined}
										commandError={queueDraftError ?? (queueEdit && !queueEdit.clientMessageId && !queuedMessages.some((entry) => entry.turnId === queueEdit.turnId) ? "chat.draft.queueMissing" : commandError)}
										settings={draftSettings ?? <>
											{agentSwitch ? null : <ContextMeter usage={snapshot.usage} rateLimits={snapshot.rateLimits} compactsAt={compactsAt} />}
											{composerSettings}
										</>}
										addMenuItems={agentSwitch ? (
											<ComposerModeItems
												configOptions={configOptions ?? []}
												settings={stableSettings}
												models={models}
												nativePlan={can(snapshot, "plan_mode")}
												onChange={newWorkDisabled ? undefined : onChooseSettings}
												onChangeConfigOption={newWorkDisabled ? undefined : onChooseConfigOption}
												disabled={configOptionPending || controllerTransitioning}
											/>
										) : undefined}
										busy={busy}
										willQueue={Boolean(turn) || startupQueues || accountMoveQueues}
										queuePlaceholder={
											session?.provisionState === "provisioning" || cloudStarting || accountMoveQueues
												? t("chat.startup.queuePlaceholder", { agent: agentLabel(snapshot.harness) })
												: undefined
										}
										starting={orchestratorStarting || arrivingInChat || cloudStarting || composerStarting}
										disabled={(orchestratorBlocksComposer || (openingStarting && !session) || loadingQuietly || (snapshot.controller.state === "stopped" && !suppressStopped && !accountMoveQueues && (!resumingAgent || session?.provisionState === "failed" || terminated)) || (controllerTransitioning && !accountMoveQueues) || newWorkDisabled || composerLockedPlaceholder !== undefined) && !queueEdit?.clientMessageId}
										disabledPlaceholder={
											composerLockedPlaceholder !== undefined
												? composerLockedPlaceholder
												: arrivingInChat
												? arrivingPlaceholder
												: (openingStarting && !session) || pendingInPlaceStart
												// Same words the session shows once it can queue, so nothing changes on handoff.
												? t("chat.startup.queuePlaceholder", { agent: agentLabel(snapshot.harness) })
												: orchestratorBlocksComposer
												? setupPlaceholder
												: controllerTransitioning || newWorkDisabled
												? ""
												: terminated && snapshot.controller.state === "stopped"
												? t(resumingAgent ? "shell.restoringSession" : "chat.archived.placeholder")
												: agentResuming
													? (sessionRole === "orchestrator" ? "Starting the orchestrator" : "Resuming agent")
													: startupReconnecting
														? "Reconnecting to the agent…"
														: signInRequired && snapshot.controller.state === "stopped"
															? t("chat.reauth.composerPlaceholder")
															: undefined
										}
										// Keep the composer useful outside the centered welcome state too. A
										// task can have non-message activity before its first visible chat
										// message, and the generic placeholder makes a still-empty composer
										// look like a regression.
										emptyPlaceholder={conversationEmpty ? (artifactSession ? t("chat.welcome.artifactPlaceholder") : emptyChatPlaceholder) : undefined}
										skills={skills}
										filePaths={filePaths}
										filePathsTruncated={filePathsTruncated}
										onFileQuery={onFileQuery}
										onStageAttachments={newWorkDisabled ? undefined : onStageAttachments}
										cloudAttachments={cloudAttachments}
										holdAttachments={newWorkDisabled ? undefined : holdAttachments}
										onHeldAttachmentsChange={onHeldAttachmentsChange}
										nativeImages={queueEdit?.clientMessageId ? queueEdit.nativeImages ?? nativeImages : nativeImages}
										autoFocus={!reviewerActive}
										autoFocusKey={draftScope.sessionId}
										// Steering is only meaningful into a turn that is running. A queued turn
										// has not reached the provider, so there is nothing to steer.
										onSteer={newWorkDisabled ? undefined : steer}
										showSteerButton={showSteerButton}
										canSteer={Boolean(onSteer) && turn?.state === "running"}
										sendPending={sendPending || agentResuming}
										steerPending={steerPending}
										steerRefusal={steerRefusal}
										onCompact={newWorkDisabled || snapshot.controller.state === "hibernated" ? undefined : onCompact}
										compacting={compacting}
										compactUnavailable={compactUnavailable}
										compactBlocked={Boolean(durableTurn)}
										draftSessionId={draftPersistenceAvailable && !queueEdit ? draftScope.sessionId : undefined}
										draftSessionIncarnation={draftPersistenceAvailable ? draftScope.incarnation : undefined}
										assetBaseUrl={assetBaseUrl}
										assetSessionId={snapshot.sessionId}
										remoteHost={Boolean(activeRemoteHostId)}
										acceptedClientMessageIds={acceptedClientMessageIds}
										parkUnsentSends={parkUnsentSends}
									/>}
								</div>
								{conversationEmpty && composerTray ? <div className="chat-composer-tray flex items-center">{composerTray}</div> : null}
								{compactStartup ? <OrchestratorStartupStatus {...compactStartup} /> : null}
								{draftNotice}
							</div>
						</div>
					</div>
				</HoldWhileHidden>
				</div>
			</div>

			{/* The copy has to be honest about the cost: this is not "hide these
			    messages", it is "the agent forgets them". Nothing in the worktree is
			    reverted either, and a user who assumed otherwise would be badly
			    surprised, so it is said out loud. */}
			<ConfirmDialog
				open={Boolean(confirming) && !reviewerActive && !shellActive}
				onOpenChange={(open) => {
					if (!open) setConfirming(undefined);
				}}
				title="Roll back to this point?"
				description={
					<>
						<p className="text-xs font-medium text-foreground">
							The agent will forget this exchange and everything after it.
						</p>
						<p className="mt-1 text-xs text-muted-foreground">
							Its memory of the conversation is discarded up to this point, so it will not know
							about anything you or it said later. Files it already changed in the worktree are left
							exactly as they are; only the conversation is rolled back. This cannot be undone.
						</p>
					</>
				}
				confirmLabel="Roll back"
				destructive
				busy={rollbackPending}
				error={rollbackError ?? null}
				onConfirm={() => {
					const turnId = confirming;
					if (!turnId) return;
					setConfirming(undefined);
					void Promise.resolve(onRollback?.(turnId)).catch(() => {});
				}}
			/>
		</section>
 </FileAnnotationContext.Provider>
	);
}

function OrchestratorStartupStatus({ error, onRetry, retrying, retryError }: ComponentProps<typeof SessionStartup>) {
	const { t } = useTranslation();
	return (
		<div data-testid="orchestrator-startup-status" className="px-2 text-xs text-muted-foreground">
			<div role="alert" className="flex items-center gap-2">
				<TriangleAlert aria-hidden="true" className="size-3.5 text-destructive" />
				<span>{t("chat.startup.failed")}</span>
				{onRetry ? <Button type="button" variant="link" className="ml-auto text-xs text-foreground" onClick={onRetry} disabled={retrying}>{retrying ? t("chat.startup.retrying") : t("chat.startup.retry")}</Button> : null}
			</div>
			{error ? <details className="mt-2"><summary className="cursor-pointer">Error details</summary><p className="mt-1 break-words">{error}</p></details> : null}
			{retryError ? <p role="alert" className="mt-1 text-destructive">{retryError}</p> : null}
		</div>
	);
}

/**
 * What an undo took away.
 *
 * Stated above the composer rather than as a timeline entry, because the discarded
 * turns keep their original sequence positions and this notice has none of its own —
 * placing it in the timeline would be claiming an order it cannot know. It sits where
 * the user is looking after an undo, and it says the part that matters: the agent
 * does not remember.
 */
function RolledBackNotice({ count }: { count: number }) {
	return (
		<p className="flex items-center gap-1.5 text-xs text-muted-foreground">
			<Undo2 aria-hidden="true" className="size-3 shrink-0" />
			{count === 1
				? "1 turn was rolled back. The agent no longer remembers it."
				: `${count} turns were rolled back. The agent no longer remembers them.`}
		</p>
	);
}

/**
 * Batch consecutive plain activities into a run.
 *
 * A run of tool calls is one thought, not several, so it renders as one tight
 * block on a shared rail. Messages and approvals break a run because each is
 * something the reader stops on: prose to read, or a decision to make.
 */
type TimelineRun =
	| { kind: "activities"; key: string; items: ConversationItem[] }
	| { kind: "single"; key: string; items: [ConversationItem] };

function runsOf(items: ConversationItem[]): TimelineRun[] {
	const runs: TimelineRun[] = [];
	for (const item of items) {
		// Approvals, user input, errors, reasoning, and stamped system events stay
		// boundaries. Confirmed AO actions have no event stamp, so they batch.
		const runnable = isOrdinaryActivity(item);
		const last = runs.at(-1);
		if (runnable && last?.kind === "activities") {
			last.items.push(item);
			continue;
		}
		runs.push(
			runnable
				? {
						kind: "activities",
						key: `run-${item.sequence}`,
						items: [item],
					}
				: { kind: "single", key: itemKey(item), items: [item] },
		);
	}
	return runs;
}

/**
 * What belongs in a conversation, as opposed to what the provider happens to emit.
 *
 * Two kinds are dropped:
 *
 *   - usage. The daemon now projects it as current state on the snapshot rather
 *     than as a timeline entry, so this filter is a guard for conversations
 *     recorded by an older build whose usage rows are still on disk. Rendering one
 *     row per report is what buried the actual conversation; it lives in the
 *     header meter instead.
 *   - reasoning. Providers can emit one per tool call and they usually carry no
 *     readable body, so they are internal signal rather than conversation chrome.
 *
 *   - a plan row whose turn already carries the plan. The daemon writes both, from
 *     one event, so they cannot disagree; the turn's copy renders as a checklist at
 *     the end of the turn and a second rendering of the same plan in the middle of
 *     it would just be the same list twice.
 *   - a plan row the daemon marked superseded: an older copy of a list the agent
 *     keeps across turns, which a later turn or run carries.
 *   - a completed html_render call. AO records the page it showed as a render row
 *     right after it, and the call's own answer is a note to the agent.
 *
 * Everything else is kept, including an activity this build does not fully
 * understand — dropping an unrecognized item would hide work the agent really did.
 */
function readableItems(snapshot: ConversationSnapshot): ConversationItem[] {
	const plannedTurns = new Set(
		snapshot.turns.filter((turn) => turn.plan?.steps.length).map((turn) => turn.id),
	);
	const pendingQuestionTurns = new Set(
		snapshot.items
			.filter((item) => item.kind === "activity" && item.activityKind === "user_input" && item.status === "pending")
			.map((item) => item.turnId),
	);
	return snapshot.items.filter((item) => {
		if (item.kind !== "activity") return true;
		if (item.activityKind === "usage") return false;
		if (item.activityKind === "plan" && item.turnId && plannedTurns.has(item.turnId)) return false;
		if (item.activityKind === "plan" && item.detail?.superseded) return false;
		if (item.activityKind === "reasoning") return false;
		// The page an html_render call shows is its own row right after it.
		if (isShownAORender(item)) return false;
		// Reading Orchestrator's own guides or `ao --help` is how the agent learns the
		// tool, not work on the user's task.
		if (isOrchestratorPlumbing(item)) return false;
		// Claude emits both a tool call and an elicitation for the same question.
		// The pending form already presents it above the composer.
		if (
			item.activityKind === "mcp_tool" &&
			item.detail?.providerToolName === "AskUserQuestion" &&
			(item.status === "pending" || item.status === "running") &&
			item.turnId && pendingQuestionTurns.has(item.turnId)
		) return false;
		return true;
	});
}

/* -------------------------------------------------------------------------- */

// The conversation panel stays mounted (display:none, inert) while a terminal
// or reviewer tab covers it. Re-rendering its timeline on every workspace
// update while nobody can see it made opening a terminal re-render the whole
// transcript. Hold it while hidden; it catches up when it is shown again.
// Components inside still re-render on their own state and subscriptions.
const HoldWhileHidden = memo(
	function HoldWhileHidden({ children }: { hidden: boolean; children: ReactNode }) {
		return <>{children}</>;
	},
	(_previous, next) => next.hidden,
);

function ChatHeader({
	snapshot,
	sessionTitle,
	sessionRole,
	onOpenReviewerTerminal,
	onOpenReviewerChat,
	reviewerActive,
	onSelectChat,
	shellActiveHandleId,
	onSelectShellTerminal,
	onCloseShellTerminal,
	onRenameShellTerminal,
	onTabsKeyDown,
	headerActions,
	sessionTabAction,
	sessionTabActionPinned = false,
	tabStripAction,
	workspaceTabActions,
	workspaceActiveTabKey,
	orderedAuxiliaryTabs,
	onReorderAuxiliaryTabs,
	inline,
	slideIn = false,
	fullscreen,
	session,
	onSessionRenamed,
}: {
	snapshot: ConversationSnapshot;
	sessionTitle?: string;
	sessionRole: SessionKind;
	onOpenReviewerTerminal?: (target: { handleId: string; harness: string }) => void;
	onOpenReviewerChat?: (target: { reviewId: string; harness: string }) => void;
	/** The reviewer tab is selected; the chat tab is the clickable alternative. */
	reviewerActive?: boolean;
	/** Return the tab strip to the chat tab. */
	onSelectChat?: () => void;
	/** The selected shell tab, if any. */
	shellActiveHandleId?: string;
	onSelectShellTerminal?: (handleId: string) => void;
	onCloseShellTerminal?: (handleId: string) => void;
	onRenameShellTerminal?: (handleId: string, title: string) => void;
	onTabsKeyDown?: (event: ReactKeyboardEvent<HTMLDivElement>) => void;
	headerActions?: ReactNode;
	sessionTabAction?: ReactNode;
	sessionTabActionPinned?: boolean;
	tabStripAction?: ReactNode;
	workspaceTabActions?: ReactNode;
	workspaceActiveTabKey?: string;
	orderedAuxiliaryTabs: ChatAuxiliaryTab[];
	onReorderAuxiliaryTabs: (keys: string[]) => void;
	session?: WorkspaceSession;
	onSessionRenamed?: () => void | Promise<void>;
	/** Fullscreen content cannot see the normal topbar portal outside its subtree. */
	inline?: boolean;
	slideIn?: boolean;
	fullscreen?: boolean;
}) {
	const { t } = useTranslation();
	const sessionIsOrchestrator = session
		? isOrchestratorSession(session)
		: sessionRole === "orchestrator";
	const label = sessionIsOrchestrator
		? t("shell.orchestrator")
		: (sessionTitle || session?.title || snapshot.title || snapshot.sessionId);
	const tabScrollWatch = `${session?.id ?? ""}|${orderedAuxiliaryTabs.map((tab) => tab.key).join("|")}`;
	const {
		scrollRef: tabsOverflowRef,
		scrollToEnd: scrollTabsToEnd,
	} = useTabScrollEdges([tabScrollWatch]);
	const previousTabCountRef = useRef(orderedAuxiliaryTabs.length);
	useEffect(() => {
		if (orderedAuxiliaryTabs.length > previousTabCountRef.current) scrollTabsToEnd();
		previousTabCountRef.current = orderedAuxiliaryTabs.length;
	}, [orderedAuxiliaryTabs.length, scrollTabsToEnd]);
	const selectShellTerminal = useStableCallback(onSelectShellTerminal);
	const closeShellTerminal = useStableCallback(onCloseShellTerminal);
	const renameShellTerminal = useStableCallback(onRenameShellTerminal);
	const stripShellHandleIds = useMemo(
		() => orderedAuxiliaryTabs.flatMap((tab) => (tab.kind === "shell" ? [tab.terminal.handleId] : [])),
		[orderedAuxiliaryTabs],
	);
	// The chat tab is "selected" only when neither terminal pane is the body.
	const timelineActive = !workspaceActiveTabKey && !reviewerActive && !shellActiveHandleId;
	// Match CenterPane: when the sidebar is off-canvas, the fixed TitlebarNav
	// cluster sits over the session tab strip. Terminal already reserves that
	// space; chat must too or the back/forward buttons land on the tab label.
	const isSidebarOpen = useUiStore(sidebarOccupiesLayout);
	const clearanceRef = useSidebarChromeClearanceRef<HTMLDivElement>(isMac);
	const tabListRef = useTabListTabStop<HTMLDivElement>();
	const header = (
		<SessionTabsTopbar
			className={slideIn ? "chat-header-slide-in" : undefined}
			fullscreen={fullscreen}
			actions={headerActions}
			clearanceClassName={cn(
				isMac && "session-topbar-titlebar-clearance-mac",
				!isSidebarOpen && isLinux && "session-topbar-titlebar-clearance-linux",
			)}
			clearanceRef={clearanceRef}
			tabsTrailing={tabStripAction}
			tabs={
					<div
						aria-label="Chat tabs"
						className="flex h-full min-w-0 items-stretch"
						onKeyDown={onTabsKeyDown ?? handleTerminalTabListKeyDown}
						ref={tabListRef}
						role="tablist"
					>
						<div className="relative min-w-0 flex-1 self-stretch overflow-hidden">
							<motion.div ref={tabsOverflowRef} className="edge-fade edge-fade-x scrollbar-none flex h-full min-w-flex-min min-w-0 items-stretch overflow-x-auto" data-tab-scroll-region layoutScroll>
								{/* The macOS header is a window drag region outside its controls; the
								    whole tab row opts out so separators and tab edges still take
								    wheel scrolling and clicks. */}
								<div className="flex w-max items-stretch [-webkit-app-region:no-drag]">
								<SessionTabSlot>
									{session ? (
										<SessionPaneTab
											isActive={timelineActive}
											label={label}
											onSelect={timelineActive ? undefined : onSelectChat}
											onRenamed={onSessionRenamed}
											session={session}
											tabAction={sessionTabAction}
											tabActionPinned={sessionTabActionPinned}
										/>
									) : (
										<SessionPaneTab
											isActive={timelineActive}
											label={label}
											onSelect={timelineActive ? undefined : onSelectChat}
											provider={snapshot.harness}
											title={label}
										/>
									)}
								</SessionTabSlot>
								<Reorder.Group
									as="div"
									axis="x"
									className="flex items-stretch self-stretch"
									onReorder={onReorderAuxiliaryTabs}
									values={orderedAuxiliaryTabs.map((tab) => tab.key)}
								>
									{orderedAuxiliaryTabs.map((tab) => tab.kind === "shell" ? (
										<ShellTerminalTabEntry
											isActive={tab.terminal.handleId === shellActiveHandleId && !workspaceActiveTabKey}
											key={tab.key}
											onClose={closeShellTerminal}
											onRename={onRenameShellTerminal ? renameShellTerminal : undefined}
											onSelect={selectShellTerminal}
											shell={tab.terminal}
											stripShellHandleIds={stripShellHandleIds}
											tabKey={tab.key}
										/>
									) : (
										<DraggableWorkspaceTab key={tab.key} value={tab.key}>
											{tab.kind === "reviewer" || tab.kind === "reviewer-chat" ? (
												<SessionPaneTab
													icon={<AgentAvatar className="size-icon-sm!" decorative provider={tab.terminal.harness} />}
													isActive={Boolean(reviewerActive && !workspaceActiveTabKey)}
													label={t("terminal.reviewer")}
													onSelect={() => tab.kind === "reviewer" ? onOpenReviewerTerminal?.(tab.terminal) : onOpenReviewerChat?.(tab.terminal)}
													tabKey={tab.key}
													tabMenu
													title={tab.terminal.harness}
												/>
											) : (
												tab.tab.menu ? (
													<TabContextMenu spec={{ ...tab.tab.menu, closeShortcut: tab.key === workspaceActiveTabKey }}>
														{tab.tab.content}
													</TabContextMenu>
												) : tab.tab.content
											)}
										</DraggableWorkspaceTab>
									))}
								</Reorder.Group>
								{workspaceTabActions}
								</div>
							</motion.div>
						</div>
					</div>
			}
		/>
	);
	return inline ? header : <SessionTopbarPortal>{header}</SessionTopbarPortal>;
}

const OPEN_ELSEWHERE_RECHECK_MS = 30_000;

/**
 * Controller health and hibernation. A silent surface is indistinguishable from
 * an agent that is simply thinking.
 */
function ControllerBanner({
	controller,
	provisionState,
	needsResume,
	transitioning,
	automaticWakePending,
	reconnecting,
	terminated = false,
	onResume,
	resuming,
	resumeError,
	resumeOpenElsewhere,
	harness,
	onOpenShell,
	openingShell,
	shellError,
	quietConnect = false,
	signInRequired = false,
}: {
	controller: { state: ControllerState; error?: string };
	/** The chat already shows the opening brief as working; connecting needs no banner. */
	quietConnect?: boolean;
	provisionState?: WorkspaceSession["provisionState"];
	needsResume?: boolean;
	transitioning?: boolean;
	automaticWakePending?: boolean;
	/** The daemon is still reconnecting this session after a restart. */
	reconnecting?: boolean;
	/** The session is archived, so `onResume` restores it instead of resuming its agent. */
	terminated?: boolean;
	onResume?: () => void | Promise<unknown>;
	resuming?: boolean;
	resumeError?: string;
	resumeOpenElsewhere?: boolean;
	harness?: string;
	onOpenShell?: () => void;
	openingShell?: boolean;
	shellError?: string;
	signInRequired?: boolean;
}) {
	const { t } = useTranslation();
	// An earlier failure can outlive a later successful resume; only a stopped agent is still held elsewhere.
	// An archived session is restored, never resumed, so it is not "open elsewhere".
	const openElsewhere = Boolean(resumeOpenElsewhere) && controller.state === "stopped" && !terminated;
	const resumeRef = useRef(onResume);
	resumeRef.current = onResume;
	// The other app releases the chat without telling AO (Codex can hold a closed
	// one for many minutes), so keep checking instead of waiting for Retry; but
	// not while a handoff, restart or startup owns the agent.
	const recheck = openElsewhere && Boolean(onResume) && !resuming && !transitioning && !reconnecting &&
		provisionState !== "provisioning" && provisionState !== "failed";
	useEffect(() => {
		if (!recheck) return;
		const timer = window.setTimeout(() => {
			void Promise.resolve().then(() => resumeRef.current?.()).catch(() => {});
		}, OPEN_ELSEWHERE_RECHECK_MS);
		return () => window.clearTimeout(timer);
	}, [recheck]);
	// Startup failures use the checklist's Retry action.
	if (provisionState === "provisioning" || provisionState === "failed") return null;
	const waking = Boolean(resuming && controller.state === "stopped");
	// Nothing to act on yet: offering Resume here invites a click the daemon is
	// about to make unnecessary.
	const restarting = Boolean(reconnecting && controller.state === "stopped" && !waking);
	const resumeClick = () => {
		void Promise.resolve().then(() => onResume?.()).catch(() => {});
	};

	// The transition coordinator intentionally stops one controller before it
	// starts the other. The top-bar handoff state already explains that interval;
	// presenting its intermediate snapshot as a crash produces a red false alarm.
	if (controller.state === "ready" || controller.state === "busy" || controller.state === "hibernated") return null;
	if (controller.state === "stopped" && (transitioning || automaticWakePending)) return null;
	if (controller.state === "connecting" && quietConnect) return null;

	const copy: Partial<Record<ControllerState, { title: string; tone: string }>> = {
		connecting: {
			title: "Connecting to the agent…",
			tone: "text-muted-foreground",
		},
		recovering: {
			title: "Reconnecting to the agent",
			tone: "text-warning",
		},
		stopped: {
			title: terminated
				? t(waking ? "shell.restoringSession" : "terminal.sessionTerminated")
				: waking
					? "Waking agent…"
					: restarting
						? "Reconnecting to the agent…"
						: openElsewhere
							? t("chat.controller.openElsewhere")
							: needsResume
								? t("chat.controller.couldNotConnect")
								: "The agent controller stopped",
			tone: terminated || waking || restarting || needsResume ? "text-muted-foreground" : "text-destructive",
		},
	};
	const shown = copy[controller.state];
	if (!shown) return null;
	const loading = controller.state === "connecting" || waking || restarting;
	const actionable = controller.state === "stopped" && !waking && !restarting;
	if (actionable && !terminated && signInRequired) return null;

	return (
		<div
			role={actionable && !terminated ? "alert" : "status"}
			aria-atomic="true"
			className="flex shrink-0 items-start gap-2.5 border-b border-border bg-surface px-4 py-2.5"
		>
			{loading ? (
				<Loader2
					aria-hidden="true"
					className="mt-0.5 size-3.5 shrink-0 animate-spin text-muted-foreground"
				/>
			) : terminated ? (
				<Archive aria-hidden="true" className={cn("mt-0.5 size-3.5 shrink-0", shown.tone)} />
			) : (
				<TriangleAlert aria-hidden="true" className={cn("mt-0.5 size-3.5 shrink-0", shown.tone)} />
			)}
			<div className="flex min-w-0 flex-1 flex-col gap-0.5">
				<strong className={cn("text-xs font-medium", shown.tone)}>{shown.title}</strong>
				{controller.state === "stopped" && waking && !terminated ? (
					<span className="text-xs leading-snug text-muted-foreground">Restoring the agent. You can keep typing.</span>
				) : restarting ? (
					<span className="text-xs leading-snug text-muted-foreground">Orchestrator restarted. History is kept.</span>
				) : controller.error ? (
					<span className="text-xs leading-snug text-muted-foreground">{controller.error}</span>
				) : null}
				{actionable ? (
					<>
						<span className="text-xs leading-snug text-muted-foreground">
							{terminated
								? t("chat.archived.body")
								: openElsewhere
									? [
										t("chat.controller.openElsewhereBody"),
										harness === "codex" ? t("chat.controller.openElsewhereCodex") : "",
										t("chat.controller.openElsewhereRecheck", { seconds: OPEN_ELSEWHERE_RECHECK_MS / 1000 }),
									].filter(Boolean).join(" ")
									: needsResume ? t("chat.controller.retryBody") : "History is kept. Resume the agent or open a shell in the same worktree."}
						</span>
						{!openElsewhere && (resumeError || shellError) ? (
							<span className="text-xs leading-snug text-destructive">
								{resumeError ?? shellError}
							</span>
						) : null}
						<div className="mt-1.5 flex flex-wrap gap-2">
							{onResume ? (
								<Button
									type="button"
									size="sm"
									variant="secondary"
									onClick={resumeClick}
									disabled={resuming}
								>
									{terminated ? t("terminal.restoreSession") : resuming ? "Resuming…" : needsResume || openElsewhere ? t("chat.controller.retry") : "Resume agent"}
								</Button>
							) : null}
							{/* Once the worktree is gone a shell would open in the project root. */}
							{onOpenShell && !needsResume && !terminated ? (
								<Button
									type="button"
									size="sm"
									variant="ghost"
									onClick={onOpenShell}
									disabled={openingShell}
								>
									{openingShell ? "Opening shell…" : "Open shell"}
								</Button>
							) : null}
						</div>
					</>
				) : null}
			</div>
		</div>
	);
}

/* -------------------------------------------------------------------------- */

/**
 * The scrolling timeline.
 *
 * Auto-scroll only follows new items while the user is already near the bottom.
 * Once they scroll up to read, new output must not yank them away — it surfaces a
 * jump control instead.
 *
 * A trailing spacer sizes itself so the latest human prompt can sit near the top
 * of the viewport when following — the Cursor/ChatGPT "drop a prompt and the
 * history moves up" behaviour. As the reply grows the spacer shrinks, which keeps
 * the prompt in place without fighting the reader.
 *
 * Finished turns are memoized so a targeted live-event invalidation only redraws
 * the turn that changed. Older pages are prepended explicitly instead of keeping
 * an unbounded history in every snapshot response.
 */
const CHAT_VIRTUALIZE_THRESHOLD = 20;
/** How long the welcome heading takes to fade out on the first send. */
const WELCOME_FADE_MS = 150;
/** Opening a chat snaps to the end for this long while rows measure and history arrives. */
const INITIAL_SNAP_MS = 1000;
/** Where a reader left a chat they scrolled away from the end of, by session; absent while following the end. */
type ReadingPosition = { key: string | number | bigint; offset: number };
const MAX_READING_POSITIONS = 64;
const readingPositions = new Map<string, ReadingPosition>();
function rememberReadingPosition(sessionId: string, position: ReadingPosition | undefined) {
	readingPositions.delete(sessionId);
	if (!position) return;
	readingPositions.set(sessionId, position);
	if (readingPositions.size > MAX_READING_POSITIONS) readingPositions.delete(readingPositions.keys().next().value!);
}
/** Test hook: tests share session ids, so a position saved by one must not reopen the next. */
export function forgetReadingPositions() {
	readingPositions.clear();
}
/** Wheel, key and touch intent pause the follow this long so the resulting scroll decides. */
const READER_INTENT_HOLD_MS = 250;
/** A settled turn holds the reader's view still this long while its rows re-measure. */
const SETTLE_HOLD_MS = 400;
/** A settled turn's rows slide from where the reader last saw them for this long. */
const SETTLE_SLIDE_MS = 240;
/** The same ease-out the turn's other settle motion ("Worked for" fading in) uses. */
const SETTLE_EASE = "cubic-bezier(0.22, 1, 0.36, 1)";
/**
 * A reader who nudges the view up a little and then stops has paused, not left.
 * After this long with no further input, while the turn is still live and they are
 * still within `relatchBand` of the end, the follow resumes from where they are.
 */
const RELATCH_AFTER_MS = 1800;
/** A scroll this soon after the reader's own input counts as theirs. */
const RELATCH_INTENT_MS = 400;
/** How far above the end still counts as a pause rather than reading back. */
const relatchBand = (node: HTMLElement) => Math.min(320, Math.round(node.clientHeight * 0.4));
const CHAT_ESTIMATED_TURN_HEIGHT = 600;
const CHAT_TURN_GAP = 18;
const CHAT_INITIAL_VIEWPORT_HEIGHT = 800;

/**
 * The conversation transcript: turn grouping, live status, "Worked for", and
 * scroll follow. Exported so a side chat renders its transcript with exactly
 * this behavior while keeping its own composer.
 */
export function Timeline({
	onAddToSideChat,
	checkingSideChatAvailability,
	onAddToChat,
	onForeignAnnotation,
	annotationNavigationRef,
	snapshot: sourceSnapshot,
	assetBaseUrl,
	remoteHost,
	findUnavailable = false,
	sideChat = false,
	draftScope,
	draftPersistenceAvailable = true,
	hasOlder,
	loadingOlder,
	onLoadOlder,
	excerptsEnabled,
	onDecide,
	busy,
	onRollback,
	onOpenFiles,
	onOpenFile,
	onOpenArtifact,
	retryControl,
	composerRecoveryTurnId,
	onEditHumanMessage,
	editPending,
	editBusy,
	editError,
	onActivateBranch,
	activateBranchPending,
	activateBranchError,
	newWorkDisabled,
	rollbackDisabled = false,
	localEchos = [],
	startup,
	arriving = false,
	workerUpdates = false,
	openingStartedAt,
	openingStartupLabel,
	controllerStopped = false,
	shownAt,
}: {
	/** The agent controller is really gone; a turn still marked running settles with an unknown outcome. */
	controllerStopped?: boolean;
	/** When the panel holding this transcript was last shown again after being covered (epoch ms). */
	shownAt?: number;
	onAddToSideChat?: ChatWorkspaceProps["onAddToSideChat"];
	checkingSideChatAvailability?: boolean;
	/** Replaces the default "Add to chat" (write to this draft scope's composer). */
	onAddToChat?: (excerpt: ChatDraftExcerptReference) => void | Promise<void>;
	/** Handles annotations whose source is another conversation. */
	onForeignAnnotation?: (annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number; conversationId?: string }) => void;
	annotationNavigationRef: MutableRefObject<((annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number }) => void) | null>;
	snapshot: ConversationSnapshot;
	assetBaseUrl?: string;
	remoteHost?: boolean;
	findUnavailable?: boolean;
	sideChat?: boolean;
	draftScope: ChatDraftScope;
	/** False when drafts can't be saved; persisted inline edits are then ignored. */
	draftPersistenceAvailable?: boolean;
	hasOlder?: boolean;
	loadingOlder?: boolean;
	onLoadOlder?: () => void;
	excerptsEnabled?: boolean;
	onDecide?: (requestId: string, decisionId: string) => void;
	busy?: boolean;
	onRollback?: (turnId: string) => void;
	onOpenFiles?: () => void;
	onOpenFile?: (path: string, line?: number) => void;
	/** Opens one of the session's artifacts in the inspector's Artifacts tab, by its artifact path. */
	onOpenArtifact?: (target: { path: string }) => void;
	retryControl?: ChatRetryControl;
	composerRecoveryTurnId?: string;
	onEditHumanMessage?: ChatWorkspaceProps["onEditMessage"];
	editPending?: boolean;
	editBusy?: boolean;
	editError?: string;
	onActivateBranch?: (branchId: string) => Promise<unknown> | void;
	activateBranchPending?: boolean;
	activateBranchError?: string;
	newWorkDisabled?: boolean;
	rollbackDisabled?: boolean;
	localEchos?: ConversationLocalEcho[];
	/** A session that is starting, or failed to start, and its setup checklist. */
	startup?: ComponentProps<typeof SessionStartup> & { openingTurnId?: string; label?: string };
	/** An interface switch is bringing this conversation in; only then does the transcript fade in. */
	arriving?: boolean;
	/** Orchestrator chat: draw worker messages and reports as rows, and the orchestrator's handling under them. */
	workerUpdates?: boolean;
	/** When the brief that started this chat was sent; see ChatWorkspaceProps. */
	openingStartedAt?: string;
	/** Names the opening start in place of its timer while the sandbox comes up. */
	openingStartupLabel?: string;
}) {
	const findSurface = useRef<HTMLDivElement>(null);
	const scroller = useRef<HTMLDivElement>(null);
	const pauseFindFollow = useRef<() => void>(() => {});
	const findScrollWritten = useRef<(top: number) => void>(() => {});
	const findPausedFollow = useRef(false);
	const [findTarget, setFindTarget] = useState<FindOccurrence>();
	const find = useChatFind({ source: sourceSnapshot, root: findSurface, viewport: scroller, hostId: useContext(ChatHostId), unavailable: findUnavailable, sideChat, pauseFollow: () => pauseFindFollow.current(), onScrollWritten: (top) => findScrollWritten.current(top), reveal: (hit) => setFindTarget({ ...hit }) });
	const snapshot = find.snapshot;
	const hasTranscript = snapshot.items.length > 0;
	const arrivedEmpty = useRef(false);
	if (arriving && !hasTranscript) arrivedEmpty.current = true;
	const revealTranscript = arrivedEmpty.current && hasTranscript;
	// Rows above the newest sequence this chat opened with arrived while it was on
	// screen; only they play an entrance (chat-entrance.ts). Taken from the first
	// render with a transcript, so a placeholder or loading frame does not count.
	// Rows the reader did not watch arrive (written while they were away, or landing
	// while the window is hidden) raise the floor before they mount, and the view
	// lands on them without a glide (the gate's catchUpAt).
	const [entranceGate] = useState(createChatEntranceGate);
	// Shown again after a terminal or reviewer tab covered it: the reader watches from then.
	if (shownAt !== undefined && shownAt > entranceGate.watchingSince) entranceGate.watchingSince = shownAt;
	admitArrivals(entranceGate, sourceSnapshot);
	useEffect(() => {
		entranceGate.settled = true;
	}, [entranceGate]);
	const translateDraft = useChatDraftTranslation();
	const uiSessionId = draftScope.sessionId;
	// The transcript fades out under the top bar once it is scrolled, and above
	// the composer while there is more below. A callback ref, not an effect: the
	// scroller is not rendered while the chat is empty, and an effect that ran
	// then never attached once it was.
	const edgeFadeRef = useEdgeFadeRef<HTMLDivElement>();
	const scrollerRef = useCallback((node: HTMLDivElement | null) => {
		scroller.current = node;
		edgeFadeRef(node);
	}, [edgeFadeRef]);
	const scrollContent = useRef<HTMLDivElement>(null);
	const virtualContent = useRef<HTMLDivElement>(null);
	const [virtualScrollMargin, setVirtualScrollMargin] = useState(20);
	const measuredVirtualGroups = useRef(new WeakSet<TimelineGroup>());
	const pendingVirtualLayout = useRef(false);
	const renderedVirtualHeight = useRef(0);
	// Width reflow must settle in place, even when virtual measurements reach the
	// DOM in a later commit. Smooth follow is reserved for conversation growth.
	const viewportSize = useRef<{ width: number; height: number } | undefined>(undefined);
	const viewportResize = useRef<{ anchor?: { key: string | number | bigint; offset: number } } | undefined>(undefined);
	const promptSpacer = useRef<HTMLDivElement>(null);
	const scrollTrack = useRef<HTMLDivElement>(null);
	const drag = useRef<{
		pointerId: number;
		startY: number;
		startScrollTop: number;
	} | null>(null);
	// Coming back to a chat the reader left mid-history reopens there, not at the end.
	const pendingReading = useRef<ReadingPosition | undefined>(readingPositions.get(uiSessionId));
	const pinnedRef = useRef(!pendingReading.current);
	const [pinned, setPinned] = useState(!pendingReading.current);
	const getScroller = useCallback(() => scroller.current, []);
	const {
		glideToEnd,
		followEnd,
		cancel: cancelScrollFollow,
		hold: holdScrollFollow,
		markWritten,
		isOwnScroll,
		isFlying,
		isGliding,
		reaim,
		endFlight,
	} = useScrollFollow(getScroller);
	// Set when the reader sends or jumps to the latest: the next layout glides there
	// instead of snapping. Opening a chat never animates, so the first layout snaps.
	const glideRequested = useRef(false);
	const initialLayoutDone = useRef(false);
	const initialLayoutAt = useRef(0);
	const lastScrollTop = useRef(0);
	const skipRelatchFollow = useRef(false);
	const reducedMotion = useReducedMotion();
	const reducedMotionRef = useRef(reducedMotion);
	reducedMotionRef.current = reducedMotion;
	// Set while the virtualizer applies a size change that the prompt spacer takes
	// instead of the scroll position (see scrollToFn).
	const spacerTakesResize = useRef(false);
	// Set once syncStreamedGrowth exists, for the virtualizer's onChange to call.
	const followVirtualHeight = useRef<() => void>(() => {});
	const spacerTakes = useCallback((takes: boolean) => {
		spacerTakesResize.current = takes && (promptSpacer.current?.offsetHeight ?? 0) > 0;
		if (spacerTakesResize.current) queueMicrotask(() => { spacerTakesResize.current = false; });
	}, []);
	const setPinnedNow = useCallback((next: boolean) => {
		pinnedRef.current = next;
		setPinned(next);
	}, []);
	// The reader took over (scrolled up, opened a disclosure): stop following.
	const releaseFollow = useCallback(() => {
		cancelScrollFollow();
		if (pinnedRef.current) setPinnedNow(false);
	}, [cancelScrollFollow, setPinnedNow]);
	findScrollWritten.current = markWritten;
	pauseFindFollow.current = () => {
		findPausedFollow.current = true;
		window.clearTimeout(relatchTimer.current);
		relatchTimer.current = undefined;
		releaseFollow();
	};
	const dropFindWindow = find.dropWindow;
	const requestGlideToEnd = useCallback(() => {
		findPausedFollow.current = false;
		dropFindWindow();
		glideRequested.current = true;
		setPinnedNow(true);
	}, [setPinnedNow, dropFindWindow]);
	// Find pauses following while it steers the view; closing hands it back and
	// leaves the reader where the last match put them.
	useEffect(() => {
		if (find.open || !findPausedFollow.current) return;
		findPausedFollow.current = false;
		const node = scroller.current;
		if (node && node.scrollHeight - node.scrollTop - node.clientHeight <= 2) setPinnedNow(true);
	}, [find.open, setPinnedNow]);
	const [activityDisclosureOverrides, setActivityDisclosureOverrides] = useState<Record<string, boolean>>({});
	const onActivityDisclosureChange = useCallback((key: string, open: boolean) => {
		setActivityDisclosureOverrides((current) => ({ ...current, [key]: open }));
	}, []);
	const [hoveredMarker, setHoveredMarker] = useState<number | null>(null);
	const [selectionAction, setSelectionAction] = useState<{
		excerpt: ChatDraftExcerptReference;
		range: Range;
	} | null>(null);
	const clearSelectionAction = useCallback(() => setSelectionAction(null), []);
	const selectionActionRef = useRef(selectionAction);
	selectionActionRef.current = selectionAction;
	const dismissedSelection = useRef<typeof selectionAction>(null);
	const dismissedEscapePress = useRef(false);
	useEffect(() => { dismissedSelection.current = null; clearSelectionAction(); }, [snapshot.conversationId, clearSelectionAction]);
	const [annotationTarget, setAnnotationTarget] = useState<{ text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number }>();
	const [annotationNavigationError, setAnnotationNavigationError] = useState<string>();
	const annotationHighlightCleanup = useRef<(() => void) | undefined>(undefined);
	const annotationHighlightTimer = useRef<number | undefined>(undefined);
	const annotationRetryCount = useRef(0);
	// Group count when navigation last asked for older history. A finished load
	// that adds nothing (a failed page or a source outside this branch) must end
	// the search instead of requesting the same page again.
	const annotationOlderRequestGroups = useRef<number | undefined>(undefined);
	const selectAnnotation = useCallback((annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number; conversationId?: string }) => {
		if (onForeignAnnotation && annotation.conversationId && annotation.conversationId !== snapshot.conversationId) {
			// The source lives in another conversation (a side chat quoting the main
			// chat); its host navigates there instead of searching this transcript.
			onForeignAnnotation(annotation);
			return;
		}
		releaseFollow();
		setAnnotationNavigationError(undefined);
		annotationRetryCount.current = 0;
		annotationOlderRequestGroups.current = undefined;
		if (!annotation.messageId) {
			const source = Array.from(scrollContent.current?.querySelectorAll<HTMLElement>("[data-chat-message-id]") ?? [])
				.find((element) => annotationTextMatches(annotationBody(element), annotation.text));
			if (source) {
				setAnnotationTarget({ ...annotation, messageId: source.dataset.chatMessageId });
				return;
			}
			setAnnotationNavigationError("This annotation's source is unavailable. Reselect the text to create a new reference.");
			return;
		}
		setAnnotationTarget({ ...annotation });
	}, [onForeignAnnotation, releaseFollow, snapshot.conversationId]);
	useEffect(() => {
		annotationNavigationRef.current = selectAnnotation;
		return () => { annotationNavigationRef.current = null; };
	}, [annotationNavigationRef, selectAnnotation]);
	useEffect(() => () => {
		window.clearTimeout(annotationHighlightTimer.current);
		annotationHighlightCleanup.current?.();
	}, []);
	const hoveredMarkerRef = useRef<number | null>(null);
	hoveredMarkerRef.current = hoveredMarker;
	const [messageEdit, setMessageEdit] = useState<MessageEditDraft | undefined>(
		() => (draftPersistenceAvailable ? readChatSessionDraft(draftScope).inlineEdit : undefined),
	);
	const messageEditRef = useRef(messageEdit);
	const [durableInlineEditDelivery, setDurableInlineEditDelivery] =
		useState<ChatInlineEditDelivery | undefined>(
			() => (draftPersistenceAvailable ? readChatSessionDraft(draftScope).inlineEditDelivery : undefined),
		);
	const [inlineEditUncertain, setInlineEditUncertain] = useState(
		() => draftPersistenceAvailable && readChatSessionDraft(draftScope).inlineEditDelivery?.state === "dispatching",
	);
	const automaticInlineRecoveryAttempted = useRef<string | undefined>(undefined);
	const [draftPersistenceError, setDraftPersistenceError] = useState<string>();
	const [inlineEditRecoveryNotice, setInlineEditRecoveryNotice] = useState<string>();
	const [inlineEditOutcomeNotice, setInlineEditOutcomeNotice] = useState<string>();
	const subscribeInlineEditMutation = useCallback(
		(listener: () => void) => subscribeChatDraftRuntime(draftScope, listener),
		[draftScope],
	);
	const getInlineEditMutation = useCallback(
		() => getChatInlineEditMutation(draftScope),
		[draftScope],
	);
	const inlineEditMutation = useSyncExternalStore(
		subscribeInlineEditMutation,
		getInlineEditMutation,
		getInlineEditMutation,
	);
	const [appliedEditAcceptanceSequence, setAppliedEditAcceptanceSequence] = useState(0);
	const acceptedEditWaiting = Boolean(
		inlineEditMutation.accepted &&
			inlineEditMutation.accepted.sequence > appliedEditAcceptanceSequence,
	);
	const acceptedEditClearFailed = inlineEditMutation.accepted?.result.ok === false;
	const inlineEditPending = Boolean(editPending) || inlineEditMutation.pending || acceptedEditWaiting;
	const inlineEditLocked = inlineEditPending || Boolean(durableInlineEditDelivery);
	const inlineEditSendBlocked =
		inlineEditPending || (acceptedEditClearFailed && !durableInlineEditDelivery);

	const captureTranscriptSelection = useCallback((event?: Event) => {
		if ((event?.target as Element | null)?.closest?.("[data-chat-selection-actions]")) return;
		if (event instanceof KeyboardEvent && !/^(Arrow(Up|Down|Left|Right)|Home|End|PageUp|PageDown|Shift)$/i.test(event.key) &&
			!((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "a")) return;
		const selection = window.getSelection();
		if (!excerptsEnabled || !selection || selection.isCollapsed || selection.rangeCount === 0) {
			setSelectionAction(null);
			return;
		}
		const anchor = selection.anchorNode instanceof Element
			? selection.anchorNode
			: selection.anchorNode?.parentElement;
		const focus = selection.focusNode instanceof Element
			? selection.focusNode
			: selection.focusNode?.parentElement;
		const source = anchor?.closest<HTMLElement>("[data-chat-message-id]");
		if (
			!source ||
			source !== focus?.closest<HTMLElement>("[data-chat-message-id]") ||
			!scrollContent.current?.contains(source)
		) {
			setSelectionAction(null);
			return;
		}
		let text = selection.toString();
		const messageId = source.dataset.chatMessageId;
		const revision = Number(source.dataset.chatMessageRevision);
		const role = source.dataset.chatMessageRole;
		if (
			!text ||
			!messageId ||
			!Number.isSafeInteger(revision) ||
			(role !== "user" && role !== "assistant")
		) {
			setSelectionAction(null);
			return;
		}
		const selectedRange = selection.getRangeAt(0).cloneRange();
		const message = snapshot.items.find(item => item.kind === "message" && item.id === messageId);
		if (!message || message.kind !== "message") { setSelectionAction(null); return; }
		let captured;
		try { captured = captureExcerptSnapshot(annotationBody(source), selectedRange, message.text); }
		catch { setSelectionAction(null); return; }
		text = captured.text;
		const dismissed = dismissedSelection.current;
		if (dismissed && dismissed.excerpt.messageId === messageId && dismissed.excerpt.revision === revision &&
			dismissed.excerpt.text === text && selectionRangesEqual(dismissed.range, selectedRange)) return;
		dismissedSelection.current = null;
		const next = {
			excerpt: {
				id: crypto.randomUUID(),
				conversationId: snapshot.conversationId,
				messageId,
				revision,
				text,
				role: role as "user" | "assistant",
				snapshot: captured.snapshot,
			},
			range: selectedRange,
		};
		setSelectionAction((previous) => previous && previous.excerpt.messageId === messageId &&
			previous.excerpt.revision === revision && previous.excerpt.text === text && selectionRangesEqual(previous.range, selectedRange)
			? previous : next);
	}, [excerptsEnabled, snapshot]);
	useEffect(() => {
		if (!excerptsEnabled) return;
		const capture = (event: Event) => captureTranscriptSelection(event);
		const selectionChanged = () => {
			const current = window.getSelection();
			if (!current || current.isCollapsed || !current.rangeCount || !selectionRangesEqual(current.getRangeAt(0), dismissedSelection.current?.range)) dismissedSelection.current = null;
		};
		const escape = (event: KeyboardEvent) => {
			if (event.key !== "Escape" || event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey || isDialogOrMenuOpen()) return;
			if (event.repeat && dismissedEscapePress.current) { event.preventDefault(); event.stopPropagation(); return; }
			const origin = event.composedPath()[0];
			const target = origin instanceof Element ? origin : document.activeElement;
			if (target?.closest('input, textarea, select, [contenteditable], [role="textbox"], .xterm, webview, [data-browser-view]')) return;
			const pane = scroller.current?.parentElement;
			const toolbar = pane?.querySelector<HTMLElement>("[data-chat-selection-actions]");
			const action = selectionActionRef.current;
			if (!action || !toolbar || getComputedStyle(toolbar).visibility === "hidden" ||
				(event.target instanceof Node && event.target !== document && event.target !== document.body && event.target !== document.documentElement && !pane?.contains(event.target))) return;
			const native = window.getSelection();
			if (!native?.rangeCount || native.isCollapsed || !selectionRangesEqual(native.getRangeAt(0), action.range)) return;
			dismissedSelection.current = action;
			dismissedEscapePress.current = true;
			clearSelectionAction();
			event.preventDefault(); event.stopPropagation();
		};
		const release = (event: KeyboardEvent) => { if (event.key === "Escape") dismissedEscapePress.current = false; };
		document.addEventListener("selectionchange", selectionChanged);
		document.addEventListener("keyup", release);
		const blur = () => { dismissedEscapePress.current = false; };
		window.addEventListener("blur", blur);
		document.addEventListener("pointerup", capture);
		document.addEventListener("keyup", capture);
		document.addEventListener("keydown", escape, true);
		return () => {
			document.removeEventListener("pointerup", capture);
			document.removeEventListener("keyup", capture);
			document.removeEventListener("keydown", escape, true);
			document.removeEventListener("selectionchange", selectionChanged);
			document.removeEventListener("keyup", release);
			window.removeEventListener("blur", blur);
		};
	}, [captureTranscriptSelection, clearSelectionAction, excerptsEnabled]);

	const addSelectionToChat = useCallback(async () => {
		if (!selectionAction) return;
		const excerpt = captureExcerptTurn(selectionAction.excerpt, snapshot);
		if (onAddToChat) {
			// A host with its own composer (a side chat) owns where references go.
			await onAddToChat(excerpt);
			setSelectionAction(null);
			window.getSelection()?.removeAllRanges();
			return;
		}
		const composer = readChatSessionDraft(draftScope).composer;
		// A pending delivery is cleared by revision on acceptance. Changing the
		// draft now would keep the sent text and excerpts for a second send.
		if (composer.delivery) throw new Error("chat.draft.excerptPendingDelivery");
		const current = composer.excerpts ?? [];
		const duplicate = current.some(
			(item) =>
				item.messageId === selectionAction.excerpt.messageId &&
				item.revision === selectionAction.excerpt.revision &&
				item.text === selectionAction.excerpt.text &&
				JSON.stringify(item.snapshot?.sourceRange) === JSON.stringify(selectionAction.excerpt.snapshot?.sourceRange) &&
				JSON.stringify(item.snapshot?.renderedRange) === JSON.stringify(selectionAction.excerpt.snapshot?.renderedRange) &&
				JSON.stringify(item.snapshot?.diagramRange) === JSON.stringify(selectionAction.excerpt.snapshot?.diagramRange),
		);
		if (!duplicate && current.length + (composer.fileReferences?.length ?? 0) >= 8) {
			setDraftPersistenceError("chat.draft.annotationLimit");
			return;
		}
		const order = Math.max(Date.now(), ...current.map(item => (item.order ?? 0) + 1), ...(composer.fileReferences ?? []).map(item => item.order + 1));
		const next = duplicate ? current : [...current, { ...excerpt, order }];
		const sizeError = excerptSizeError(next);
		if (sizeError) throw new Error(sizeError);
		const result = writeChatExcerptReferences(draftScope, next);
		setDraftPersistenceError(result.ok ? undefined : "chat.draft.saveFailed");
		if (!result.ok) throw new Error("chat.draft.excerptSaveFailed");
		setSelectionAction(null);
		window.getSelection()?.removeAllRanges();
	}, [draftScope, onAddToChat, selectionAction, snapshot]);
	const inlineEditRecoveryLabel = durableInlineEditDelivery
		? durableInlineEditDelivery.state === "accepted"
			? "chat.draft.clearEdit"
			: "chat.draft.retryEdit"
		: undefined;
	const canAbandonInlineEditRecovery = Boolean(
		inlineEditUncertain && durableInlineEditDelivery?.state === "dispatching",
	);
	useEffect(() => {
		setChatDraftBoundary(
			draftScope.sessionId,
			"inline-edit",
			[
				...(draftPersistenceError ? (["persistence-failed"] as const) : []),
			],
		);
	}, [draftPersistenceError, draftScope.sessionId]);
	useEffect(
		() => () => setChatDraftBoundary(draftScope.sessionId, "inline-edit", undefined),
		[draftScope.sessionId],
	);
	// The inspector changes the minimap's visibility, but it must not cause this
	// entire timeline to rerender. A live conversation can contain hundreds of
	// DOM nodes, and inspector toggles are otherwise a broad synchronous commit.
	// Keep that small accessibility/interaction boundary imperative instead.
	const inspectorOpenRef = useRef(
		inspectorIsOpen(useUiStore.getState().inspectorSessions, uiSessionId),
	);
	const turn = activeTurn(snapshot, { controllerStopped });
	// The daemon refuses a retry while any turn is open, settled on screen or not.
	const durableTurn = activeTurn(snapshot);
	const [scrollbar, setScrollbar] = useState({
		visible: false,
		top: 0,
		height: 40,
		percent: 0,
		/** Nothing below the viewport: there is no "latest" left to jump to. */
		atEnd: true,
		markers: [] as Array<{
			top: number;
			scrollTop: number;
			visible: boolean;
		}>,
	});
	// Show turn ticks whenever the inspector is closed and there are human
	// prompts — not only when the transcript overflows. Closing the rail
	// widens chat; shorter histories (common on non-Codex harnesses) often
	// stop overflowing and used to lose the minimap exactly then.
	const minimapEnabled = scrollbar.markers.length > 0;
	// Replies rendered with the chat's first commit were already on screen
	// before the switch; streamed text in them must not fade in again.
	const chatOpening = useRef(true);
	useEffect(() => {
		chatOpening.current = false;
	}, []);
	// The minimap appears once the opened chat is measured. That first reveal is
	// part of opening the chat, so it is instant; only later changes (the inspector
	// covering it) fade.
	const [minimapRevealed, setMinimapRevealed] = useState(false);
	useEffect(() => {
		if (!minimapEnabled || minimapRevealed) return;
		const frame = window.requestAnimationFrame(() => setMinimapRevealed(true));
		return () => window.cancelAnimationFrame(frame);
	}, [minimapEnabled, minimapRevealed]);
	const { t } = useTranslation();
	const openingTurnId = startup?.openingTurnId;
	// The checklist holds the working slot until the agent is up and its first
	// turn is live; a clean start then leaves no trace.
	const showStartup = Boolean(
		startup &&
			(startup.failed ||
				!(turn?.state === "running" && turn.id === openingTurnId &&
					startup.steps.length > 0 && startup.steps.every((step) => step.status === "done"))),
	);
	const queued = useMemo(() => {
		const ids = queuedTurnIds(snapshot);
		if (openingTurnId) ids.delete(openingTurnId);
		const firstTurn = snapshot.turns[0];
		if (openingStartedAt && firstTurn && !firstTurnSettled(snapshot)) ids.delete(firstTurn.id);
		return ids;
	}, [openingStartedAt, openingTurnId, snapshot]);
	const decide = useStableCallback(onDecide);
	const rollback = useStableCallback(onRollback);
	const openFiles = useStableCallback(onOpenFiles);
	const openFile = useStableCallback(onOpenFile);
	const openArtifact = useStableCallback(onOpenArtifact);
	const retryTurn = useStableCallback(retryControl?.retry);
	const localBaseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const apiBaseUrl = remoteHost ? assetBaseUrl ?? null : assetBaseUrl ?? localBaseUrl;
	const editHumanMessage = useStableCallback(onEditHumanMessage);
	const activateBranch = useStableCallback(onActivateBranch);
	const canEditHumanMessage = Boolean(onEditHumanMessage) && !newWorkDisabled;
	const canActivateBranch = Boolean(onActivateBranch) && !newWorkDisabled;
	const canForkHistoricalContext = can(snapshot, "fork");
	const canReconstructHistoricalContext =
		can(snapshot, "prompt_replay") && can(snapshot, "embedded_context");
	const stableBranchPoints = useStableValue(snapshot.branchPoints);
	const branchPoints = useMemo(
		() => new Map((stableBranchPoints ?? []).map((point) => [point.turnId, point])),
		[stableBranchPoints],
	);
	const firstEditableHumanPromptTurnId = useMemo(
		() =>
			snapshot.items.find(
				(item) =>
					item.kind === "message" &&
					item.role === "user" &&
					item.origin === "human" &&
					item.editAvailable &&
					item.turnId,
			)?.turnId,
		[snapshot.items],
	);
	const editableTurnsNow = useMemo(() => {
		const editable = new Set<string>();
		if (snapshot.controller.state !== "ready") return editable;
		const accepted = new Set(
			snapshot.turns
				.filter(
					(turn) =>
						turn.state === "completed" || turn.state === "interrupted" || turn.state === "failed",
				)
				.map((turn) => turn.id),
		);
		for (const item of snapshot.items) {
			if (
				item.kind !== "message" ||
				item.role !== "user" ||
				item.origin !== "human" ||
				!item.turnId
			) {
				continue;
			}
			const firstPromptInBinding =
				!snapshot.hasMoreBefore && item.turnId === firstEditableHumanPromptTurnId;
			const canNativeFork =
				canForkHistoricalContext &&
				(snapshot.nativeForkAvailableAfterSequence ?? 0) > 0 &&
				item.sequence > (snapshot.nativeForkAvailableAfterSequence ?? 0);
			if (
				item.editAvailable &&
				accepted.has(item.turnId) &&
				(firstPromptInBinding || canNativeFork || canReconstructHistoricalContext)
			) {
				editable.add(item.turnId);
			}
		}
		return editable;
	}, [
		canForkHistoricalContext,
		canReconstructHistoricalContext,
		firstEditableHumanPromptTurnId,
		snapshot.controller.state,
		snapshot.hasMoreBefore,
		snapshot.items,
		snapshot.nativeForkAvailableAfterSequence,
		snapshot.turns,
	]);

	// An ordinary branch change hides an edit that belongs to the previous branch.
	// An unresolved delivery is different: its exact persisted editor owns the only
	// Retry/Abandon controls, so keep it actionable when an ambiguous send activates
	// and refetches the replacement branch.
	useEffect(() => {
		if (draftPersistenceAvailable && readChatSessionDraft(draftScope).inlineEditDelivery) return;
		setMessageEdit(undefined);
	}, [draftPersistenceAvailable, draftScope, snapshot.activeBranchId, snapshot.sessionId]);
	useEffect(() => {
		const setInspectorOpen = (isOpen: boolean) => {
			inspectorOpenRef.current = isOpen;
			const track = scrollTrack.current;
			if (track) {
				track.dataset.inspectorOpen = String(isOpen);
				track.setAttribute("aria-hidden", String(isOpen));
				track.tabIndex = minimapEnabled && !isOpen ? 0 : -1;
				track.classList.toggle("cursor-pointer", minimapEnabled && !isOpen);
				track.classList.toggle("pointer-events-none", !minimapEnabled || isOpen);
				track.classList.toggle("opacity-100", minimapEnabled && !isOpen);
				track.classList.toggle("opacity-0", !minimapEnabled || isOpen);
				if (isOpen && document.activeElement === track) track.blur();
			}
			if (isOpen) {
				drag.current = null;
				if (hoveredMarkerRef.current !== null) setHoveredMarker(null);
			}
		};
		setInspectorOpen(inspectorIsOpen(useUiStore.getState().inspectorSessions, uiSessionId));
		return useUiStore.subscribe((state, previous) => {
			const currentOpen = inspectorIsOpen(state.inspectorSessions, uiSessionId);
			const previousOpen = inspectorIsOpen(previous.inspectorSessions, uiSessionId);
			if (currentOpen !== previousOpen) setInspectorOpen(currentOpen);
		});
	}, [minimapEnabled, uiSessionId]);
	const consumedRetrySources = useMemo(() => retrySourceTurnIds(snapshot), [snapshot]);
	const retryableTurns = useMemo(() => {
		// A steer can split one logical turn into several visual groups. Its prompt
		// still makes the final failed group retryable, even when it is in an earlier one.
		const humanPromptTurnIds = new Set(snapshot.items.flatMap((item) =>
			item.kind === "message" && item.role === "user" && item.origin === "human" && item.turnId
				? [item.turnId]
				: [],
		));
		return new Map(
			snapshot.turns
				.filter(
					(turn) =>
						turn.state === "failed" &&
						Boolean(turn.providerTurnId) &&
						!consumedRetrySources.has(turn.id) &&
						(humanPromptTurnIds.has(turn.id) || Boolean(turn.usageLimit && turn.hasHumanPrompt)),
				)
				.map((turn) => [turn.id, turn]),
		);
	}, [snapshot.items, snapshot.turns, consumedRetrySources]);

	useEffect(() => {
		pinnedRef.current = pinned;
	}, [pinned]);

	useEffect(() => {
		const restored = draftPersistenceAvailable ? readChatSessionDraft(draftScope) : undefined;
		messageEditRef.current = restored?.inlineEdit;
		setMessageEdit(restored?.inlineEdit);
		setDurableInlineEditDelivery(restored?.inlineEditDelivery);
		setInlineEditUncertain(restored?.inlineEditDelivery?.state === "dispatching");
		setDraftPersistenceError(undefined);
		setInlineEditRecoveryNotice(
			restored?.inlineEditDelivery
				? restored.inlineEditDelivery.state === "accepted"
					? "chat.draft.acceptedEdit"
					: "chat.draft.editRestart"
				: undefined,
		);
		setAppliedEditAcceptanceSequence(0);
		setInlineEditOutcomeNotice(undefined);
		automaticInlineRecoveryAttempted.current = undefined;
	}, [draftPersistenceAvailable, draftScope]);

	const applyAcceptedInlineEditResult = useCallback((result: DraftClearResult) => {
		setDurableInlineEditDelivery(result.draft.inlineEditDelivery);
		if (!result.ok) {
			setInlineEditRecoveryNotice(
				"chat.draft.acceptedEdit",
			);
			setDraftPersistenceError(
				"chat.draft.clearEditFailed",
			);
			return;
		}
		setDraftPersistenceError(undefined);
		setInlineEditRecoveryNotice(undefined);
		if (!result.cleared) return;
		messageEditRef.current = undefined;
		setMessageEdit(undefined);
	}, []);

	const acceptAndClearInlineEditDelivery = useCallback(
		(delivery: ChatInlineEditDelivery, mutationToken?: symbol) => {
			const accepted = markChatInlineEditDeliveryAccepted(
				draftScope,
				delivery.clientMessageId,
				delivery.revision,
			);
			if (!accepted.ok) {
				if (mutationToken) {
					cancelChatInlineEditMutation(draftScope, mutationToken);
				}
				setDurableInlineEditDelivery(delivery);
				setDraftPersistenceError(
					"chat.draft.recordEditFailed",
				);
				return false;
			}
			setDurableInlineEditDelivery(
				accepted.draft.inlineEditDelivery ?? { ...delivery, state: "accepted" },
			);
			const result = clearAcceptedChatInlineEdit(draftScope, delivery.revision);
			if (mutationToken) {
				finishChatInlineEditMutation(
					draftScope,
					mutationToken,
					delivery.revision,
					result,
				);
				return result.ok;
			}
			applyAcceptedInlineEditResult(result);
			return result.ok;
		},
		[applyAcceptedInlineEditResult, draftScope],
	);

	const abandonUncertainInlineEdit = useCallback(() => {
		if (!durableInlineEditDelivery) return;
		const result = clearUncertainChatInlineEditDelivery(
			draftScope,
			durableInlineEditDelivery.clientMessageId,
			durableInlineEditDelivery.revision,
		);
		setDurableInlineEditDelivery(result.draft.inlineEditDelivery);
		if (!result.ok) {
			setDraftPersistenceError(
				"chat.draft.abandonEditFailed",
			);
			return;
		}
		setInlineEditUncertain(false);
		setDraftPersistenceError(undefined);
		setInlineEditRecoveryNotice(undefined);
		setInlineEditOutcomeNotice(
			"chat.draft.abandonedEdit",
		);
	}, [draftScope, durableInlineEditDelivery]);

	useEffect(() => {
		if (!durableInlineEditDelivery || durableInlineEditDelivery.state !== "accepted") return;
		if (
			automaticInlineRecoveryAttempted.current ===
			durableInlineEditDelivery.clientMessageId
		) return;
		automaticInlineRecoveryAttempted.current = durableInlineEditDelivery.clientMessageId;
		acceptAndClearInlineEditDelivery(durableInlineEditDelivery);
	}, [acceptAndClearInlineEditDelivery, durableInlineEditDelivery]);

	useEffect(() => {
		const accepted = inlineEditMutation.accepted;
		if (!accepted || accepted.sequence <= appliedEditAcceptanceSequence) return;
		applyAcceptedInlineEditResult(accepted.result);
		setAppliedEditAcceptanceSequence(accepted.sequence);
		acknowledgeChatInlineEditMutation(draftScope, accepted.sequence);
	}, [
		appliedEditAcceptanceSequence,
		applyAcceptedInlineEditResult,
		draftScope,
		inlineEditMutation.accepted,
	]);

	const editableTurns = useStableSet(editableTurnsNow);
	const startMessageEdit = useCallback((message: ConversationMessage) => {
		if (!message.turnId || inlineEditLocked) return;
		const result = writeChatInlineEdit(draftScope, {
			turnId: message.turnId,
			text: message.text,
			content: message.content ?? [],
		});
		const next = result.draft.inlineEdit;
		if (!next) return;
		messageEditRef.current = next;
		setMessageEdit(next);
		setInlineEditRecoveryNotice(undefined);
		setInlineEditOutcomeNotice(undefined);
		setDraftPersistenceError(
			result.ok
				? undefined
				: "chat.draft.editSaveFailed",
		);
	}, [draftScope, inlineEditLocked]);
	const updateMessageEdit = useCallback((text: string) => {
		if (inlineEditLocked) return;
		const current = messageEditRef.current;
		if (!current) return;
		const result = writeChatInlineEdit(draftScope, {
			turnId: current.turnId,
			text,
			content: current.content,
		});
		const next = result.draft.inlineEdit;
		if (!next) return;
		messageEditRef.current = next;
		setMessageEdit(next);
		setInlineEditRecoveryNotice(undefined);
		setInlineEditOutcomeNotice(undefined);
		setDraftPersistenceError(
			result.ok
				? undefined
				: "chat.draft.editSaveFailed",
		);
	}, [draftScope, inlineEditLocked]);
	const cancelMessageEdit = useCallback(() => {
		if (inlineEditLocked) return;
		const result = writeChatInlineEdit(draftScope, undefined);
		if (!result.ok) {
			setDraftPersistenceError(
				"chat.draft.editDiscardFailed",
			);
			return;
		}
		messageEditRef.current = undefined;
		setMessageEdit(undefined);
		setInlineEditRecoveryNotice(undefined);
		setInlineEditOutcomeNotice(undefined);
		setDraftPersistenceError(undefined);
	}, [draftScope, inlineEditLocked]);
	const submitMessageEdit = useCallback(
		async (text: string) => {
			const current = messageEditRef.current;
			if (
				!current ||
				!onEditHumanMessage ||
				inlineEditPending ||
				(!durableInlineEditDelivery &&
					getChatInlineEditMutation(draftScope).accepted?.result.ok === false)
			) return;
				const prepared = prepareChatInlineEditDelivery(draftScope, {
					turnId: current.turnId,
					text,
					content: current.content,
					clientMessageId:
					durableInlineEditDelivery?.clientMessageId ?? crypto.randomUUID(),
			});
			if (!prepared.ok) {
				setDraftPersistenceError(
					"chat.draft.prepareEditFailed",
				);
				return;
			}
			const delivery = prepared.mutation;
			setInlineEditUncertain(false);
			setDurableInlineEditDelivery(delivery);
			setDraftPersistenceError(undefined);
			setInlineEditRecoveryNotice(
				delivery.state === "accepted"
					? "chat.draft.acceptedEdit"
					: undefined,
			);
			if (delivery.state === "accepted") {
				acceptAndClearInlineEditDelivery(delivery);
				return;
			}
			const mutationToken = beginChatInlineEditMutation(draftScope);
			if (!mutationToken) return;
			let mutationFinished = false;
			try {
				const outcome = await editHumanMessage(
					delivery.turnId,
					delivery.requestText,
					delivery.clientMessageId,
				);
				if (outcome?.status === "not-accepted") {
					const cleared = clearRejectedChatInlineEditDelivery(
						draftScope,
							delivery.clientMessageId,
							delivery.revision,
						);
						setDurableInlineEditDelivery(cleared.draft.inlineEditDelivery);
						if (cleared.ok) {
							setDraftPersistenceError(undefined);
							setInlineEditRecoveryNotice(undefined);
							setInlineEditOutcomeNotice(outcome.reason);
						} else {
						setDraftPersistenceError(
							"chat.draft.clearRefusedEditFailed",
						);
					}
					return;
				}
				acceptAndClearInlineEditDelivery(delivery, mutationToken);
				mutationFinished = true;
			} catch {
				setInlineEditUncertain(true);
				setInlineEditRecoveryNotice(
					"chat.draft.editUncertain",
				);
			} finally {
				if (!mutationFinished) {
					cancelChatInlineEditMutation(draftScope, mutationToken);
				}
			}
		},
		[
			acceptAndClearInlineEditDelivery,
			durableInlineEditDelivery,
			editHumanMessage,
			inlineEditPending,
			onEditHumanMessage,
			draftScope,
		],
	);

	// Both churn with their deps while streaming; a stable identity keeps memoized turn groups from re-rendering.
	const stableStartMessageEdit = useLatestCallback(startMessageEdit);
	const stableSubmitMessageEdit = useLatestCallback(submitMessageEdit);

	const readable = useMemo(() => readableItems(snapshot), [snapshot]);
	const items = useStableList(readable, itemKey, sameContent);
	const seenHumanMessageIds = useRef<Set<string> | undefined>(undefined);
	const lastSeenLatestSequence = useRef<number | undefined>(undefined);
	const [newHumanMessageIds, setNewHumanMessageIds] = useState<ReadonlySet<string>>(new Set());
	const editedMessageVisible = Boolean(
		messageEdit &&
		items.some(
			(item) =>
				item.kind === "message" && item.role === "user" && item.turnId === messageEdit.turnId,
		),
	);
	const localItems = useMemo(() => {
		const backgroundQueuedTurnIds = new Set(
			localEchos
				.filter((echo) => echo.backgroundWake && echo.turnId)
				.map((echo) => echo.turnId as string)
				.filter((turnId) => snapshot.turns.some((turn) => turn.id === turnId && turn.state === "queued")),
		);
		return localEchos
			.filter(
				(echo) =>
					// A send made behind a running turn lives in the queue dock, not the chat.
					!echo.queued &&
					!items.some(
						(item) =>
							item.kind === "message" &&
							item.role === "user" &&
							item.origin === "human" &&
							echoMatchesItem(echo, item, backgroundQueuedTurnIds),
					),
			)
			.flatMap((echo, index) => withLocalSwitchMarker(echo, items, {
				kind: "message",
				id: `local:${echo.clientMessageId}`,
				turnId: `local:${echo.clientMessageId}`,
				clientMessageId: echo.clientMessageId,
				sequence: snapshot.latestSequence + index + 0.01,
				revision: 0,
				role: "user",
				origin: "human",
				text: echo.text,
				continuation: echo.continuation,
				content: [...(echo.excerpts?.map((excerpt) => ({
					annotationId: excerpt.id, annotationOrder: excerpt.order,
					type: "excerpt",
					text: excerpt.text,
					sourceConversationId: excerpt.conversationId,
					sourceMessageId: excerpt.messageId,
					sourceRevision: excerpt.revision, snapshot: excerpt.snapshot,
				})) ?? []), ...(echo.fileReferences ?? []).map(fileReference => ({ type: "file_reference", fileReference, annotationId: fileReference.id, annotationOrder: fileReference.order }))],
				streaming: false,
				delivery: echo.backgroundWake || echo.turnId ? "accepted" : "sending",
				createdAt: echo.createdAt,
			}));
	}, [items, localEchos, snapshot.latestSequence, snapshot.turns]);
	const backgroundQueuedTurnIds = useMemo(
		() => new Set(
			localEchos
				.filter((echo) => echo.backgroundWake && echo.turnId)
				.map((echo) => echo.turnId as string)
				.filter((turnId) => snapshot.turns.some((turn) => turn.id === turnId && turn.state === "queued")),
		),
		[localEchos, snapshot.turns],
	);
	const timelineItems = useStableList(
		[
			...withSwitchMarkerKeys(items.filter(
				(item) => !(item.kind === "message" && item.role === "user" && (
					(item.turnId && backgroundQueuedTurnIds.has(item.turnId)) ||
					// A terminal switch hands the note over as the first prompt; it is not the user's.
					item.text.trimStart().startsWith("<ao-handoff-request")
				)) &&
					// The model picker already names the model that answered.
					!isModelReroute(item),
			), localEchos),
			...localItems,
		],
		itemKey,
		sameContent,
	);
	// Keyed by identity, not row id: the local echo already played the entrance, so the
	// durable row that replaces it must not announce itself a second time. A layout
	// effect, so the flag lands before the bubble's first paint: set after it, the
	// entrance would restart from opacity 0 and the bubble would blink.
	useLayoutEffect(() => {
		const humanMessages = timelineItems.filter(
			(item): item is ConversationMessage =>
				item.kind === "message" && item.role === "user" && item.origin === "human",
		);
		const humanMessageKeys = new Set(humanMessages.map(itemKey));
		if (!seenHumanMessageIds.current) {
			seenHumanMessageIds.current = humanMessageKeys;
			lastSeenLatestSequence.current = snapshot.latestSequence;
			return;
		}
		const openingTurnId = openingStartedAt ? snapshot.turns[0]?.id : undefined;
		const added = new Set(
			humanMessages
				.filter(
					(item) =>
						!seenHumanMessageIds.current?.has(itemKey(item)) &&
						item.sequence > (lastSeenLatestSequence.current ?? -Infinity) &&
						// A prompt the reader did not watch arrive (sent from another device
						// while they were away, or while the window was hidden) is history.
						item.sequence > (entranceGate.floor ?? -Infinity) &&
						// A local echo can appear while hidden with no new snapshot to raise
						// the floor (a send that resolved after the reader left); it is seen
						// here, so it never enters on their return.
						!pageHidden() &&
						// The brief that started the chat made its entrance on the new chat page.
						!(openingTurnId && item.turnId === openingTurnId),
				)
				.map(itemKey),
		);
		seenHumanMessageIds.current = humanMessageKeys;
		lastSeenLatestSequence.current = snapshot.latestSequence;
		if (added.size > 0) setNewHumanMessageIds(added);
	}, [timelineItems, snapshot.latestSequence, openingStartedAt, snapshot.turns]);
	const previousEchoIds = useRef<ReadonlySet<string>>(new Set(localEchos.map((echo) => echo.clientMessageId)));
	useLayoutEffect(() => {
		const ids = new Set(localEchos.map((echo) => echo.clientMessageId));
		const sent = [...ids].some((id) => !previousEchoIds.current.has(id));
		previousEchoIds.current = ids;
		if (!sent) return;
		// The reader just sent: bring them to their prompt even if they had scrolled away.
		requestGlideToEnd();
	}, [localEchos, requestGlideToEnd]);
	// The brief that started this chat reads as working from the moment it was
	// sent, through the agent connecting, until its turn settles.
	const openingPending = Boolean(openingStartedAt) && !openingTurnSettled(snapshot, controllerStopped);
	const batchedReports = useMemo(
		() => workerUpdates ? placeBatchedReports(snapshot.batchedDeliveries ?? [], snapshot.items) : NO_BATCHED_REPORTS,
		[snapshot.batchedDeliveries, snapshot.items, workerUpdates],
	);
	const grouped = useMemo(() => {
		const hiddenTurns = hiddenTimelineTurnIds(snapshot);
		if (openingTurnId) hiddenTurns.delete(openingTurnId);
		const firstTurn = snapshot.turns[0];
		if (openingPending && firstTurn) hiddenTurns.delete(firstTurn.id);
		// An AO action outlives a rolled-back or hidden turn, so it stands on its own there.
		const dropped = new Set([...hiddenTurns, ...snapshot.turns.filter((turn) => turn.rolledBack).map((turn) => turn.id)]);
		const kept = dropped.size > 0 ? timelineItems.map((item) => outlivingAction(item, dropped)) : timelineItems;
		const items = atCarryingSteer(kept, snapshot.batchedDeliveries ?? [], batchedReports.moved);
		const groups = groupByTurn({ ...snapshot, items }, { controllerStopped })
			.filter((group) => !group.turnId || !hiddenTurns.has(group.turnId))
			.map((group, index) => openingPending && (firstTurn ? group.turnId === firstTurn.id : index === 0 && group.turnId?.startsWith("local:"))
				? { ...group, live: true, liveStartedAt: openingStartedAt, liveStartupLabel: openingStartupLabel }
				: group);
		if (!workerUpdates) return groups;
		const placed = batchedReports.moved.size > 0 ? groups.filter((group) => !group.turnId || !batchedReports.moved.has(group.turnId)) : groups;
		const spawnedIds = new Set(placed.flatMap((group) => spawnedSessionIds(group.items)));
		if (spawnedIds.size === 0) return placed;
		return placed
			.map((group) => ({ ...group, items: group.items.filter((item) => !isDuplicateSpawnAction(item, spawnedIds)) }))
			.filter((group) => group.items.length > 0);
	}, [batchedReports, controllerStopped, openingPending, openingStartedAt, openingStartupLabel, openingTurnId, snapshot, timelineItems, workerUpdates]);
	const groups = useStableList(grouped, groupKey, sameGroup);
	// Whether the reader opened a step from the live row, kept for the step's own row
	// (LiveStepChoices). Read once, when that row mounts, so nothing re-renders on it.
	const [liveStepChoices] = useState(() => new Map<string, boolean>());
	const workerUpdateIndex = useWorkerUpdateIndex(workerUpdates ? snapshot.items : undefined);
	// One slot carries the start-up row from the first frame: the opening turn's
	// group, or before that turn exists, the brief's own live group. Drawing it
	// anywhere else first would make it jump lines when the turn arrives.
	// A chat started from the new chat page reads as setting up from the send, before
	// its session reports any steps, so the row keeps its label through the handoff
	// and the steps fill in under it.
	const openingSetup = !startup && openingPending && turn?.state !== "running"
		? { failed: false, steps: [], agentName: agentLabel(snapshot.harness), label: openingStartupLabel }
		: undefined;
	const rowStartup = startup ?? openingSetup;
	const startupGroupKey = rowStartup && (openingSetup || showStartup)
		? groups.find((group) => (openingTurnId ? group.turnId === openingTurnId : group.live))?.key
		: undefined;
	// Read by syncScrollLayout (a stable callback) so the initial snap only latches
	// once real conversation content exists (history can load after the chat opens).
	const groupCountRef = useRef(groups.length);
	groupCountRef.current = groups.length;
	const navigableGroups = useMemo(() => groups.filter(groupHasHumanPrompt), [groups]);
	const previews = useMemo(() => navigableGroups.map(groupPreview), [navigableGroups]);
	const virtualized = groups.length > CHAT_VIRTUALIZE_THRESHOLD;
	const virtualItemKey = useCallback((index: number) => groups[index]!.key, [groups]);
	const captureViewportResize = useCallback((instance: Virtualizer<HTMLDivElement, Element>) => {
		const node = instance.scrollElement;
		if (!node) return;
		const previous = viewportSize.current;
		const next = { width: node.clientWidth, height: node.clientHeight };
		if (previous && (previous.width !== next.width || previous.height !== next.height) && !viewportResize.current) {
			const row = instance.getVirtualItemForOffset(node.scrollTop);
			viewportResize.current = {
				anchor: !pinnedRef.current && row ? { key: row.key, offset: row.start - node.scrollTop } : undefined,
			};
		}
		viewportSize.current = next;
	}, []);
	const virtualizer = useVirtualizer({
		// Scroll anchoring may notify during a layout effect; defer the React
		// update rather than attempting a synchronous flush inside that effect.
		useFlushSync: false,
		// Measure short histories too, so crossing the windowing threshold can
		// preserve the visible turn by key rather than restarting at the bottom.
		enabled: groups.length > 0,
		count: groups.length,
		getScrollElement: () => scroller.current,
		getItemKey: virtualItemKey,
		estimateSize: () => CHAT_ESTIMATED_TURN_HEIGHT,
		onChange: () => followVirtualHeight.current(),
		gap: CHAT_TURN_GAP,
		overscan: 2,
		scrollMargin: virtualScrollMargin,
		initialRect: { width: 768, height: CHAT_INITIAL_VIEWPORT_HEIGHT },
		initialOffset: () => virtualized
			? Math.max(0, groups.length * (CHAT_ESTIMATED_TURN_HEIGHT + CHAT_TURN_GAP) - CHAT_INITIAL_VIEWPORT_HEIGHT)
			: 0,
		anchorTo: virtualized ? "end" : "start",
		// The virtualizer's distance-from-end excludes the trailing prompt spacer.
		// Disable end anchoring while unpinned so streamed output cannot mistake the
		// reader's position for the physical end of the scroll container.
		scrollEndThreshold: pinned ? 1 : -1,
		// Appends are followed by syncScrollLayout, which glides and accounts for the
		// prompt spacer; the virtualizer's own follow would snap past both.
		followOnAppend: false,
		// Bootstrap unmeasurable panels until their real geometry is available.
		observeElementRect: (instance, callback) => observeElementRect(instance, (rect) => {
			captureViewportResize(instance);
			callback(rect.height ? rect : { width: rect.width, height: CHAT_INITIAL_VIEWPORT_HEIGHT });
		}),
		measureElement: (element, entry, instance) => {
			// Capture the old row positions before the first width remeasurement.
			captureViewportResize(instance);
			const index = Number(element.getAttribute("data-index"));
			const group = groups[index];
			if (group && !measuredVirtualGroups.current.has(group)) {
				measuredVirtualGroups.current.add(group);
				pendingVirtualLayout.current = true;
			}
			// The resize this measure leads to is applied synchronously, right after it.
			spacerTakes(pinnedRef.current && index >= groups.findLastIndex(groupHasHumanPrompt));
			return measureElement(element, entry, instance) || CHAT_ESTIMATED_TURN_HEIGHT;
		},
		scrollToFn: (offset, { adjustments = 0 }, instance) => {
			if (!instance.scrollElement) return;
			// A resize restores its anchor after the new row positions reach the DOM.
			if (viewportResize.current) return;
			// An instant write would abort our smooth glide (and land at a stale offset).
			// Let the glide carry the adjustment: re-aim it at the new end instead. The
			// next scroll event re-syncs the virtualizer's offset.
			if (isFlying()) {
				reaim();
				return;
			}
			// Anchored to the end, the virtualizer moves the view by every change in the
			// latest prompt's turn: a fold opening or closing, a settled turn folding its
			// steps away. While the spacer under that prompt is open, the change is the
			// spacer's to take (syncStreamedGrowth sizes it, as without windowing), so
			// the view holds still instead of sliding the wrong way and snapping back.
			// The next scroll event re-syncs the virtualizer's offset.
			if (spacerTakesResize.current && adjustments !== 0) return;
			instance.scrollElement.scrollTop = offset + adjustments;
			// Anchoring writes are ours; they must never read as the reader leaving the end.
			markWritten(instance.scrollElement.scrollTop);
		},
		rangeExtractor: (range) => {
			const indexes = defaultRangeExtractor(range);
			// Keep an in-progress inline edit mounted while its row leaves view.
			const editing = groups.findIndex((group) => turnEditing(group, messageEdit));
			if (editing >= 0 && !indexes.includes(editing)) indexes.push(editing);
			if (pinned && !indexes.includes(groups.length - 1)) indexes.push(groups.length - 1);
			return indexes.sort((a, b) => a - b);
		},
	});
	virtualizer.shouldAdjustScrollPositionOnItemSizeChange = virtualized ? undefined : () => false;
	const virtualRows = virtualizer.getVirtualItems();
	const virtualHeight = virtualizer.getTotalSize();
	useEffect(() => {
		if (!findTarget) return;
		if (!find.open || !find.engaged || !find.active || occurrenceKey(find.active) !== occurrenceKey(findTarget)) {
			setFindTarget(undefined);
			return;
		}
		const index = groups.findIndex((group) => group.items.some((item) => item.id === findTarget.messageId));
		if (index < 0) return;
		const mounted = Array.from(scrollContent.current?.querySelectorAll<HTMLElement>("[data-find-message-id]") ?? [])
			.some((element) => element.dataset.findMessageId === findTarget.messageId);
		// The active highlight persists; mounting completes this one reveal request.
		if (mounted) { setFindTarget(undefined); return; }
		const disclosureKey = `${groups[index].key}:worked`;
		if (!activityDisclosureOverrides[disclosureKey]) setActivityDisclosureOverrides((current) => ({ ...current, [disclosureKey]: true }));
		if (virtualized) virtualizer.scrollToIndex(index, { align: "center" });
	}, [find.open, find.engaged, find.active, findTarget, groups, virtualRows, virtualized, virtualizer, activityDisclosureOverrides]);
	renderedVirtualHeight.current = virtualHeight;
	const findGapGroupKey = find.gapBeforeSequence === undefined ? undefined
		: groups.find((group) => group.items.some((item) => item.sequence >= find.gapBeforeSequence!))?.key;
	const renderedGroups = virtualized
		? virtualRows.map((row) => ({ group: groups[row.index]!, row, index: row.index }))
		: groups.map((group, index) => ({ group, row: undefined, index }));
	useEffect(() => {
		if (!annotationTarget) return;
		const index = groups.findIndex((group) => group.items.some((item) => item.id === annotationTarget.messageId));
		if (index < 0) {
			if (loadingOlder) return;
			if (hasOlder && onLoadOlder && annotationOlderRequestGroups.current !== groups.length) {
				annotationOlderRequestGroups.current = groups.length;
				onLoadOlder();
				return;
			}
			setAnnotationNavigationError("The referenced message is no longer available in this chat.");
			setAnnotationTarget(undefined);
			return;
		}
		const source = Array.from(scrollContent.current?.querySelectorAll<HTMLElement>("[data-chat-message-id]") ?? [])
			.find((element) => element.dataset.chatMessageId === annotationTarget.messageId);
		if (!source) {
			const disclosureKey = `${groups[index].key}:worked`;
			if (!activityDisclosureOverrides[disclosureKey]) {
				setActivityDisclosureOverrides((current) => ({ ...current, [disclosureKey]: true }));
			}
			if (virtualized) virtualizer.scrollToIndex(index, { align: "center" });
			if (annotationRetryCount.current++ >= 20) {
				setAnnotationNavigationError("The referenced message could not be brought into view.");
				setAnnotationTarget(undefined);
				return;
			}
			const retryTimer = window.setTimeout(() => {
				setAnnotationTarget((current) => current ? { ...current } : current);
			}, 80);
			return () => window.clearTimeout(retryTimer);
		}
		// Wait until the popover has closed/restored focus before scrolling.
		const frame = requestAnimationFrame(() => {
			window.clearTimeout(annotationHighlightTimer.current);
			annotationHighlightCleanup.current?.();
			annotationHighlightCleanup.current = highlightChatAnnotation(source, annotationTarget.text, annotationTarget.revision, annotationTarget.snapshot);
			const scrollerNode = scroller.current;
			const diagram = annotationDiagramBlock(source, annotationTarget);
			let range = annotationReferenceRange(source, annotationTarget);
			if (diagram && scrollerNode) {
				const alignDiagram = () => {
					if (!diagram.isConnected) return;
					const rect = diagram.getBoundingClientRect();
					const viewport = scrollerNode.getBoundingClientRect();
					if (!rect.height || !viewport.height) return;
					const padding = 12;
					if (rect.top >= viewport.top + padding && rect.bottom <= viewport.bottom - padding) return;
					const delta = rect.height > viewport.height - padding * 2
						? rect.top - viewport.top - padding
						: rect.top + rect.height / 2 - (viewport.top + viewport.height / 2);
					scrollerNode.scrollTo({ top: scrollerNode.scrollTop + delta, behavior: "auto" });
				};
				alignDiagram();
				// Mermaid may replace its loading source with a differently sized SVG.
				// Follow that layout briefly, but stop immediately on user interaction.
				const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(alignDiagram);
				observer?.observe(diagram);
				observer?.observe(source);
				const stopFollowing = () => observer?.disconnect();
				const events = ["wheel", "touchstart", "pointerdown", "keydown"] as const;
				const interactionTarget = scrollerNode.ownerDocument;
				for (const event of events) interactionTarget.addEventListener(event, stopFollowing, { passive: true });
				const clearHighlight = annotationHighlightCleanup.current;
				annotationHighlightCleanup.current = () => {
					stopFollowing();
					for (const event of events) interactionTarget.removeEventListener(event, stopFollowing);
					clearHighlight?.();
				};
			} else if (range && scrollerNode) {
				const rangeRect = range.getBoundingClientRect();
				const scrollerRect = scrollerNode.getBoundingClientRect();
				const padding = 12;
				const visible = rangeRect.top >= scrollerRect.top + padding && rangeRect.bottom <= scrollerRect.bottom - padding;
				if (!visible) {
					source.scrollIntoView({ behavior: "auto", block: "center" });
					// Re-read after the source jump; layout may have moved the selected
					// range before the final centering adjustment.
					range = annotationReferenceRange(source, annotationTarget);
					if (range) {
						const nextRect = range.getBoundingClientRect();
						const delta = nextRect.top + nextRect.height / 2 - (scrollerRect.top + scrollerRect.height / 2);
						scrollerNode.scrollTo({ top: scrollerNode.scrollTop + delta, behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
					}
				}
			} else if (scrollerNode) {
				// The text is not an exact match for the rendered message (a search
				// snippet of raw Markdown, say): still bring the whole message into view.
				const sourceRect = source.getBoundingClientRect();
				const scrollerRect = scrollerNode.getBoundingClientRect();
				if (sourceRect.top < scrollerRect.top || sourceRect.bottom > scrollerRect.bottom) {
					source.scrollIntoView({ behavior: "auto", block: sourceRect.height > scrollerRect.height ? "start" : "center" });
				}
			}
			annotationHighlightTimer.current = window.setTimeout(() => annotationHighlightCleanup.current?.(), 2200);
			setAnnotationTarget(undefined);
		});
		return () => cancelAnimationFrame(frame);
	}, [annotationTarget, groups, hasOlder, loadingOlder, onLoadOlder, virtualized, virtualizer, virtualRows, activityDisclosureOverrides]);
	useEffect(() => {
		const node = scroller.current;
		const content = virtualized ? virtualContent.current
			: virtualContent.current?.querySelector<HTMLElement>('[data-index="0"]');
		if (node && content) {
			setVirtualScrollMargin(content.getBoundingClientRect().top - node.getBoundingClientRect().top + node.scrollTop);
		}
	}, [virtualized, hasOlder]);

	// Long transcripts use measured/estimated row positions for the full minimap,
	// including turns that are currently outside the mounted viewport.
	const anchorGeometry = useRef<{ height: number; width: number; positions: number[] } | null>(null);
	const contentMutations = useRef<MutationObserver | null>(null);

	const updateScrollbar = useCallback(() => {
		const node = scroller.current;
		const track = scrollTrack.current;
		if (!node || !track) return;

		const maxScroll = Math.max(0, node.scrollHeight - node.clientHeight);
		const visible = maxScroll > 1;
		const trackHeight = track.clientHeight;
		const height = visible
			? Math.max(40, trackHeight * (node.clientHeight / node.scrollHeight))
			: trackHeight;
		const travel = Math.max(0, trackHeight - height);
		const fraction = maxScroll > 0 ? Math.min(1, Math.max(0, node.scrollTop / maxScroll)) : 0;
		if (contentMutations.current?.takeRecords().length) anchorGeometry.current = null;
		let geometry = anchorGeometry.current;
		if (virtualized || !geometry || geometry.height !== node.scrollHeight || geometry.width !== node.clientWidth) {
			const viewportRect = node.getBoundingClientRect();
			const anchors = Array.from(
				scrollContent.current?.querySelectorAll<HTMLElement>("[data-chat-scroll-anchor]") ?? [],
			);
			geometry = {
				height: node.scrollHeight,
				width: node.clientWidth,
				positions: virtualized ? groups.flatMap((group, index) => {
					const row = virtualizer.measurementsCache[index];
					return groupHasHumanPrompt(group) && row ? [row.start + row.size / 2] : [];
				}) : anchors.map((anchor) => {
					const rect = anchor.getBoundingClientRect();
					return rect.top - viewportRect.top + node.scrollTop + rect.height / 2;
				}),
			};
			anchorGeometry.current = geometry;
		}
		const positions = geometry.positions;
		// 8px is the existing 2px dash + 6px gap cadence.
		const markerGap = positions.length > 1 ? Math.min(8, (trackHeight - 12) / (positions.length - 1)) : 0;
		const markerStart = (trackHeight - markerGap * Math.max(0, positions.length - 1)) / 2;
		const markers = positions.map((contentY, index) => ({
			top: markerStart + index * markerGap,
			scrollTop: Math.min(maxScroll, Math.max(0, contentY - node.clientHeight / 2)),
			visible: contentY >= node.scrollTop && contentY <= node.scrollTop + node.clientHeight,
		}));
		const next = {
			visible,
			top: travel * fraction,
			height,
			percent: Math.round(fraction * 100),
			atEnd: maxScroll - node.scrollTop <= 2,
			markers,
		};
		setScrollbar((current) =>
			current.visible === next.visible &&
			current.atEnd === next.atEnd &&
			Math.abs(current.top - next.top) < 0.5 &&
			Math.abs(current.height - next.height) < 0.5 &&
			current.percent === next.percent &&
			current.markers.length === next.markers.length &&
			current.markers.every((marker, index) => {
				const candidate = next.markers[index];
				return (
					candidate !== undefined &&
					Math.abs(marker.top - candidate.top) < 0.5 &&
					Math.abs(marker.scrollTop - candidate.scrollTop) < 0.5 &&
					marker.visible === candidate.visible
				);
			})
				? current
				: next,
		);
	}, [virtualized, virtualizer, groups]);

	/**
	 * Size the trailing spacer so scrolling to the bottom parks the latest human
	 * prompt below a band of prior chat — matching Cursor, where a little of the
	 * previous turn stays visible above the new prompt instead of flush to the top.
	 * Shrinks naturally as that reply grows.
	 */
	const syncPromptSpacer = useCallback(() => {
		const node = scroller.current;
		const pad = promptSpacer.current;
		if (!node || !pad) return;

		const anchors = scrollContent.current?.querySelectorAll<HTMLElement>(
			"[data-chat-scroll-anchor]",
		);
		const anchor = anchors && anchors.length > 0 ? anchors[anchors.length - 1] : null;
		const lastPromptIndex = groups.findLastIndex(groupHasHumanPrompt);
		const virtualAnchor = virtualized && lastPromptIndex >= 0 ? virtualizer.measurementsCache[lastPromptIndex] : undefined;
		// A just-appended turn is measured in steps: the virtualizer learns its real height
		// before the DOM does, so for a moment the rendered content still carries the
		// 600px estimate. Sized from that, the spacer collapses to zero, the browser clamps
		// scrollTop to the shorter content, and the view jumps up and never comes back.
		// Wait for the DOM to catch up; the resize that follows re-runs this.
		if (virtualized && renderedVirtualHeight.current !== virtualizer.getTotalSize()) return;
		const nextHeight = (virtualAnchor || anchor)
			? promptSpacerHeight({
					viewportHeight: node.clientHeight,
					contentHeightWithoutSpacer: node.scrollHeight - pad.offsetHeight,
					anchorOffset:
						virtualAnchor?.start ?? (anchor!.getBoundingClientRect().top - node.getBoundingClientRect().top + node.scrollTop),
					topInset: promptTopInset(node.clientHeight),
				})
			: 0;
		if (Math.abs(pad.offsetHeight - nextHeight) > 0.5) {
			pad.style.height = `${nextHeight}px`;
		}
	}, [virtualized, virtualizer, groups]);

	// Opening a chat lands at the end instantly. Long histories measure their rows
	// over the first frames (and may arrive in more than one snapshot), so keep
	// snapping briefly after the first real layout instead of animating across them.
	const followOrSnapToEnd = useCallback((node: HTMLElement) => {
		// Sending shrinks the composer, which resizes the viewport. That must not turn
		// the send's glide into a jump: keep it on course to the new end.
		if (viewportResize.current && (glideRequested.current || isFlying())) {
			reaim();
			return;
		}
		// The same goes for rows the reader did not watch arrive (a catch-up after they
		// return, or anything landing while the window is hidden, where a glide would
		// only start once they are back), and for the moments after the window is seen
		// again while rows that landed hidden measure.
		const now = performance.now();
		const hidden = pageHidden();
		const opening = !initialLayoutDone.current || now - initialLayoutAt.current < INITIAL_SNAP_MS;
		if (viewportResize.current || opening || now - entranceGate.catchUpAt < INITIAL_SNAP_MS || hidden) {
			if (!viewportResize.current && !opening && isFlying()) {
				// The reader's own send or Jump to latest keeps on course to the new end;
				// an automatic follow glide stops and lands. Hidden, neither could play.
				if (!hidden && isGliding()) {
					reaim();
					return;
				}
				cancelScrollFollow();
			}
			if (viewportResize.current) cancelScrollFollow();
			node.scrollTop = node.scrollHeight;
			markWritten(node.scrollTop);
			return;
		}
		followEnd();
	}, [followEnd, markWritten, cancelScrollFollow, isFlying, isGliding, reaim]);

	const syncScrollLayout = useCallback(() => {
		anchorGeometry.current = null;
		syncPromptSpacer();
		const node = scroller.current;
		const relatched = skipRelatchFollow.current;
		skipRelatchFollow.current = false;
		const reading = pendingReading.current;
		if (reading && node && groupCountRef.current > 0 && !initialLayoutDone.current) {
			pendingReading.current = undefined;
			const row = virtualizer.measurementsCache.find((candidate) => candidate.key === reading.key);
			if (row) {
				// Disclosures reopen collapsed, so the turn may be shorter than when the reader
				// left it; never land past its end, in a later turn.
				node.scrollTop = Math.min(row.start - reading.offset, Math.max(row.start, row.end - node.clientHeight));
				markWritten(node.scrollTop);
				lastScrollTop.current = node.scrollTop;
			} else {
				setPinnedNow(true);
			}
		}
		if (node && pinnedRef.current && !(relatched && !glideRequested.current)) {
			if (glideRequested.current) {
				glideRequested.current = false;
				glideToEnd();
			} else {
				followOrSnapToEnd(node);
			}
		}
		if (groupCountRef.current > 0 && !initialLayoutDone.current) {
			initialLayoutDone.current = true;
			initialLayoutAt.current = performance.now();
		}
		updateScrollbar();
	}, [syncPromptSpacer, updateScrollbar, glideToEnd, followOrSnapToEnd, virtualizer, markWritten, setPinnedNow]);

	const restoreViewportResize = useCallback(() => {
		const resize = viewportResize.current;
		const node = scroller.current;
		if (!resize || !node || (virtualized && renderedVirtualHeight.current !== virtualizer.getTotalSize())) return;
		syncPromptSpacer();
		if (pinnedRef.current) followOrSnapToEnd(node);
		else if (resize.anchor) {
			const anchor = resize.anchor;
			const row = virtualizer.measurementsCache.find((candidate) => candidate.key === anchor.key);
			if (row) node.scrollTop = row.start - anchor.offset;
		}
		markWritten(node.scrollTop);
		lastScrollTop.current = node.scrollTop;
		viewportResize.current = undefined;
		updateScrollbar();
	}, [virtualized, virtualizer, syncPromptSpacer, followOrSnapToEnd, markWritten, updateScrollbar]);

	// Streamed text grows the content without a new sequence. While following, let
	// the reply fill the prompt spacer first (the view holds still), then follow the
	// end smoothly once the reply is taller than the viewport.
	const syncStreamedGrowth = useCallback(() => {
		if (!pinnedRef.current || !initialLayoutDone.current) return;
		anchorGeometry.current = null;
		const node = scroller.current;
		// Something at the end shrank on its own (a finished plan folding away). The
		// browser has already clamped scrollTop by the time this observer runs, before
		// the spacer could grow back, so put the view back now, before paint, instead
		// of gliding back from the clamped spot. Without a spacer the clamp is already
		// the end and this writes nothing.
		const clampedFrom = node && node.scrollTop < lastScrollTop.current - 1 ? lastScrollTop.current : undefined;
		syncPromptSpacer();
		if (node && clampedFrom !== undefined) {
			node.scrollTop = Math.min(clampedFrom, Math.max(0, node.scrollHeight - node.clientHeight));
			markWritten(node.scrollTop);
			lastScrollTop.current = node.scrollTop;
		}
		if (node) followOrSnapToEnd(node);
		updateScrollbar();
	}, [syncPromptSpacer, followOrSnapToEnd, updateScrollbar, markWritten]);

	// A windowed chat learns a turn's new height (a fold opening or closing) a frame
	// before React draws it, and the spacer waits for the two to agree. Near the end,
	// a shrink then clamps the view to the stale height on every frame of the fold's
	// animation and snaps it back once the animation ends. Take the new height now and
	// size the spacer against it, as an unwindowed chat already does: following the
	// end, always; reading back, only when the shrink would otherwise clamp the view.
	followVirtualHeight.current = () => {
		const node = scroller.current;
		const content = virtualContent.current;
		if (!virtualized || !node || !content || viewportResize.current || !initialLayoutDone.current) return;
		const total = virtualizer.getTotalSize();
		const shrink = renderedVirtualHeight.current - total;
		if (shrink === 0) return;
		const following = pinnedRef.current;
		if (!following && (shrink < 0 || node.scrollTop + node.clientHeight <= node.scrollHeight - shrink)) return;
		const top = node.scrollTop;
		content.style.height = `${total}px`;
		renderedVirtualHeight.current = total;
		if (following) {
			syncStreamedGrowth();
			return;
		}
		syncPromptSpacer();
		node.scrollTop = Math.min(top, Math.max(0, node.scrollHeight - node.clientHeight));
		markWritten(node.scrollTop);
		lastScrollTop.current = node.scrollTop;
	};

	// Keeps a settled turn's row still under a reader who scrolled up, while its rows
	// re-measure (a reply leaving its streaming render, code highlighting landing).
	const settleHold = useRef<{ observer: ResizeObserver; timer: number } | undefined>(undefined);
	const releaseSettleHold = useCallback(() => {
		const hold = settleHold.current;
		if (!hold) return;
		hold.observer.disconnect();
		window.clearTimeout(hold.timer);
		settleHold.current = undefined;
	}, []);
	useEffect(() => releaseSettleHold, [releaseSettleHold]);
	// A settling turn folds all of its steps under "Worked for" in one commit, so
	// everything under them (the reply, its action row) moves up by their height at
	// once, and the turn shrinks before the prompt spacer can grow back to take it.
	// Left alone, the browser or the virtualizer pulls the view up and the follow
	// glides it back down: the whole chat dips and returns. So note where the turn's
	// rows were on screen just before the fold...
	const captureSettle = useCallback((key: string): SettleAnchor | undefined => {
		const node = scroller.current;
		if (!node || !initialLayoutDone.current) return undefined;
		if (!groups.some((group) => group.key === key && !group.live)) return undefined;
		const wrapper = Array.from(virtualContent.current?.children ?? [])
			.find((child) => (child as HTMLElement).dataset.chatTurn === key);
		const turn = wrapper?.firstElementChild as HTMLElement | null | undefined;
		if (!turn) return undefined;
		const top = node.getBoundingClientRect().top;
		const rows = Array.from(turn.children) as HTMLElement[];
		const rects = rows.map((row) => row.getBoundingClientRect());
		const following = pinnedRef.current;
		const index = following ? -1 : rects.findIndex((rect) => rect.bottom > top);
		if (!following && index < 0) return undefined;
		return {
			turn,
			rows,
			tops: rects.map((rect) => rect.top),
			scrollTop: node.scrollTop,
			following,
			index,
			offset: index >= 0 ? rects[index]!.top - top : 0,
		};
	}, [groups]);
	// ...and put the view back right after, before paint. A reader following the end
	// stays where they were: only a shrink the spacer cannot take moves the view, and
	// that lands on the end. For a reader who scrolled up, a row still in the turn
	// (the prompt, a steer, the reply) holds its place; a step that folded away hands
	// its place to "Worked for", which lands at the top of the view with the reply
	// under it. Then every row that still moved slides there from where it was.
	const restoreSettle = useCallback((settle: SettleAnchor) => {
		const node = scroller.current;
		if (!node) return;
		const { turn, rows, index, offset } = settle;
		// The live row popLayout lifted out of the flow still props the scroll height up
		// where it was, so leave it out while the spacer and the view are worked out.
		const liveRows = rows.filter((row) => row.isConnected && row.hasAttribute("data-turn-live-row"));
		for (const row of liveRows) row.style.display = "none";
		// A windowed list learns the turn's new height on its next measure, a frame
		// later, and only then shrinks; take it now, so the end is final before paint.
		const wrapper = turn.parentElement;
		const virtualIndex = Number(wrapper?.dataset.index);
		if (virtualized && wrapper && Number.isInteger(virtualIndex)) {
			spacerTakes(settle.following);
			virtualizer.resizeItem(virtualIndex, Math.round(wrapper.getBoundingClientRect().height));
			const total = virtualizer.getTotalSize();
			if (virtualContent.current) virtualContent.current.style.height = `${total}px`;
			renderedVirtualHeight.current = total;
		}
		// The fold changed what the end can reach, so size the spacer before moving.
		syncPromptSpacer();
		let held: Element | undefined;
		if (settle.following) {
			const end = Math.max(0, node.scrollHeight - node.clientHeight);
			const top = Math.min(settle.scrollTop, end);
			if (Math.abs(node.scrollTop - top) >= 0.5) node.scrollTop = top;
			markWritten(node.scrollTop);
			lastScrollTop.current = node.scrollTop;
		} else {
			const anchor = rows[index]!;
			const kept = anchor.isConnected && !anchor.hasAttribute("data-turn-live-row");
			const target = kept
				? anchor
				: Array.from(turn.children).find((child) => child.hasAttribute("data-turn-worked"))
					?? rows.slice(index + 1).find((row) => row.isConnected);
			if (target) {
				held = target;
				const landing = kept ? offset : 0;
				const place = () => {
					const delta = target.getBoundingClientRect().top - node.getBoundingClientRect().top - landing;
					if (Math.abs(delta) < 0.5) return;
					node.scrollTop += delta;
					markWritten(node.scrollTop);
					lastScrollTop.current = node.scrollTop;
				};
				place();
				// The log has no scroll anchoring of its own: re-place the row after each
				// layout step while the turn re-measures, or until the reader scrolls.
				releaseSettleHold();
				if (typeof ResizeObserver !== "undefined") {
					const observer = new ResizeObserver(place);
					observer.observe(turn);
					settleHold.current = { observer, timer: window.setTimeout(releaseSettleHold, SETTLE_HOLD_MS) };
				}
			}
		}
		for (const row of liveRows) row.style.display = "";
		updateScrollbar();
		// A hidden window would hold the slide until the reader returns, then play it.
		if (reducedMotionRef.current || pageHidden() || typeof Element.prototype.animate !== "function") return;
		rows.forEach((row, at) => {
			// The row the hold keeps in place is measured on every re-place, so it must not move.
			if (row === held || !row.isConnected || row.parentElement !== turn) return;
			const delta = settle.tops[at]! - row.getBoundingClientRect().top;
			if (Math.abs(delta) < 1) return;
			const from = `translateY(${delta}px)`;
			// The live row is already out of the flow, fading where it was; if the view
			// moved under it, it stays put on screen (and inside the scroll height) while
			// it goes.
			if (row.hasAttribute("data-turn-live-row")) {
				row.animate([{ transform: from }, { transform: from }], { duration: SETTLE_SLIDE_MS, fill: "forwards" });
				return;
			}
			row.animate([{ transform: from }, { transform: "none" }], { duration: SETTLE_SLIDE_MS, easing: SETTLE_EASE });
		});
	}, [virtualized, virtualizer, spacerTakes, syncPromptSpacer, markWritten, updateScrollbar, releaseSettleHold]);

	// A disclosure animation changes the content box but is not new conversation
	// content. Re-measure it without re-pinning the viewport to the bottom; doing
	// that during a height animation makes the whole chat jump under the reader.
	const syncScrollMetrics = useCallback(() => {
		anchorGeometry.current = null;
		// UI-only disclosure resizes must not rewrite the trailing spacer. Doing so
		// every animation frame changes the scroll range and causes a small jerk when
		// a panel collapses near the bottom of the viewport.
		updateScrollbar();
	}, [updateScrollbar]);

	useEffect(() => {
		syncScrollLayout();
	}, [pinned, snapshot.latestSequence, groups.length, messageEdit?.turnId, syncScrollLayout]);
	// The window is seen again: the reader starts watching from now. What landed
	// while it was hidden is history, so a glide queued then (it cannot play without
	// frames) is dropped, a reader at the end stays at the end, and the rows that
	// landed hidden measure in place instead of being chased down the log.
	// Read through a ref so streamed chunks (new layout callbacks) never re-register it.
	const onPageShown = useRef(() => {});
	onPageShown.current = () => {
		entranceGate.watchingSince = Date.now();
		entranceGate.catchUpAt = performance.now();
		if (isFlying()) cancelScrollFollow();
		const node = scroller.current;
		if (!node || !initialLayoutDone.current) return;
		if (pinnedRef.current) {
			syncPromptSpacer();
			followOrSnapToEnd(node);
		}
		updateScrollbar();
	};
	useEffect(() => {
		const onVisibilityChange = () => {
			if (!pageHidden()) onPageShown.current();
		};
		document.addEventListener("visibilitychange", onVisibilityChange);
		return () => document.removeEventListener("visibilitychange", onVisibilityChange);
	}, []);
	useLayoutEffect(() => {
		restoreViewportResize();
	}, [virtualHeight, restoreViewportResize]);
	useEffect(() => {
		if (!virtualized || viewportResize.current) return;
		// Wait for measured sizes to reach the DOM before following new content.
		// Resizing an already measured group only refreshes scroll metrics.
		if (pendingVirtualLayout.current && virtualHeight === virtualizer.getTotalSize()) {
			pendingVirtualLayout.current = false;
			syncScrollLayout();
		} else syncScrollMetrics();
	}, [virtualized, virtualHeight, virtualizer, syncScrollLayout, syncScrollMetrics]);

	useEffect(() => {
		const content = scrollContent.current;
		const mutations = new MutationObserver(() => { anchorGeometry.current = null; });
		if (content) mutations.observe(content, {
			subtree: true, childList: true, characterData: true, attributes: true,
			attributeFilter: ["class", "style", "hidden", "open", "data-chat-scroll-anchor"],
		});
		contentMutations.current = mutations;
		return () => {
			mutations.disconnect();
			contentMutations.current = null;
			anchorGeometry.current = null;
		};
	}, []);

	// Read through a ref so a streamed chunk (new `groups` identity) does not tear
	// down and re-arm the observer, which would re-run a full layout per chunk.
	const onContentResize = useRef(syncScrollMetrics);
	onContentResize.current = () => {
		if (viewportResize.current) {
			restoreViewportResize();
			return;
		}
		if (pinnedRef.current) syncStreamedGrowth();
		else syncScrollMetrics();
	};
	useEffect(() => {
		syncScrollLayout();
		if (typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(() => {
			captureViewportResize(virtualizer);
			onContentResize.current();
		});
		if (scroller.current) observer.observe(scroller.current);
		if (scrollContent.current) observer.observe(scrollContent.current);
		return () => observer.disconnect();
	}, [groups.length, captureViewportResize, virtualizer]);

	// The pause-then-resume for a reader who only nudged up a little: armed by their
	// scroll, re-armed by each further scroll, dropped once they go past the band or
	// open something to read. Only the reader's own scrolling arms it, so streamed
	// growth while unpinned (which moves nothing) never does.
	const relatchTimer = useRef<number | undefined>(undefined);
	// Set by the reader's own wheel, touch, key and scrollbar-drag input. A jump to a
	// marker or a prompt moves the view without it, and must not be pulled back.
	const readerIntentAt = useRef(0);
	const noteReaderIntent = useCallback(() => {
		readerIntentAt.current = performance.now();
	}, []);
	const liveTurnRef = useRef(false);
	liveTurnRef.current = turn?.state === "running";
	const clearRelatch = useCallback(() => {
		if (relatchTimer.current === undefined) return;
		window.clearTimeout(relatchTimer.current);
		relatchTimer.current = undefined;
	}, []);
	const scheduleRelatch = useCallback(() => {
		if (findPausedFollow.current) return;
		clearRelatch();
		relatchTimer.current = window.setTimeout(() => {
			relatchTimer.current = undefined;
			const node = scroller.current;
			if (!node || pinnedRef.current || !liveTurnRef.current) return;
			const distance = node.scrollHeight - node.scrollTop - node.clientHeight;
			if (distance > relatchBand(node)) return;
			requestGlideToEnd();
		}, RELATCH_AFTER_MS);
	}, [clearRelatch, requestGlideToEnd]);
	useEffect(() => clearRelatch, [clearRelatch]);

	function onScroll() {
		const node = scroller.current;
		if (!node) return;
		const top = node.scrollTop;
		const movedUp = top < lastScrollTop.current - 1;
		lastScrollTop.current = top;
		const distance = node.scrollHeight - top - node.clientHeight;
		// Classified by where the scroll landed, not when: our own glide, follow and
		// anchoring writes are recognized by position and never unpin. A reader scroll
		// releases the follow once it is away from the end, or whenever it moves up
		// (so a small nudge up is not pulled straight back). A scroll that only clamps
		// to the end after content shrank is not the reader leaving.
		// Any scroll of the reader's own ends a settled turn's hold on their row.
		if (!isOwnScroll(top)) releaseSettleHold();
		if (isOwnScroll(top) || findPausedFollow.current) {
			// Ours: no change.
		} else if (distance >= 64 || (movedUp && distance > 2)) {
			if (pinnedRef.current) releaseFollow();
			// Armed by the reader's input, then kept alive by each further scroll (touch
			// momentum scrolls after the finger lifts).
			const reading = relatchTimer.current !== undefined || performance.now() - readerIntentAt.current < RELATCH_INTENT_MS;
			if (reading && distance <= relatchBand(node)) scheduleRelatch();
			else clearRelatch();
		} else if (!pinnedRef.current && !movedUp) {
			// The reader is already here: the follow layout this re-pin triggers must not
			// snap the last few pixels under their scroll, which reads as a jitter.
			clearRelatch();
			skipRelatchFollow.current = true;
			setPinnedNow(true);
		}
		// Older history loads as the reader scrolls up within a screen of the top,
		// before they reach it. The button above the first turn stays as the
		// loading state and for histories too short to scroll.
		if (movedUp && !isOwnScroll(top) && top < node.clientHeight && hasOlder && !loadingOlder) onLoadOlder?.();
		if (initialLayoutDone.current && !pendingReading.current) {
			const row = pinnedRef.current ? undefined : virtualizer.getVirtualItemForOffset(top);
			rememberReadingPosition(uiSessionId, row ? { key: row.key, offset: row.start - top } : undefined);
		}
		updateScrollbar();
	}

	function onViewportWheel(event: ReactWheelEvent<HTMLDivElement>) {
		// Wheel intent arrives before the scroll it causes. Hold the follow so a
		// concurrent stream commit cannot pull the reader back, and let the scroll
		// itself decide: a wheel consumed by a nested scroller (a code block) never
		// moves the log, so it must not unpin it.
		if (event.deltaY < 0) {
			noteReaderIntent();
			holdScrollFollow(READER_INTENT_HOLD_MS);
		}
	}

	function onViewportKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
		const target = event.target as HTMLElement;
		if (event.defaultPrevented || target.closest("input, textarea, select, [contenteditable='true'], [contenteditable=''], [role='menu'], [role='listbox'], [role='textbox']")) return;
		if (event.key === "ArrowUp" || event.key === "PageUp" || event.key === "Home" || (event.key === " " && event.shiftKey)) {
			noteReaderIntent();
			holdScrollFollow(READER_INTENT_HOLD_MS);
		}
	}

	function onViewportPointerDown(event: ReactPointerEvent<HTMLDivElement>) {
		// A disclosure the reader is opening: following the end would pull it out from
		// under them. Menu triggers (aria-haspopup) open a popup, not content, so they
		// keep the follow; so do clicks on empty margins.
		if ((event.target as HTMLElement).closest("[aria-expanded]:not([aria-haspopup])")) {
			clearRelatch();
			releaseFollow();
		}
	}

	function setScrollFromTrack(clientY: number) {
		cancelScrollFollow();
		const node = scroller.current;
		const track = scrollTrack.current;
		if (!node || !track) return;
		const rect = track.getBoundingClientRect();
		const travel = Math.max(1, track.clientHeight - scrollbar.height);
		const top = Math.min(travel, Math.max(0, clientY - rect.top - scrollbar.height / 2));
		node.scrollTop = (top / travel) * Math.max(0, node.scrollHeight - node.clientHeight);
		updateScrollbar();
	}

	function onScrollbarPointerDown(event: ReactPointerEvent<HTMLDivElement>) {
		cancelScrollFollow();
		if (!minimapEnabled || inspectorOpenRef.current) return;
		const track = scrollTrack.current;
		const node = scroller.current;
		if (!track || !node) return;
		const marker = (event.target as HTMLElement).closest<HTMLElement>("[data-chat-scroll-marker]");
		if (marker?.dataset.scrollTarget) {
			const group = navigableGroups[Number(marker.dataset.markerIndex)];
			if (virtualized && group) {
				virtualizer.scrollToIndex(groups.findIndex((candidate) => candidate.key === group.key), { align: "center" });
			} else {
				node.scrollTop = Number(marker.dataset.scrollTarget);
			}
			onScroll();
			return;
		}
		noteReaderIntent();
		setScrollFromTrack(event.clientY);
		drag.current = {
			pointerId: event.pointerId,
			startY: event.clientY,
			startScrollTop: node.scrollTop,
		};
		event.currentTarget.setPointerCapture(event.pointerId);
	}

	function onScrollbarPointerMove(event: ReactPointerEvent<HTMLDivElement>) {
		if (!minimapEnabled || inspectorOpenRef.current) return;
		const active = drag.current;
		const node = scroller.current;
		const track = scrollTrack.current;
		if (!node || !track) return;
		if (!active) {
			const pointerY = event.clientY - track.getBoundingClientRect().top;
			let nearest = 0;
			for (let index = 1; index < scrollbar.markers.length; index += 1) {
				if (
					Math.abs(scrollbar.markers[index]!.top - pointerY) <
					Math.abs(scrollbar.markers[nearest]!.top - pointerY)
				) {
					nearest = index;
				}
			}
			if (scrollbar.markers.length > 0) setHoveredMarker(nearest);
			return;
		}
		if (active.pointerId !== event.pointerId) return;
		noteReaderIntent();
		const travel = Math.max(1, track.clientHeight - scrollbar.height);
		const maxScroll = Math.max(0, node.scrollHeight - node.clientHeight);
		node.scrollTop = active.startScrollTop + ((event.clientY - active.startY) / travel) * maxScroll;
		updateScrollbar();
	}

	function stopScrollbarDrag(event: ReactPointerEvent<HTMLDivElement>) {
		if (drag.current?.pointerId !== event.pointerId) return;
		drag.current = null;
		if (event.currentTarget.hasPointerCapture(event.pointerId)) {
			event.currentTarget.releasePointerCapture(event.pointerId);
		}
	}

	function onScrollbarWheel(event: ReactWheelEvent<HTMLDivElement>) {
		cancelScrollFollow();
		if (!minimapEnabled || inspectorOpenRef.current) return;
		const node = scroller.current;
		if (!node) return;
		event.preventDefault();
		node.scrollTop += event.deltaY;
		onScroll();
	}

	function onScrollbarKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
		cancelScrollFollow();
		if (!minimapEnabled || inspectorOpenRef.current) return;
		const node = scroller.current;
		if (!node) return;
		const maxScroll = Math.max(0, node.scrollHeight - node.clientHeight);
		const page = Math.max(48, node.clientHeight * 0.85);
		const next: Record<string, number> = {
			ArrowUp: node.scrollTop - 48,
			ArrowDown: node.scrollTop + 48,
			PageUp: node.scrollTop - page,
			PageDown: node.scrollTop + page,
			Home: 0,
			End: maxScroll,
		};
		if (!(event.key in next)) return;
		event.preventDefault();
		node.scrollTop = Math.min(maxScroll, Math.max(0, next[event.key]!));
		updateScrollbar();
	}

	if (timelineItems.length === 0 && !messageEdit && !turn && !showStartup) {
		return null;
	}

	return (
		<ChatOpeningRef.Provider value={chatOpening}>
		<ChatEntrance.Provider value={entranceGate}>
		<LiveStepChoices.Provider value={liveStepChoices}>
		<div
			ref={findSurface}
			data-chat-find-surface=""
			className="relative flex min-h-0 flex-1 flex-col"
			data-testid="chat-timeline"
			// The timeline is a fixed-size flex item with its own scroll viewport.
			// Isolating its layout and paint means Lexical selection/composer updates
			// cannot invalidate the complete mounted conversation history or shell.
			style={{ contain: "layout paint" }}
		>
			{find.bar}
			{selectionAction ? <ChatSelectionActions key={selectionAction.excerpt.id} selection={selectionAction}
				viewportRef={scroller} onInvalid={clearSelectionAction} onAdd={addSelectionToChat}
				onSide={onAddToSideChat ? excerpt => onAddToSideChat(captureExcerptTurn(excerpt, snapshot)) : undefined} checkingSide={checkingSideChatAvailability} /> : null}
			<div
				ref={scrollerRef}
				tabIndex={-1}
				onScroll={onScroll}
				onScrollEnd={endFlight}
				onWheel={onViewportWheel}
				onTouchMove={() => {
					noteReaderIntent();
					holdScrollFollow(READER_INTENT_HOLD_MS);
				}}
				onKeyDown={onViewportKeyDown}
				onPointerDown={onViewportPointerDown}
				className="chat-scroll-viewport cursor-chat-timeline edge-fade [--edge-fade-end-size:var(--space-3)] min-h-0 min-w-0 flex-1 select-text overflow-x-hidden overflow-y-auto px-4 pt-5 pb-0"
				role="log"
				aria-live="polite"
				aria-label="Conversation"
				style={virtualized ? { overflowAnchor: "none" } : undefined}
			>
				<TurnSettleSnapshot liveKey={groups.find((group) => group.live)?.key} capture={captureSettle} restore={restoreSettle} />
				<div ref={scrollContent} className={cn("mx-auto flex w-full min-w-0 max-w-3xl flex-col gap-4.5", revealTranscript && "chat-transcript-reveal")}>
					{annotationNavigationError ? <p role="status" className="text-xs text-muted-foreground">{annotationNavigationError}</p> : null}
					{hasOlder ? (
						<div className="flex justify-center pb-1">
							<Button
								type="button"
								size="sm"
								variant="quiet"
								disabled={loadingOlder}
								onClick={onLoadOlder}
							>
								{loadingOlder ? (
									<Loader2 aria-hidden="true" className="size-3.5 animate-spin" />
								) : null}
								Load earlier messages
							</Button>
						</div>
					) : null}
					<div ref={virtualContent} className={virtualized ? "relative w-full" : "contents"}
						style={virtualized ? { height: virtualHeight } : undefined}>
					{renderedGroups.map(({ group, row, index }) => {
						const retrySelected = !retryControl?.turnId || retryControl.turnId === group.turnId;
						const retryableTurn = group.turnId ? retryableTurns.get(group.turnId) : undefined;
						const retry =
							group.turnId &&
							retryControl &&
							retryableTurn &&
							group.turnId !== composerRecoveryTurnId &&
								group.outcome?.state === "failed"
								? {
										onRetry: () => {
											void Promise.resolve(retryTurn(group.turnId as string)).catch(
												() => undefined,
											);
										},
										pending: retryControl.pending && retrySelected,
										error: retrySelected ? retryControl.error : undefined,
										disabled:
											Boolean(durableTurn) ||
											Boolean(retryControl.pending) ||
											Boolean(newWorkDisabled),
									}
								: undefined;
						return (
							<div
								key={group.key}
								ref={virtualizer.measureElement}
								data-index={index}
								style={row ? {
									position: "absolute", top: 0, left: 0, width: "100%",
									transform: `translateY(${row.start - virtualScrollMargin}px)`,
								} : undefined}
								data-chat-scroll-anchor={groupHasHumanPrompt(group) ? "" : undefined}
								data-chat-turn={group.key}
							>
								{group.key === findGapGroupKey ? <div role="note" className="py-3 text-center text-caption text-muted-foreground">{t("chat.find.gap")}</div> : null}
								<WorkerUpdateIndexContext.Provider value={workerUpdateIndex}>
								<BatchedReportsContext.Provider value={batchedReports}>
								<TurnGroup
									group={group}
									harness={snapshot.harness}
									workerUpdates={workerUpdates}
									startup={group.key === startupGroupKey ? rowStartup : undefined}
									onSelectAnnotation={selectAnnotation}
									activityDisclosureOverrides={activityDisclosureOverrides}
									onActivityDisclosureChange={onActivityDisclosureChange}
									sessionId={snapshot.sessionId}
									apiBaseUrl={apiBaseUrl}
									onDecide={decide}
									onRollback={rollback}
									onOpenFiles={onOpenFiles ? openFiles : undefined}
									onOpenFile={onOpenFile ? openFile : undefined}
									onOpenArtifact={onOpenArtifact ? openArtifact : undefined}
									retry={retry}
									onEditHumanMessage={canEditHumanMessage ? editHumanMessage : undefined}
									messageEdit={messageEdit}
									onStartMessageEdit={stableStartMessageEdit}
									onUpdateMessageEdit={updateMessageEdit}
									onCancelMessageEdit={cancelMessageEdit}
									onAbandonEditRecovery={
										canAbandonInlineEditRecovery ? abandonUncertainInlineEdit : undefined
									}
									onSubmitMessageEdit={stableSubmitMessageEdit}
									editPending={inlineEditPending}
									editSendBlocked={inlineEditSendBlocked}
									editRecoveryLabel={translateDraft(inlineEditRecoveryLabel)}
									editBusy={turnEditing(group, messageEdit) && Boolean(editBusy || newWorkDisabled)}
									editError={translateDraft(editError ??
										draftPersistenceError ??
										inlineEditRecoveryNotice ??
										inlineEditOutcomeNotice)}
									branchPoints={branchPoints}
									editableTurns={editableTurns}
									newHumanMessageIds={turnNewHumanMessageIds(group, newHumanMessageIds)}
									onActivateBranch={canActivateBranch ? activateBranch : undefined}
									activateBranchPending={activateBranchPending}
									activateBranchError={activateBranchError}
									// Only a turn the provider actually accepted can be undone: a turn it
									// never saw holds no history to discard, and the daemon refuses it
									// rather than hiding rows the agent still remembers.
									canRollback={Boolean(onRollback && group.turnId && group.rollbackable)}
									rollbackDisabled={rollbackDisabled}
									busy={turnShowsApproval(group) ? busy : undefined}
									queued={Boolean(group.turnId && queued.has(group.turnId))}
								/>
								</BatchedReportsContext.Provider>
								</WorkerUpdateIndexContext.Provider>
							</div>
						);
					})}
					</div>
					{/* Normally drawn inside the opening turn's group; this covers a start
					    whose brief is not in the timeline. */}
					{rowStartup && (openingSetup || showStartup) && !startupGroupKey ? (
						<LiveResponseStatus startupLabel={rowStartup.label ?? t(rowStartup.failed ? "chat.startup.failed" : "chat.startup.running")} failed={rowStartup.failed}>
							{rowStartup.steps.length > 0 || rowStartup.failed ? <SessionStartup {...rowStartup} /> : null}
						</LiveResponseStatus>
					) : turn?.state === "running" && !groups.some((group) => group.turnId === turn.id) ? (
						<LiveResponseStatus startedAt={turn.startedAt ?? turn.requestedAt} task="Thinking" />
					) : openingPending && !groups.some((group) => group.live) ? (
						// Before the brief has a turn of its own (still being created).
						<LiveResponseStatus startedAt={openingStartedAt} startupLabel={openingStartupLabel} />
					) : null}
					{messageEdit && !editedMessageVisible ? (
						// No bubble to stand in for here, so the editor draws one, with Cancel
						// and Send on their own row below it.
						<div className="flex flex-col items-end gap-1" data-chat-scroll-anchor="">
							<HumanMessageEditor
								text={messageEdit.text}
								content={messageEdit.content}
								sessionId={snapshot.sessionId}
								apiBaseUrl={apiBaseUrl}
								className={humanBubbleClass()}
								actionsClassName="flex h-7 items-center gap-1"
								pending={inlineEditPending}
								locked={Boolean(inlineEditRecoveryLabel)}
								recoveryLabel={translateDraft(inlineEditRecoveryLabel)}
								sendBlocked={inlineEditSendBlocked}
								busy={Boolean(editBusy || newWorkDisabled)}
								error={translateDraft(editError ??
									draftPersistenceError ??
									inlineEditRecoveryNotice ??
									inlineEditOutcomeNotice)}
								onDraftChange={updateMessageEdit}
								onCancel={cancelMessageEdit}
								onAbandonRecovery={
									canAbandonInlineEditRecovery ? abandonUncertainInlineEdit : undefined
								}
								onSend={submitMessageEdit}
							/>
						</div>
					) : null}
					{/* Pushes the latest prompt toward the top of the viewport when following,
					    then shrinks as the reply fills the space below it. */}
					<div
						ref={promptSpacer}
						aria-hidden="true"
						className="chat-prompt-spacer"
						data-testid="chat-prompt-spacer"
					/>
				</div>
			</div>

				<div
					ref={scrollTrack}
					role="scrollbar"
					data-testid="chat-conversation-minimap"
					data-inspector-open={inspectorOpenRef.current}
					tabIndex={minimapEnabled && !inspectorOpenRef.current ? 0 : -1}
					aria-hidden={inspectorOpenRef.current || undefined}
				aria-label="Conversation scrollbar"
				aria-orientation="vertical"
				aria-valuemin={0}
				aria-valuemax={100}
				aria-valuenow={scrollbar.percent}
				onPointerDown={onScrollbarPointerDown}
				onPointerMove={onScrollbarPointerMove}
				onPointerUp={stopScrollbarDrag}
				onPointerCancel={stopScrollbarDrag}
				onWheel={onScrollbarWheel}
				onKeyDown={onScrollbarKeyDown}
				onFocus={() => {
						if (!minimapEnabled || inspectorOpenRef.current || scrollbar.markers.length === 0) return;
					setHoveredMarker(
						Math.min(
							scrollbar.markers.length - 1,
							Math.round((scrollbar.percent / 100) * (scrollbar.markers.length - 1)),
						),
					);
				}}
				onBlur={() => setHoveredMarker(null)}
				onPointerLeave={() => setHoveredMarker(null)}
					className={cn(
						"group/scroll absolute inset-y-3 right-1 z-10 w-6 touch-none rounded-full outline-none focus-visible:ring-1 focus-visible:ring-logo-accent/60",
						minimapRevealed && "transition-opacity",
						minimapEnabled && !inspectorOpenRef.current
							? "cursor-pointer opacity-100"
							: "pointer-events-none opacity-0",
						"data-[inspector-open=true]:pointer-events-none data-[inspector-open=true]:opacity-0",
					)}
				>
					<div className="absolute inset-0 cursor-grab group-active/scroll:cursor-grabbing">
						{minimapEnabled && !inspectorOpenRef.current
						? scrollbar.markers.map((marker, index) => {
								const distance =
									hoveredMarker === null
										? Number.POSITIVE_INFINITY
										: Math.abs(index - hoveredMarker);
								return (
									<span
										key={index}
										data-chat-scroll-marker=""
										data-scroll-target={marker.scrollTop}
										data-marker-index={index}
										onPointerEnter={() => setHoveredMarker(index)}
										className="chat-scroll-marker-hit"
										style={{ top: marker.top }}
									>
										<span
											aria-hidden="true"
											className={cn(
												"chat-scroll-marker",
												marker.visible && "chat-scroll-marker-visible",
												distance === 0 && "chat-scroll-marker-active",
												distance === 1 && "chat-scroll-marker-adjacent",
												distance === 2 && "chat-scroll-marker-near",
											)}
										/>
									</span>
								);
							})
						: null}
				</div>

				{minimapEnabled &&
				hoveredMarker !== null &&
				scrollbar.markers[hoveredMarker] &&
				previews[hoveredMarker] ? (
					<div
						role="tooltip"
						className="chat-scroll-preview pointer-events-none absolute right-full z-20 mr-3 w-80 rounded-xl border border-(--color-border-overlay) bg-raised px-3.5 py-3 shadow-(--shadow-popover)"
						style={{
							top: `clamp(4rem, ${scrollbar.markers[hoveredMarker].top}px, calc(100% - 4rem))`,
						}}
					>
						<strong className="line-clamp-1 text-sm font-medium leading-snug text-foreground">
							{previews[hoveredMarker].title}
						</strong>
						{previews[hoveredMarker].detail ? (
							<p className="mt-1.5 line-clamp-3 text-xs leading-relaxed text-muted-foreground">
								{previews[hoveredMarker].detail}
							</p>
						) : null}
					</div>
				) : null}
			</div>

			{/* Released is not the same as away: folding a panel at the end releases the
			    follow but leaves the reader at the bottom, where a jump has nowhere to go
			    and no scroll could bring the follow back to retire it. */}
			{!pinned && !scrollbar.atEnd ? (
				<Button
					type="button"
					size="icon-sm"
					variant="secondary"
					aria-label="Jump to latest"
					title="Jump to latest"
					onClick={requestGlideToEnd}
					className="absolute bottom-3 left-1/2 size-12 -translate-x-1/2 rounded-full border-border-strong bg-raised p-0 text-foreground shadow-sm hover:bg-surface dark:bg-raised dark:hover:bg-surface"
				>
					<ArrowDown aria-hidden="true" className="size-5" />
				</Button>
			) : null}
		</div>
		</LiveStepChoices.Provider>
		</ChatEntrance.Provider>
		</ChatOpeningRef.Provider>
	);
}

/**
 * How long a step stays on the live row once it shows there. Claude Code reports
 * most tool calls as pending and then done, with nothing in between, and a quick
 * one is done within a second. Without a floor the live row would never name it,
 * and it would only ever appear in the list above.
 */
const LIVE_STEP_MIN_MS = 900;

/**
 * The step the live row names in place of its own row: the newest step that is
 * running, or pending while nothing waits on the reader. Once no step is in
 * progress, the newest one stays on the row until it has been there for
 * LIVE_STEP_MIN_MS, so each step shows beside the loader before it moves up into
 * the list. Only a step that completed stays: one that failed or was stopped goes
 * straight to its row, which says so. A step that was already done when the turn
 * mounted (a session reopened mid-turn) never comes back to the row. A step in a
 * subagent's tree, or one with a subagent under it, keeps its row, so the tree
 * stays whole.
 *
 * While the row shows a retry or the setup checklist (`paused`), it cannot show a
 * step, so every step keeps its own row.
 */
function useLiveRowStep(group: TimelineGroup, paused: boolean): ConversationActivity | undefined {
	const [, setTick] = useState(0);
	const memory = useRef<{ shownAt: Map<string, number>; doneAtMount?: Set<string> }>({ shownAt: new Map() });
	const parents = new Set<string>();
	for (const item of group.items) {
		if (item.kind === "activity" && item.detail?.parentProviderItemId) parents.add(item.detail.parentProviderItemId);
	}
	const steps = group.live
		? group.items.filter(
				(item): item is ConversationActivity =>
					item.kind === "activity" &&
					isLiveStepKind(item) &&
					!item.detail?.parentProviderItemId &&
					!(item.providerItemId && parents.has(item.providerItemId)),
			)
		: [];
	memory.current.doneAtMount ??= new Set(steps.filter((step) => !stepInProgress(step)).map((step) => step.id));

	const now = Date.now();
	let step = paused
		? undefined
		: [...steps]
				.reverse()
				.find((candidate) => candidate.status === "running" || (candidate.status === "pending" && !group.blocked));
	const latest = steps.at(-1);
	if (!paused && !step && latest?.status === "completed" && !memory.current.doneAtMount.has(latest.id)) {
		const shownAt = memory.current.shownAt.get(latest.id);
		if (shownAt === undefined || now - shownAt < LIVE_STEP_MIN_MS) step = latest;
	}
	if (step && !memory.current.shownAt.has(step.id)) memory.current.shownAt.set(step.id, now);

	// A finished step on the row moves up once its time is out.
	const holding = step && !stepInProgress(step) ? step.id : undefined;
	useEffect(() => {
		if (!holding) return;
		const shownAt = memory.current.shownAt.get(holding) ?? Date.now();
		const timer = window.setTimeout(
			() => setTick((tick) => tick + 1),
			Math.max(0, shownAt + LIVE_STEP_MIN_MS - Date.now()) + 20,
		);
		return () => window.clearTimeout(timer);
	}, [holding]);
	return step;
}

/**
 * One turn, and the memo boundary that keeps a poll from re-rendering the whole
 * conversation. A turn is the right granularity because it is what changes: while
 * the agent works, one group grows and every other group is finished history.
 */
const TurnGroup = memo(function TurnGroup({
	group,
	harness,
	activityDisclosureOverrides,
	onActivityDisclosureChange,
	sessionId,
	apiBaseUrl,
	onDecide,
	onRollback,
	onOpenFiles,
	onOpenFile,
	onOpenArtifact,
	onEditHumanMessage,
	messageEdit,
	onStartMessageEdit,
	onUpdateMessageEdit,
	onCancelMessageEdit,
	onAbandonEditRecovery,
	onSubmitMessageEdit,
	editPending,
	editSendBlocked,
	editRecoveryLabel,
	editBusy,
	editError,
	branchPoints,
	editableTurns,
	onActivateBranch,
	activateBranchPending,
	activateBranchError,
	canRollback,
	rollbackDisabled,
	retry,
	busy,
	queued,
	newHumanMessageIds,
	startup,
	onSelectAnnotation,
	workerUpdates = false,
}: {
	group: TimelineGroup;
	harness: string;
	activityDisclosureOverrides: Readonly<Record<string, boolean>>;
	onActivityDisclosureChange: (key: string, open: boolean) => void;
	sessionId: string;
	apiBaseUrl: string | null;
	onDecide: (requestId: string, decisionId: string) => void;
	onRollback: (turnId: string) => void;
	onOpenFiles?: () => void;
	onOpenFile?: (path: string, line?: number) => void;
	/** Opens one of the session's artifacts in the inspector's Artifacts tab, by its artifact path. */
	onOpenArtifact?: (target: { path: string }) => void;
	onEditHumanMessage?: ChatWorkspaceProps["onEditMessage"];
	messageEdit?: MessageEditDraft;
	onStartMessageEdit: (message: ConversationMessage) => void;
	onUpdateMessageEdit: (text: string) => void;
	onCancelMessageEdit: () => void;
	onAbandonEditRecovery?: () => void;
	onSubmitMessageEdit: (text: string) => Promise<void>;
	editPending?: boolean;
	editSendBlocked?: boolean;
	editRecoveryLabel?: string;
	editBusy?: boolean;
	editError?: string;
	branchPoints: Map<string, ConversationBranchPoint>;
	editableTurns: Set<string>;
	onActivateBranch?: (branchId: string) => Promise<unknown> | void;
	activateBranchPending?: boolean;
	activateBranchError?: string;
	/** The daemon would accept a rollback of this turn, so offer the affordance. */
	canRollback: boolean;
	/** Keep rollback mounted but inert while another turn is active. */
	rollbackDisabled: boolean;
	/** Present only when this failed turn is eligible for a new attempt. */
	retry?: TurnOutcomeRetryControl;
	busy?: boolean;
	/** This turn was recorded but not sent, so its message can say so. */
	queued: boolean;
	newHumanMessageIds: ReadonlySet<string>;
	/** The opening turn of a starting session: its setup checklist. */
	startup?: ComponentProps<typeof SessionStartup> & { label?: string };
	onSelectAnnotation?: (annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number }) => void;
	workerUpdates?: boolean;
}) {
	const hostId = useContext(ChatHostId);
	const batched = useContext(BatchedReportsContext);
	const { t } = useTranslation();
	const reducedMotion = useReducedMotion();
	const hasTerminalFailure =
		group.outcome?.state === "failed" && Boolean(group.outcome.error);
	const runs = useMemo(
		() =>
			runsOf(
				group.items.filter((item) => {
					if (item.id === group.liveProviderFailure?.id) return false;
					if (hasTerminalFailure && item.kind === "activity" && item.activityKind === "error" && item.summary === group.outcome?.error) return false;
					return !(
						hasTerminalFailure &&
						item.kind === "activity" &&
						item.detail?.event === "provider.failure" &&
						item.status === "failed"
					);
				}),
			),
		[group.items, group.liveProviderFailure, group.outcome?.error, hasTerminalFailure],
	);
	const lastAssistantId = [...group.items]
		.reverse()
		.find((item) => item.kind === "message" && item.role === "assistant" && item.text.trim() !== "")?.id;
	// A running turn offers copy only while its newest item is the reply itself, so the
	// action does not flash under prose the agent is about to follow with more work.
	const copyableMessageId = group.outcome
		? lastAssistantId
		: group.live && group.items.at(-1)?.id === lastAssistantId
			? lastAssistantId
			: undefined;
	// A turn that stopped or failed closes on its outcome, so the answer above it
	// hands its copy and undo down to the outcome's action row: one row per turn.
	// An account switch shows its own marker instead of an outcome, so its turn keeps
	// the answer's row (or the tool-only fallback) like a finished one.
	const endsWithOutcome = Boolean(group.outcome && group.outcome.state !== "completed" && !group.accountSwitched);
	const copyableMessage = endsWithOutcome && copyableMessageId
		? group.items.find(
				(item): item is ConversationMessage => item.kind === "message" && item.id === copyableMessageId,
			)
		: undefined;
	let finalAssistantRunIndex = -1;
	if (group.outcome) {
		for (let index = runs.length - 1; index >= 0; index -= 1) {
			const item = runs[index]?.items[0];
			if (item?.kind === "message" && item.role === "assistant" && item.text.trim() !== "") {
				finalAssistantRunIndex = index;
				break;
			}
		}
	}
	// Only the prompt that opened the turn sits above the Working row. A steer or a
	// later message stays where it happened among the work, so the reader sees
	// what the agent had done when it arrived.
	const firstWorkIndex = runs.findIndex((run) => !isHumanRun(run.items[0]));
	const leadingHumanCount = firstWorkIndex < 0 ? runs.length : firstWorkIndex;
	const humanRuns = runs.slice(0, leadingHumanCount);
	const workedRuns = runs.filter(
		(_run, index) => index >= leadingHumanCount && index !== finalAssistantRunIndex,
	);
	const finalRun = finalAssistantRunIndex >= 0 ? runs[finalAssistantRunIndex] : undefined;
	// Questions, errors, and system warnings stay readable after the turn settles, and
	// so does anything the user said mid-turn: folding a steer away would leave the
	// reply under the Worked row answering a message the reader can no longer see.
	const noticeRuns = group.outcome
		? workedRuns.filter((run) => isHumanRun(run.items[0]) || isNoticeRun(run))
		: [];
	const foldedRuns = workedRuns.filter((run) => !noticeRuns.includes(run) && !rendersNothing(run));
	// A worker turn the orchestrator only read has nothing to unfold; its duration alone is noise.
	const onlyRead = workerUpdates && foldedRuns.length === 0 && !finalRun && isWorkerLead(group.items[0]);
	// What the live row names: the step the agent is on right now. Items the user acts
	// on (approvals, questions) are not work, and a provider retry has its own slot.
	const currentTask = group.live
		? [...group.items]
				.reverse()
				.find(
					(item): item is ConversationActivity =>
						item.kind === "activity" &&
						item.status === "running" &&
						item.activityKind !== "plan" &&
						item.activityKind !== "approval" &&
						item.activityKind !== "user_input" &&
						item.detail?.event !== "provider.failure",
				)
		: undefined;
	// The step in progress shows once, on the live row next to the loader, which
	// opens onto its command and output; its own row joins the steps above when it
	// is done and has had its moment on the live row.
	const liveStep = useLiveRowStep(group, Boolean(startup || group.liveStartupLabel || group.liveProviderFailure));
	// The live row names a visible step while it runs; between steps it says Thinking.
	const liveTask = liveStep ?? currentTask;
	const renderRun = (run: TimelineRun) => {
		if (run.kind !== "activities") return renderSingleRun(run);
		const activities = run.items.filter(
			(item): item is ConversationActivity => item.kind === "activity" && item.id !== liveStep?.id,
		);
		if (activities.length === 0) return null;
		return (
			<ActivityRun
				key={run.key}
				disclosureKey={`${group.key}:${run.key}`}
				disclosureOverrides={activityDisclosureOverrides}
				onDisclosureChange={onActivityDisclosureChange}
				activities={activities}
			/>
		);
	};
	const renderSingleRun = (run: TimelineRun) => (
		<TimelineItem
			key={run.key}
			item={run.items[0]!}
			sessionId={sessionId}
			apiBaseUrl={apiBaseUrl}
			onDecide={onDecide}
			onEditHumanMessage={onEditHumanMessage}
			messageEdit={messageEdit}
			onStartMessageEdit={onStartMessageEdit}
			onUpdateMessageEdit={onUpdateMessageEdit}
			onCancelMessageEdit={onCancelMessageEdit}
			onAbandonEditRecovery={onAbandonEditRecovery}
			onSubmitMessageEdit={onSubmitMessageEdit}
			editPending={editPending}
			editSendBlocked={editSendBlocked}
			editRecoveryLabel={editRecoveryLabel}
			editBusy={editBusy}
			editError={editError}
			branchPoints={branchPoints}
			editableTurns={editableTurns}
			onActivateBranch={onActivateBranch}
			activateBranchPending={activateBranchPending}
			activateBranchError={activateBranchError}
			busy={busy}
			queued={queued}
			newHumanMessageIds={newHumanMessageIds}
			showCopy={!endsWithOutcome && run.items[0]?.id === copyableMessageId}
			live={group.live}
			onRollback={
				canRollback && !endsWithOutcome && run.items[0]?.id === copyableMessageId
					? () => onRollback(group.turnId as string)
					: undefined
			}
			rollbackDisabled={rollbackDisabled}
			onSelectAnnotation={onSelectAnnotation}
		/>
	);
	// One flat keyed list rather than a slot per section, so a run keeps its element
	// when the turn settles: the final answer leaving the worked runs, or a notice
	// moving out from among them, does not remount it or drop the reader's selection.
	// Orchestrator chat: whatever a worker sent (a message, a mid-turn steer, a
	// report batch, or reports folded into the user's message) draws as rows, and
	// AO's task-naming prompt as the name it produced.
	const reportsLine = (key: string, updates: readonly WorkerUpdate[]) => (
		<ReportsLine
			key={key}
			updates={updates}
			open={Boolean(activityDisclosureOverrides[key])}
			onOpenChange={(open) => onActivityDisclosureChange(key, open)}
		/>
	);
	// Set when a reports line was drawn under a message or steer, so the prompt's action row knows it is taken.
	let drewReportsUnder = false;
	const renderWorkerRun = (run: TimelineRun): ReactNode | ReactNode[] => {
		const item = run.kind === "single" ? run.items[0] : undefined;
		if (!workerUpdates || !item) return renderRun(run);
		const updates = workerUpdatesOf(item);
		// A direct send or steer is one row; a batch of reports is one line that opens to them.
		if (updates.length > 0 && updates[0]?.state === undefined) {
			const rows = <WorkerUpdateRows key={run.key} updates={updates} />;
			// Reports a worker's steer carried in travel with it, as their own line.
			const carried = batched?.under.get(item.id) ?? [];
			if (carried.length === 0) return rows;
			drewReportsUnder = true;
			return [rows, reportsLine(`${run.key}:reports`, carried)];
		}
		if (updates.length > 0) return reportsLine(run.key, updates);
		const namedTitle = isTaskTitleUpdate(item) ? renamedTitle(group.items) : undefined;
		if (namedTitle) return <TaskNamedLine key={run.key} title={namedTitle} />;
		// Reports delivered with a human message or steer are not drawn: the message stands alone.
		return renderRun(run);
	};
	const body: ReactNode[] = humanRuns.flatMap(renderWorkerRun);
	const tail = workedRuns.at(-1);
	const tailIsReply = group.live && tail?.kind === "single" && tail.items[0].kind === "message" && tail.items[0].role === "assistant";
	// The reports line under the prompt takes the prompt's action row itself.
	const reportsUnderPrompt = drewReportsUnder;
	// The live row sits at the end of the turn, under what the agent has written so
	// far, where the reader is already looking: orb, current step, timer. A provider
	// retry shows in the same row. The setup checklist shares it too, so the handoff
	// from setup to work does not move anything.
	const workingRow =
		startup || group.live ? (
			<LiveResponseStatus
				entranceKey={`live:${group.key}`}
				startedAt={group.liveStartedAt}
				startupLabel={startup ? startup.label ?? t(startup.failed ? "chat.startup.failed" : "chat.startup.running") : group.liveStartupLabel}
				failed={startup?.failed}
				// Between steps the agent is still working out what to do next, so the row
				// says "Thinking" beside the loader rather than only a timer. A turn waiting
				// on the reader's answer is not thinking, and one writing its reply is
				// writing, so both keep the timer alone.
				task={liveTask ? liveTaskLabel(liveTask) : group.live && !group.blocked && !tailIsReply ? "Thinking" : undefined}
				step={liveStep}
				reconnect={group.liveProviderFailure ? reconnectCopy(group.liveProviderFailure) : undefined}
			>
				{startup && (startup.steps.length > 0 || startup.failed) ? <SessionStartup {...startup} /> : null}
			</LiveResponseStatus>
		) : null;
	const outcome = group.outcome;
	// "Worked for" heads every settled turn with how long it ran. With work to fold it
	// is the fold, opening onto the steps; a turn that went straight to its answer has
	// nothing to open, so it is the same words without the chevron. As the turn
	// settles it fades in where the turn reserved its row, and the reply slides up
	// into the room its steps left (restoreSettle), so the work reads as collapsing
	// into its summary. A settled turn loaded from history mounts it without the fade.
	const disclosureKey = `${group.key}:worked`;
	const workedDurationMs = outcome?.durationMs !== undefined && outcome.durationMs > 0 ? outcome.durationMs : undefined;
	// Agents the turn spawned are part of its work, so they fold with its steps.
	const spawned = workerUpdates && outcome ? spawnedSessionIds(group.items) : [];
	const spawnedLine = spawned.length > 0 ? <SpawnedAgents key="spawned" sessionIds={spawned} /> : null;
	const hasFoldedWork = foldedRuns.length > 0 || spawnedLine !== null;
	const foldedWork = (): ReactNode[] => [...foldedRuns.flatMap(renderWorkerRun), ...(spawnedLine ? [spawnedLine] : [])];
	const showsWorked = Boolean(
		outcome && !onlyRead && (hasFoldedWork || workedDurationMs !== undefined),
	);
	// The prompt's hover actions (time, edit, copy) float in the row under its bubble.
	// "Worked for" fills that row; without it, an empty row of the same height keeps
	// them off the reply. A branch navigator already reserves the row in the flow.
	const promptBubble = group.items.some(
		(item) => item.kind === "message" && item.role === "user" && item.origin === "human" && !item.continuation && !relayedVia(item),
	);
	const reservesActionRow = promptBubble && !reportsUnderPrompt && !(group.turnId && onActivateBranch && branchPoints.get(group.turnId));
	// The empty row is exactly as tall as "Worked for", which takes its place as the
	// turn settles, so the handover moves nothing. Only one of the two is ever in the
	// DOM, so the turn's row gap is never counted twice.
	if (!showsWorked && reservesActionRow) {
		body.push(<div key="turn-worked-gap" aria-hidden="true" className="h-7 shrink-0" data-turn-worked-gap="" />);
	}
	body.push(
		<AnimatePresence key="turn-worked" initial={false}>
			{outcome && showsWorked ? (
				<motion.div
					key="turn-worked-summary"
					// A turn that settled while the window was hidden is already settled
					// when the reader returns; its fade would only start then.
					initial={pageHidden() ? false : { opacity: 0 }}
					animate={{ opacity: 1 }}
					transition={{ duration: reducedMotion ? 0 : 0.24, ease: [0.22, 1, 0.36, 1] }}
					data-turn-worked=""
				>
					<TurnWorkSummary
						durationMs={workedDurationMs}
						open={Boolean(activityDisclosureOverrides[disclosureKey])}
						onOpenChange={(open) => onActivityDisclosureChange(disclosureKey, open)}
					>
						{hasFoldedWork ? foldedWork() : undefined}
					</TurnWorkSummary>
				</motion.div>
			) : null}
		</AnimatePresence>,
	);
	// While the agent works, its steps draw in the conversation itself as they arrive,
	// with no scroll box of their own. They go straight into the turn's flat list, so
	// a step keeps its element as later ones land under it and an image the reader
	// opened from it stays open. Once the turn settles they fold beneath "Worked for".
	if (!outcome) body.push(...workedRuns.flatMap(renderWorkerRun));
	// Settled steers still carry the reports they delivered.
	if (outcome) body.push(...noticeRuns.flatMap(renderWorkerRun));
	if (finalRun) body.push(renderRun(finalRun));
	return (
		// Positioned, so the live row that popLayout lifts out of the flow as the turn
		// settles fades where it was rather than against some outer ancestor.
		<div className="relative flex min-w-0 flex-col gap-2.5">
			{body}
			{/* The live row comes straight after the steps, so the step it names sits
			    under the work it continues, and the plan reads below it. Once the turn
			    settles the row fades away and "Worked for" takes over at the top of the
			    turn. */}
			{/* popLayout lifts the leaving row out of the flow at once, so the action row
			    that takes its place sits where it was instead of waiting for a collapse. */}
			<AnimatePresence initial={false} mode="popLayout">
				{workingRow ? (
					<motion.div
						key="turn-working"
						exit={{ opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.12, ease: [0.22, 1, 0.36, 1] }}
						// Under a reply, sit where that reply's action row will (its 4px
						// gap, not the turn's 10px).
						className={cn("overflow-hidden", tailIsReply && "-mt-1.5")}
						// Kept in the DOM while it fades, but it never outlasts the turn.
						data-turn-live-row=""
					>
						{workingRow}
					</motion.div>
				) : null}
			</AnimatePresence>
			{/* Both of these are current state of the turn rather than steps in it, which
			    is why they sit at its end, under the live row: a checklist that ticks
			    itself off and a file list that grows both change while the reader
			    watches, and at the end of a turn neither pushes anything the reader is
			    already looking at. */}
			{group.plan ? <TurnPlan plan={group.plan} live={group.live} /> : null}
			{/* Changed-files review is a settled result, not live activity. Keep it
			    below the completed assistant response and its bottom action row. */}
			{(group.diff || group.artifacts?.length) && group.outcome?.state === "completed" && copyableMessageId ? (
				<TurnResults
					diff={group.diff}
					reportedArtifacts={group.artifacts}
					items={group.items}
					onReview={onOpenFiles}
					onOpenFile={onOpenFile}
					onOpenArtifact={onOpenArtifact}
				/>
			) : null}
			{group.live ? <TurnLiveStatus blocked={group.blocked} /> : null}
			{/* No assistant prose to hang the undo on — still offer it so a tool-only
			    turn is not stuck without a way back. A stopped or failed turn carries
			    it on its outcome's row instead. */}
			{!copyableMessageId && !group.live && !endsWithOutcome && canRollback ? (
				<TurnActions
					onRollback={() => onRollback(group.turnId as string)}
					rollbackDisabled={rollbackDisabled}
				/>
			) : null}
			{group.outcome && group.outcome.state !== "completed" && !group.accountSwitched ? (
				<TurnOutcome
					state={group.outcome.state}
					error={group.outcome.error}
					failureCode={group.outcome.failureCode}
					onConfigure={harness === "deepseek-harness" && group.outcome.failureCode === "provider_key_missing"
						? () => useUiStore.getState().openGlobalSettings("harness", { focusAgentId: harness, ...(hostId ? { hostId } : {}), harnessView: "local", preserveProject: true, startLogin: true })
						: undefined}
					usageLimit={group.outcome.usageLimit}
					retry={group.outcome.state === "failed" ? retry : undefined}
					copyText={copyableMessage?.text}
					onRollback={canRollback ? () => onRollback(group.turnId as string) : undefined}
					rollbackDisabled={rollbackDisabled}
					timestamp={copyableMessage?.createdAt}
				/>
			) : null}
		</div>
	);
});

/** The one live state that has no row of its own: a decision the reader has to make. */
function TurnLiveStatus({ blocked }: { blocked?: boolean }) {
	if (!blocked) return null;
	return (
		<span role="alert" className="sr-only">
			The agent is waiting for your decision.
		</span>
	);
}

function TimelineItem({
	item,
	sessionId,
	apiBaseUrl,
	onDecide,
	onEditHumanMessage,
	messageEdit,
	onStartMessageEdit,
	onUpdateMessageEdit,
	onCancelMessageEdit,
	onAbandonEditRecovery,
	onSubmitMessageEdit,
	editPending,
	editSendBlocked,
	editRecoveryLabel,
	editBusy,
	editError,
	branchPoints,
	editableTurns,
	onActivateBranch,
	activateBranchPending,
	activateBranchError,
	busy,
	queued,
	newHumanMessageIds,
	showCopy,
	live,
	onRollback,
	rollbackDisabled,
	onSelectAnnotation,
}: {
	item: ConversationItem;
	sessionId: string;
	apiBaseUrl: string | null;
	onDecide?: (requestId: string, decisionId: string) => void;
	onEditHumanMessage?: ChatWorkspaceProps["onEditMessage"];
	messageEdit?: MessageEditDraft;
	onStartMessageEdit: (message: ConversationMessage) => void;
	onUpdateMessageEdit: (text: string) => void;
	onCancelMessageEdit: () => void;
	onAbandonEditRecovery?: () => void;
	onSubmitMessageEdit: (text: string) => Promise<void>;
	editPending?: boolean;
	editSendBlocked?: boolean;
	editRecoveryLabel?: string;
	editBusy?: boolean;
	editError?: string;
	branchPoints: Map<string, ConversationBranchPoint>;
	editableTurns: Set<string>;
	onActivateBranch?: (branchId: string) => Promise<unknown> | void;
	activateBranchPending?: boolean;
	activateBranchError?: string;
	busy?: boolean;
	/**
	 * The enclosing turn was recorded but not yet sent, so a waiting message can say
	 * so. A group is one turn, so this holds for every item in it.
	 */
	queued?: boolean;
	newHumanMessageIds: ReadonlySet<string>;
	/** This is the final assistant response of a turn that has finished. */
	showCopy?: boolean;
	live?: boolean;
	/** Undo this finished turn from the answer that owns its copy action. */
	onRollback?: () => void;
	/** Keep the action row mounted while another turn is running. */
	rollbackDisabled?: boolean;
	onSelectAnnotation?: (annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number }) => void;
	/** This message is the live edge of its turn, rather than an earlier fragment
	 * followed by tool activity. */
}) {
	if (item.kind === "message") {
		if (item.role === "assistant") {
			return (
				<AssistantMessage
					message={item}
					showCopy={showCopy}
					live={live}
					onRollback={onRollback}
					rollbackDisabled={rollbackDisabled}
				/>
			);
		}
		// A continue after a stop reaches the agent as a message, but the user did
		// not write it: mark where the turn picked back up instead of a bubble.
		if (item.origin === "human" && item.continuation) return <ContinuedMarker />;
		// A user-role message that did not come from this human is an automation or
		// worker relay, and is attributed differently. So is one an outside tool
		// sent through the API, which AO can store as the human's.
		if (item.origin === "human" && !relayedVia(item)) {
			const editAvailable = Boolean(
				item.editAvailable && item.turnId && editableTurns.has(item.turnId) && onEditHumanMessage,
			);
			const editing = Boolean(item.turnId && messageEdit?.turnId === item.turnId);
			return (
				<HumanMessage
					message={item}
					sessionId={sessionId}
					apiBaseUrl={apiBaseUrl}
					queued={queued}
					animateIn={newHumanMessageIds.has(itemKey(item))}
					onEdit={editAvailable ? (_turnID, text) => onSubmitMessageEdit(text) : undefined}
					editing={editing}
					editText={editing ? messageEdit?.text : undefined}
					onEditStart={editAvailable ? () => onStartMessageEdit(item) : undefined}
					onEditDraftChange={onUpdateMessageEdit}
					onEditCancel={onCancelMessageEdit}
					onEditAbandonRecovery={onAbandonEditRecovery}
					editPending={editPending}
					editSendBlocked={editSendBlocked}
					editRecoveryLabel={editRecoveryLabel}
					editBusy={editBusy}
					editError={editError}
					branchPoint={item.turnId ? branchPoints.get(item.turnId) : undefined}
					onActivateBranch={onActivateBranch}
					activateBranchPending={activateBranchPending}
					activateBranchError={activateBranchError}
					onSelectAnnotation={onSelectAnnotation}
				/>
			);
		}
		return (
			<FromAgent item={item} queued={queued}>
				<AutomationMessage message={item} queued={queued} fallback={<OriginMessage message={item} />} />
			</FromAgent>
		);
	}
	if (item.activityKind === "approval") {
		// Pending approval owns the composer. Keeping it in the grouping model still
		// marks the live turn as blocked, while omitting the timeline card itself.
		if (item.status === "pending") return null;
		return <ApprovalCard activity={item} onDecide={onDecide} busy={busy} />;
	}
	if (item.activityKind === "user_input") {
		return item.status === "pending" ? null : <ElicitationHistory activity={item} />;
	}
	if (isCompaction(item)) {
		return <CompactionMarker activity={item} />;
	}
	if (item.detail?.event === "provider_switch") {
		return <ProviderSwitchMarker activity={item} />;
	}
	// Read by the event, not the kind: a steer is stored as a `system` activity
	// because that is AO's only durable write that can attach to a turn in flight,
	// but it is the user speaking and the timeline shows it that way.
	if (isSteer(item)) {
		const annotations = steeredBrowserFeedback(item);
		return (
			<FromAgent item={item}>
				{annotations ? (
					<SteeredBrowserFeedback annotations={annotations} />
				) : (
					<SteerMessage activity={item} sessionId={sessionId} apiBaseUrl={apiBaseUrl} />
				)}
			</FromAgent>
		);
	}
	// A plan no turn holds: work the agent did on its own after a turn ended, or a
	// provider turn AO never correlated. The turn-level checklist cannot show it, so
	// the row carries it. AO cannot tell when such work stops, so its step never spins.
	if (item.activityKind === "plan") {
		const plan = activityPlan(item);
		return plan ? <TurnPlan plan={plan} /> : <ActivityRow activity={item} />;
	}
	return <ActivityRow activity={item} />;
}

/* -------------------------------------------------------------------------- */
/* identity                                                                    */
/* -------------------------------------------------------------------------- */

/**
 * A human message keeps one key from the moment it is sent. The local echo carries the
 * sender's `clientMessageId` and the daemon row echoes it back, so swapping one for the
 * other keeps the same React node instead of unmounting and replaying the entrance.
 */
const itemKey = (item: ConversationItem): string =>
	item.kind === "message" && item.role === "user" && item.clientMessageId
		? `client:${item.clientMessageId}`
		: item.id;

/**
 * The daemon's "Switched" line takes the id of the optimistic one above the same message,
 * so React keeps the node and its label morphs from "Switching…" to "Switched". The line
 * can arrive before its message does; it then takes the id of the echo still showing.
 */
function withSwitchMarkerKeys(items: ConversationItem[], echoes: readonly ConversationLocalEcho[]): ConversationItem[] {
	const nextMessageId = (marker: ConversationItem) => {
		let next: ConversationMessage | undefined;
		for (const candidate of items) {
			if (candidate.kind !== "message" || candidate.role !== "user" || !candidate.clientMessageId) continue;
			if (candidate.sequence > marker.sequence && (!next || candidate.sequence < next.sequence)) next = candidate;
		}
		if (next?.clientMessageId || marker.kind !== "activity") return next?.clientMessageId;
		return echoes.find(({ providerSwitch: sent }) =>
			sent !== undefined && sent.targetHarness === marker.detail?.targetHarness && marker.sequence > sent.afterSequence,
		)?.clientMessageId;
	};
	const isMarker = (item: ConversationItem) => item.kind === "activity" && item.detail?.event === "provider_switch";
	// Only the marker nearest before a message was drawn with it (a failed switch, then another, leaves two).
	const nearest = new Map<string, number>();
	for (const item of items) {
		const id = isMarker(item) ? nextMessageId(item) : undefined;
		if (id && item.sequence > (nearest.get(id) ?? -Infinity)) nearest.set(id, item.sequence);
	}
	return items.map((item) => {
		const id = isMarker(item) ? nextMessageId(item) : undefined;
		return id && nearest.get(id) === item.sequence ? { ...item, id: `local-switch:${id}` } : item;
	});
}

/** A message sent with a switch shows the "Switched" line above it at once, until the daemon's arrives. */
function withLocalSwitchMarker(echo: ConversationLocalEcho, items: ConversationItem[], message: ConversationMessage): ConversationItem[] {
	const sent = echo.providerSwitch;
	const switchArrived = sent && items.some(
		(item) =>
			item.kind === "activity" &&
			item.detail?.event === "provider_switch" &&
			item.detail.targetHarness === sent.targetHarness &&
			item.sequence > sent.afterSequence,
	);
	if (!sent || switchArrived) return [message];
	const { afterSequence: _afterSequence, ...display } = sent;
	const marker: ConversationActivity = {
		kind: "activity",
		id: `local-switch:${echo.clientMessageId}`,
		sequence: message.sequence - 0.005,
		revision: 0,
		activityKind: "system",
		status: "completed",
		summary: "Switched agent",
		createdAt: echo.createdAt,
		detail: { event: "provider_switch", ...display, switching: true },
	};
	return [marker, message];
}

/** Whether a durable row is the one a local echo stands in for. */
function echoMatchesItem(
	echo: ConversationLocalEcho,
	item: ConversationItem,
	backgroundQueuedTurnIds: ReadonlySet<string>,
): boolean {
	if (item.kind !== "message" || item.role !== "user" || item.origin !== "human") return false;
	if (echo.opening) return true;
	// A background wake keeps its echo in the timeline while the durable turn sits queued.
	if (echo.turnId && backgroundQueuedTurnIds.has(echo.turnId) && item.turnId === echo.turnId) return false;
	if (item.clientMessageId) return item.clientMessageId === echo.clientMessageId;
	// Rows from a sender that supplied no key fall back to the turn id, then to text and time.
	if (echo.turnId) return item.turnId === echo.turnId;
	return item.text === echo.text && item.createdAt >= echo.createdAt;
}

/** Guidance the user typed into a running turn, not a steer from another AO session. */
function isHumanSteer(item: ConversationItem): item is ConversationActivity {
	return item.kind === "activity" && isSteer(item) && !item.detail?.senderSessionId &&
		(item.detail?.origin ?? "human") === "human";
}

/**
 * The messages Up and Down recall into the composer, oldest first: the human prompts
 * and steers the timeline shows as sent, then local echoes the daemon has not
 * returned yet. The daemon already leaves other branches out; rolled-back turns and
 * queued prompts, which the dock edits, are skipped here. A repeat of the message
 * before it is one stop, not two.
 */
function sentMessageHistory(
	snapshot: ConversationSnapshot,
	localEchos: readonly ConversationLocalEcho[],
	openingTurnId: string | undefined,
	backgroundQueuedTurnIds: ReadonlySet<string>,
): string[] {
	const skipped = hiddenTimelineTurnIds(snapshot);
	if (openingTurnId) skipped.delete(openingTurnId);
	for (const turn of snapshot.turns) if (turn.rolledBack) skipped.add(turn.id);
	const human = snapshot.items.filter(
		(item): item is ConversationMessage => item.kind === "message" && item.role === "user" && item.origin === "human",
	);
	const sent = [...human, ...snapshot.items.filter(isHumanSteer)]
		.filter((item) => !item.turnId || !skipped.has(item.turnId))
		.sort((a, b) => a.sequence - b.sequence);
	const texts = [
		...sent.map((item) => item.kind === "message" ? item.text : item.detail?.text ?? item.summary),
		...localEchos
			.filter((echo) => !echo.queued && !human.some((item) => echoMatchesItem(echo, item, backgroundQueuedTurnIds)))
			.map((echo) => echo.text),
	];
	return texts.reduce<string[]>((history, text) => {
		// The body the bubble shows; AO's attachment and report footers are not the user's words.
		const body = stagedAttachmentParts(humanVisibleText(text)).body;
		if (body.trim() && body !== history.at(-1)) history.push(body);
		return history;
	}, []);
}
const groupKey = (group: TimelineGroup): string => group.key;

/**
 * Whether two groups say the same thing.
 *
 * A group carries more than its items: a turn's plan and its changed files are
 * current state that the daemon overwrites, and both move while the turn's items
 * stay exactly as they were. Comparing only the items would keep the previous
 * group object — and with it the previous plan — so a checklist would stop ticking
 * until something else in the turn happened to change.
 */
function sameGroup(a: TimelineGroup, b: TimelineGroup): boolean {
	return (
		a.anchor === b.anchor &&
		a.turnId === b.turnId &&
		a.live === b.live &&
		a.liveStartupLabel === b.liveStartupLabel &&
		a.rollbackable === b.rollbackable &&
		sameContent(a.outcome, b.outcome) &&
		sameContent(a.diff, b.diff) &&
		sameContent(a.plan, b.plan) &&
		a.items.length === b.items.length &&
		// The items are already identity-stable by the time a group is compared, so a
		// reference check here is exact and avoids walking their contents twice.
		a.items.every((item, index) => item === b.items[index])
	);
}

/**
 * A steer the composer sent that the daemon has not yet recorded. `sentAfterSequence`
 * is the newest row when it was sent; `acceptedAfterSequence`, the newest when the
 * daemon accepted it, which is where the daemon's own row lands.
 */
type SteerEcho = { clientMessageId: string; text: string; createdAt: string; turnId: string; sentAfterSequence: number; acceptedAfterSequence?: number };

/** Whether this row is the daemon's record of the steer. */
function steerEchoArrived(item: ConversationItem, echo: SteerEcho): boolean {
	if (item.kind !== "activity" || item.detail?.event !== "steer") return false;
	return item.detail.clientMessageId
		? item.detail.clientMessageId === echo.clientMessageId
		: item.sequence > echo.sentAfterSequence && (item.detail.text ?? item.summary) === echo.text;
}

/**
 * A callback whose identity survives its caller re-rendering.
 *
 * `useConversationCommands` returns fresh arrows every render and the preview
 * harness passes literals, so without this every memo boundary below would be
 * invalidated by the one prop that never meaningfully changes.
 */
function useStableCallback<Args extends unknown[], Result>(
	fn: ((...args: Args) => Result) | undefined,
): (...args: Args) => Result | undefined {
	const latest = useRef(fn);
	useEffect(() => {
		latest.current = fn;
	});
	// Only ever called from an event handler, which runs after the commit that
	// updated the ref — there is no render-phase caller to read a stale closure.
	return useCallback((...args: Args) => latest.current?.(...args), []);
}

/** Retain an unchanged JSON-shaped value across snapshot refreshes. */
function useStableValue<T>(value: T): T {
	const previous = useRef(value);
	if (!sameContent(previous.current, value)) previous.current = value;
	return previous.current;
}

/** A settling turn's rows from just before the fold, and the one at the top of the view. */
type SettleAnchor = {
	turn: HTMLElement;
	rows: HTMLElement[];
	/** Each row's top on screen just before the fold, for it to slide from. */
	tops: number[];
	scrollTop: number;
	/** The reader was following the end, rather than reading back. */
	following: boolean;
	/** For a reader who scrolled up: the first row they could see, and how far down. */
	index: number;
	offset: number;
};

type TimelineGroup = {
	key: string;
	turnId?: string;
	/** Where this group sits in the timeline: the lowest sequence it contains. */
	anchor: number;
	items: ConversationItem[];
	outcome?: {
		/** `unknown`: the controller stopped while the turn ran, so nothing reported how it ended. */
		state: "completed" | "recovered" | "interrupted" | "failed" | "unknown";
		durationMs?: number;
		error?: string;
		usageLimit?: ConversationUsageLimit;
		failureCode?: "provider_key_missing";
	};
	/** What the turn changed on disk, when the daemon reported anything. */
	diff?: TurnDiff;
	/** The session artifacts the turn created or edited, whatever wrote them. */
	artifacts?: TurnArtifactChange[];
	/** The agent's plan for this turn, when it made one. */
	plan?: ConversationPlan;
	/** The turn is still running, so its diff and plan can still change. */
	live?: boolean;
	liveStartedAt?: string;
	/** Names the start in place of the timer, such as a Cloud sandbox still coming up. */
	liveStartupLabel?: string;
	blocked?: boolean;
	/** A live provider failure replaces the generic Working label until the turn settles. */
	liveProviderFailure?: ConversationActivity;
	/** The provider accepted this turn, so there is history it can be asked to drop. */
	rollbackable?: boolean;
	/** An account switch stopped this turn; its notice replaces the outcome. */
	accountSwitched?: boolean;
};

type GroupPreview = { title: string; detail?: string };

/** A turn-sized, plain-text preview for the minimap hover card. */
function groupPreview(group: TimelineGroup): GroupPreview {
	const userMessage = group.items.find(
		(item) => item.kind === "message" && item.role === "user" && item.origin === "human",
	);
	const assistantMessage = [...group.items]
		.reverse()
		.find(
			(item) => item.kind === "message" && item.role === "assistant" && item.text.trim() !== "",
		);
	const firstActivity = group.items.find(
		(item): item is ConversationActivity => item.kind === "activity",
	);
	const title = previewText(
		userMessage?.kind === "message"
			? userMessage.text
			: firstActivity?.summary || "Conversation update",
		120,
	);
	const detailSource =
		assistantMessage?.kind === "message"
			? assistantMessage.text
			: firstActivity?.detail?.text || firstActivity?.detail?.output || firstActivity?.summary;
	const detail = detailSource ? previewText(detailSource, 240) : undefined;
	return { title, detail: detail && detail !== title ? detail : undefined };
}

function groupHasHumanPrompt(group: TimelineGroup): boolean {
	return group.items.some(
		(item) => item.kind === "message" && item.role === "user" && item.origin === "human",
	);
}

function settledTurnDuration(turn: ConversationTurn): number | undefined {
	if (!turn.completedAt) return undefined;
	const startedAt = Date.parse(turn.startedAt ?? turn.requestedAt);
	const completedAt = Date.parse(turn.completedAt);
	if (!Number.isFinite(startedAt) || !Number.isFinite(completedAt)) return undefined;
	// Match the live label's start fallback and whole-second minimum so completion
	// cannot make a visible timer disappear or jump backward.
	return Math.max(1_000, completedAt - startedAt);
}

// Retry correlation is daemon-owned rather than inferred from repeated text.
// Match it against turn ids in this snapshot before consuming an affordance.
function retrySourceTurnIds(snapshot: ConversationSnapshot): Set<string> {
	const turnIds = new Set(snapshot.turns.map((turn) => turn.id));
	const sources = new Set<string>();
	for (const turn of snapshot.turns) {
		if (turn.hasRetryAttempt) sources.add(turn.id);
		const source = turn.retryOfTurnId;
		if (source && turnIds.has(source)) sources.add(source);
	}
	return sources;
}

/** How tall the trailing spacer must be to park `anchorOffset` near the top when scrolled to the end. */
export function promptSpacerHeight({
	viewportHeight,
	contentHeightWithoutSpacer,
	anchorOffset,
	topInset = 12,
}: {
	viewportHeight: number;
	contentHeightWithoutSpacer: number;
	anchorOffset: number;
	topInset?: number;
}): number {
	const afterAnchor = Math.max(0, contentHeightWithoutSpacer - anchorOffset);
	return Math.max(0, Math.round(viewportHeight - topInset - afterAnchor));
}

/**
 * Leave a band of prior chat above the latest prompt (Cursor-style), not flush to
 * the top edge. Scales with the timeline viewport, with a floor so short panes
 * still keep a peek of history.
 */
export function promptTopInset(viewportHeight: number): number {
	return Math.max(80, Math.round(viewportHeight * 0.2));
}

function previewText(value: string, limit: number): string {
	const plain = value
		.replace(/```[\s\S]*?```/g, " code sample ")
		.replace(/\[([^\]]+)]\([^)]*\)/g, "$1")
		.replace(/[*_`#>~]+/g, " ")
		.replace(/\s+/g, " ")
		.trim();
	return plain.length > limit ? `${plain.slice(0, limit - 1).trimEnd()}…` : plain;
}

/** Sessions whose header already slid in on arrival from the new chat page. */
const headerSlidSessions = new Set<string>();

/** The first turn has an outcome, so the opening brief no longer reads as pending. */
function firstTurnSettled(snapshot: ConversationSnapshot): boolean {
	const first = snapshot.turns[0];
	return Boolean(first && first.state !== "queued" && first.state !== "running");
}

/**
 * Whether the opening brief has stopped reading as working. A running first turn
 * under a really stopped controller is settled too (#6064): its group shows the
 * unknown outcome, so the opening must not dress it as live again. Only a running
 * one: a queued first turn under a stopped controller can still be a chat that is
 * provisioning, whose brief rightly reads as setting up.
 */
function openingTurnSettled(snapshot: ConversationSnapshot, controllerStopped: boolean): boolean {
	return firstTurnSettled(snapshot) || (controllerStopped && snapshot.turns[0]?.state === "running");
}

/**
 * Group items by the turn that produced them, so a completed turn can be closed
 * off with its outcome.
 *
 * A turn is one block, even when its items are not contiguous in sequence. That
 * matters because of the send queue: a message typed mid-turn is recorded
 * immediately, so its sequence lands before the answers to everything ahead of
 * it. Reading strictly by sequence would stack every queued question at the top
 * and every answer below, separating each question from its own reply.
 *
 * Sequence still decides order — a turn takes the position of its first item, and
 * items inside a turn stay in sequence order. Nothing is re-derived: this is the
 * daemon's ordering, grouped.
 *
 * Items with no turn (an automation relay that arrived between turns) form their
 * own group and keep their sequence position.
 *
 * With the controller really stopped, a turn the daemon still holds as running is
 * not live: nothing is working on it. It settles with an unknown outcome instead
 * of a Working row that never ends (#6064). It offers no rollback, since the
 * daemon refuses to roll back a turn it still holds open; the stopped banner's
 * Resume and Open shell are the way on.
 */
function groupByTurn(snapshot: ConversationSnapshot, { controllerStopped = false }: { controllerStopped?: boolean } = {}): TimelineGroup[] {
	const byTurn = new Map(snapshot.turns.map((turn) => [turn.id, turn]));
	const groups: TimelineGroup[] = [];
	const groupForTurn = new Map<string, TimelineGroup>();

	for (const item of snapshot.items) {
		const turnId = carriedInto.get(item) ?? item.turnId;
		if (turnId === undefined) {
			// Consecutive turn-less items share one group rather than getting one each.
			// A provider can run a turn AO never dispatched — a compaction, or a turn
			// resumed inside the provider's own history — and every item it emits then
			// correlates to no AO turn. One group per item made `runsOf` see no two
			// adjacent activities, so a wall of tool calls stopped collapsing and the
			// conversation turned back into a log. Grouping must not depend on
			// correlation succeeding.
			const last = groups.at(-1);
			if (last && last.turnId === undefined) {
				last.items.push(item);
				continue;
			}
			groups.push({
				// A "Switched" line keeps its key from echo to the daemon's row, as its position changes.
				key: item.id.startsWith("local-switch:") ? item.id : `loose-${item.sequence}`,
				anchor: item.sequence,
				items: [item],
			});
			continue;
		}
		const existing = groupForTurn.get(turnId);
		if (existing) {
			existing.items.push(item);
			continue;
		}
		const group: TimelineGroup = {
			// A turn opened by a keyed human message keeps the key its echo had.
			key: itemKey(item) === item.id ? byTurn.get(turnId)?.groupKey ?? `${turnId}-${item.sequence}` : itemKey(item),
			turnId,
			// A turn that began above the loaded history sorts where it began.
			anchor: byTurn.get(turnId)?.anchorSequence ?? item.sequence,
			items: [item],
		};
		groupForTurn.set(turnId, group);
		groups.push(group);
	}

	groups.sort((a, b) => a.anchor - b.anchor);

	for (const group of groups) {
		if (!group.turnId) continue;
		const turn = byTurn.get(group.turnId);
		if (!turn) continue;
		// The diff and the plan are attached whether or not the turn has finished: a
		// running turn's changed-file list growing, and its checklist ticking itself
		// off, are the useful parts.
		group.diff = turn.diff;
		group.artifacts = turn.artifacts;
		// A later turn or run carries a newer copy of a list that spans turns.
		group.plan = turn.plan?.steps.length && !turn.plan.superseded ? turn.plan : undefined;
		const orphaned = controllerStopped && turn.state === "running";
		group.live = turn.state === "running" && !orphaned;
		group.liveStartedAt = turn.startedAt ?? turn.requestedAt;
		group.blocked = group.items.some(
			(item) =>
				item.kind === "activity" &&
				(item.activityKind === "approval" || item.activityKind === "user_input") &&
				item.status === "pending",
		);
		group.liveProviderFailure = [...group.items]
			.reverse()
			.find(
				(item): item is ConversationActivity =>
					item.kind === "activity" &&
					item.status === "running" &&
					item.detail?.event === "provider.failure",
			);
		if (orphaned) {
			group.liveProviderFailure = undefined;
			group.outcome = { state: "unknown" };
			continue;
		}
		if (turn.state === "running" || turn.state === "queued" || turn.state === "cancelled") continue;
		group.rollbackable = Boolean(turn.providerTurnId);
		group.accountSwitched = group.items.some((item) => item.kind === "activity" && item.detail?.event === "account.switched");
		group.outcome = {
			state: turn.state,
			usageLimit: turn.usageLimit,
			durationMs: settledTurnDuration(turn),
			error: turn.errorMessage || (turn.state === "failed"
				? [...group.items].reverse().find(
					(item): item is ConversationActivity => item.kind === "activity" && item.activityKind === "error",
				)?.summary
				: undefined),
			failureCode: turn.failureCode,
		};
	}

	return groups.flatMap(splitAtSteers);
}

/**
 * A steer is the user speaking, so it opens a new visual turn even though the
 * provider keeps running the same one: everything before it settles into its own
 * "Worked for" row, and the steer starts a fresh Working row and reply. Only the
 * last segment carries the live state, outcome, diff and plan of the real turn.
 */
function splitAtSteers(group: TimelineGroup): TimelineGroup[] {
	const starts = group.items.flatMap((item, index) =>
		index > 0 && item.kind === "activity" && isSteer(item) && !item.detail?.senderSessionId ? [index] : [],
	);
	if (starts.length === 0) return [group];
	const bounds = [0, ...starts, group.items.length];
	const segmentStarts = bounds.slice(0, -1).map((from, index) =>
		index === 0 ? Date.parse(group.liveStartedAt ?? "") : Date.parse(group.items[from]!.createdAt),
	);
	const lastIndex = starts.length;
	return starts.concat(group.items.length).map((_end, index) => {
		const items = group.items.slice(bounds[index], bounds[index + 1]);
		const first = items[0]!;
		const key = index === 0 ? group.key : `${group.key}:steer:${first.id}`;
		const startedAt = segmentStarts[index]!;
		if (index === lastIndex) {
			const priorMs = Number.isFinite(startedAt) ? startedAt - segmentStarts[0]! : 0;
			return {
				...group,
				key,
				anchor: first.sequence,
				items,
				liveStartedAt: Number.isFinite(startedAt) ? new Date(startedAt).toISOString() : group.liveStartedAt,
				outcome: group.outcome
					? {
						...group.outcome,
						durationMs:
							group.outcome.durationMs === undefined || !Number.isFinite(priorMs)
								? group.outcome.durationMs
								: Math.max(1_000, group.outcome.durationMs - priorMs),
					}
					: undefined,
			};
		}
		const endedAt = segmentStarts[index + 1]!;
		return {
			key,
			turnId: group.turnId,
			anchor: first.sequence,
			items,
			outcome: {
				state: "completed" as const,
				durationMs:
					Number.isFinite(startedAt) && Number.isFinite(endedAt)
						? Math.max(1_000, endedAt - startedAt)
						: undefined,
			},
		};
	});
}

function isHumanRun(item: ConversationItem | undefined): boolean {
	return (item?.kind === "message" && item.role === "user") || (item?.kind === "activity" && isSteer(item));
}

/** Conversation records that stay readable outside a settled turn's folded work. */
function isNoticeRun(run: TimelineRun): boolean {
	return run.items.some(isNoticeItem);
}

function isNoticeItem(item: ConversationItem | undefined): boolean {
	if (item?.kind !== "activity") return false;
	if (item.activityKind === "error") return true;
	// A run holding a failed call stays out of the fold, so the failure shows under it.
	if (isOrdinaryActivity(item) && isFailedCall(item)) return true;
	if (item.activityKind === "user_input") return item.status !== "pending";
	return item.detail?.event !== undefined && !isSteer(item) && !isCompaction(item) && item.activityKind !== "plan";
}

const NO_BATCHED_REPORTS: BatchedReports = { under: new Map(), moved: new Set() };

/**
 * What a turn reads of the timeline-wide flags, so a send or steer redraws only
 * the turns it changes. A turn uses the new-message ids only for its own human
 * messages' entrance, `editBusy` only in the message being edited, and `busy`
 * only on an approval card; other turns get the same value every time and keep
 * their memoized render.
 */
const NO_NEW_HUMAN_MESSAGES: ReadonlySet<string> = new Set();
function turnNewHumanMessageIds(group: TimelineGroup, ids: ReadonlySet<string>): ReadonlySet<string> {
	return ids.size > 0 && group.items.some((item) => ids.has(itemKey(item))) ? ids : NO_NEW_HUMAN_MESSAGES;
}
function turnEditing(group: TimelineGroup, edit: MessageEditDraft | undefined): boolean {
	return Boolean(edit && group.items.some((item) => item.kind === "message" && item.turnId === edit.turnId));
}
function turnShowsApproval(group: TimelineGroup): boolean {
	return group.items.some((item) => item.kind === "activity" && item.activityKind === "approval");
}

const detachedActions = new WeakMap<ConversationItem, ConversationItem>();

/** An AO action filed under a turn the timeline drops, as a turn-less row; anything else unchanged. */
function outlivingAction(item: ConversationItem, dropped: ReadonlySet<string>): ConversationItem {
	if (item.kind !== "activity" || item.activityKind !== "ao_action" || !item.turnId || !dropped.has(item.turnId)) return item;
	let detached = detachedActions.get(item);
	if (!detached) {
		detached = { ...item, turnId: undefined };
		detachedActions.set(item, detached);
	}
	return detached;
}

const carriedRows = new WeakMap<ConversationItem, ConversationItem>();
const carriedInto = new WeakMap<ConversationItem, string>();

function atCarryingSteer(items: ConversationItem[], deliveries: readonly BatchedDelivery[], moved: ReadonlySet<string>): ConversationItem[] {
	if (deliveries.length === 0) return items;
	const steers = new Map(items.flatMap((item) => (item.kind === "activity" && item.turnId && isSteer(item) ? [[item.id, item] as const] : [])));
	const carriers = new Map(deliveries.flatMap((delivery) => {
		const steer = delivery.steerActivityId ? steers.get(delivery.steerActivityId) : undefined;
		return steer && !moved.has(delivery.turnId) ? [[delivery.turnId, steer] as const] : [];
	}));
	if (carriers.size === 0) return items;
	const sentAt = (turnId: string) => {
		const index = carriers.get(turnId)?.detail?.providerInputSourceTurnIds?.indexOf(turnId) ?? -1;
		return index < 0 ? Number.MAX_SAFE_INTEGER : index;
	};
	const carried = items.filter((item) => item.turnId && carriers.has(item.turnId));
	const blocks = new Map<ConversationItem, ConversationItem[]>();
	for (const item of carried.sort((a, b) => sentAt(a.turnId!) - sentAt(b.turnId!) || a.sequence - b.sequence)) {
		const steer = carriers.get(item.turnId!)!;
		const block = blocks.get(steer);
		if (block) block.push(item);
		else blocks.set(steer, [item]);
	}
	const isCarried = new Set(carried);
	const place = new Map([...blocks].flatMap(([steer, block]) => {
		const next = items.reduce((min, item) => (!isCarried.has(item) && item.sequence > steer.sequence && item.sequence < min ? item.sequence : min), steer.sequence + 1);
		return block.map((item, index) => [item, steer.sequence + ((next - steer.sequence) * (index + 1)) / (block.length + 1)] as const);
	}));
	return items
		.map((item) => {
			const steer = item.turnId ? carriers.get(item.turnId) : undefined;
			const sequence = place.get(item);
			if (!steer || sequence === undefined) return item;
			const cached = carriedRows.get(item);
			if (cached && carriedInto.get(cached) === steer.turnId && cached.sequence === sequence) return cached;
			const row = { ...item, sequence };
			carriedRows.set(item, row);
			carriedInto.set(row, steer.turnId!);
			return row;
		})
		.sort((a, b) => a.sequence - b.sequence);
}

/** Mirrors the items `TimelineItem` draws nothing for, so they cannot fill an empty accordion. */
function rendersNothing(run: TimelineRun): boolean {
	const item = run.items[0];
	if (run.kind !== "single" || !item) return false;
	if (item.kind === "message") return item.role === "assistant" && item.text.trim() === "";
	return (item.activityKind === "user_input" || item.activityKind === "approval") && item.status === "pending";
}

// Each chip names its request in a few words; picking it fills in the whole request.
const ARTIFACT_PROMPTS = [
	{ prompt: "chat.welcome.artifactPrompt.plan", chip: "chat.welcome.chip.plan", icon: ListTodo },
	{ prompt: "chat.welcome.artifactPrompt.compare", chip: "chat.welcome.chip.compare", icon: Columns2 },
	{ prompt: "chat.welcome.artifactPrompt.notes", chip: "chat.welcome.chip.notes", icon: NotebookPen },
	{ prompt: "chat.welcome.artifactPrompt.slides", chip: "chat.welcome.chip.slides", icon: Presentation },
] as const;

const CHAT_PROMPTS = [
	{ prompt: "chat.welcome.prompt.review", chip: "chat.welcome.chip.review", icon: ScanSearch },
	{ prompt: "chat.welcome.prompt.tests", chip: "chat.welcome.chip.tests", icon: FlaskConical },
	{ prompt: "chat.welcome.prompt.trace", chip: "chat.welcome.chip.trace", icon: Route },
	{ prompt: "chat.welcome.prompt.simplify", chip: "chat.welcome.chip.simplify", icon: WandSparkles },
] as const;

/**
 * Sample requests above an empty chat's composer; picking one fills the composer.
 * The list collapses its height, and the gap below it, on the way in and out so the
 * centered composer glides instead of jumping.
 */
function WelcomePromptSuggestions({ open, artifact, unavailable = false, onPick }: { open: boolean; artifact: boolean; unavailable?: boolean; onPick: (text: string) => void }) {
	const { t } = useTranslation();
	const reducedMotion = useReducedMotion();
	const prompts = artifact ? ARTIFACT_PROMPTS : CHAT_PROMPTS;
	return (
		<AnimatePresence initial={false}>
			{open ? (
				<motion.div
					key="welcome-prompts"
					initial={{ height: 0, opacity: 0 }}
					animate={{ height: "auto", opacity: 1, transition: { duration: reducedMotion ? 0 : 0.2, ease: [0.22, 1, 0.36, 1] } }}
					// The chips fade first, then the row folds away with a soft ease in and out,
					// so the composer below settles instead of being yanked up.
					exit={{
						height: 0,
						opacity: 0,
						transition: reducedMotion ? { duration: 0 } : {
							height: { duration: 0.32, ease: [0.65, 0, 0.35, 1] },
							opacity: { duration: 0.14, ease: "easeOut" },
						},
					}}
					// The dock's gap-2 sits outside this box. Cancel it and put the space back as
					// padding inside, so it collapses with the height instead of snapping.
					className="-mb-2 overflow-hidden"
				>
					<ul aria-label={t(artifact ? "chat.welcome.artifactPrompts" : "chat.welcome.prompts")} className="flex flex-wrap justify-center gap-2 pb-4">
						{prompts.map(({ prompt, chip, icon: Icon }) => (
							<li key={prompt}>
								<Button
									type="button"
									variant="quiet"
									shape="pill"
									title={t(prompt)}
									className="welcome-prompt-suggestion border-border"
									disabled={unavailable}
									onClick={() => onPick(t(prompt))}
								>
									<Icon aria-hidden="true" className="size-icon-sm shrink-0" />
									<span className="whitespace-nowrap">{t(chip)}</span>
								</Button>
							</li>
						))}
					</ul>
				</motion.div>
			) : null}
		</AnimatePresence>
	);
}
