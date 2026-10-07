import { useChatDraftTranslation } from "../../lib/chat-draft-messages";
import { useLatestCallback, useStableSet } from "../../hooks/useStable";
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
	useCallback,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	useSyncExternalStore,
	type ComponentProps,
	type CSSProperties,
	type KeyboardEvent as ReactKeyboardEvent,
	type MouseEvent as ReactMouseEvent,
	type PointerEvent as ReactPointerEvent,
	type ReactNode,
	type WheelEvent as ReactWheelEvent,
} from "react";
import { ArrowDown, ChevronRight, Loader2, TriangleAlert, Undo2 } from "lucide-react";
import { Reorder, useDragControls } from "motion/react";
import { useTranslation } from "react-i18next";
import { useScrollFollow } from "../../hooks/useScrollFollow";
import {
	defaultRangeExtractor,
	measureElement,
	observeElementRect,
	useVirtualizer,
} from "@tanstack/react-virtual";
import { cn } from "../../lib/utils";
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
	writeChatQueuedEdit,
	type ChatDraftQueuedEdit,
	type ChatDraftAttachment,
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
	type SessionKind,
	type WorkspaceSession,
	type SessionProvisionStep,
} from "../../types/workspace";
import { AgentAvatar } from "../AgentAvatar";
import { SessionPaneTab } from "../CenterPane";
import { Button } from "../ui/button";
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "../ui/accordion";
import { ConfirmDialog } from "../ConfirmDialog";
import { SessionTopbarPortal } from "../SessionTopbarPortal";
import { ShellTerminalTab } from "../ShellTerminalTab";
import { TerminalPane } from "../TerminalPane";
import {
	ActivityRow,
	ApprovalCard,
	AssistantMessage,
	CompactionMarker,
	HumanMessage,
	OriginMessage,
	SteerMessage,
	TurnChangedFiles,
	TurnDuration,
	TurnOutcome,
	LiveResponseStatus,
	type TurnOutcomeRetryControl,
} from "./ChatTimelineItems";
import { HumanMessageEditor } from "./HumanMessageEditor";
import { ChatLinkProvider } from "./ChatMarkdown";
import { ChatImageSourceProvider } from "./chat-image-source";
import { ChatComposer, type ChatComposerHandle, type StoredComposerAttachment } from "./ChatComposer";
import { ContextMeter } from "./ContextMeter";
import { stagedAttachmentParts, attachmentName } from "./messageAttachments";
import type { QueuedMessageEditOptions } from "../../types/conversation";
import { QueuedMessageDock, type QueuedMessage } from "./QueuedMessageDock";
import { SessionStartup } from "./SessionStartup";
import { ActivityRun } from "./ActivityRun";
import { TurnPlan } from "./TurnPlan";
import { TurnSettingsBar } from "./TurnSettingsBar";
import { ElicitationDock } from "./ElicitationDock";
import { McpServerBanner, ReauthBanner, ThreadStateBanner } from "./ChatStatusBanners";
import {
	activeTurn,
	activityPlan,
	brokenMcpServers,
	can,
	isCompaction,
	isSteer,
	hiddenTimelineTurnIds,
	queuedTurnIds,
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
	type TurnDiff,
	type TurnSettings,
} from "../../types/conversation";

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

export interface ChatRetryControl {
	retry: (turnId: string) => void | Promise<unknown>;
	pending?: boolean;
	error?: string;
	turnId?: string;
}

type ReviewerTerminalTarget = Extract<TerminalTarget, { kind: "reviewer" }>;
type ShellTerminalTarget = Extract<TerminalTarget, { kind: "shell" }>;
type WorkspaceTab = { key: string; content: ReactNode; onSelect: () => void; onClose?: () => void };
type ChatAuxiliaryTab =
	| { key: string; kind: "reviewer"; terminal: { handleId: string; harness: string } }
	| { key: string; kind: "reviewer-chat"; terminal: { reviewId: string; harness: string } }
	| { key: string; kind: "shell"; terminal: ShellTerminal }
	| { key: string; kind: "workspace"; tab: WorkspaceTab };

function DraggableChatTab({ children, value }: { children: ReactNode; value: string }) {
	const dragControls = useDragControls();
	return (
		<Reorder.Item
			as="div"
			className="flex shrink-0 self-stretch touch-pan-y"
			data-terminal-tab-key={value}
			drag="x"
			dragControls={dragControls}
			dragListener={false}
			onPointerDown={(event: ReactPointerEvent<HTMLDivElement>) => {
				if ((event.target as HTMLElement).closest("[data-terminal-tab-action],input,a")) return;
				dragControls.start(event);
			}}
			value={value}
		>
			{children}
		</Reorder.Item>
	);
}

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
				.filter(
					(item): item is ConversationMessage =>
						item.kind === "message" &&
						item.role === "user" &&
						item.origin === "human" &&
						Boolean(item.turnId),
				)
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
	snapshot: ConversationSnapshot;
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
	sessionTabActionWide?: boolean;
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
	/**
	 * A stopped agent is being resumed after the chat opened. Unlike a mode
	 * handoff, the history is final, so it stays readable; only sending waits.
	 */
	agentResuming?: boolean;
	startingSteps?: readonly SessionProvisionStep[];
	settingsReady?: boolean;
	/** Freeze agent-owned Chat controls while a durable session mutation owns input. */
	agentInputDisabled?: boolean;
	/** Fence new agent work without blocking decisions required by the current turn. */
	newWorkDisabled?: boolean;
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
	onSend?: (
		text: string,
		attachments?: { mimeType: string; data: string }[],
		clientMessageId?: string,
	) => void | Promise<unknown>;
	onDecide?: (requestId: string, decisionId: string) => void;
	onResolveInput?: (
		requestId: string,
		action: "accept" | "decline" | "cancel",
		content?: Record<string, unknown>,
	) => Promise<unknown> | void;
	onInterrupt?: () => void;
	commandError?: string;
	onResumeAgent?: () => void | Promise<unknown>;
	resumingAgent?: boolean;
	resumeError?: string;
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
	onChooseSettings?: (settings: TurnSettings) => void;
	onRememberPermissions?: (mode: ApprovalMode) => Promise<unknown> | void;
	showApprovalMode?: boolean;
	approvalModes?: ApprovalMode[];
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
	/** The path list was capped by the daemon rather than being all of them. */
	filePathsTruncated?: boolean;
	/** Renderer-only human messages awaiting their exact durable counterpart. */
	localEchos?: ConversationLocalEcho[];
	/**
	 * Writes staged images into the worktree and answers with the paths the agent
	 * can open. Absent means no attach control is offered — the fixture preview has
	 * no worktree to write into.
	 */
	onStageAttachments?: (attachments: { mimeType: string; data: string }[]) => Promise<string[]>;
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
	snapshot,
	sessionTitle,
	sessionRole = "worker",
	assetBaseUrl,
	remoteHostId,
	headerActions,
	sessionTabAction,
	sessionTabActionWide = false,
	tabStripAction,
	workspaceTabs,
	workspaceTabActions,
	workspaceActiveTabKey,
	auxiliaryTabOrder,
	onAuxiliaryTabOrderChange,
	controllerTransitioning,
	agentResuming = false,
	startingSteps,
	settingsReady = true,
	agentInputDisabled = false,
	newWorkDisabled = false,
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
	onSend,
	onDecide,
	onResolveInput,
	onInterrupt,
	commandError,
	onResumeAgent,
	resumingAgent,
	resumeError,
	onOpenShell,
	openingShell,
	shellError,
	onLinkOpen,
	onSessionLinkOpen,
	busy,
	models,
	onChooseSettings,
	onRememberPermissions,
	showApprovalMode,
	approvalModes,
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
	localEchos,
	onStageAttachments,
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
	const remoteCreateMux = useMemo(
		() => assetBaseUrl ? () => createTerminalMux(muxUrlFromApiBase(assetBaseUrl)) : undefined,
		[assetBaseUrl],
	);
	const turn = activeTurn(snapshot);
	// The primary Chat view wakes a sleeping provider in the background. Its
	// marker clears before the new controller is ready, so an intermediate
	// "stopped" snapshot is still part of that wake, not a crashed agent.
	const wakingFromHibernate = useRef(snapshot.controller.state === "hibernated");
	if (snapshot.controller.state === "hibernated") wakingFromHibernate.current = true;
	if (snapshot.controller.state === "ready" || snapshot.controller.state === "busy") wakingFromHibernate.current = false;
	if (snapshot.controller.state === "stopped" && !resumingAgent) wakingFromHibernate.current = false;
	const suppressStopped = wakingFromHibernate.current && snapshot.controller.state === "stopped";
	const startupState =
		session?.provisionState === "provisioning" || session?.provisionState === "failed"
			? session.provisionState
			: undefined;
	// While a session starts, its opening brief reads as sent: the setup checklist
	// under it explains why nothing has answered yet. Later messages still queue.
	const provisionSteps = session?.provisionSteps ?? startingSteps;
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
	const sessionBranch = session?.branch;
	const startup = useMemo(
		() =>
			hasStartup
				? {
						failed: startupState === "failed",
						steps: provisionSteps ?? [],
						error: provisionError,
						agentName: agentLabel(snapshot.harness),
						branch: sessionBranch,
						openingTurnId,
						onRetry: newWorkDisabled ? undefined : onResumeAgent,
						retrying: resumingAgent,
						retryError: resumeError,
					}
				: undefined,
		[
			hasStartup,
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
		],
		[reviewerChat, reviewerTerminal, shellTerminals, workspaceTabs],
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
			...queuedMessages.filter((message) => !backgroundWakeQueuedTurnIds.has(message.turnId)),
			...pendingQueuedMessages,
		],
		[backgroundWakeQueuedTurnIds, pendingQueuedMessages, queuedMessages],
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
		async (text: string, attachments?: Parameters<NonNullable<typeof onSend>>[1], clientMessageId?: string, retainedContent?: number[]) => {
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
			return onSend?.(text, attachments, clientMessageId);
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
		async (turnId: string) => stablePromoteQueuedTurn(turnId),
		[stablePromoteQueuedTurn],
	);
	const steer = useCallback(
		async (
			text: string,
			attachments?: { mimeType: string; data: string }[],
			clientMessageId?: string,
			recoverOnly?: boolean,
		) => stableSteer(text, attachments, clientMessageId, recoverOnly),
		[stableSteer],
	);
	const acceptedClientMessageIds = useMemo(
		() =>
			new Set(
				snapshot.items.flatMap((item) => {
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
			aoBridge.app.onCloseShellTerminalShortcut(() => {
				if (activeWorkspaceTab?.onClose) activeWorkspaceTab.onClose();
				else if (shellTarget) onCloseShellTerminal?.(shellTarget.handleId);
			}),
		[activeWorkspaceTab, onCloseShellTerminal, shellTarget],
	);

	useEffect(() => {
		const disposePrevious = aoBridge.app.onPreviousTabShortcut(() => selectAdjacentTab(-1));
		const disposeNext = aoBridge.app.onNextTabShortcut(() => selectAdjacentTab(1));
		return () => {
			disposePrevious();
			disposeNext();
		};
	}, [selectAdjacentTab]);

	useEffect(() => {
		aoBridge.app.setCloseShellTerminalShortcutEnabled(
			Boolean(activeWorkspaceTab?.onClose) || Boolean(shellTarget && onCloseShellTerminal),
		);
		return () => aoBridge.app.setCloseShellTerminalShortcutEnabled(false);
	}, [activeWorkspaceTab, onCloseShellTerminal, shellTarget]);

	// Keep the rollback affordance mounted while a new turn runs; disable it until
	// the daemon can safely accept it so the action row never shifts.
	const rollbackTarget =
		onRollback && !newWorkDisabled && snapshot.controller.state !== "hibernated"
			? (id: string) => setConfirming(id)
			: undefined;
	const discarded = snapshot.turns.filter((t) => t.rolledBack).length;

	const brokenServers = useMemo(() => brokenMcpServers(snapshot), [snapshot]);
	const reauthErrorInChat = snapshot.turns.some(
		(entry) => entry.state === "failed" && Boolean(entry.errorMessage?.trim()) &&
			entry.errorMessage?.trim() === snapshot.account?.reauthReason?.trim(),
	) || snapshot.items.some(
		(item) => item.kind === "activity" && item.activityKind === "error" &&
			Boolean(item.summary.trim()) && item.summary.trim() === snapshot.account?.reauthReason?.trim(),
	);
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
	const stableModelReroute = useStableValue(snapshot.modelReroute);
	const stablePendingApproval = useStableValue(pendingApproval);
	const stablePendingUserInput = useStableValue(pendingUserInput);
	const composerSettings = useMemo(
		() =>
			settingsReady && (onChooseSettings || onChooseConfigOption) ? (
				<TurnSettingsBar
					models={models ?? []}
					settings={stableSettings}
					onRememberPermissions={onRememberPermissions}
					rememberPermissionsPending={rememberPermissionsPending}
					rememberPermissionsError={rememberPermissionsError}
					rememberedPermissionMode={rememberedPermissionMode}
					harness={snapshot.harness}
					showApprovalMode={showApprovalMode ?? !session?.cloud}
					approvalModes={approvalModes}
					reroute={stableModelReroute}
					onChange={newWorkDisabled ? undefined : onChooseSettings}
					configOptions={configOptions ?? []}
					onChangeConfigOption={newWorkDisabled ? undefined : onChooseConfigOption}
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
							session?.provisionState === "provisioning" ||
							controllerTransitioning || configOptionPending || newWorkDisabled
						}
				/>
			) : null,
		[
			settingsReady,
			configOptionError,
			configOptionPending,
			configOptions,
			controllerTransitioning,
			models,
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
			session?.cloud,
			snapshot.controller.state,
			snapshot.items.length,
			turn,
			stableModelReroute,
			stableSettings,
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
				<ElicitationDock activity={stablePendingUserInput} onResolve={onResolveInput} />
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
					onReorderQueuedTurns={newWorkDisabled ? undefined : onReorderQueuedTurns}
					promotePendingTurnId={promoteQueuedTurnPendingTurnId}
					cancelPendingTurnId={cancelQueuedTurnPendingTurnId}
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
			promoteQueuedTurn,
			promoteQueuedTurnPendingTurnId,
			queueEdit?.clientMessageId,
			queueEdit?.turnId,
			visibleQueuedMessages,
		],
	);
	const composerDraftSeed = useMemo(
		() => (queueEdit ? { id: `${queueEdit.turnId}:${queueEdit.ownerId ?? queueEdit.expectedRevision ?? "legacy"}`, text: queueEdit.text, attachments: queueEdit.attachments, stagedAttachments: queueEdit.stagedAttachments } : undefined),
		[queueEdit],
	);
	// Empty chats center the prompt; once a turn or item exists the composer docks
	// at the bottom and stays there for the rest of the session.
	const orchestratorStarting = sessionRole === "orchestrator" && startupState !== "failed" && (
		startupState === "provisioning" || agentResuming ||
		snapshot.controller.state === "connecting" || snapshot.controller.state === "recovering"
	);
	const currentSetupStep = provisionSteps?.find((step) => step.status === "running")?.id;
	const setupPlaceholder = currentSetupStep === "fetch" ? "Getting the latest code"
		: currentSetupStep === "worktree" ? "Preparing your workspace"
		: currentSetupStep === "setup" ? "Running your project setup"
		: currentSetupStep === "agent" ? "Starting your orchestrator"
		: provisionSteps?.length && provisionSteps.every((step) => step.status === "done")
			? "Connecting to your orchestrator"
			: "Getting your project ready";
	const compactStartup = sessionRole === "orchestrator" && startupState === "failed" ? startup : undefined;
	const conversationEmpty = snapshot.items.length === 0 && !turn && (localEchos?.length ?? 0) === 0 && (!hasStartup || sessionRole === "orchestrator");
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
			composerCenteredTopRef.current = composerTop();
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
		<section
			ref={surfaceRef}
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
				sessionTabActionWide={sessionTabActionWide}
				tabStripAction={tabStripAction}
				workspaceTabActions={workspaceTabActions}
				workspaceActiveTabKey={workspaceActiveTabKey}
				orderedAuxiliaryTabs={orderedAuxiliaryTabs}
				onReorderAuxiliaryTabs={reorderAuxiliaryTabs}
				inline={isFullscreen || inlineHeader}
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
						reviewerActive || shellActive || agentInputDisabled || controllerTransitioning
							? true
							: undefined
					}
					role="tabpanel"
				>
					{/* Keep sign-in guidance available without repeating the error from chat. */}
					{snapshot.account ? (
						<ReauthBanner key={`${snapshot.sessionId}:${snapshot.conversationId}`} account={snapshot.account} harness={snapshot.harness} reasonInTimeline={reauthErrorInChat} />
					) : null}
					{!draftPersistenceAvailable ? (
						<div role="status" className="flex shrink-0 items-center gap-2 border-b border-border bg-surface px-4 py-2 text-xs text-muted-foreground">
							<TriangleAlert aria-hidden="true" className="size-3.5 shrink-0 text-warning" />
							{t("chat.draft.storageUnavailable")}
						</div>
					) : null}
					<ControllerBanner
						controller={snapshot.controller}
						provisionState={orchestratorStarting ? "provisioning" : session?.provisionState}
						transitioning={controllerTransitioning || agentResuming}
						automaticWakePending={suppressStopped}
						onResume={newWorkDisabled ? undefined : onResumeAgent}
						resuming={resumingAgent}
						resumeError={resumeError}
						onOpenShell={onOpenShell}
						openingShell={openingShell}
						shellError={shellError}
					/>
					{snapshot.threadState ? <ThreadStateBanner threadState={snapshot.threadState} /> : null}
					<div
						className={cn("flex min-h-0 flex-1 flex-col", conversationEmpty && "justify-center")}
						data-composer-placement={conversationEmpty ? "center" : "dock"}
					>
						<ChatLinkProvider onLinkOpen={onLinkOpen} onFileOpen={onOpenFile} onSessionLinkOpen={onSessionLinkOpen} remoteHost={Boolean(activeRemoteHostId)} workspacePaths={filePaths}>
							<ChatImageSourceProvider sessionId={snapshot.sessionId} assetBaseUrl={assetBaseUrl} remoteHost={Boolean(activeRemoteHostId)}>
								<Timeline
									key={draftScopeKey}
									snapshot={snapshot}
									assetBaseUrl={assetBaseUrl}
									remoteHost={Boolean(activeRemoteHostId)}
									draftScope={draftScope}
									draftPersistenceAvailable={draftPersistenceAvailable}
									hasOlder={hasOlder}
									loadingOlder={loadingOlder}
									onLoadOlder={onLoadOlder}
									onDecide={onDecide}
									busy={busy}
									onRollback={rollbackTarget}
									onOpenFiles={onOpenFiles}
									onOpenFile={onOpenFile}
									retryControl={retryControl}
									onEditHumanMessage={draftPersistenceAvailable ? editHumanMessage : undefined}
									editPending={editMessagePending}
									editBusy={Boolean(turn)}
									editError={editMessageError}
									onActivateBranch={onActivateBranch}
									activateBranchPending={activateBranchPending}
									activateBranchError={activateBranchError}
									newWorkDisabled={newWorkDisabled}
									rollbackDisabled={Boolean(turn || rollbackPending || newWorkDisabled)}
									localEchos={localEchos}
									startup={sessionRole === "orchestrator" ? undefined : startup}
								/>
							</ChatImageSourceProvider>
						</ChatLinkProvider>

						<div ref={composerDockRef} className="cursor-chat-composer-dock shrink-0 px-4 pb-3">
							<div aria-hidden="true" className="chat-composer-fade" />
							<div
								data-empty={conversationEmpty || undefined}
								className="mx-auto flex w-full max-w-3xl flex-col gap-2 transition-[max-width] duration-500 ease-out data-[empty]:max-w-2xl"
							>
								{discarded > 0 ? <RolledBackNotice count={discarded} /> : null}
								{conversationEmpty ? (
									<h1 className="mb-5 text-center text-2xl font-normal tracking-tight text-foreground sm:text-3xl">
										{t("chat.welcome.heading")}
									</h1>
								) : null}
								<div ref={composerBoxRef} className="relative">
									{welcomeLeaving ? (
										<h1
											aria-hidden="true"
											className="chat-welcome-leaving pointer-events-none absolute inset-x-0 bottom-full mb-5 text-center text-2xl font-normal tracking-tight text-foreground sm:text-3xl"
										>
											{t("chat.welcome.heading")}
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
									<ChatComposer
										focusRef={composerFocusRef}
										key={`${draftScopeKey}:${draftPersistenceAvailable ? "saved" : "memory"}:${queueEdit ? `${queueEdit.turnId}:${queueEdit.ownerId ?? queueEdit.expectedRevision ?? "legacy"}` : "composer"}`}
										queuedDock={composerQueuedDock}
										approval={composerApproval}
										elicitation={composerElicitation}
										onSend={handleComposerSend}
										draftSeed={composerDraftSeed}
										editingQueuedTurnId={queueEdit?.turnId}
										queuedEditRecovery={Boolean(queueEdit?.clientMessageId)}
										savingQueuedEditPending={Boolean(
											queueEdit?.turnId &&
												editQueuedTurnPendingTurnId === queueEdit.turnId,
										)}
										onCancelQueuedEdit={cancelQueuedEdit}
										onQueuedDraftChange={queueEdit ? changeQueuedDraft : undefined}
										queuedDraftScope={queueEdit ? draftScope : undefined}
										onQueuedAttachmentsChange={changeQueuedStagedAttachments}
										onQueuedRetainedAttachmentsChange={changeQueuedRetainedAttachments}
										onInterrupt={turn && !newWorkDisabled ? stableInterrupt : undefined}
										commandError={queueDraftError ?? (queueEdit && !queueEdit.clientMessageId && !queuedMessages.some((entry) => entry.turnId === queueEdit.turnId) ? "chat.draft.queueMissing" : commandError)}
										settings={<><ContextMeter usage={snapshot.usage} />{composerSettings}</>}
										busy={busy}
										willQueue={Boolean(turn) || (sessionRole !== "orchestrator" && session?.provisionState === "provisioning")}
										queuePlaceholder={
											session?.provisionState === "provisioning"
												? t("chat.startup.queuePlaceholder", { agent: agentLabel(snapshot.harness) })
												: undefined
										}
										starting={orchestratorStarting}
										disabled={(orchestratorStarting || (snapshot.controller.state === "stopped" && !suppressStopped && (!resumingAgent || session?.provisionState === "failed")) || controllerTransitioning || newWorkDisabled) && !queueEdit?.clientMessageId}
										// Switch/reconnect status is the topbar spinner beside ⋮ — not composer text.
										disabledPlaceholder={
											orchestratorStarting
												? setupPlaceholder
												: controllerTransitioning || newWorkDisabled
												? ""
												: agentResuming
													? (sessionRole === "orchestrator" ? "Starting the orchestrator" : "Resuming agent")
													: undefined
										}
										// Keep the composer useful outside the centered welcome state too. A
										// task can have non-message activity before its first visible chat
										// message, and the generic placeholder makes a still-empty composer
										// look like a regression.
										emptyPlaceholder={conversationEmpty ? emptyChatPlaceholder : undefined}
										skills={skills}
										filePaths={filePaths}
										filePathsTruncated={filePathsTruncated}
										onStageAttachments={newWorkDisabled ? undefined : onStageAttachments}
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
										compactBlocked={Boolean(turn)}
										draftSessionId={draftPersistenceAvailable && !queueEdit ? draftScope.sessionId : undefined}
										draftSessionIncarnation={draftPersistenceAvailable ? draftScope.incarnation : undefined}
										assetBaseUrl={assetBaseUrl}
										assetSessionId={snapshot.sessionId}
										remoteHost={Boolean(activeRemoteHostId)}
										acceptedClientMessageIds={acceptedClientMessageIds}
									/>
								</div>
								{compactStartup ? <OrchestratorStartupStatus {...compactStartup} /> : null}
							</div>
						</div>
					</div>
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
	);
}

function OrchestratorStartupStatus({ error, onRetry, retrying, retryError }: ComponentProps<typeof SessionStartup>) {
	const { t } = useTranslation();
	return (
		<div data-testid="orchestrator-startup-status" className="px-2 text-xs text-muted-foreground">
			<div role="alert" className="flex items-center gap-2">
				<TriangleAlert aria-hidden="true" className="size-3.5 text-destructive" />
				<span>{t("chat.startup.failed")}</span>
				{onRetry ? <button type="button" className="ml-auto text-foreground underline underline-offset-2" onClick={onRetry} disabled={retrying}>{retrying ? t("chat.startup.retrying") : t("chat.startup.retry")}</button> : null}
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
		const runnable =
			item.kind === "activity" &&
			item.activityKind !== "approval" &&
			item.activityKind !== "user_input" &&
			item.activityKind !== "error" &&
			// Reasoning only reaches the timeline when the reader asked for it, so
			// folding it into "Explored 4 files" would answer that request with the
			// summary they were trying to get past.
			item.activityKind !== "reasoning" &&
			// A compaction is a boundary in the conversation, not a step in one. Folding
			// it into a run of tool calls would hide that everything above it is no
			// longer what the agent sees verbatim. The same holds for every other
			// stamped system event: a reroute, a credential demand and the user's own
			// steer are all things a run summary would swallow.
			item.detail?.event === undefined;
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
 *
 * Everything else is kept, including an activity this build does not fully
 * understand — dropping an unrecognized item would hide work the agent really did.
 */
function readableItems(snapshot: ConversationSnapshot): ConversationItem[] {
	const plannedTurns = new Set(
		snapshot.turns.filter((turn) => turn.plan?.steps.length).map((turn) => turn.id),
	);
	return snapshot.items.filter((item) => {
		if (item.kind !== "activity") return true;
		if (item.activityKind === "usage") return false;
		if (item.activityKind === "plan" && item.turnId && plannedTurns.has(item.turnId)) return false;
		if (item.activityKind === "reasoning") return false;
		return true;
	});
}

/* -------------------------------------------------------------------------- */

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
	sessionTabActionWide = false,
	tabStripAction,
	workspaceTabActions,
	workspaceActiveTabKey,
	orderedAuxiliaryTabs,
	onReorderAuxiliaryTabs,
	inline,
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
	sessionTabActionWide?: boolean;
	tabStripAction?: ReactNode;
	workspaceTabActions?: ReactNode;
	workspaceActiveTabKey?: string;
	orderedAuxiliaryTabs: ChatAuxiliaryTab[];
	onReorderAuxiliaryTabs: (keys: string[]) => void;
	session?: WorkspaceSession;
	onSessionRenamed?: () => void | Promise<void>;
	/** Fullscreen content cannot see the normal topbar portal outside its subtree. */
	inline?: boolean;
}) {
	const { t } = useTranslation();
	const providerLabel = agentLabel(snapshot.harness);
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
		showLeftFade,
		showRightFade,
	} = useTabScrollEdges([tabScrollWatch]);
	const previousTabCountRef = useRef(orderedAuxiliaryTabs.length);
	useEffect(() => {
		if (orderedAuxiliaryTabs.length > previousTabCountRef.current) scrollTabsToEnd();
		previousTabCountRef.current = orderedAuxiliaryTabs.length;
	}, [orderedAuxiliaryTabs.length, scrollTabsToEnd]);
	// The chat tab is "selected" only when neither terminal pane is the body.
	const timelineActive = !workspaceActiveTabKey && !reviewerActive && !shellActiveHandleId;
	// Match CenterPane: when the sidebar is off-canvas, the fixed TitlebarNav
	// cluster sits over the session tab strip. Terminal already reserves that
	// space; chat must too or the back/forward buttons land on the tab label.
	const isSidebarOpen = useUiStore(sidebarOccupiesLayout);
	const clearanceRef = useSidebarChromeClearanceRef<HTMLDivElement>(isMac);
	const header = (
		<header className="flex h-inspector-tabs w-full shrink-0 items-stretch bg-sidebar">
			<div
				className="session-topbar-surface flex min-w-0 flex-1"
				data-testid="session-workspace-topbar"
			>
				<div
					className={cn(
						"flex min-w-0 shrink items-stretch",
						isMac && "session-topbar-titlebar-clearance-mac",
						!isSidebarOpen && isLinux && "session-topbar-titlebar-clearance-linux",
					)}
					data-testid="session-terminal-region"
					ref={clearanceRef}
					style={{ width: "100%" }}
				>
					<div
						aria-label="Chat tabs"
						className="flex h-full min-w-0 flex-1 items-stretch"
						onKeyDown={onTabsKeyDown ?? handleTerminalTabListKeyDown}
						role="tablist"
					>
						{session ? (
							<SessionPaneTab
								isActive={timelineActive}
								label={label}
								onSelect={timelineActive ? undefined : onSelectChat}
								onRenamed={onSessionRenamed}
								session={session}
								tabAction={sessionTabAction}
								tabActionWide={sessionTabActionWide}
							/>
						) : (
							<button
								aria-current={timelineActive ? true : undefined}
								aria-label={`${label} · ${providerLabel}`}
								aria-selected={timelineActive}
								data-terminal-role="primary"
								className={cn(
									"group relative inline-flex min-w-shell-tab-min max-w-shell-tab-max shrink-0 self-stretch cursor-pointer items-center gap-1.5 border-r border-border px-3 text-control font-medium leading-none transition-colors focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent/50",
									timelineActive
										? "bg-overlay text-foreground after:absolute after:inset-x-0 after:bottom-0 after:h-0.5 after:bg-foreground/80"
										: "text-muted-foreground hover:bg-raised hover:text-foreground",
								)}
								onClick={timelineActive ? undefined : onSelectChat}
								role="tab"
								tabIndex={timelineActive || orderedAuxiliaryTabs.length === 0 ? 0 : -1}
								title={label}
								type="button"
							>
								<AgentAvatar className="size-icon-base" decorative provider={snapshot.harness} />
								<span className="truncate">{label}</span>
							</button>
						)}
						<div className="relative min-w-0 flex-1 self-stretch overflow-hidden">
							<div ref={tabsOverflowRef} className="scrollbar-none flex h-full min-w-flex-min min-w-0 items-stretch overflow-x-auto">
								<div className="flex w-max items-stretch">
								<Reorder.Group
									as="div"
									axis="x"
									className="flex items-stretch self-stretch"
									onReorder={onReorderAuxiliaryTabs}
									values={orderedAuxiliaryTabs.map((tab) => tab.key)}
								>
									{orderedAuxiliaryTabs.map((tab) => (
										<DraggableChatTab key={tab.key} value={tab.key}>
											{tab.kind === "reviewer" || tab.kind === "reviewer-chat" ? (
												<button
													aria-current={reviewerActive && !workspaceActiveTabKey ? true : undefined}
													aria-label="Reviewer"
													aria-selected={Boolean(reviewerActive && !workspaceActiveTabKey)}
													className={cn(
														"group relative inline-flex min-w-shell-tab-min max-w-shell-tab-max self-stretch cursor-pointer items-center gap-1.5 border-r border-border px-3 text-control font-medium leading-none transition-colors focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent/50",
														reviewerActive && !workspaceActiveTabKey
															? "bg-overlay text-foreground after:absolute after:inset-x-0 after:bottom-0 after:h-0.5 after:bg-foreground/80"
															: "text-muted-foreground hover:bg-raised hover:text-foreground",
													)}
													onClick={() => tab.kind === "reviewer" ? onOpenReviewerTerminal?.(tab.terminal) : onOpenReviewerChat?.(tab.terminal)}
													role="tab"
													tabIndex={reviewerActive && !workspaceActiveTabKey ? 0 : -1}
													title={tab.terminal.harness}
													type="button"
												>
													<AgentAvatar className="size-icon-base" decorative provider={tab.terminal.harness} />
													<span className="truncate">Reviewer</span>
												</button>
											) : tab.kind === "shell" ? (
												<ShellTerminalTab
													appearance="connected"
													isActive={tab.terminal.handleId === shellActiveHandleId && !workspaceActiveTabKey}
													onClose={() => onCloseShellTerminal?.(tab.terminal.handleId)}
													onRename={onRenameShellTerminal ? (title) => onRenameShellTerminal(tab.terminal.handleId, title) : undefined}
													onSelect={() => onSelectShellTerminal?.(tab.terminal.handleId)}
													shell={tab.terminal}
												/>
											) : (
												tab.tab.content
											)}
										</DraggableChatTab>
									))}
								</Reorder.Group>
								{workspaceTabActions}
								</div>
							</div>
							{showLeftFade ? <div aria-hidden="true" className="session-tab-scroll-fade session-tab-scroll-fade--left" /> : null}
							{showRightFade ? <div aria-hidden="true" className="session-tab-scroll-fade" /> : null}
						</div>
					</div>
				</div>
				<div
					className="ml-auto flex shrink-0 items-center gap-1 pl-2 pr-3"
					data-testid="session-action-region"
				>
					{tabStripAction ? <div data-testid="session-tab-strip-action">{tabStripAction}</div> : null}
					{headerActions}
				</div>
			</div>
		</header>
	);
	return inline ? header : <SessionTopbarPortal>{header}</SessionTopbarPortal>;
}

/**
 * Controller health and hibernation. A silent surface is indistinguishable from
 * an agent that is simply thinking.
 */
function ControllerBanner({
	controller,
	provisionState,
	transitioning,
	automaticWakePending,
	onResume,
	resuming,
	resumeError,
	onOpenShell,
	openingShell,
	shellError,
}: {
	controller: { state: ControllerState; error?: string };
	provisionState?: WorkspaceSession["provisionState"];
	transitioning?: boolean;
	automaticWakePending?: boolean;
	onResume?: () => void | Promise<unknown>;
	resuming?: boolean;
	resumeError?: string;
	onOpenShell?: () => void;
	openingShell?: boolean;
	shellError?: string;
}) {
	// Startup failures use the checklist's Retry action.
	if (provisionState === "provisioning" || provisionState === "failed") return null;
	const waking = Boolean(resuming && controller.state === "stopped");
	const resumeClick = () => {
		void Promise.resolve().then(() => onResume?.()).catch(() => {});
	};

	// The transition coordinator intentionally stops one controller before it
	// starts the other. The top-bar handoff state already explains that interval;
	// presenting its intermediate snapshot as a crash produces a red false alarm.
	if (controller.state === "ready" || controller.state === "busy" || controller.state === "hibernated") return null;
	if (controller.state === "stopped" && (transitioning || automaticWakePending)) return null;

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
			title: waking ? "Waking agent…" : "The agent controller stopped",
			tone: waking ? "text-muted-foreground" : "text-destructive",
		},
	};
	const shown = copy[controller.state];
	if (!shown) return null;
	const loading = controller.state === "connecting" || waking;

	return (
		<div
			role={controller.state === "stopped" && !waking ? "alert" : "status"}
			aria-atomic="true"
			className="flex shrink-0 items-start gap-2.5 border-b border-border bg-surface px-4 py-2.5"
		>
			{loading ? (
				<Loader2
					aria-hidden="true"
					className="mt-0.5 size-3.5 shrink-0 animate-spin text-muted-foreground"
				/>
			) : (
				<TriangleAlert aria-hidden="true" className={cn("mt-0.5 size-3.5 shrink-0", shown.tone)} />
			)}
			<div className="flex min-w-0 flex-1 flex-col gap-0.5">
				<strong className={cn("text-xs font-medium", shown.tone)}>{shown.title}</strong>
				{controller.state === "stopped" && waking ? (
					<span className="text-xs leading-snug text-muted-foreground">Restoring the agent. You can keep typing.</span>
				) : controller.error ? (
					<span className="text-xs leading-snug text-muted-foreground">{controller.error}</span>
				) : null}
				{controller.state === "stopped" && !waking ? (
					<>
						<span className="text-xs leading-snug text-muted-foreground">
							History is kept. Resume the agent or open a shell in the same worktree.
						</span>
						{resumeError || shellError ? (
							<span className="text-xs leading-snug text-destructive">
								{resumeError ?? shellError}
							</span>
						) : null}
						<div className="mt-1.5 flex flex-wrap gap-2">
							{onResume ? (
								<Button
									type="button"
									size="sm"
									variant="outline"
									onClick={resumeClick}
									disabled={resuming}
								>
									{resuming ? "Resuming…" : "Resume agent"}
								</Button>
							) : null}
							{onOpenShell ? (
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
/** Wheel, key and touch intent pause the follow this long so the resulting scroll decides. */
const READER_INTENT_HOLD_MS = 250;
const CHAT_ESTIMATED_TURN_HEIGHT = 600;
const CHAT_TURN_GAP = 18;
const CHAT_INITIAL_VIEWPORT_HEIGHT = 800;

function Timeline({
	snapshot,
	assetBaseUrl,
	remoteHost,
	draftScope,
	draftPersistenceAvailable = true,
	hasOlder,
	loadingOlder,
	onLoadOlder,
	onDecide,
	busy,
	onRollback,
	onOpenFiles,
	onOpenFile,
	retryControl,
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
}: {
	snapshot: ConversationSnapshot;
	assetBaseUrl?: string;
	remoteHost?: boolean;
	draftScope: ChatDraftScope;
	/** False when drafts can't be saved; persisted inline edits are then ignored. */
	draftPersistenceAvailable?: boolean;
	hasOlder?: boolean;
	loadingOlder?: boolean;
	onLoadOlder?: () => void;
	onDecide?: (requestId: string, decisionId: string) => void;
	busy?: boolean;
	onRollback?: (turnId: string) => void;
	onOpenFiles?: () => void;
	onOpenFile?: (path: string, line?: number) => void;
	retryControl?: ChatRetryControl;
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
	startup?: ComponentProps<typeof SessionStartup> & { openingTurnId?: string };
}) {
	const translateDraft = useChatDraftTranslation();
	const uiSessionId = draftScope.sessionId;
	const scroller = useRef<HTMLDivElement>(null);
	const scrollContent = useRef<HTMLDivElement>(null);
	const virtualContent = useRef<HTMLDivElement>(null);
	const [virtualScrollMargin, setVirtualScrollMargin] = useState(20);
	const measuredVirtualGroups = useRef(new WeakSet<TimelineGroup>());
	const pendingVirtualLayout = useRef(false);
	const renderedVirtualHeight = useRef(0);
	const promptSpacer = useRef<HTMLDivElement>(null);
	const scrollTrack = useRef<HTMLDivElement>(null);
	const drag = useRef<{
		pointerId: number;
		startY: number;
		startScrollTop: number;
	} | null>(null);
	const pinnedRef = useRef(true);
	const [pinned, setPinned] = useState(true);
	const getScroller = useCallback(() => scroller.current, []);
	const {
		glideToEnd,
		followEnd,
		cancel: cancelScrollFollow,
		hold: holdScrollFollow,
		markWritten,
		isOwnScroll,
		isFlying,
		reaim,
		endFlight,
	} = useScrollFollow(getScroller);
	// Set when the reader sends or jumps to the latest: the next layout glides there
	// instead of snapping. Opening a chat never animates, so the first layout snaps.
	const glideRequested = useRef(false);
	const initialLayoutDone = useRef(false);
	const initialLayoutAt = useRef(0);
	const lastScrollTop = useRef(0);
	const setPinnedNow = useCallback((next: boolean) => {
		pinnedRef.current = next;
		setPinned(next);
	}, []);
	// The reader took over (scrolled up, opened a disclosure): stop following.
	const releaseFollow = useCallback(() => {
		cancelScrollFollow();
		if (pinnedRef.current) setPinnedNow(false);
	}, [cancelScrollFollow, setPinnedNow]);
	const requestGlideToEnd = useCallback(() => {
		glideRequested.current = true;
		setPinnedNow(true);
	}, [setPinnedNow]);
	const [activityDisclosureOverrides, setActivityDisclosureOverrides] = useState<Record<string, boolean>>({});
	const onActivityDisclosureChange = useCallback((key: string, open: boolean) => {
		setActivityDisclosureOverrides((current) => ({ ...current, [key]: open }));
	}, []);
	const [hoveredMarker, setHoveredMarker] = useState<number | null>(null);
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
	const turn = activeTurn(snapshot);
	const [scrollbar, setScrollbar] = useState({
		visible: false,
		top: 0,
		height: 40,
		percent: 0,
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
		return ids;
	}, [openingTurnId, snapshot]);
	const decide = useStableCallback(onDecide);
	const rollback = useStableCallback(onRollback);
	const openFiles = useStableCallback(onOpenFiles);
	const openFile = useStableCallback(onOpenFile);
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
	const branchPoints = useMemo(
		() => new Map((snapshot.branchPoints ?? []).map((point) => [point.turnId, point])),
		[snapshot.branchPoints],
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
	const { editableTurns: editableTurnsNow, reconstructedTurns } = useMemo(() => {
		const editable = new Set<string>();
		const reconstructed = new Set<string>();
		if (snapshot.controller.state !== "ready") {
			return {
				editableTurns: editable,
				reconstructedTurns: reconstructed,
			};
		}
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
				if (!firstPromptInBinding && !canNativeFork) reconstructed.add(item.turnId);
			}
		}
		return { editableTurns: editable, reconstructedTurns: reconstructed };
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
	const retryableTurns = useMemo(
		() =>
			new Set(
				snapshot.turns
					.filter(
						(turn) =>
							turn.state === "failed" &&
							Boolean(turn.providerTurnId) &&
							!consumedRetrySources.has(turn.id),
					)
					.map((turn) => turn.id),
			),
		[snapshot.turns, consumedRetrySources],
	);

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
			reconstructedContext: reconstructedTurns.has(message.turnId),
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
	}, [draftScope, inlineEditLocked, reconstructedTurns]);
	const updateMessageEdit = useCallback((text: string) => {
		if (inlineEditLocked) return;
		const current = messageEditRef.current;
		if (!current) return;
		const result = writeChatInlineEdit(draftScope, {
			turnId: current.turnId,
			text,
			content: current.content,
			reconstructedContext: current.reconstructedContext,
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
					reconstructedContext: current.reconstructedContext,
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
			.map((echo, index): ConversationMessage => ({
				kind: "message",
				id: `local:${echo.clientMessageId}`,
				turnId: `local:${echo.clientMessageId}`,
				clientMessageId: echo.clientMessageId,
				sequence: snapshot.latestSequence + index + 0.01,
				revision: 0,
				role: "user",
				origin: "human",
				text: echo.text,
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
			...items.filter(
				(item) => !(item.kind === "message" && item.role === "user" && item.turnId && backgroundQueuedTurnIds.has(item.turnId)),
			),
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
		const added = new Set(
			humanMessages
				.filter(
					(item) =>
						!seenHumanMessageIds.current?.has(itemKey(item)) &&
						item.sequence > (lastSeenLatestSequence.current ?? -Infinity),
				)
				.map(itemKey),
		);
		seenHumanMessageIds.current = humanMessageKeys;
		lastSeenLatestSequence.current = snapshot.latestSequence;
		if (added.size > 0) setNewHumanMessageIds(added);
	}, [timelineItems, snapshot.latestSequence]);
	const previousEchoIds = useRef<ReadonlySet<string>>(new Set(localEchos.map((echo) => echo.clientMessageId)));
	useLayoutEffect(() => {
		const ids = new Set(localEchos.map((echo) => echo.clientMessageId));
		const sent = [...ids].some((id) => !previousEchoIds.current.has(id));
		previousEchoIds.current = ids;
		if (!sent) return;
		// The reader just sent: bring them to their prompt even if they had scrolled away.
		requestGlideToEnd();
	}, [localEchos, requestGlideToEnd]);
	const grouped = useMemo(() => {
		const hiddenTurns = hiddenTimelineTurnIds(snapshot);
		if (openingTurnId) hiddenTurns.delete(openingTurnId);
		return groupByTurn({ ...snapshot, items: timelineItems }).filter(
			(group) => !group.turnId || !hiddenTurns.has(group.turnId),
		);
	}, [openingTurnId, snapshot, timelineItems]);
	const groups = useStableList(grouped, groupKey, sameGroup);
	// Read by syncScrollLayout (a stable callback) so the initial snap only latches
	// once real conversation content exists (history can load after the chat opens).
	const groupCountRef = useRef(groups.length);
	groupCountRef.current = groups.length;
	const navigableGroups = useMemo(() => groups.filter(groupHasHumanPrompt), [groups]);
	const previews = useMemo(() => navigableGroups.map(groupPreview), [navigableGroups]);
	const virtualized = groups.length > CHAT_VIRTUALIZE_THRESHOLD;
	const virtualItemKey = useCallback((index: number) => groups[index]!.key, [groups]);
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
		observeElementRect: (instance, callback) => observeElementRect(instance, (rect) =>
			callback(rect.height ? rect : { width: rect.width, height: CHAT_INITIAL_VIEWPORT_HEIGHT })),
		measureElement: (element, entry, instance) => {
			const group = groups[Number(element.getAttribute("data-index"))];
			if (group && !measuredVirtualGroups.current.has(group)) {
				measuredVirtualGroups.current.add(group);
				pendingVirtualLayout.current = true;
			}
			return measureElement(element, entry, instance) || CHAT_ESTIMATED_TURN_HEIGHT;
		},
		scrollToFn: (offset, { adjustments = 0 }, instance) => {
			if (!instance.scrollElement) return;
			// An instant write would abort our smooth glide (and land at a stale offset).
			// Let the glide carry the adjustment: re-aim it at the new end instead. The
			// next scroll event re-syncs the virtualizer's offset.
			if (isFlying()) {
				reaim();
				return;
			}
			instance.scrollElement.scrollTop = offset + adjustments;
			// Anchoring writes are ours; they must never read as the reader leaving the end.
			markWritten(instance.scrollElement.scrollTop);
		},
		rangeExtractor: (range) => {
			const indexes = defaultRangeExtractor(range);
			// Keep an in-progress inline edit mounted while its row leaves view.
			const editing = messageEdit ? groups.findIndex((group) => group.turnId === messageEdit.turnId) : -1;
			if (editing >= 0 && !indexes.includes(editing)) indexes.push(editing);
			if (pinned && !indexes.includes(groups.length - 1)) indexes.push(groups.length - 1);
			return indexes.sort((a, b) => a - b);
		},
	});
	virtualizer.shouldAdjustScrollPositionOnItemSizeChange = virtualized ? undefined : () => false;
	const virtualRows = virtualizer.getVirtualItems();
	const virtualHeight = virtualizer.getTotalSize();
	renderedVirtualHeight.current = virtualHeight;
	const renderedGroups = virtualized
		? virtualRows.map((row) => ({ group: groups[row.index]!, row, index: row.index }))
		: groups.map((group, index) => ({ group, row: undefined, index }));
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
			markers,
		};
		setScrollbar((current) =>
			current.visible === next.visible &&
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
		if (!initialLayoutDone.current || performance.now() - initialLayoutAt.current < INITIAL_SNAP_MS) {
			node.scrollTop = node.scrollHeight;
			markWritten(node.scrollTop);
			return;
		}
		followEnd();
	}, [followEnd, markWritten]);

	const syncScrollLayout = useCallback(() => {
		anchorGeometry.current = null;
		syncPromptSpacer();
		const node = scroller.current;
		if (node && pinnedRef.current) {
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
	}, [syncPromptSpacer, updateScrollbar, glideToEnd, followOrSnapToEnd]);

	// Streamed text grows the content without a new sequence. While following, let
	// the reply fill the prompt spacer first (the view holds still), then follow the
	// end smoothly once the reply is taller than the viewport.
	const syncStreamedGrowth = useCallback(() => {
		if (!pinnedRef.current || !initialLayoutDone.current) return;
		anchorGeometry.current = null;
		syncPromptSpacer();
		const node = scroller.current;
		if (node) followOrSnapToEnd(node);
		updateScrollbar();
	}, [syncPromptSpacer, followOrSnapToEnd, updateScrollbar]);

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
	useEffect(() => {
		if (!virtualized) return;
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
		if (pinnedRef.current) syncStreamedGrowth();
		else syncScrollMetrics();
	};
	useEffect(() => {
		syncScrollLayout();
		if (typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(() => onContentResize.current());
		if (scroller.current) observer.observe(scroller.current);
		if (scrollContent.current) observer.observe(scrollContent.current);
		return () => observer.disconnect();
	}, [groups.length]);

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
		if (isOwnScroll(top)) {
			// Ours: no change.
		} else if (distance >= 64 || (movedUp && distance > 2)) {
			if (pinnedRef.current) releaseFollow();
		} else if (!pinnedRef.current && !movedUp) {
			setPinnedNow(true);
		}
		updateScrollbar();
	}

	function onViewportWheel(event: ReactWheelEvent<HTMLDivElement>) {
		// Wheel intent arrives before the scroll it causes. Hold the follow so a
		// concurrent stream commit cannot pull the reader back, and let the scroll
		// itself decide: a wheel consumed by a nested scroller (a code block) never
		// moves the log, so it must not unpin it.
		if (event.deltaY < 0) holdScrollFollow(READER_INTENT_HOLD_MS);
	}

	function onViewportKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
		const target = event.target as HTMLElement;
		if (event.defaultPrevented || target.closest("input, textarea, select, [contenteditable='true'], [contenteditable=''], [role='menu'], [role='listbox'], [role='textbox']")) return;
		if (event.key === "ArrowUp" || event.key === "PageUp" || event.key === "Home" || (event.key === " " && event.shiftKey)) {
			holdScrollFollow(READER_INTENT_HOLD_MS);
		}
	}

	function onViewportPointerDown(event: ReactPointerEvent<HTMLDivElement>) {
		// A disclosure the reader is opening: following the end would pull it out from
		// under them. Menu triggers (aria-haspopup) open a popup, not content, so they
		// keep the follow; so do clicks on empty margins.
		if ((event.target as HTMLElement).closest("[aria-expanded]:not([aria-haspopup])")) {
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
		<div
			className="relative min-h-0 flex-1"
			data-testid="chat-timeline"
			// The timeline is a fixed-size flex item with its own scroll viewport.
			// Isolating its layout and paint means Lexical selection/composer updates
			// cannot invalidate the complete mounted conversation history or shell.
			style={{ contain: "layout paint" }}
		>
			<div
				ref={scroller}
				onScroll={onScroll}
				onScrollEnd={endFlight}
				onWheel={onViewportWheel}
				onTouchMove={() => holdScrollFollow(READER_INTENT_HOLD_MS)}
				onKeyDown={onViewportKeyDown}
				onPointerDown={onViewportPointerDown}
				className="chat-scroll-viewport cursor-chat-timeline h-full min-w-0 select-text overflow-x-hidden overflow-y-auto px-4 pt-5 pb-0"
				role="log"
				aria-live="polite"
				aria-label="Conversation"
				style={virtualized ? { overflowAnchor: "none" } : undefined}
			>
				<div ref={scrollContent} className="mx-auto flex w-full min-w-0 max-w-3xl flex-col gap-4.5">
					{hasOlder ? (
						<div className="flex justify-center pb-1">
							<Button
								type="button"
								size="sm"
								variant="ghost"
								disabled={loadingOlder}
								onClick={onLoadOlder}
								className="gap-1.5 text-muted-foreground"
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
						const retry =
							group.turnId &&
							retryControl &&
							retryableTurns.has(group.turnId) &&
							groupHasHumanPrompt(group)
								? {
										onRetry: () => {
											void Promise.resolve(retryTurn(group.turnId as string)).catch(
												() => undefined,
											);
										},
										pending: retryControl.pending && retrySelected,
										error: retrySelected ? retryControl.error : undefined,
										disabled:
											Boolean(turn) ||
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
							>
								<TurnGroup
									group={group}
									startup={showStartup && group.turnId === openingTurnId ? startup : undefined}
									activityDisclosureOverrides={activityDisclosureOverrides}
									onActivityDisclosureChange={onActivityDisclosureChange}
									sessionId={snapshot.sessionId}
									apiBaseUrl={apiBaseUrl}
									onDecide={decide}
									onRollback={rollback}
									onOpenFiles={onOpenFiles ? openFiles : undefined}
									onOpenFile={onOpenFile ? openFile : undefined}
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
									editBusy={Boolean(editBusy || newWorkDisabled)}
									editError={translateDraft(editError ??
										draftPersistenceError ??
										inlineEditRecoveryNotice ??
										inlineEditOutcomeNotice)}
									branchPoints={branchPoints}
									editableTurns={editableTurns}
									newHumanMessageIds={newHumanMessageIds}
									onActivateBranch={canActivateBranch ? activateBranch : undefined}
									activateBranchPending={activateBranchPending}
									activateBranchError={activateBranchError}
									// Only a turn the provider actually accepted can be undone: a turn it
									// never saw holds no history to discard, and the daemon refuses it
									// rather than hiding rows the agent still remembers.
									canRollback={Boolean(onRollback && group.turnId && group.rollbackable)}
									rollbackDisabled={rollbackDisabled}
									busy={busy}
									queued={Boolean(group.turnId && queued.has(group.turnId))}
								/>
							</div>
						);
					})}
					</div>
					{/* Normally drawn inside the opening turn's group; this covers a start
					    whose brief is not in the timeline. */}
					{startup && showStartup && !groups.some((group) => group.turnId === openingTurnId) ? (
						<LiveResponseStatus startupLabel={t(startup.failed ? "chat.startup.failed" : "chat.startup.running")} failed={startup.failed}>
							<SessionStartup {...startup} />
						</LiveResponseStatus>
					) : turn?.state === "running" && !groups.some((group) => group.turnId === turn.id) ? (
						<LiveResponseStatus startedAt={turn.startedAt ?? turn.requestedAt} />
					) : null}
					{messageEdit && !editedMessageVisible ? (
						<div className="flex justify-end" data-chat-scroll-anchor="">
							<HumanMessageEditor
								text={messageEdit.text}
								content={messageEdit.content}
								pending={inlineEditPending}
								locked={Boolean(inlineEditRecoveryLabel)}
								recoveryLabel={translateDraft(inlineEditRecoveryLabel)}
								sendBlocked={inlineEditSendBlocked}
								busy={Boolean(editBusy || newWorkDisabled)}
								error={translateDraft(editError ??
									draftPersistenceError ??
									inlineEditRecoveryNotice ??
									inlineEditOutcomeNotice)}
								reconstructedContext={messageEdit.reconstructedContext}
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
						"group/scroll absolute inset-y-3 right-1 z-10 w-6 touch-none rounded-full outline-none transition-opacity focus-visible:ring-1 focus-visible:ring-logo-accent/60",
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
						className="chat-scroll-preview pointer-events-none absolute right-full z-20 mr-3 w-80 rounded-xl border border-border-strong bg-raised px-3.5 py-3 shadow-lg"
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

			{!pinned ? (
				<Button
					type="button"
					size="icon-sm"
					variant="outline"
					aria-label="Jump to latest"
					title="Jump to latest"
					onClick={requestGlideToEnd}
					className="absolute bottom-3 left-1/2 size-12 -translate-x-1/2 rounded-full border-border-strong bg-raised p-0 text-foreground shadow-sm hover:bg-surface dark:bg-raised dark:hover:bg-surface"
				>
					<ArrowDown aria-hidden="true" className="size-5" />
				</Button>
			) : null}
		</div>
	);
}

/**
 * One turn, and the memo boundary that keeps a poll from re-rendering the whole
 * conversation. A turn is the right granularity because it is what changes: while
 * the agent works, one group grows and every other group is finished history.
 */
const TurnGroup = memo(function TurnGroup({
	group,
	activityDisclosureOverrides,
	onActivityDisclosureChange,
	sessionId,
	apiBaseUrl,
	onDecide,
	onRollback,
	onOpenFiles,
	onOpenFile,
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
}: {
	group: TimelineGroup;
	activityDisclosureOverrides: Readonly<Record<string, boolean>>;
	onActivityDisclosureChange: (key: string, open: boolean) => void;
	sessionId: string;
	apiBaseUrl: string | null;
	onDecide: (requestId: string, decisionId: string) => void;
	onRollback: (turnId: string) => void;
	onOpenFiles?: () => void;
	onOpenFile?: (path: string, line?: number) => void;
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
	startup?: ComponentProps<typeof SessionStartup>;
}) {
	const { t } = useTranslation();
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
	const copyableMessageId = group.live || group.outcome
		? [...group.items]
				.reverse()
				.find((item) => item.kind === "message" && item.role === "assistant" && item.text.trim() !== "")?.id
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
	// Errors and system warnings stay readable after the turn settles instead of
	// folding away with the routine work.
	const noticeRuns = group.outcome ? workedRuns.filter(isNoticeRun) : [];
	const foldedRuns = workedRuns.filter((run) => !noticeRuns.includes(run) && !rendersNothing(run));
	// Keep the live row mounted while its spinner animates out, then hand over to the settled row.
	const [showSettledStatus, setShowSettledStatus] = useState(!group.live);
	useEffect(() => {
		if (group.live) {
			setShowSettledStatus(false);
			return;
		}
		const timer = window.setTimeout(() => setShowSettledStatus(true), 440);
		return () => window.clearTimeout(timer);
	}, [group.live]);
	const renderRun = (run: TimelineRun) =>
		run.kind === "activities" ? (
			<ActivityRun
				key={run.key}
				disclosureKey={`${group.key}:${run.key}`}
				disclosureOverrides={activityDisclosureOverrides}
				onDisclosureChange={onActivityDisclosureChange}
				activities={run.items.filter(
					(item): item is ConversationActivity => item.kind === "activity",
				)}
			/>
		) : (
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
				showCopy={run.items[0]?.id === copyableMessageId}
				live={group.live}
				onRollback={
					canRollback && run.items[0]?.id === copyableMessageId
						? () => onRollback(group.turnId as string)
						: undefined
				}
				rollbackDisabled={rollbackDisabled}
			/>
		);
	// One flat keyed list rather than a slot per section, so a run keeps its element
	// when the turn settles: the final answer leaving the worked runs, or a notice
	// moving out from among them, does not remount it or drop the reader's selection.
	const body: ReactNode[] = humanRuns.map(renderRun);
	// A live provider failure replaces the Working row with its reconnect card, and a
	// turn that stops without an outcome (cancelled) has no settled row to hand over to.
	// The setup checklist sits exactly where the Working row will, so the handoff
	// does not move anything.
	if (startup || (!group.liveProviderFailure && (group.live || (group.outcome && !showSettledStatus)))) {
		body.push(
			<LiveResponseStatus
				key="turn-working"
				startedAt={group.liveStartedAt}
				settling={!startup && !group.live}
				startupLabel={startup ? t(startup.failed ? "chat.startup.failed" : "chat.startup.running") : undefined}
				failed={startup?.failed}
			>
				{startup ? <SessionStartup {...startup} /> : null}
			</LiveResponseStatus>,
		);
	}
	const outcome = showSettledStatus ? group.outcome : undefined;
	if (!outcome) {
		body.push(...workedRuns.map(renderRun));
	} else if (foldedRuns.length > 0) {
		const disclosureKey = `${group.key}:worked`;
		body.push(
			<Accordion
				key="turn-worked"
				type="single"
				collapsible
				className="-mx-1 border-b border-border"
				// Held above the virtualizer, so the accordion stays open when its row scrolls away and back.
				value={activityDisclosureOverrides[disclosureKey] ? "worked" : ""}
				onValueChange={(value) => onActivityDisclosureChange(disclosureKey, value === "worked")}
			>
				<AccordionItem value="worked" className="border-0">
					<AccordionTrigger
						className="chat-worked-trigger h-7 select-none gap-1 px-1 py-0 text-sm font-normal text-muted-foreground transition-colors hover:text-foreground active:transform-none"
						headerClassName="hover:bg-transparent data-[state=open]:bg-transparent"
						trailing={null}
					>
						<span className="inline-flex w-fit items-center gap-1">
							Worked for
							{outcome.durationMs !== undefined ? <TurnDuration durationMs={outcome.durationMs} inline /> : null}
							<ChevronRight aria-hidden="true" className="size-3.5 shrink-0 transition-transform duration-200 group-data-[state=open]/row:rotate-90" />
						</span>
					</AccordionTrigger>
					{/* Padding lives on the inner div: the content element's height is what animates,
					    so any padding on it itself would stay put while it opens and closes. */}
					<AccordionContent className="chat-worked-accordion-content">
						<div className="space-y-2 px-1 pb-2 pt-1">{foldedRuns.map(renderRun)}</div>
					</AccordionContent>
				</AccordionItem>
			</Accordion>,
		);
	} else {
		body.push(
			<div key="turn-worked-plain" className="-mx-1 flex h-7 select-none items-center border-b border-border px-1 py-0 text-sm font-normal text-muted-foreground">
				<span className="inline-flex w-fit items-center gap-1">
					Worked for
					{outcome.durationMs !== undefined ? <TurnDuration durationMs={outcome.durationMs} inline /> : null}
				</span>
			</div>,
		);
	}
	if (outcome) body.push(...noticeRuns.map(renderRun));
	if (finalRun) body.push(renderRun(finalRun));
	return (
		<div className="flex min-w-0 flex-col gap-2.5">
			{body}
			{/* Both of these are current state of the turn rather than steps in it, which
			    is why they sit at its end: a checklist that ticks itself off and a file
			    list that grows both change while the reader watches, and at the end of a
			    turn neither pushes anything the reader is already looking at. */}
			{group.plan ? <TurnPlan plan={group.plan} live={group.live} /> : null}
			{/* Changed-files review is a settled result, not live activity. Keep it
			    below the completed assistant response and its bottom action row. */}
			{group.diff && group.outcome?.state === "completed" && copyableMessageId ? (
				<TurnChangedFiles
					diff={group.diff}
					items={group.items}
					onReview={onOpenFiles}
					onOpenFile={onOpenFile}
				/>
			) : null}
			{group.live ? <TurnLiveStatus blocked={group.blocked} providerFailure={group.liveProviderFailure} /> : null}
			{/* No assistant prose to hang the undo / duration on — still offer them
			    before the outcome divider so a tool-only turn is not stuck without a
			    way back or a record of how long it took. */}
			{!copyableMessageId && !group.live && canRollback ? (
				<div className="flex h-7 items-center gap-0.5">
					{canRollback ? (
						<button
							type="button"
							onClick={() => onRollback(group.turnId as string)}
							disabled={rollbackDisabled}
							aria-label="Roll back to here"
							title="Roll back to here"
							className="flex items-center rounded px-1.5 py-0.5 text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
						>
							<Undo2 aria-hidden="true" className="size-3" />
						</button>
					) : null}
				</div>
			) : null}
			{group.outcome && group.outcome.state !== "completed" ? (
				<TurnOutcome
					state={group.outcome.state}
					error={group.outcome.error}
					retry={group.outcome.state === "failed" ? retry : undefined}
				/>
			) : null}
		</div>
	);
});

function TurnLiveStatus({
	blocked,
	providerFailure,
}: {
	blocked?: boolean;
	providerFailure?: ConversationActivity;
}) {
	if (blocked) {
		return (
			<span role="alert" className="sr-only">
				The agent is waiting for your decision.
			</span>
		);
	}
	if (providerFailure) {
		return (
			<div
				role="status"
				aria-live="polite"
				className="flex min-h-8 items-start gap-2 px-1 py-1"
				data-testid="live-turn-status"
			>
				<TriangleAlert
					aria-hidden="true"
					className="mt-0.5 size-3.5 shrink-0 text-warning"
				/>
				<span className="flex min-w-0 flex-col gap-0.5">
					<strong className="text-xs font-medium text-warning">
						{providerFailure.summary}
					</strong>
					{providerFailure.detail?.text ? (
						<span className="text-xs leading-snug text-muted-foreground">
							{providerFailure.detail.text}
						</span>
					) : null}
				</span>
			</div>
		);
	}

	return null;
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
		// A user-role message that did not come from this human is an automation or
		// worker relay, and is attributed differently.
		if (item.origin === "human") {
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
					editReconstructedContext={editing && messageEdit?.reconstructedContext}
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
				/>
			);
		}
		return <OriginMessage message={item} />;
	}
	if (item.activityKind === "approval") {
		// Pending approval owns the composer. Keeping it in the grouping model still
		// marks the live turn as blocked, while omitting the timeline card itself.
		if (item.status === "pending") return null;
		return <ApprovalCard activity={item} onDecide={onDecide} busy={busy} />;
	}
	// A question is answered on the composer, never in the transcript: pending, it
	// owns the dock above the input, and once answered it leaves nothing behind.
	if (item.activityKind === "user_input") return null;
	if (isCompaction(item)) {
		return <CompactionMarker activity={item} />;
	}
	// Read by the event, not the kind: a steer is stored as a `system` activity
	// because that is AO's only durable write that can attach to a turn in flight,
	// but it is the user speaking and the timeline shows it that way.
	if (isSteer(item)) {
		return <SteerMessage activity={item} sessionId={sessionId} apiBaseUrl={apiBaseUrl} />;
	}
	// A plan whose turn AO never correlated — one from before this controller
	// started. The turn-level checklist cannot show it, so the row carries it.
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

/** Whether a durable row is the one a local echo stands in for. */
function echoMatchesItem(
	echo: ConversationLocalEcho,
	item: ConversationItem,
	backgroundQueuedTurnIds: ReadonlySet<string>,
): boolean {
	if (item.kind !== "message" || item.role !== "user" || item.origin !== "human") return false;
	// A background wake keeps its echo in the timeline while the durable turn sits queued.
	if (echo.turnId && backgroundQueuedTurnIds.has(echo.turnId) && item.turnId === echo.turnId) return false;
	if (item.clientMessageId) return item.clientMessageId === echo.clientMessageId;
	// Rows from a sender that supplied no key fall back to the turn id, then to text and time.
	if (echo.turnId) return item.turnId === echo.turnId;
	return item.text === echo.text && item.createdAt >= echo.createdAt;
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

type TimelineGroup = {
	key: string;
	turnId?: string;
	/** Where this group sits in the timeline: the lowest sequence it contains. */
	anchor: number;
	items: ConversationItem[];
	outcome?: {
		state: "completed" | "recovered" | "interrupted" | "failed";
		durationMs?: number;
		error?: string;
	};
	/** What the turn changed on disk, when the daemon reported anything. */
	diff?: TurnDiff;
	/** The agent's plan for this turn, when it made one. */
	plan?: ConversationPlan;
	/** The turn is still running, so its diff and plan can still change. */
	live?: boolean;
	liveStartedAt?: string;
	blocked?: boolean;
	/** A live provider failure replaces the generic Working label until the turn settles. */
	liveProviderFailure?: ConversationActivity;
	/** The provider accepted this turn, so there is history it can be asked to drop. */
	rollbackable?: boolean;
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
 */
function groupByTurn(snapshot: ConversationSnapshot): TimelineGroup[] {
	const byTurn = new Map(snapshot.turns.map((turn) => [turn.id, turn]));
	const groups: TimelineGroup[] = [];
	const groupForTurn = new Map<string, TimelineGroup>();

	for (const item of snapshot.items) {
		if (item.turnId === undefined) {
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
				key: `loose-${item.sequence}`,
				anchor: item.sequence,
				items: [item],
			});
			continue;
		}
		const existing = groupForTurn.get(item.turnId);
		if (existing) {
			existing.items.push(item);
			continue;
		}
		const group: TimelineGroup = {
			// A turn opened by a keyed human message keeps the key its echo had.
			key: itemKey(item) === item.id ? `${item.turnId}-${item.sequence}` : itemKey(item),
			turnId: item.turnId,
			anchor: item.sequence,
			items: [item],
		};
		groupForTurn.set(item.turnId, group);
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
		group.plan = turn.plan?.steps.length ? turn.plan : undefined;
		group.live = turn.state === "running";
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
		if (turn.state === "running" || turn.state === "queued" || turn.state === "cancelled") continue;
		group.rollbackable = Boolean(turn.providerTurnId);
		group.outcome = {
			state: turn.state,
			durationMs: settledTurnDuration(turn),
			error: turn.errorMessage || (turn.state === "failed"
				? [...group.items].reverse().find(
					(item): item is ConversationActivity => item.kind === "activity" && item.activityKind === "error",
				)?.summary
				: undefined),
		};
	}

	return groups;
}

function isHumanRun(item: ConversationItem | undefined): boolean {
	return (item?.kind === "message" && item.role === "user") || (item?.kind === "activity" && isSteer(item));
}

/** An error or stamped system warning, which a settled turn keeps outside its folded work. */
function isNoticeRun(run: TimelineRun): boolean {
	const item = run.items[0];
	if (run.kind !== "single" || item?.kind !== "activity") return false;
	if (item.activityKind === "error") return true;
	return item.detail?.event !== undefined && !isSteer(item) && !isCompaction(item) && item.activityKind !== "plan";
}

/** Mirrors the items `TimelineItem` draws nothing for, so they cannot fill an empty accordion. */
function rendersNothing(run: TimelineRun): boolean {
	const item = run.items[0];
	if (run.kind !== "single" || !item) return false;
	if (item.kind === "message") return item.role === "assistant" && item.text.trim() === "";
	return item.activityKind === "user_input" || (item.activityKind === "approval" && item.status === "pending");
}
