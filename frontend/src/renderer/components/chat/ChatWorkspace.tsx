import type { ChatExcerptSnapshot } from "../../../shared/chat-excerpt";
import { excerptSizeError } from "../../../shared/chat-excerpt";
import { captureExcerptSnapshot, captureExcerptTurn } from "../../lib/chat-excerpt-snapshot";
import { FileAnnotationContext } from "./FileAnnotationPreview";
import { useChatFind } from "../../hooks/useChatFind";
import { occurrenceKey, type FindOccurrence } from "../../lib/chat-find";
import "./chat-find.css";
import { ForkableTurnContext, ForkTextCopyNotice, LatestForkTurnContext } from "../SessionForkControls";
import type { ChatFileReference, OpenFileAnnotation } from "../../../shared/chat-file-reference";
import { TurnWorkSummary } from "./TurnWorkSummary";
import { TurnSettleSnapshot } from "./TurnSettleSnapshot";
import { useChatDraftTranslation } from "../../lib/chat-draft-messages";
import { cloudAttachmentReference } from "../../lib/cloud-attachments";
import { cloudChatStartingInPlace } from "../../lib/cloud-lifecycle";
import { isSettingsPageOpen, onDedicatedShortcut } from "../../hooks/useAppShortcut";
import { agentAccountProviderFor, fetchSessionAgentAccounts } from "../../hooks/useAgentAccounts";
import { useLatestCallback, useStableSet } from "../../hooks/useStable";
import { useEdgeFadeRef } from "../../hooks/useEdgeFade";
import { useTabListTabStop } from "../../hooks/useTabListTabStop";

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
import { createPortal } from "react-dom";
import { Archive, ArrowDown, Columns2, FlaskConical, ListTodo, Loader2, NotebookPen, Presentation, Route, ScanSearch, TriangleAlert, Undo2, WandSparkles } from "@/components/app-icons";
import { AnimatePresence, motion, Reorder, useReducedMotion } from "motion/react";
import { useTranslation } from "react-i18next";
import { ChatSelectionActions } from "./ChatSelectionActions";
import { ChatAnnotationMarkers, DraftAnnotationMarkers, numberedReferences } from "./ChatAnnotationMarkers";
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
import { annotationBody, annotationTextMatches, annotationReferenceRange, annotationDiagramBlock, highlightChatAnnotation, revealFoldedRange } from "../../lib/chat-annotation-navigation";
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
	getChatComposerMutation,
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
import { apiErrorCode, apiErrorMessage, getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
import type { components } from "../../../api/schema";
import { aoBridge } from "../../lib/bridge";
import { isDialogOrMenuOpen } from "../../lib/dom-selectors";
import { clampTerminalFontSize, initialTerminalFontSize, terminalFontSizeStorageKey } from "../../lib/terminal-font-size";
import { createTerminalMux, muxUrlFromApiBase } from "../../lib/terminal-mux";
import { isLinuxPlatform, isMacPlatform } from "../../lib/platform";
import { handleTerminalTabListKeyDown } from "../../lib/terminal-tabs";
import { agentLabel } from "../../lib/agent-options";
import { labelForHost } from "../../lib/host-clients";
import type { ApprovalMode } from "../../types/conversation";
import type { ConversationLocalEcho } from "../../hooks/useConversation";
import type { ShellTerminal } from "../../hooks/useShellTerminals";
import { sidebarOccupiesLayout, useUiStore } from "../../stores/ui-store";
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
	LoginStatusNote,
	LiveResponseStatus,
	LiveStepChoices,
	RevealedChatActivity,
	isLiveStepKind,
	isModelReroute,
	liveTaskLabel,
	reconnectCopy,
	stepInProgress,
	type TurnOutcomeLogin,
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
import { SubagentPanel, type SubagentPanelHost } from "./SubagentPanel";
import { OpenSubagentContext, SubagentTeam } from "./SubagentTeam";
import { isSubagentLaunch, partitionSubagentSteps, readSubagent, type SubagentRef } from "./subagent-team";
import { isOrdinaryActivity } from "./activity-action";
import { isFailedCall } from "./activity-command";
import { TurnPlan } from "./TurnPlan";
import { ComposerModeItems, TurnSettingsBar, type ApprovalModeLimits } from "./TurnSettingsBar";
import { ElicitationDock, ElicitationHistory } from "./ElicitationDock";
import { ChatLoginBand, type ChatLoginBandProps } from "./ChatLoginBand";
import { ImportedAgentBlocked, McpServerBanner, ReauthBanner, ThreadStateBanner, VersionSwitchBanner } from "./ChatStatusBanners";
import { HostAwayBanner } from "../HostOfflineStatus";
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
	turnsThatDidWork,
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

export const SENT_OUTSIDE_TRANSCRIPT = Symbol("chat.sentOutsideTranscript");

export const CONTINUE_STOPPED_TURN_PROMPT = "Continue from where you stopped.";

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

export type ChatFindRequest = { id: string; query: string; messageId?: string };

export interface ChatWorkspaceProps {
	onAddToSideChat?: (excerpt: ChatDraftExcerptReference) => void | Promise<void>;
	subagentPanel?: SubagentPanelHost;
	checkingSideChatAvailability?: boolean;
	annotationNavigationRequest?: { id: string; text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number; activitySequence?: number };
	onAnnotationNavigationHandled?: (id: string) => void;
	findRequest?: ChatFindRequest;
	onFindRequestHandled?: (id: string) => void;
	snapshot: ConversationSnapshot;
	promptSuggestion?: ComposerPromptSuggestion;
	uiSessionId?: string;
	draftOwner?: ChatDraftScope;
	sessionTitle?: string;
	sessionRole?: SessionKind;
	assetBaseUrl?: string;
	remoteHostId?: string;
	headerActions?: ReactNode;
	sessionTabAction?: ReactNode;
	sessionTabActionPinned?: boolean;
	tabStripAction?: ReactNode;
	workspaceTabs?: WorkspaceTab[];
	workspaceTabActions?: ReactNode;
	workspaceActiveTabKey?: string;
	auxiliaryTabOrder?: string[];
	onAuxiliaryTabOrderChange?: (keys: string[]) => void;
	controllerTransitioning?: boolean;
	accountMoving?: boolean;
	accountMoveQueues?: boolean;
	loadingQuietly?: boolean;
	historyPending?: boolean;
	historyUnread?: boolean;
	agentResuming?: boolean;
	startingSteps?: readonly SessionProvisionStep[];
	settingsReady?: boolean;
	settingsLoading?: boolean;
	lastModel?: string;
	agentInputDisabled?: boolean;
	newWorkDisabled?: boolean;
	pendingInPlaceStart?: boolean;
	reviewerTerminal?: { handleId: string; harness: string };
	onOpenReviewerTerminal?: (target: { handleId: string; harness: string }) => void;
	reviewerChat?: { reviewId: string; harness: string };
	onOpenReviewerChat?: (target: { reviewId: string; harness: string }) => void;
	reviewerChatSelected?: boolean;
	hideHeader?: boolean;
	inlineHeader?: boolean;
	hasOlder?: boolean;
	loadingOlder?: boolean;
	onLoadOlder?: () => void;
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
		displayText?: string,
	) => void | Promise<unknown>;
	onDecide?: (requestId: string, decisionId: string) => void;
	onResolveInput?: (
		requestId: string,
		action: "accept" | "decline" | "cancel",
		content?: Record<string, unknown>,
	) => Promise<unknown> | void;
	onInterrupt?: () => void;
	onContinueTurn?: () => Promise<unknown> | void;
	commandError?: string;
	onResumeAgent?: () => void | Promise<unknown>;
	resumingAgent?: boolean;
	resumeError?: string;
	resumeOpenElsewhere?: boolean;
	onOpenShell?: () => void;
	openingShell?: boolean;
	shellError?: string;
	onLinkOpen?: (url: string) => void;
	onSessionLinkOpen?: (url: string) => void;
	busy?: boolean;
	models?: ChatModel[];
	agentSwitch?: AgentSwitchProps;
	composerNotice?: ReactNode;
	composerLockedPlaceholder?: string;
	sendBlockedReason?: string;
	savedCopy?: boolean;
	importBlock?: AgentBlock;
	onComposerInput?: (text: string, caret?: number) => void;
	settingsLocked?: boolean;
	session?: WorkspaceSession;
	onSessionRenamed?: () => void | Promise<void>;
	reviewerTarget?: ReviewerTerminalTarget;
	onSelectChat?: () => void;
	shellTerminals?: ShellTerminal[];
	shellTarget?: ShellTerminalTarget;
	onSelectShellTerminal?: (handleId: string) => void;
	onCloseShellTerminal?: (handleId: string) => void;
	onRenameShellTerminal?: (handleId: string, title: string) => void;
	daemonReady?: boolean;
	theme?: "light" | "dark";
	onChooseSettings?: (settings: TurnSettings, picked?: keyof TurnSettings) => void;
	onRememberPermissions?: (mode: ApprovalMode | null) => Promise<unknown> | void;
	showApprovalMode?: boolean;
	approvalModes?: ApprovalMode[];
	approvalModeLimits?: ApprovalModeLimits;
	rememberPermissionsPending?: boolean;
	rememberPermissionsError?: string;
	rememberedPermissionMode?: ApprovalMode;
	configOptions?: ChatConfigOption[];
	onChooseConfigOption?: (
		optionId: string,
		value: ChatConfigOptionValue,
	) => Promise<unknown> | void;
	configOptionPending?: boolean;
	configOptionError?: string;
	onCompact?: () => void;
	compacting?: boolean;
	compactUnavailable?: string;
	onRollback?: (turnId: string, filePlanId?: string) => void | Promise<unknown>;
	onPreviewRollback?: (turnId: string) => Promise<RollbackPreview>;
	rollbackPending?: boolean;
	rollbackError?: string;
	onOpenFiles?: () => void;
	onOpenFile?: (path: string, line?: number) => void;
	onOpenArtifact?: (target: { path: string }) => void;
	onOpenFileAnnotation?: OpenFileAnnotation;
	retryControl?: ChatRetryControl;
	onEditMessage?: (
		turnId: string,
		text: string,
		clientMessageId?: string,
	) => void | ChatEditOutcome | Promise<ChatEditOutcome | void>;
	editMessagePending?: boolean;
	editMessageError?: string;
	onActivateBranch?: (branchId: string) => void | Promise<unknown>;
	activateBranchPending?: boolean;
	activateBranchError?: string;
	skills?: ChatSkill[];
	filePaths?: string[];
	onFileQuery?: (query: string) => void;
	composerStarting?: boolean;
	filePathsTruncated?: boolean;
	localEchos?: ConversationLocalEcho[];
	onStageAttachments?: (attachments: { mimeType: string; data: string; name?: string }[], signal?: AbortSignal) => Promise<string[]>;
	cloudAttachments?: { maxBytes: number };
	nativeImages?: boolean;
	onSteer?: (
		text: string,
		attachments?: { mimeType: string; data: string }[],
		clientMessageId?: string,
		recoverOnly?: boolean,
	) => Promise<ChatSteerOutcome | void>;
	showSteerButton?: boolean;
	sendPending?: boolean;
	steerPending?: boolean;
	steerRefusal?: string;
	onPromoteQueuedTurn?: (turnId: string) => Promise<unknown>;
	queuedSteerErrors?: Readonly<Record<string, string>>;
	steeringQueuedTurnIds?: ReadonlySet<string>;
	onEditQueuedTurn?: (turnId: string, text: string, options?: QueuedMessageEditOptions) => Promise<unknown>;
	canEditQueuedMessage?: (message: ConversationMessage) => boolean;
	onCancelQueuedTurn?: (turnId: string) => Promise<unknown>;
	canCancelQueuedMessage?: (message: ConversationMessage) => boolean;
	onReorderQueuedTurns?: (turnIds: string[]) => Promise<unknown>;
	promoteQueuedTurnPendingTurnId?: string;
	cancelQueuedTurnPendingTurnId?: string;
	editQueuedTurnPendingTurnId?: string;
	draftSettings?: ReactNode;
	draftNotice?: ReactNode;
	openingStartedAt?: string;
	composerTray?: ReactNode;
	parkUnsentSends?: boolean;
	centerInWindow?: boolean;
	welcomeHeading?: string;
	showSetupStep?: boolean;
	holdAttachments?: boolean;
	onHeldAttachmentsChange?: ComponentProps<typeof ChatComposer>["onHeldAttachmentsChange"];
	attachmentsRefusal?: string;
}

type ChatWorkspaceActivation =
	| { key: string; state: "active" }
	| { key: string; state: "failed"; reason: "obsolete" | "storage" };

function OfflineRemoteTerminal() {
	const { t } = useTranslation();
	return <div className="flex h-full items-center justify-center text-sm text-muted-foreground" role="status">{t("remote.hostOffline")}</div>;
}

export function ChatWorkspace(props: ChatWorkspaceProps) {
	const { snapshot, session, draftOwner, uiSessionId = draftOwner?.sessionId ?? snapshot.sessionId } = props;
	const draftScope = useMemo<ChatDraftScope>(
		() => draftOwner ?? ({
			sessionId: uiSessionId,
			incarnation: session?.createdAt ?? uiSessionId,
		}),
		[draftOwner, session?.createdAt, uiSessionId],
	);
	const scopeKey = chatDraftScopeKey(draftScope);
	const [activation, setActivation] = useState<ChatWorkspaceActivation>();
	const [activationAttempt, setActivationAttempt] = useState(0);
	const retryDraftPersistence = useCallback(() => setActivationAttempt((attempt) => attempt + 1), []);

	useLayoutEffect(() => {
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
	subagentPanel,
	checkingSideChatAvailability,
	annotationNavigationRequest,
	onAnnotationNavigationHandled,
	findRequest,
	onFindRequestHandled,
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
	historyUnread = false,
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
	sendBlockedReason,
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
	onPreviewRollback,
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
	queuedSteerErrors,
	steeringQueuedTurnIds,
	onEditQueuedTurn,
	canEditQueuedMessage,
	onCancelQueuedTurn,
	canCancelQueuedMessage,
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
	welcomeHeading,
	showSetupStep = true,
	holdAttachments,
	onHeldAttachmentsChange,
	attachmentsRefusal,
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
	const durableTurn = activeTurn(snapshot);
	const canContinue = Boolean(onContinueTurn) && !durableTurn && !newWorkDisabled && Boolean(continuableTurnId(snapshot));
	const latestSettledTurn = snapshot.turns.findLast((entry) =>
		!entry.rolledBack && entry.state !== "queued" && entry.state !== "cancelled",
	);
	const retryableTurns = useMemo(() => retryableFailedTurns(snapshot, retrySourceTurnIds(snapshot)), [snapshot]);
	const chatLogin = useChatLogin({
		snapshot,
		retryable: retryableTurns,
		retryControl,
		hostId: activeRemoteHostId,
		cloud: Boolean(session?.cloud),
		newWorkDisabled: Boolean(newWorkDisabled),
	});
	const limitedTurn = latestSettledTurn?.state === "failed" && latestSettledTurn.usageLimit &&
		latestSettledTurn.providerTurnId && !latestSettledTurn.hasRetryAttempt &&
		!snapshot.turns.some((entry) => entry.retryOfTurnId === latestSettledTurn.id) &&
		latestSettledTurn.hasHumanPrompt
		? latestSettledTurn : undefined;
	const wakingFromHibernate = useRef(snapshot.controller.state === "hibernated");
	if (snapshot.controller.state === "hibernated") wakingFromHibernate.current = true;
	if (snapshot.controller.state === "ready" || snapshot.controller.state === "busy") wakingFromHibernate.current = false;
	if (snapshot.controller.state === "stopped" && !resumingAgent) wakingFromHibernate.current = false;
	const suppressStopped = wakingFromHibernate.current && snapshot.controller.state === "stopped";
	const terminated = Boolean(session && !sessionIsActive(session));
	const startupReconnecting = snapshot.controller.state === "stopped" && !resumingAgent && !terminated &&
		session?.statusReadiness === "checking";
	const controllerStopped = snapshot.controller.state === "stopped" &&
		!(controllerTransitioning || agentResuming || suppressStopped || resumingAgent || startupReconnecting);
	const turn = activeTurn(snapshot, { controllerStopped });
	const signInRequired = needsReauth(snapshot) && agentAccountProviderFor(snapshot.harness) !== null && !activeRemoteHostId && !session?.cloud;
	const startupState =
		session?.provisionState === "provisioning" || session?.provisionState === "failed"
			? session.provisionState
			: undefined;
	const plannedSteps = session?.provisionSteps ?? startingSteps;
	const provisionSteps = useMemo(
		() => plannedSteps?.filter((step) => step.id !== "setup" || showSetupStep || (startupState === "failed" && step.status === "running")),
		[plannedSteps, showSetupStep, startupState],
	);
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
				!(event.target instanceof Node && event.currentTarget.contains(event.target)) ||
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
		if (!event.currentTarget.contains(target)) return;
		if (window.getSelection()?.isCollapsed === false) return;
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
	const reviewerActive = reviewerChatSelected || Boolean(reviewerTarget && session);
	const shellActive = Boolean(shellTarget && session);
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
	const [composerSends, setComposerSends] = useState(0);
	const backgroundWakeQueuedTurnIds = useMemo(
		() => new Set(
			(localEchos ?? [])
				.filter((echo) => echo.backgroundWake && echo.turnId)
				.map((echo) => echo.turnId as string)
				.filter((turnId) => snapshot.turns.some((turn) => turn.id === turnId && turn.state === "queued")),
		),
		[localEchos, snapshot.turns],
	);
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
	const steerInQueue = steeringQueuedTurnIds !== undefined;
	const visibleQueuedMessages = useMemo(
		() => [
			...queuedMessages.filter(
				(message) => !backgroundWakeQueuedTurnIds.has(message.turnId) && (steerInQueue || !steerStartedAt.has(message.turnId)),
			),
			...pendingQueuedMessages,
		],
		[backgroundWakeQueuedTurnIds, pendingQueuedMessages, queuedMessages, steerInQueue, steerStartedAt],
	);
	const steeringTurnIds = useMemo(
		() => (steerInQueue ? new Set([...steeringQueuedTurnIds, ...steerStartedAt.keys()]) : undefined),
		[steerInQueue, steeringQueuedTurnIds, steerStartedAt],
	);
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
		const steering = steerInQueue ? [] : queuedMessages.filter(({ turnId }) => steerStartedAt.has(turnId));
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
	}, [pendingSteerEchoes, queuedMessages, snapshot, steerInQueue, steerStartedAt, turn]);
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
		[onCancelQueuedTurn, queueEdit, stableCancelQueuedTurn, updateQueueDraft],
	);
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
					if ([
						"CHAT_TURN_NOT_QUEUED",
						"CHAT_QUEUED_EDIT_CONFLICT",
						"CHAT_QUEUED_CONTENT_INVALID",
						"CHAT_QUEUED_TEXT_REQUIRED",
						"CHAT_QUEUED_EDIT_REFUSED",
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
			if (wasConversationEmpty.current && composerBoxRef.current) {
				composerCenteredTopRef.current = composerBoxRef.current.getBoundingClientRect().top;
			}
			const sent = await onSend?.(text, attachments, clientMessageId, excerpts, fileReferences, displayText);
			if (sent !== SENT_OUTSIDE_TRANSCRIPT) setComposerSends((count) => count + 1);
			return sent;
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
			const attachments: StoredComposerAttachment[] = parts.attachments.map((path) => ({
				id: path, name: attachmentName(path), path,
			}));
			(message.content ?? []).forEach((item, index) => {
				attachments.push({ id: `content-${index}`, contentIndex: index, contentType: item.type,
					name: item.name || (item.type === "image" ? `Image ${index + 1}` : item.uri || "Attachment") });
			});
			(message.cloudAttachments ?? []).forEach((attachment) => {
				const path = cloudAttachmentReference(attachment);
				attachments.push({ id: path, name: attachment.fileName, path });
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
				if (outcome?.status === "not-accepted") dropEcho();
				else {
					const acceptedAfterSequence = latestSequence() ?? 0;
					setSteerEchoes((current) => current.map((echo) =>
						echo.clientMessageId === id && echo.acceptedAfterSequence === undefined ? { ...echo, acceptedAfterSequence } : echo));
					if (target) setComposerSends((count) => count + 1);
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
	const [confirming, setConfirming] = useState<string | undefined>(undefined);
	const previewRollback = useStableCallback(onPreviewRollback);
	const surfaceRef = useRef<HTMLElement | null>(null);
	const composerFocusRef = useRef<ChatComposerHandle>(null);
	const artifactSession = session?.requestedOutput === "artifact";
	const [composerEmpty, setComposerEmpty] = useState(() => {
		const { composer } = readChatSessionDraft(draftScope);
		return !composer.text.trim() && composer.attachments.length === 0 && !composer.excerpts?.length;
	});
	const annotationNavigationRef = useRef<((annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number; activitySequence?: number }) => void) | null>(null);
	const navigateToAnnotation = useCallback((annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number; activitySequence?: number }) => {
		annotationNavigationRef.current?.(annotation);
	}, []);
	useEffect(() => {
		if (!annotationNavigationRequest) return;
		navigateToAnnotation(annotationNavigationRequest);
		onAnnotationNavigationHandled?.(annotationNavigationRequest.id);
	}, [annotationNavigationRequest, navigateToAnnotation, onAnnotationNavigationHandled]);
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
			!hideHeader ? onDedicatedShortcut("close-shell-terminal", (handler) => aoBridge.app.onCloseShellTerminalShortcut(handler), () => {
				if (closeFocusedSplitPane) closeFocusedSplitPane();
				else if (activeWorkspaceTab?.onClose) activeWorkspaceTab.onClose();
				else if (shellTarget) onCloseShellTerminal?.(shellTarget.handleId);
			}) : undefined,
		[activeWorkspaceTab, closeFocusedSplitPane, hideHeader, onCloseShellTerminal, shellTarget],
	);

	useEffect(() => {
		if (hideHeader) return;
		const disposePrevious = onDedicatedShortcut("previous-tab", (handler) => aoBridge.app.onPreviousTabShortcut(handler), () => {
			if (!isSettingsPageOpen()) selectAdjacentTab(-1);
		});
		const disposeNext = onDedicatedShortcut("next-tab", (handler) => aoBridge.app.onNextTabShortcut(handler), () => {
			if (!isSettingsPageOpen()) selectAdjacentTab(1);
		});
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
	const compactsAt = snapshot.harness === "claude-code" ? snapshot.settings?.autoCompactWindow : undefined;
	const stableUsage = useStableValue(snapshot.usage);
	const stableRateLimits = useStableValue(snapshot.rateLimits);
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
					disabled={
							(!agentResuming && (
								snapshot.controller.state === "connecting" ||
								snapshot.controller.state === "stopped"
							)) ||
							settingsLoading ||
							session?.provisionState === "provisioning" ||
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
		Boolean(onSteer) && Boolean(onPromoteQueuedTurn) && can(snapshot, "steer") && turn?.state === "running";
	const queuedSteerFailures = useMemo(
		() => (queuedSteerErrors && Object.keys(queuedSteerErrors).length > 0 ? { ...queuedSteerErrors, ...steerErrors } : steerErrors),
		[queuedSteerErrors, steerErrors],
	);
	const composerQueuedDock = useMemo(
		() =>
			visibleQueuedMessages.length > 0 ? (
				<QueuedMessageDock
					messages={visibleQueuedMessages}
					editingTurnId={queueEdit?.turnId}
					disabled={Boolean(queueEdit?.clientMessageId)}
					canSteer={canSteerQueuedMessage}
					onPromoteQueuedTurn={newWorkDisabled || !onPromoteQueuedTurn ? undefined : promoteQueuedTurn}
					onBeginQueuedEdit={
						newWorkDisabled || !draftPersistenceAvailable || !onEditQueuedTurn ? undefined : beginQueuedEdit
					}
					canEditMessage={canEditQueuedMessage}
					onCancelQueuedTurn={newWorkDisabled || !onCancelQueuedTurn ? undefined : handleCancelQueuedTurn}
					canCancelMessage={canCancelQueuedMessage}
					onReorderQueuedTurns={newWorkDisabled || !onReorderQueuedTurns ? undefined : reorderQueuedTurns}
					promotePendingTurnId={promoteQueuedTurnPendingTurnId}
					cancelPendingTurnId={cancelQueuedTurnPendingTurnId}
					steerErrors={queuedSteerFailures}
					steeringTurnIds={steeringTurnIds}
				/>
			) : null,
		[
			beginQueuedEdit,
			canCancelQueuedMessage,
			canEditQueuedMessage,
			canSteerQueuedMessage,
			cancelQueuedTurnPendingTurnId,
			draftPersistenceAvailable,
			handleCancelQueuedTurn,
			newWorkDisabled,
			onCancelQueuedTurn,
			onEditQueuedTurn,
			onPromoteQueuedTurn,
			onReorderQueuedTurns,
			reorderQueuedTurns,
			promoteQueuedTurn,
			promoteQueuedTurnPendingTurnId,
			queueEdit?.clientMessageId,
			queueEdit?.turnId,
			queuedSteerFailures,
			steeringTurnIds,
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
	const sentMessages = useStableValue(sentMessageList);
	const orchestratorStarting = sessionRole === "orchestrator" && startupState !== "failed" && (
		startupState === "provisioning" || agentResuming ||
		(!loadingQuietly && (snapshot.controller.state === "connecting" || snapshot.controller.state === "recovering"))
	);
	const cloudStarting = cloudChatStartingInPlace(session);
	const startupQueues = cloudStarting || (session?.provisionState === "provisioning" && sessionRole !== "orchestrator");
	const orchestratorBlocksComposer = orchestratorStarting && !startupQueues;
	const setupPlaceholder = "Starting your orchestrator";
	const arrivingInChat = (Boolean(controllerTransitioning) || historyPending) && !accountMoving && !newWorkDisabled && startupState !== "provisioning";
	const arrivingPlaceholder = sessionRole === "orchestrator" && !historyPending && !session?.lastUserMessageAt
		? setupPlaceholder
		: historyPending || snapshot.controller.state === "connecting" || snapshot.controller.state === "recovering"
		? "Restoring your conversation"
		: "Starting the chat agent";
	const compactStartup = sessionRole === "orchestrator" && startupState === "failed" ? startup : undefined;
	const arrivingWithHistory = arrivingInChat && (historyPending || Boolean(session?.lastUserMessageAt));
	const [headerSlideIn] = useState(() => {
		const slideKey = uiSessionId ?? snapshot.sessionId;
		if (!openingStartedAt || Date.now() - Date.parse(openingStartedAt) > 15_000 || headerSlidSessions.has(slideKey)) return false;
		headerSlidSessions.add(slideKey);
		setTimeout(() => headerSlidSessions.delete(slideKey), 15_000);
		return true;
	});
	const openingStarting = Boolean(openingStartedAt) && startupState !== "failed" && !openingTurnSettled(snapshot, controllerStopped) &&
		!(turn?.state === "running" && snapshot.controller.state !== "connecting");
	const [settledDocked, setSettledDocked] = useState<boolean | undefined>(shellActive ? undefined : historyUnread);
	if (settledDocked === undefined && !shellActive) setSettledDocked(historyUnread);
	const openedDocked = settledDocked !== false;
	const conversationEmpty = !openedDocked && !arrivingWithHistory && snapshot.items.length === 0 && !turn && (localEchos?.length ?? 0) === 0 && (!hasStartup || sessionRole === "orchestrator");
	const { t } = useTranslation();
	const [emptyChatPlaceholder] = useState(
		() => sessionRole === "orchestrator"
			? "Ask anything about this project"
			: EMPTY_CHAT_PLACEHOLDERS[Math.floor(Math.random() * EMPTY_CHAT_PLACEHOLDERS.length)],
	);
	const composerDockRef = useRef<HTMLDivElement>(null);
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
					{snapshot.account && !chatLogin.band ? (
						<ReauthBanner key={`${snapshot.sessionId}:${snapshot.conversationId}`} account={snapshot.account} harness={snapshot.harness} sessionId={snapshot.sessionId}
							hostId={activeRemoteHostId} cloud={Boolean(session?.cloud)} onLogIn={chatLogin.logIn} />
					) : null}
					{!draftPersistenceAvailable ? (
						<div role="status" className="flex shrink-0 items-center gap-2 border-b border-border bg-surface px-4 py-2 text-xs text-muted-foreground">
							<TriangleAlert aria-hidden="true" className="size-3.5 shrink-0 text-warning" />
							{t("chat.draft.storageUnavailable")}
						</div>
					) : null}
					{savedCopy && activeRemoteHostId ? <HostAwayBanner hostId={activeRemoteHostId} /> : savedCopy || importBlock ? null : (
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
						agentStopped={controllerStopped}
					/>
					<div
						className={cn("flex min-h-0 flex-1 flex-col", conversationEmpty && "justify-center", conversationEmpty && centerInWindow && "pb-inspector-tabs", (arrivingWithHistory || openedDocked) && snapshot.items.length === 0 && "justify-end")}
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

								{conversationEmpty ? <ForkTextCopyNotice /> : null}
								<Timeline
									shownAt={panelShownAt}
							onAddToSideChat={onAddToSideChat}
									subagentPanel={subagentPanel}
									checkingSideChatAvailability={checkingSideChatAvailability}
									findRequest={findRequest}
									onFindRequestHandled={onFindRequestHandled}
									workerUpdates={sessionRole === "orchestrator"}
									annotationNavigationRef={annotationNavigationRef}
									key={draftScopeKey}
									snapshot={timelineSnapshot}
									controllerStopped={controllerStopped}
									assetBaseUrl={assetBaseUrl}
									remoteHost={Boolean(activeRemoteHostId)}
									findUnavailable={Boolean(session?.cloud)}
									loginLines={chatLogin.lines}
									retryableTurns={retryableTurns}
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
									composerSends={composerSends}
									startup={sessionRole === "orchestrator" ? undefined : startup}
									openingStartedAt={openingStartedAt}
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
										{welcomeHeading ?? t(artifactSession ? "chat.welcome.artifactHeading" : "chat.welcome.heading")}
									</h1>
								) : null}
								<WelcomePromptSuggestions
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
											{welcomeHeading ?? t(artifactSession ? "chat.welcome.artifactHeading" : "chat.welcome.heading")}
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
										trayBand={chatLogin.band ? <ChatLoginBand {...chatLogin.band} /> : undefined}
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
										disabled={(orchestratorBlocksComposer || (openingStarting && !session) || loadingQuietly || (snapshot.controller.state === "stopped" && !suppressStopped && !accountMoveQueues && (!resumingAgent || session?.provisionState === "failed" || terminated)) || (controllerTransitioning && !accountMoveQueues) || (newWorkDisabled && sendBlockedReason === undefined) || composerLockedPlaceholder !== undefined) && !queueEdit?.clientMessageId}
										sendBlockedReason={sendBlockedReason}
										typingLocked={chatLogin.locked}
										disabledPlaceholder={
											composerLockedPlaceholder !== undefined
												? composerLockedPlaceholder
												: sendBlockedReason !== undefined
												? sendBlockedReason
												: arrivingInChat
												? arrivingPlaceholder
												: (openingStarting && !session) || pendingInPlaceStart
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
										emptyPlaceholder={conversationEmpty ? (artifactSession ? t("chat.welcome.artifactPlaceholder") : emptyChatPlaceholder) : undefined}
										skills={skills}
										filePaths={filePaths}
										filePathsTruncated={filePathsTruncated}
										onFileQuery={onFileQuery}
										onStageAttachments={newWorkDisabled ? undefined : onStageAttachments}
										cloudAttachments={cloudAttachments}
										holdAttachments={newWorkDisabled ? undefined : holdAttachments}
										onHeldAttachmentsChange={onHeldAttachmentsChange}
										attachmentsRefusal={attachmentsRefusal}
										nativeImages={queueEdit?.clientMessageId ? queueEdit.nativeImages ?? nativeImages : nativeImages}
										autoFocus={!reviewerActive}
										autoFocusKey={draftScope.sessionId}
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

			<RollbackConfirmation
				open={Boolean(confirming) && !reviewerActive && !shellActive}
				turnId={confirming}
				busy={rollbackPending}
				error={rollbackError}
				onPreview={onPreviewRollback ? previewRollback : undefined}
				onConfirm={(turnId, filePlanId) => {
					setConfirming(undefined);
					void Promise.resolve(onRollback?.(turnId, filePlanId)).catch(() => {
						setConfirming(turnId);
					});
				}}
				onClose={() => setConfirming(undefined)}
			/>
		</section>
 </FileAnnotationContext.Provider>
	);
}

type RollbackPreview = components["schemas"]["RollbackPreviewResponse"];

const rollbackChangeLabels: Record<RollbackPreview["files"][number]["change"], string> = {
	restored: "Restored",
	reverted: "Reverted",
	removed: "Deleted",
};

function RollbackConfirmation({
	open,
	turnId,
	busy,
	error,
	onPreview,
	onConfirm,
	onClose,
}: {
	open: boolean;
	turnId?: string;
	busy?: boolean;
	error?: string;
	onPreview?: (turnId: string) => Promise<RollbackPreview> | undefined;
	onConfirm: (turnId: string, filePlanId?: string) => void;
	onClose: () => void;
}) {
	const [preview, setPreview] = useState<{ turnId: string; result?: RollbackPreview; error?: string }>();
	useEffect(() => {
		const request = turnId ? onPreview?.(turnId) : undefined;
		if (!turnId || !request) return;
		let current = true;
		setPreview({ turnId });
		request.then(
			(result) => {
				if (current) setPreview({ turnId, result });
			},
			(cause: unknown) => {
				if (current) setPreview({ turnId, error: apiErrorMessage(cause) });
			},
		);
		return () => {
			current = false;
		};
	}, [turnId, onPreview]);

	const loaded = preview?.turnId === turnId ? preview : undefined;
	const checking = Boolean(onPreview) && !loaded?.result && !loaded?.error;
	const restore = loaded?.result?.filesRestorable ? loaded.result : undefined;
	const unavailable = loaded?.result?.reason ?? loaded?.error;

	let detail: string;
	if (checking) {
		detail = "Checking which files this would restore…";
	} else if (restore) {
		const files = restore.totalFiles === 0
			? "No files have changed since then."
			: `${restore.totalFiles} ${restore.totalFiles === 1 ? "file" : "files"} will change.`;
		const commits = restore.commitsSince === 1
			? " 1 commit made since then stays on the branch, and restoring the files can leave uncommitted changes that undo it."
			: restore.commitsSince > 1
				? ` ${restore.commitsSince} commits made since then stay on the branch, and restoring the files can leave uncommitted changes that undo them.`
				: "";
		detail = `${files}${commits} Ignored files this rollback does not change are left alone. This cannot be undone.`;
	} else {
		detail = `${unavailable ? `Files can't be restored: ${unavailable}. ` : ""}Its memory of the conversation is discarded up to this point, so it will not know about anything you or it said later. Files it already changed in the worktree are left exactly as they are; only the conversation is rolled back. This cannot be undone.`;
	}

	return (
		<ConfirmDialog
			open={open}
			onOpenChange={(next) => {
				if (!next) onClose();
			}}
			title="Roll back to this point?"
			description={
				<>
					<p className="text-xs font-medium text-foreground">
						{restore
							? "The agent will forget this exchange and everything after it, and the worktree goes back to how it was before it."
							: "The agent will forget this exchange and everything after it."}
					</p>
					<p className="mt-1 text-xs text-muted-foreground">{detail}</p>
				</>
			}
			confirmLabel="Roll back"
			destructive
			busy={busy}
			confirmDisabled={checking}
			error={error ?? null}
			onConfirm={() => {
				if (turnId) onConfirm(turnId, restore?.planId);
			}}
		>
			{restore && restore.files.length > 0 ? (
				<ul aria-label="Files that will change" className="max-h-40 overflow-y-auto rounded-md bg-surface py-1">
					{restore.files.map((file) => (
						<li key={file.path} className="flex items-center gap-2 px-2.5 py-1 text-xs">
							<span className="min-w-0 flex-1 truncate font-mono text-foreground/80" title={file.path}>
								{file.path}
							</span>
							<span className="shrink-0 text-muted-foreground">{rollbackChangeLabels[file.change]}</span>
						</li>
					))}
				</ul>
			) : null}
		</ConfirmDialog>
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

type TimelineRun =
	| { kind: "activities"; key: string; items: ConversationItem[] }
	| { kind: "subagents"; key: string; items: ConversationActivity[] }
	| { kind: "single"; key: string; items: [ConversationItem] };

function turnFlowItems(group: TimelineGroup): ConversationItem[] {
	const hasTerminalFailure = group.outcome?.state === "failed" && Boolean(group.outcome.error);
	return group.items.filter((item) => {
		if (group.subagents?.stepIds.has(item.id)) return false;
		if (item.id === group.liveProviderFailure?.id) return false;
		if (hasTerminalFailure && item.kind === "activity" && item.activityKind === "error" && item.summary === group.outcome?.error) return false;
		return !(
			hasTerminalFailure &&
			item.kind === "activity" &&
			item.detail?.event === "provider.failure" &&
			item.status === "failed"
		);
	});
}

function owningSubagent(groups: readonly TimelineGroup[], activityId: string): { launchId: string; groupIndex: number; teamKey: string } | undefined {
	for (let groupIndex = 0; groupIndex < groups.length; groupIndex++) {
		const group = groups[groupIndex]!;
		if (!group.subagents) continue;
		for (const [launchId, steps] of group.subagents.steps) {
			if (!steps.some((step) => step.id === activityId)) continue;
			const run = runsOf(turnFlowItems(group)).find((each) => each.kind === "subagents" && each.items.some((item) => item.id === launchId));
			if (!run) return undefined;
			return { launchId, groupIndex, teamKey: `${group.key}:${run.key}` };
		}
	}
	return undefined;
}

function runsOf(items: ConversationItem[]): TimelineRun[] {
	const runs: TimelineRun[] = [];
	for (const item of items) {
		const last = runs.at(-1);
		if (isSubagentLaunch(item)) {
			if (last?.kind === "subagents") last.items.push(item);
			else runs.push({ kind: "subagents", key: `subagents-${item.sequence}`, items: [item] });
			continue;
		}
		const runnable = isOrdinaryActivity(item);
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
		if (item.activityKind === "reasoning" && !isSubagentLaunch(item)) return false;
		if (isShownAORender(item)) return false;
		if (isOrchestratorPlumbing(item)) return false;
		if (
			item.activityKind === "mcp_tool" &&
			item.detail?.providerToolName === "AskUserQuestion" &&
			(item.status === "pending" || item.status === "running") &&
			item.turnId && pendingQuestionTurns.has(item.turnId)
		) return false;
		return true;
	});
}


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
	reviewerActive?: boolean;
	onSelectChat?: () => void;
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
	const timelineActive = !workspaceActiveTabKey && !reviewerActive && !shellActiveHandleId;
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
	quietConnect?: boolean;
	provisionState?: WorkspaceSession["provisionState"];
	needsResume?: boolean;
	transitioning?: boolean;
	automaticWakePending?: boolean;
	reconnecting?: boolean;
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
	const openElsewhere = Boolean(resumeOpenElsewhere) && controller.state === "stopped" && !terminated;
	const resumeRef = useRef(onResume);
	resumeRef.current = onResume;
	const recheck = openElsewhere && Boolean(onResume) && !resuming && !transitioning && !reconnecting &&
		provisionState !== "provisioning" && provisionState !== "failed";
	useEffect(() => {
		if (!recheck) return;
		const timer = window.setTimeout(() => {
			void Promise.resolve().then(() => resumeRef.current?.()).catch(() => {});
		}, OPEN_ELSEWHERE_RECHECK_MS);
		return () => window.clearTimeout(timer);
	}, [recheck]);
	if (provisionState === "provisioning" || provisionState === "failed") return null;
	const waking = Boolean(resuming && controller.state === "stopped");
	const restarting = Boolean(reconnecting && controller.state === "stopped" && !waking);
	const resumeClick = () => {
		void Promise.resolve().then(() => onResume?.()).catch(() => {});
	};

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


const CHAT_VIRTUALIZE_THRESHOLD = 20;
const WELCOME_FADE_MS = 150;
const INITIAL_SNAP_MS = 1000;
type ReadingPosition = { key: string | number | bigint; offset: number };
const MAX_READING_POSITIONS = 64;
const readingPositions = new Map<string, ReadingPosition>();
function rememberReadingPosition(sessionId: string, position: ReadingPosition | undefined) {
	readingPositions.delete(sessionId);
	if (!position) return;
	readingPositions.set(sessionId, position);
	if (readingPositions.size > MAX_READING_POSITIONS) readingPositions.delete(readingPositions.keys().next().value!);
}
export function forgetReadingPositions() {
	readingPositions.clear();
}
const READER_INTENT_HOLD_MS = 250;
const SETTLE_HOLD_MS = 400;
const SETTLE_SLIDE_MS = 240;
const SETTLE_EASE = "cubic-bezier(0.22, 1, 0.36, 1)";
const RELATCH_AFTER_MS = 1800;
const RELATCH_INTENT_MS = 400;
const relatchBand = (node: HTMLElement) => Math.min(320, Math.round(node.clientHeight * 0.4));
const CHAT_ESTIMATED_TURN_HEIGHT = 600;
const CHAT_TURN_GAP = 18;
const CHAT_INITIAL_VIEWPORT_HEIGHT = 800;
const MINIMAP_TEXT_CLEARANCE_PX = 16;

function minimapCrowdsText(track: HTMLElement, content: HTMLElement): boolean {
	const trackRect = track.getBoundingClientRect();
	const contentRect = content.getBoundingClientRect();
	if (!trackRect.width || !contentRect.width) return false;
	return trackRect.left - contentRect.right < MINIMAP_TEXT_CLEARANCE_PX;
}

export function Timeline({
	onAddToSideChat,
	subagentPanel,
	checkingSideChatAvailability,
	onAddToChat,
	annotations,
	onAnnotationsChange,
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
	composerSends = 0,
	startup,
	arriving = false,
	workerUpdates = false,
	openingStartedAt,
	openingStartupLabel,
	loginLines,
	retryableTurns: chatRetryableTurns,
	controllerStopped = false,
	shownAt,
	findRequest,
	onFindRequestHandled,
}: {
	controllerStopped?: boolean;
	shownAt?: number;
	onAddToSideChat?: ChatWorkspaceProps["onAddToSideChat"];
	subagentPanel?: SubagentPanelHost;
	checkingSideChatAvailability?: boolean;
	onAddToChat?: (excerpt: ChatDraftExcerptReference) => void | Promise<void>;
	annotations?: readonly ChatDraftExcerptReference[];
	onAnnotationsChange?: (annotations: ChatDraftExcerptReference[]) => void | Promise<void>;
	onForeignAnnotation?: (annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number; conversationId?: string }) => void;
	annotationNavigationRef: MutableRefObject<((annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number; activitySequence?: number }) => void) | null>;
	snapshot: ConversationSnapshot;
	assetBaseUrl?: string;
	remoteHost?: boolean;
	findUnavailable?: boolean;
	sideChat?: boolean;
	draftScope: ChatDraftScope;
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
	composerSends?: number;
	startup?: ComponentProps<typeof SessionStartup> & { openingTurnId?: string; label?: string };
	arriving?: boolean;
	workerUpdates?: boolean;
	openingStartedAt?: string;
	openingStartupLabel?: string;
	loginLines?: ReadonlyMap<string, TurnOutcomeLogin>;
	retryableTurns?: ReadonlyMap<string, ConversationTurn>;
	findRequest?: ChatFindRequest;
	onFindRequestHandled?: ChatWorkspaceProps["onFindRequestHandled"];
}) {
	const findSurface = useRef<HTMLDivElement>(null);
	const scroller = useRef<HTMLDivElement>(null);
	const pauseFindFollow = useRef<() => void>(() => {});
	const findScrollWritten = useRef<(top: number) => void>(() => {});
	const findPausedFollow = useRef(false);
	const [findTarget, setFindTarget] = useState<FindOccurrence>();
	const find = useChatFind({ source: sourceSnapshot, root: findSurface, viewport: scroller, hostId: useContext(ChatHostId), unavailable: findUnavailable, sideChat, pauseFollow: () => pauseFindFollow.current(), onScrollWritten: (top) => findScrollWritten.current(top), reveal: (hit) => setFindTarget({ ...hit }) });
	const openFind = useRef(find.openWith);
	openFind.current = find.openWith;
	useEffect(() => {
		if (!findRequest) return;
		openFind.current(findRequest.query, findRequest.messageId);
		onFindRequestHandled?.(findRequest.id);
	}, [findRequest, onFindRequestHandled]);
	const snapshot = find.snapshot;
	const hasTranscript = snapshot.items.length > 0;
	const arrivedEmpty = useRef(false);
	if (arriving && !hasTranscript) arrivedEmpty.current = true;
	const revealTranscript = arrivedEmpty.current && hasTranscript;
	const [entranceGate] = useState(createChatEntranceGate);
	if (shownAt !== undefined && shownAt > entranceGate.watchingSince) entranceGate.watchingSince = shownAt;
	admitArrivals(entranceGate, sourceSnapshot);
	useEffect(() => {
		entranceGate.settled = true;
	}, [entranceGate]);
	const translateDraft = useChatDraftTranslation();
	const uiSessionId = draftScope.sessionId;
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
	const viewportSize = useRef<{ width: number; height: number } | undefined>(undefined);
	const viewportResize = useRef<{ anchor?: { key: string | number | bigint; offset: number } } | undefined>(undefined);
	const promptSpacer = useRef<HTMLDivElement>(null);
	const scrollTrack = useRef<HTMLDivElement>(null);
	const drag = useRef<{
		pointerId: number;
		startY: number;
		startScrollTop: number;
	} | null>(null);
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
	const glideRequested = useRef(false);
	const initialLayoutDone = useRef(false);
	const initialLayoutAt = useRef(0);
	const lastScrollTop = useRef(0);
	const skipRelatchFollow = useRef(false);
	const reducedMotion = useReducedMotion();
	const reducedMotionRef = useRef(reducedMotion);
	reducedMotionRef.current = reducedMotion;
	const spacerTakesResize = useRef(false);
	const followVirtualHeight = useRef<() => void>(() => {});
	const spacerTakes = useCallback((takes: boolean) => {
		spacerTakesResize.current = takes && (promptSpacer.current?.offsetHeight ?? 0) > 0;
		if (spacerTakesResize.current) queueMicrotask(() => { spacerTakesResize.current = false; });
	}, []);
	const setPinnedNow = useCallback((next: boolean) => {
		pinnedRef.current = next;
		setPinned(next);
	}, []);
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
	const showFindWindowAt = find.showWindowAt;
	const requestGlideToEnd = useCallback(() => {
		findPausedFollow.current = false;
		dropFindWindow();
		glideRequested.current = true;
		setPinnedNow(true);
	}, [setPinnedNow, dropFindWindow]);
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
	const selectionComment = useRef("");
	const [editingAnnotation, setEditingAnnotation] = useState<{ excerpt: ChatDraftExcerptReference; range: Range } | null>(null);
	const closeEditingAnnotation = useCallback(() => setEditingAnnotation(null), []);
	const selectionActionRef = useRef(selectionAction);
	selectionActionRef.current = selectionAction;
	const dismissedSelection = useRef<typeof selectionAction>(null);
	const dismissedEscapePress = useRef(false);
	useEffect(() => { dismissedSelection.current = null; clearSelectionAction(); closeEditingAnnotation(); }, [snapshot.conversationId, clearSelectionAction, closeEditingAnnotation]);
	const [annotationTarget, setAnnotationTarget] = useState<{ text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number; activitySequence?: number }>();
	const [annotationNavigationError, setAnnotationNavigationError] = useState<string>();
	const annotationHighlightCleanup = useRef<(() => void) | undefined>(undefined);
	const annotationHighlightTimer = useRef<number | undefined>(undefined);
	const annotationRetryCount = useRef(0);
	const annotationOlderRequestGroups = useRef<number | undefined>(undefined);
	const [revealedActivity, setRevealedActivity] = useState<string>();
	const annotationWindow = useRef<{ sequence: number; done: boolean } | undefined>(undefined);
	const selectAnnotation = useCallback((annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number; activitySequence?: number; conversationId?: string }) => {
		if (onForeignAnnotation && annotation.conversationId && annotation.conversationId !== snapshot.conversationId) {
			onForeignAnnotation(annotation);
			return;
		}
		releaseFollow();
		setAnnotationNavigationError(undefined);
		annotationRetryCount.current = 0;
		annotationOlderRequestGroups.current = undefined;
		annotationWindow.current = undefined;
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
		if ((event?.target as Element | null)?.closest?.("[data-chat-selection-actions], [data-chat-annotation-marker]")) return;
		if (event instanceof KeyboardEvent && !/^(Arrow(Up|Down|Left|Right)|Home|End|PageUp|PageDown|Shift)$/i.test(event.key) &&
			!((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "a")) return;
		const selection = window.getSelection();
		if (event?.type === "pointerup") setEditingAnnotation(null);
		if (!excerptsEnabled || !selection || selection.isCollapsed || selection.rangeCount === 0) {
			if (!selectionComment.current.trim()) setSelectionAction(null);
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

	const addSelectionToChat = useCallback(async (comment: string) => {
		if (!selectionAction) return;
		const excerpt = { ...captureExcerptTurn(selectionAction.excerpt, snapshot), ...(comment ? { comment } : {}) };
		if (onAddToChat) {
			await onAddToChat(excerpt);
			setSelectionAction(null);
			window.getSelection()?.removeAllRanges();
			return;
		}
		const composer = readChatSessionDraft(draftScope).composer;
		if (getChatComposerMutation(draftScope).pending && !composer.delivery) throw new Error("chat.draft.excerptPendingDelivery");
		const current = composer.excerpts ?? [];
		const duplicate = current.find(
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
		const next = duplicate
			? current.map((item) => (item === duplicate && comment ? { ...item, comment } : item))
			: [...current, { ...excerpt, order }];
		const sizeError = excerptSizeError(next);
		if (sizeError) throw new Error(sizeError);
		const result = writeChatExcerptReferences(draftScope, next);
		setDraftPersistenceError(result.ok ? undefined : "chat.draft.saveFailed");
		if (!result.ok) throw new Error("chat.draft.excerptSaveFailed");
		setSelectionAction(null);
		window.getSelection()?.removeAllRanges();
	}, [draftScope, onAddToChat, selectionAction, snapshot]);
	const rewriteAnnotation = useCallback(async (id: string, change: (item: ChatDraftExcerptReference) => ChatDraftExcerptReference | undefined) => {
		const apply = (items: readonly ChatDraftExcerptReference[]) => items.flatMap((item) => {
			if (item.id !== id) return [item];
			const next = change(item);
			return next ? [next] : [];
		});
		if (onAnnotationsChange) {
			await onAnnotationsChange(apply(annotations ?? []));
			setEditingAnnotation(null);
			return;
		}
		const composer = readChatSessionDraft(draftScope).composer;
		if (getChatComposerMutation(draftScope).pending && !composer.delivery) throw new Error("chat.draft.excerptPendingDelivery");
		const result = writeChatExcerptReferences(draftScope, apply(composer.excerpts ?? []));
		setDraftPersistenceError(result.ok ? undefined : "chat.draft.saveFailed");
		if (!result.ok) throw new Error("chat.draft.excerptSaveFailed");
		setEditingAnnotation(null);
	}, [annotations, draftScope, onAnnotationsChange]);
	const openAnnotation = useCallback((excerpt: ChatDraftExcerptReference, range: Range) => {
		selectionComment.current = "";
		setSelectionAction(null);
		window.getSelection()?.removeAllRanges();
		setEditingAnnotation((current) => (current?.excerpt.id === excerpt.id ? null : { excerpt, range }));
	}, []);
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
	const minimapCrowdedRef = useRef(false);
	const turn = activeTurn(snapshot, { controllerStopped });
	const durableTurn = activeTurn(snapshot);
	const [scrollbar, setScrollbar] = useState({
		visible: false,
		top: 0,
		height: 40,
		percent: 0,
		atEnd: true,
		markers: [] as Array<{
			top: number;
			scrollTop: number;
			visible: boolean;
		}>,
	});
	const minimapEnabled = scrollbar.markers.length > 0;
	const chatOpening = useRef(true);
	useEffect(() => {
		chatOpening.current = false;
	}, []);
	const [minimapRevealed, setMinimapRevealed] = useState(false);
	useEffect(() => {
		if (!minimapEnabled || minimapRevealed) return;
		const frame = window.requestAnimationFrame(() => setMinimapRevealed(true));
		return () => window.cancelAnimationFrame(frame);
	}, [minimapEnabled, minimapRevealed]);
	const { t } = useTranslation();
	const openingTurnId = startup?.openingTurnId;
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

	useEffect(() => {
		if (draftPersistenceAvailable && readChatSessionDraft(draftScope).inlineEditDelivery) return;
		setMessageEdit(undefined);
	}, [draftPersistenceAvailable, draftScope, snapshot.activeBranchId, snapshot.sessionId]);
	useLayoutEffect(() => {
		const setCrowded = (crowded: boolean) => {
			minimapCrowdedRef.current = crowded;
			const track = scrollTrack.current;
			if (track) {
				track.dataset.crowded = String(crowded);
				track.setAttribute("aria-hidden", String(crowded));
				track.tabIndex = minimapEnabled && !crowded ? 0 : -1;
				track.classList.toggle("cursor-pointer", minimapEnabled && !crowded);
				track.classList.toggle("pointer-events-none", !minimapEnabled || crowded);
				track.classList.toggle("opacity-100", minimapEnabled && !crowded);
				track.classList.toggle("opacity-0", !minimapEnabled || crowded);
				if (crowded && document.activeElement === track) track.blur();
			}
			if (crowded) {
				drag.current = null;
				if (hoveredMarkerRef.current !== null) setHoveredMarker(null);
			}
		};
		const crowdsText = () => {
			const track = scrollTrack.current;
			const content = scrollContent.current;
			return Boolean(minimapEnabled && track && content && minimapCrowdsText(track, content));
		};
		setCrowded(crowdsText());
		const node = scroller.current;
		if (!minimapEnabled || !node || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(() => {
			const crowded = crowdsText();
			if (crowded !== minimapCrowdedRef.current) setCrowded(crowded);
		});
		observer.observe(node);
		return () => observer.disconnect();
	}, [minimapEnabled]);
	const retryableTurns = useMemo(
		() => chatRetryableTurns && snapshot.turns === sourceSnapshot.turns
			? chatRetryableTurns
			: retryableFailedTurns(snapshot, retrySourceTurnIds(snapshot)),
		[chatRetryableTurns, snapshot, sourceSnapshot.turns],
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
					comment: excerpt.comment,
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
					item.text.trimStart().startsWith("<ao-handoff-request")
				)) &&
					!isModelReroute(item),
			), localEchos),
			...localItems,
		],
		itemKey,
		sameContent,
	);
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
						item.sequence > (entranceGate.floor ?? -Infinity) &&
						!pageHidden() &&
						!(openingTurnId && item.turnId === openingTurnId),
				)
				.map(itemKey),
		);
		seenHumanMessageIds.current = humanMessageKeys;
		lastSeenLatestSequence.current = snapshot.latestSequence;
		if (added.size > 0) setNewHumanMessageIds(added);
	}, [timelineItems, snapshot.latestSequence, openingStartedAt, snapshot.turns]);
	const previousEchoIds = useRef<ReadonlySet<string>>(new Set(localEchos.map((echo) => echo.clientMessageId)));
	const previousComposerSends = useRef(composerSends);
	useLayoutEffect(() => {
		const ids = new Set(localEchos.map((echo) => echo.clientMessageId));
		const sent = [...ids].some((id) => !previousEchoIds.current.has(id)) || composerSends !== previousComposerSends.current;
		previousEchoIds.current = ids;
		previousComposerSends.current = composerSends;
		if (!sent) return;
		requestGlideToEnd();
	}, [composerSends, localEchos, requestGlideToEnd]);
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
		const dropped = new Set([...hiddenTurns, ...snapshot.turns.filter((turn) => turn.rolledBack).map((turn) => turn.id)]);
		const kept = dropped.size > 0 ? timelineItems.map((item) => outlivingAction(item, dropped)) : timelineItems;
		const items = atCarrier(kept, snapshot.batchedDeliveries ?? [], batchedReports.moved);
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
	const subagentPanelEnabled = subagentPanel !== undefined;
	const subagents = useMemo(
		() => (subagentPanelEnabled ? conversationSubagents(groups) : NO_SUBAGENTS),
		[groups, subagentPanelEnabled],
	);
	const [openSubagentId, setOpenSubagentId] = useState<string>();
	const closeSubagent = useCallback(() => setOpenSubagentId(undefined), []);
	const hasSubagents = subagents.length > 0;
	const reportSubagents = subagentPanel?.onAvailableChange;
	useEffect(() => {
		if (!reportSubagents) return;
		reportSubagents(hasSubagents);
		return () => reportSubagents(false);
	}, [hasSubagents, reportSubagents]);
	const revealSubagents = subagentPanel?.reveal;
	const openSubagent = useMemo(
		() => revealSubagents
			? (launchId: string) => {
				setOpenSubagentId(launchId);
				revealSubagents();
			}
			: undefined,
		[revealSubagents],
	);
	const [liveStepChoices] = useState(() => new Map<string, boolean>());
	const workerUpdateIndex = useWorkerUpdateIndex(workerUpdates ? snapshot.items : undefined);
	const openingSetup = !startup && openingPending && turn?.state !== "running"
		? { failed: false, steps: [], agentName: agentLabel(snapshot.harness), label: openingStartupLabel }
		: undefined;
	const rowStartup = startup ?? openingSetup;
	const startupGroupKey = rowStartup && (openingSetup || showStartup)
		? groups.find((group) => (openingTurnId ? group.turnId === openingTurnId : group.live))?.key
		: undefined;
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
		useFlushSync: false,
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
		scrollEndThreshold: pinned ? 1 : -1,
		followOnAppend: false,
		observeElementRect: (instance, callback) => observeElementRect(instance, (rect) => {
			captureViewportResize(instance);
			callback(rect.height ? rect : { width: rect.width, height: CHAT_INITIAL_VIEWPORT_HEIGHT });
		}),
		measureElement: (element, entry, instance) => {
			captureViewportResize(instance);
			const index = Number(element.getAttribute("data-index"));
			const group = groups[index];
			if (group && !measuredVirtualGroups.current.has(group)) {
				measuredVirtualGroups.current.add(group);
				pendingVirtualLayout.current = true;
			}
			spacerTakes(pinnedRef.current && index >= groups.findLastIndex(groupHasHumanPrompt));
			return measureElement(element, entry, instance) || CHAT_ESTIMATED_TURN_HEIGHT;
		},
		scrollToFn: (offset, { adjustments = 0 }, instance) => {
			if (!instance.scrollElement) return;
			if (viewportResize.current) return;
			if (isFlying()) {
				reaim();
				return;
			}
			if (spacerTakesResize.current && adjustments !== 0) return;
			instance.scrollElement.scrollTop = offset + adjustments;
			markWritten(instance.scrollElement.scrollTop);
		},
		rangeExtractor: (range) => {
			const indexes = defaultRangeExtractor(range);
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
		if (mounted) { setFindTarget(undefined); return; }
		const disclosureKey = `${groups[index].key}:worked`;
		if (!activityDisclosureOverrides[disclosureKey]) setActivityDisclosureOverrides((current) => ({ ...current, [disclosureKey]: true }));
		if (virtualized) virtualizer.scrollToIndex(index, { align: "center" });
	}, [find.open, find.engaged, find.active, findTarget, groups, virtualRows, virtualized, virtualizer, activityDisclosureOverrides]);
	renderedVirtualHeight.current = virtualHeight;
	const findGapGroupKey = find.gapBeforeSequence === undefined ? undefined
		: groups.find((group) => group.items.some((item) => item.sequence >= find.gapBeforeSequence!))?.key;
	const latestForkTurnId = useMemo(() => {
		for (let i = groups.length - 1; i >= 0; i--) {
			const group = groups[i];
			if (forkableGroup(group)) return group.turnId as string;
		}
		return undefined;
	}, [groups]);
	const renderedGroups = virtualized
		? virtualRows.map((row) => ({ group: groups[row.index]!, row, index: row.index }))
		: groups.map((group, index) => ({ group, row: undefined, index }));
	useEffect(() => {
		if (!annotationTarget) return;
		const activitySequence = annotationTarget.activitySequence;
		if (activitySequence !== undefined) {
			const toTurn = () => setAnnotationTarget((current) => current && { ...current, activitySequence: undefined });
			const retry = () => setAnnotationTarget((current) => current && { ...current });
			const index = groups.findIndex((group) => group.items.some((item) => item.kind === "activity" && item.sequence === activitySequence));
			if (index < 0) {
				const request = annotationWindow.current;
				if (request?.sequence === activitySequence) return request.done ? toTurn() : undefined;
				const pending = { sequence: activitySequence, done: false };
				annotationWindow.current = pending;
				void showFindWindowAt(activitySequence).then(() => {
					pending.done = true;
					retry();
				});
				return;
			}
			const activity = groups[index]!.items.find((item) => item.kind === "activity" && item.sequence === activitySequence)!;
			setRevealedActivity(activity.id);
			const owner = owningSubagent(groups, activity.id);
			const inPanel = Boolean(owner && openSubagent);
			const holder = owner?.groupIndex ?? index;
			if (owner && inPanel) {
				if (openSubagentId !== owner.launchId) openSubagent!(owner.launchId);
			} else {
				const keys = [`${groups[holder]!.key}:worked`];
				if (owner) keys.push(owner.teamKey, `${owner.teamKey}:agent:${owner.launchId}`);
				if (keys.some((key) => !activityDisclosureOverrides[key])) {
					setActivityDisclosureOverrides((current) => ({ ...current, ...Object.fromEntries(keys.map((key) => [key, true])) }));
				}
			}
			const rowRoot = inPanel ? subagentPanel?.container : scrollContent.current;
			const row = Array.from(rowRoot?.querySelectorAll<HTMLElement>("[data-chat-activity-id]") ?? [])
				.find((element) => element.dataset.chatActivityId === activity.id);
			const range = row ? annotationReferenceRange(row, annotationTarget) : undefined;
			if (!row || !range) {
				if (!row && virtualized && !inPanel) virtualizer.scrollToIndex(holder, { align: "center" });
				if (annotationRetryCount.current++ >= (row ? 5 : 20)) {
					if (!row) return toTurn();
				} else {
					const retryTimer = window.setTimeout(retry, 80);
					return () => window.clearTimeout(retryTimer);
				}
			}
			const frame = requestAnimationFrame(() => {
				window.clearTimeout(annotationHighlightTimer.current);
				annotationHighlightCleanup.current?.();
				annotationHighlightCleanup.current = highlightChatAnnotation(row!, annotationTarget.text);
				const scrollerNode = scroller.current;
				const view = scrollerNode?.getBoundingClientRect();
				if (inPanel) {
					row!.scrollIntoView({ block: "center", behavior: "auto" });
				} else if (scrollerNode && view) {
					const delta = activityRevealScrollDelta((range ?? row!).getBoundingClientRect(), view, Boolean(range));
					if (delta !== 0) scrollerNode.scrollTo({ top: scrollerNode.scrollTop + delta, behavior: "auto" });
				}
				annotationHighlightTimer.current = window.setTimeout(() => {
					annotationHighlightCleanup.current?.();
					setRevealedActivity(undefined);
				}, 2200);
				setAnnotationTarget(undefined);
			});
			return () => cancelAnimationFrame(frame);
		}
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
		const frame = requestAnimationFrame(() => {
			window.clearTimeout(annotationHighlightTimer.current);
			annotationHighlightCleanup.current?.();
			annotationHighlightCleanup.current = highlightChatAnnotation(source, annotationTarget.text, annotationTarget.revision, annotationTarget.snapshot);
			const scrollerNode = scroller.current;
			const diagram = annotationDiagramBlock(source, annotationTarget);
			let range = annotationReferenceRange(source, annotationTarget);
			if (range && annotationRetryCount.current < 20 && revealFoldedRange(range)) {
				annotationRetryCount.current++;
				setAnnotationTarget((current) => current ? { ...current } : current);
				return;
			}
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
					range = annotationReferenceRange(source, annotationTarget);
					if (range) {
						const nextRect = range.getBoundingClientRect();
						const delta = nextRect.top + nextRect.height / 2 - (scrollerRect.top + scrollerRect.height / 2);
						scrollerNode.scrollTo({ top: scrollerNode.scrollTop + delta, behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
					}
				}
			} else if (scrollerNode) {
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
	}, [annotationTarget, groups, hasOlder, loadingOlder, onLoadOlder, virtualized, virtualizer, virtualRows, activityDisclosureOverrides, showFindWindowAt, openSubagent, openSubagentId, subagentPanel?.container]);
	useEffect(() => {
		const node = scroller.current;
		const content = virtualized ? virtualContent.current
			: virtualContent.current?.querySelector<HTMLElement>('[data-index="0"]');
		if (node && content) {
			setVirtualScrollMargin(content.getBoundingClientRect().top - node.getBoundingClientRect().top + node.scrollTop);
		}
	}, [virtualized, hasOlder]);

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

	const followOrSnapToEnd = useCallback((node: HTMLElement) => {
		if (viewportResize.current && (glideRequested.current || isFlying())) {
			reaim();
			return;
		}
		const now = performance.now();
		const hidden = pageHidden();
		const opening = !initialLayoutDone.current || now - initialLayoutAt.current < INITIAL_SNAP_MS;
		if (viewportResize.current || opening || now - entranceGate.catchUpAt < INITIAL_SNAP_MS || hidden) {
			if (!viewportResize.current && !opening && isFlying()) {
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

	const syncStreamedGrowth = useCallback(() => {
		if (!pinnedRef.current || !initialLayoutDone.current) return;
		anchorGeometry.current = null;
		const node = scroller.current;
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

	const settleHold = useRef<{ observer: ResizeObserver; timer: number } | undefined>(undefined);
	const releaseSettleHold = useCallback(() => {
		const hold = settleHold.current;
		if (!hold) return;
		hold.observer.disconnect();
		window.clearTimeout(hold.timer);
		settleHold.current = undefined;
	}, []);
	useEffect(() => releaseSettleHold, [releaseSettleHold]);
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
	const restoreSettle = useCallback((settle: SettleAnchor) => {
		const node = scroller.current;
		if (!node) return;
		const { turn, rows, index, offset } = settle;
		const liveRows = rows.filter((row) => row.isConnected && row.hasAttribute("data-turn-live-row"));
		for (const row of liveRows) row.style.display = "none";
		const wrapper = turn.parentElement;
		const virtualIndex = Number(wrapper?.dataset.index);
		if (virtualized && wrapper && Number.isInteger(virtualIndex)) {
			spacerTakes(settle.following);
			virtualizer.resizeItem(virtualIndex, Math.round(wrapper.getBoundingClientRect().height));
			const total = virtualizer.getTotalSize();
			if (virtualContent.current) virtualContent.current.style.height = `${total}px`;
			renderedVirtualHeight.current = total;
		}
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
		if (reducedMotionRef.current || pageHidden() || typeof Element.prototype.animate !== "function") return;
		rows.forEach((row, at) => {
			if (row === held || !row.isConnected || row.parentElement !== turn) return;
			const delta = settle.tops[at]! - row.getBoundingClientRect().top;
			if (Math.abs(delta) < 1) return;
			const from = `translateY(${delta}px)`;
			if (row.hasAttribute("data-turn-live-row")) {
				row.animate([{ transform: from }, { transform: from }], { duration: SETTLE_SLIDE_MS, fill: "forwards" });
				return;
			}
			row.animate([{ transform: from }, { transform: "none" }], { duration: SETTLE_SLIDE_MS, easing: SETTLE_EASE });
		});
	}, [virtualized, virtualizer, spacerTakes, syncPromptSpacer, markWritten, updateScrollbar, releaseSettleHold]);

	const syncScrollMetrics = useCallback(() => {
		anchorGeometry.current = null;
		updateScrollbar();
	}, [updateScrollbar]);

	useEffect(() => {
		syncScrollLayout();
	}, [pinned, snapshot.latestSequence, groups.length, messageEdit?.turnId, syncScrollLayout]);
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

	const relatchTimer = useRef<number | undefined>(undefined);
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
		if (!isOwnScroll(top)) releaseSettleHold();
		if (isOwnScroll(top) || findPausedFollow.current) {
		} else if (distance >= 64 || (movedUp && distance > 2)) {
			if (pinnedRef.current) releaseFollow();
			const reading = relatchTimer.current !== undefined || performance.now() - readerIntentAt.current < RELATCH_INTENT_MS;
			if (reading && distance <= relatchBand(node)) scheduleRelatch();
			else clearRelatch();
		} else if (!pinnedRef.current && !movedUp) {
			clearRelatch();
			skipRelatchFollow.current = true;
			setPinnedNow(true);
		}
		if (movedUp && !isOwnScroll(top) && top < node.clientHeight && hasOlder && !loadingOlder) onLoadOlder?.();
		if (initialLayoutDone.current && !pendingReading.current) {
			const row = pinnedRef.current ? undefined : virtualizer.getVirtualItemForOffset(top);
			rememberReadingPosition(uiSessionId, row ? { key: row.key, offset: row.start - top } : undefined);
		}
		updateScrollbar();
	}

	function onViewportWheel(event: ReactWheelEvent<HTMLDivElement>) {
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
		if (!minimapEnabled || minimapCrowdedRef.current) return;
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
		if (!minimapEnabled || minimapCrowdedRef.current) return;
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
		if (!minimapEnabled || minimapCrowdedRef.current) return;
		const node = scroller.current;
		if (!node) return;
		event.preventDefault();
		node.scrollTop += event.deltaY;
		onScroll();
	}

	function onScrollbarKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
		cancelScrollFollow();
		if (!minimapEnabled || minimapCrowdedRef.current) return;
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
		<OpenSubagentContext.Provider value={openSubagent}>
		{subagentPanel?.container ? createPortal(
			<RevealedChatActivity.Provider value={revealedActivity}>
			<SubagentPanel
				subagents={subagents}
				openId={openSubagentId}
				onOpen={setOpenSubagentId}
				onBack={closeSubagent}
				harness={snapshot.harness}
				models={subagentPanel.models}
				disclosureOverrides={activityDisclosureOverrides}
				onDisclosureChange={onActivityDisclosureChange}
			/>
			</RevealedChatActivity.Provider>,
			subagentPanel.container,
		) : null}
		<ChatEntrance.Provider value={entranceGate}>
		<RevealedChatActivity.Provider value={revealedActivity}>
		<LiveStepChoices.Provider value={liveStepChoices}>
		<div
			ref={findSurface}
			data-chat-find-surface=""
			className="relative flex min-h-0 flex-1 flex-col"
			data-testid="chat-timeline"
			style={{ contain: "layout paint" }}
		>
			{find.bar}
			{selectionAction ? <ChatSelectionActions key={selectionAction.excerpt.id} selection={selectionAction}
				viewportRef={scroller} onInvalid={clearSelectionAction} onAdd={addSelectionToChat} commentRef={selectionComment}
				onSide={onAddToSideChat ? excerpt => onAddToSideChat(captureExcerptTurn(excerpt, snapshot)) : undefined} checkingSide={checkingSideChatAvailability} /> :
				editingAnnotation ? <ChatSelectionActions key={`edit-${editingAnnotation.excerpt.id}`} selection={editingAnnotation}
					viewportRef={scroller} onInvalid={closeEditingAnnotation}
					onAdd={(comment) => rewriteAnnotation(editingAnnotation.excerpt.id, (item) => ({ ...item, comment: comment || undefined }))}
					onRemove={() => rewriteAnnotation(editingAnnotation.excerpt.id, () => undefined)} /> : null}
			{!excerptsEnabled ? null : onAddToChat ? (
				<ChatAnnotationMarkers markers={numberedReferences(annotations ?? [])} conversationId={snapshot.conversationId}
					contentRef={scrollContent} viewportRef={scroller} activeId={editingAnnotation?.excerpt.id} onOpen={openAnnotation} />
			) : (
				<DraftAnnotationMarkers draftScope={draftScope} conversationId={snapshot.conversationId}
					contentRef={scrollContent} viewportRef={scroller} activeId={editingAnnotation?.excerpt.id} onOpen={openAnnotation} />
			)}
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
								<LatestForkTurnContext.Provider value={latestForkTurnId}>
								<ForkableTurnContext.Provider value={forkableGroup(group) ? (group.turnId as string) : null}>
								<TurnGroup
									group={group}
									harness={snapshot.harness}
									login={group.turnId && group.outcome?.state === "failed" && group.outcome.failureCode === "auth_required"
										? loginLines?.get(group.turnId)
										: undefined}
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
									canRollback={Boolean(onRollback && group.turnId && group.rollbackable)}
									rollbackDisabled={rollbackDisabled}
									busy={turnShowsApproval(group) ? busy : undefined}
									queued={Boolean(group.turnId && queued.has(group.turnId))}
								/>
								</ForkableTurnContext.Provider>
								</LatestForkTurnContext.Provider>
								</BatchedReportsContext.Provider>
								</WorkerUpdateIndexContext.Provider>
							</div>
						);
					})}
					</div>
					{rowStartup && (openingSetup || showStartup) && !startupGroupKey ? (
						<LiveResponseStatus startupLabel={rowStartup.label ?? t(rowStartup.failed ? "chat.startup.failed" : "chat.startup.running")} failed={rowStartup.failed}>
							{rowStartup.steps.length > 0 || rowStartup.failed ? <SessionStartup {...rowStartup} /> : null}
						</LiveResponseStatus>
					) : turn?.state === "running" && !groups.some((group) => group.turnId === turn.id) ? (
						<LiveResponseStatus startedAt={turn.startedAt ?? turn.requestedAt} task="Thinking" />
					) : openingPending && !groups.some((group) => group.live) ? (
						<LiveResponseStatus startedAt={openingStartedAt} startupLabel={openingStartupLabel} />
					) : null}
					{messageEdit && !editedMessageVisible ? (
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
					data-crowded={minimapCrowdedRef.current}
					tabIndex={minimapEnabled && !minimapCrowdedRef.current ? 0 : -1}
					aria-hidden={minimapCrowdedRef.current}
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
						if (!minimapEnabled || minimapCrowdedRef.current || scrollbar.markers.length === 0) return;
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
						minimapEnabled && !minimapCrowdedRef.current
							? "cursor-pointer opacity-100"
							: "pointer-events-none opacity-0",
						"data-[crowded=true]:pointer-events-none data-[crowded=true]:opacity-0",
					)}
				>
					<div className="absolute inset-0 cursor-grab group-active/scroll:cursor-grabbing">
						{minimapEnabled
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
		</RevealedChatActivity.Provider>
		</ChatEntrance.Provider>
		</OpenSubagentContext.Provider>
		</ChatOpeningRef.Provider>
	);
}

const LIVE_STEP_MIN_MS = 900;

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
	login,
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
	canRollback: boolean;
	rollbackDisabled: boolean;
	retry?: TurnOutcomeRetryControl;
	login?: TurnOutcomeLogin;
	busy?: boolean;
	queued: boolean;
	newHumanMessageIds: ReadonlySet<string>;
	startup?: ComponentProps<typeof SessionStartup> & { label?: string };
	onSelectAnnotation?: (annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number }) => void;
	workerUpdates?: boolean;
}) {
	const hostId = useContext(ChatHostId);
	const batched = useContext(BatchedReportsContext);
	const { t } = useTranslation();
	const reducedMotion = useReducedMotion();
	const { steps: subagentSteps, live: subagentsLive, stopped: subagentsStopped } = groupSubagents(group);
	const runs = useMemo(
		() => runsOf(turnFlowItems(group)),
		// eslint-disable-next-line react-hooks/exhaustive-deps -- turnFlowItems reads only these fields of the group.
		[group.items, group.subagents, group.liveProviderFailure, group.outcome?.state, group.outcome?.error],
	);
	const lastAssistantId = [...group.items]
		.reverse()
		.find((item) => item.kind === "message" && item.role === "assistant" && item.text.trim() !== "")?.id;
	const copyableMessageId = group.outcome
		? lastAssistantId
		: group.live && group.items.at(-1)?.id === lastAssistantId
			? lastAssistantId
			: undefined;
	const endsWithOutcome = Boolean(group.outcome && group.outcome.state !== "completed" && !group.accountSwitched);
	const loginNoteMessageId = login?.align === "end"
		? group.items.find((item) => item.kind === "message" && item.role === "user" && item.origin === "human")?.id
		: undefined;
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
	const firstWorkIndex = runs.findIndex((run) => !isHumanRun(run.items[0]));
	const leadingHumanCount = firstWorkIndex < 0 ? runs.length : firstWorkIndex;
	const humanRuns = runs.slice(0, leadingHumanCount);
	const workedRuns = runs.filter(
		(_run, index) => index >= leadingHumanCount && index !== finalAssistantRunIndex,
	);
	const finalRun = finalAssistantRunIndex >= 0 ? runs[finalAssistantRunIndex] : undefined;
	const teamWorking = (run: TimelineRun) =>
		run.kind === "subagents" &&
		run.items.some((launch) => readSubagent(launch, subagentSteps.get(launch.id) ?? [], subagentsLive.has(launch.id), subagentsStopped).state === "working");
	const noticeRuns = group.outcome
		? workedRuns.filter((run) => isHumanRun(run.items[0]) || isNoticeRun(run) || teamWorking(run))
		: [];
	const foldedRuns = workedRuns.filter((run) => !noticeRuns.includes(run) && !rendersNothing(run));
	const onlyRead = workerUpdates && foldedRuns.length === 0 && !finalRun && isWorkerLead(group.items[0]);
	const currentTask = group.live
		? [...group.items]
				.reverse()
				.find(
					(item): item is ConversationActivity =>
						item.kind === "activity" &&
						item.status === "running" &&
						!group.subagents?.stepIds.has(item.id) &&
						item.activityKind !== "plan" &&
						item.activityKind !== "approval" &&
						item.activityKind !== "user_input" &&
						item.detail?.event !== "provider.failure",
				)
		: undefined;
	const liveStep = useLiveRowStep(group, Boolean(startup || group.liveStartupLabel || group.liveProviderFailure));
	const liveTask = liveStep ?? currentTask;
	const renderRun = (run: TimelineRun) => {
		if (run.kind === "subagents") {
			return (
				<SubagentTeam
					key={run.key}
					launches={run.items}
					steps={subagentSteps}
					live={subagentsLive}
					stopped={subagentsStopped}
					harness={harness}
					disclosureKey={`${group.key}:${run.key}`}
					disclosureOverrides={activityDisclosureOverrides}
					onDisclosureChange={onActivityDisclosureChange}
				/>
			);
		}
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
			status={run.items[0]?.id === loginNoteMessageId && login
				? <LoginStatusNote status={login.status} {...(login.ownRetry && retry ? { retry } : {})} />
				: undefined}
		/>
	);
	const reportsLine = (key: string, updates: readonly WorkerUpdate[]) => (
		<ReportsLine
			key={key}
			updates={updates}
			open={Boolean(activityDisclosureOverrides[key])}
			onOpenChange={(open) => onActivityDisclosureChange(key, open)}
		/>
	);
	let drewReportsUnder = false;
	const renderWorkerRun = (run: TimelineRun): ReactNode | ReactNode[] => {
		const item = run.kind === "single" ? run.items[0] : undefined;
		if (!workerUpdates || !item) return renderRun(run);
		const updates = workerUpdatesOf(item);
		if (updates.length > 0 && updates[0]?.state === undefined) {
			const rows = <WorkerUpdateRows key={run.key} updates={updates} />;
			const carried = batched?.under.get(item.id) ?? [];
			if (carried.length === 0) return rows;
			drewReportsUnder = true;
			return [rows, reportsLine(`${run.key}:reports`, carried)];
		}
		if (updates.length > 0) return reportsLine(run.key, updates);
		const namedTitle = isTaskTitleUpdate(item) ? renamedTitle(group.items) : undefined;
		if (namedTitle) return <TaskNamedLine key={run.key} title={namedTitle} />;
		return renderRun(run);
	};
	const body: ReactNode[] = humanRuns.flatMap(renderWorkerRun);
	const tail = workedRuns.at(-1);
	const tailIsReply = group.live && tail?.kind === "single" && tail.items[0].kind === "message" && tail.items[0].role === "assistant";
	const reportsUnderPrompt = drewReportsUnder;
	const workingRow =
		startup || group.live ? (
			<LiveResponseStatus
				entranceKey={`live:${group.key}`}
				startedAt={group.liveStartedAt}
				startupLabel={startup ? startup.label ?? t(startup.failed ? "chat.startup.failed" : "chat.startup.running") : group.liveStartupLabel}
				failed={startup?.failed}
				task={liveTask ? liveTaskLabel(liveTask) : group.live && !group.blocked && !tailIsReply ? "Thinking" : undefined}
				step={liveStep}
				reconnect={group.liveProviderFailure ? reconnectCopy(group.liveProviderFailure) : undefined}
			>
				{startup && (startup.steps.length > 0 || startup.failed) ? <SessionStartup {...startup} /> : null}
			</LiveResponseStatus>
		) : null;
	const outcome = group.outcome;
	const disclosureKey = `${group.key}:worked`;
	const workedDurationMs = outcome?.durationMs !== undefined && outcome.durationMs > 0 ? outcome.durationMs : undefined;
	const spawned = workerUpdates && outcome ? spawnedSessionIds(group.items) : [];
	const spawnedLine = spawned.length > 0 ? <SpawnedAgents key="spawned" sessionIds={spawned} /> : null;
	const hasFoldedWork = foldedRuns.length > 0 || spawnedLine !== null;
	const foldedWork = (): ReactNode[] => [...foldedRuns.flatMap(renderWorkerRun), ...(spawnedLine ? [spawnedLine] : [])];
	const showsWorked = Boolean(
		outcome && !onlyRead && !loginNoteMessageId && (hasFoldedWork || workedDurationMs !== undefined),
	);
	const promptBubble = group.items.some(
		(item) => item.kind === "message" && item.role === "user" && item.origin === "human" && !item.continuation && !relayedVia(item),
	);
	const reservesActionRow = promptBubble && !reportsUnderPrompt && !(group.turnId && onActivateBranch && branchPoints.get(group.turnId));
	if (!showsWorked && reservesActionRow) {
		body.push(<div key="turn-worked-gap" aria-hidden="true" className="h-7 shrink-0" data-turn-worked-gap="" />);
	}
	body.push(
		<AnimatePresence key="turn-worked" initial={false}>
			{outcome && showsWorked ? (
				<motion.div
					key="turn-worked-summary"
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
	if (!outcome) body.push(...workedRuns.flatMap(renderWorkerRun));
	if (outcome) body.push(...noticeRuns.flatMap(renderWorkerRun));
	if (finalRun) body.push(renderRun(finalRun));
	return (
		<div className="relative flex min-w-0 flex-col gap-2.5">
			{body}
			<AnimatePresence initial={false} mode="popLayout">
				{workingRow ? (
					<motion.div
						key="turn-working"
						exit={{ opacity: 0 }}
						transition={{ duration: reducedMotion ? 0 : 0.12, ease: [0.22, 1, 0.36, 1] }}
						className={cn("overflow-hidden", tailIsReply && "-mt-1.5")}
						data-turn-live-row=""
					>
						{workingRow}
					</motion.div>
				) : null}
			</AnimatePresence>
			{group.plan ? <TurnPlan plan={group.plan} live={group.live} /> : null}
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
			{!copyableMessageId && !group.live && !endsWithOutcome && canRollback ? (
				<TurnActions
					onRollback={() => onRollback(group.turnId as string)}
					rollbackDisabled={rollbackDisabled}
				/>
			) : null}
			{group.outcome && group.outcome.state !== "completed" && !group.accountSwitched && !loginNoteMessageId ? (
				<TurnOutcome
					state={group.outcome.state}
					error={group.outcome.error}
					failureCode={group.outcome.failureCode}
					onConfigure={harness === "deepseek-harness" && group.outcome.failureCode === "provider_key_missing"
						? () => useUiStore.getState().openGlobalSettings("harness", { focusAgentId: harness, ...(hostId ? { hostId } : {}), harnessView: "local", preserveProject: true, startLogin: true })
						: undefined}
					login={login}
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
	status,
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
	queued?: boolean;
	newHumanMessageIds: ReadonlySet<string>;
	showCopy?: boolean;
	live?: boolean;
	onRollback?: () => void;
	rollbackDisabled?: boolean;
	onSelectAnnotation?: (annotation: { text: string; snapshot?: ChatExcerptSnapshot; messageId?: string; revision?: number }) => void;
	status?: ReactNode;
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
		if (item.origin === "human" && item.continuation) return <ContinuedMarker />;
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
					status={status}
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
	if (isSteer(item)) {
		const annotations = steeredBrowserFeedback(item);
		return (
			<FromAgent item={item}>
				{annotations ? (
					<SteeredBrowserFeedback annotations={annotations} />
				) : (
					<SteerMessage activity={item} sessionId={sessionId} apiBaseUrl={apiBaseUrl} onSelectAnnotation={onSelectAnnotation} />
				)}
			</FromAgent>
		);
	}
	if (item.activityKind === "plan") {
		const plan = activityPlan(item);
		return plan ? <TurnPlan plan={plan} /> : <ActivityRow activity={item} />;
	}
	return <ActivityRow activity={item} />;
}


const itemKey = (item: ConversationItem): string =>
	item.kind === "message" && item.role === "user" && item.clientMessageId
		? `client:${item.clientMessageId}`
		: item.id;

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

function echoMatchesItem(
	echo: ConversationLocalEcho,
	item: ConversationItem,
	backgroundQueuedTurnIds: ReadonlySet<string>,
): boolean {
	if (item.kind !== "message" || item.role !== "user" || item.origin !== "human") return false;
	if (echo.opening) return true;
	if (echo.turnId && backgroundQueuedTurnIds.has(echo.turnId) && item.turnId === echo.turnId) return false;
	if (item.clientMessageId) return item.clientMessageId === echo.clientMessageId;
	if (echo.turnId) return item.turnId === echo.turnId;
	return item.text === echo.text && item.createdAt >= echo.createdAt;
}

function isHumanSteer(item: ConversationItem): item is ConversationActivity {
	return item.kind === "activity" && isSteer(item) && !item.detail?.senderSessionId &&
		(item.detail?.origin ?? "human") === "human";
}

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
		const body = stagedAttachmentParts(humanVisibleText(text)).body;
		if (body.trim() && body !== history.at(-1)) history.push(body);
		return history;
	}, []);
}
const groupKey = (group: TimelineGroup): string => group.key;

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
		sameSubagents(a.subagents, b.subagents) &&
		a.items.length === b.items.length &&
		a.items.every((item, index) => item === b.items[index])
	);
}

function sameSubagents(a: GroupSubagents | undefined, b: GroupSubagents | undefined): boolean {
	if (a === b) return true;
	if (!a || !b || a.stopped !== b.stopped || a.live.size !== b.live.size || a.steps.size !== b.steps.size || a.stepIds.size !== b.stepIds.size) return false;
	for (const id of a.live) if (!b.live.has(id)) return false;
	for (const id of a.stepIds) if (!b.stepIds.has(id)) return false;
	for (const [launchId, steps] of a.steps) {
		const other = b.steps.get(launchId);
		if (other?.length !== steps.length || steps.some((step, index) => step !== other[index])) return false;
	}
	return true;
}

type SteerEcho = { clientMessageId: string; text: string; createdAt: string; turnId: string; sentAfterSequence: number; acceptedAfterSequence?: number };

function steerEchoArrived(item: ConversationItem, echo: SteerEcho): boolean {
	if (item.kind !== "activity" || item.detail?.event !== "steer") return false;
	return item.detail.clientMessageId
		? item.detail.clientMessageId === echo.clientMessageId
		: item.sequence > echo.sentAfterSequence && (item.detail.text ?? item.summary) === echo.text;
}

function useStableCallback<Args extends unknown[], Result>(
	fn: ((...args: Args) => Result) | undefined,
): (...args: Args) => Result | undefined {
	const latest = useRef(fn);
	useEffect(() => {
		latest.current = fn;
	});
	return useCallback((...args: Args) => latest.current?.(...args), []);
}

function useStableValue<T>(value: T): T {
	const previous = useRef(value);
	if (!sameContent(previous.current, value)) previous.current = value;
	return previous.current;
}

function forkableGroup(group: TimelineGroup): boolean {
	if (!group.turnId || group.live) return false;
	const state = group.outcome?.state;
	return state === "completed" || state === "recovered";
}

type SettleAnchor = {
	turn: HTMLElement;
	rows: HTMLElement[];
	tops: number[];
	scrollTop: number;
	following: boolean;
	index: number;
	offset: number;
};

const LOGIN_RETRY_WINDOW_MS = 60_000;

type TimelineGroup = {
	key: string;
	turnId?: string;
	anchor: number;
	items: ConversationItem[];
	outcome?: {
		state: "completed" | "recovered" | "interrupted" | "failed" | "unknown" | "not_run";
		durationMs?: number;
		error?: string;
		usageLimit?: ConversationUsageLimit;
		failureCode?: ConversationTurn["failureCode"];
	};
	diff?: TurnDiff;
	artifacts?: TurnArtifactChange[];
	plan?: ConversationPlan;
	live?: boolean;
	liveStartedAt?: string;
	liveStartupLabel?: string;
	blocked?: boolean;
	liveProviderFailure?: ConversationActivity;
	rollbackable?: boolean;
	accountSwitched?: boolean;
	subagents?: GroupSubagents;
};

type GroupSubagents = {
	steps: ReadonlyMap<string, ConversationActivity[]>;
	stepIds: ReadonlySet<string>;
	live: ReadonlySet<string>;
	stopped: boolean;
};

const NO_SUBAGENT_STEPS: ReadonlyMap<string, ConversationActivity[]> = new Map();
const NO_LIVE_SUBAGENTS: ReadonlySet<string> = new Set();
const NO_STEPS: readonly ConversationActivity[] = [];
const NO_SUBAGENTS: readonly SubagentRef[] = [];

function groupSubagents(group: TimelineGroup): { steps: ReadonlyMap<string, ConversationActivity[]>; live: ReadonlySet<string>; stopped: boolean } {
	return {
		steps: group.subagents?.steps ?? NO_SUBAGENT_STEPS,
		live: group.subagents?.live ?? NO_LIVE_SUBAGENTS,
		stopped: group.subagents?.stopped ?? false,
	};
}

function conversationSubagents(groups: readonly TimelineGroup[]): SubagentRef[] {
	return groups.flatMap((group) => {
		const { steps, live, stopped } = groupSubagents(group);
		return group.items.flatMap((item) =>
			isSubagentLaunch(item) && !group.subagents?.stepIds.has(item.id)
				? [{ launch: item, steps: steps.get(item.id) ?? NO_STEPS, live: live.has(item.id), stopped }]
				: [],
		);
	});
}

type GroupPreview = { title: string; detail?: string };

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
	return Math.max(1_000, completedAt - startedAt);
}

function retryableFailedTurns(snapshot: ConversationSnapshot, consumed: ReadonlySet<string>): Map<string, ConversationTurn> {
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
					!consumed.has(turn.id) &&
					(humanPromptTurnIds.has(turn.id) ||
						Boolean((turn.usageLimit || turn.failureCode === "auth_required") && turn.hasHumanPrompt)),
			)
			.map((turn) => [turn.id, turn]),
	);
}

function useChatLogin({ snapshot, retryable, retryControl, hostId, cloud, newWorkDisabled }: {
	snapshot: ConversationSnapshot;
	retryable: ReadonlyMap<string, ConversationTurn>;
	retryControl?: ChatRetryControl;
	hostId?: string;
	cloud: boolean;
	newWorkDisabled: boolean;
}): { band?: ChatLoginBandProps; lines: ReadonlyMap<string, TurnOutcomeLogin>; locked: boolean; logIn: () => void } {
	const workedTurnIds = useMemo(() => turnsThatDidWork(snapshot), [snapshot]);
	const latest = snapshot.turns.findLast((entry) => !entry.rolledBack && entry.state !== "queued" && entry.state !== "cancelled");
	const switchedAway = useMemo(() => {
		if (!latest) return false;
		let lastSwitch = -Infinity;
		let latestEnd = -Infinity;
		for (const item of snapshot.items) {
			if (item.kind === "activity" && item.detail?.event === "provider_switch") lastSwitch = Math.max(lastSwitch, item.sequence);
			if (item.turnId === latest.id) latestEnd = Math.max(latestEnd, item.sequence);
		}
		return lastSwitch > latestEnd;
	}, [latest, snapshot.items]);
	const refused = latest?.state === "failed" && latest.failureCode === "auth_required" && !switchedAway &&
		!snapshot.turns.some((entry) => entry.retryOfTurnId === latest.id)
		? latest
		: undefined;
	const refusedId = refused?.id;
	const resendable = Boolean(refusedId && retryControl && retryable.has(refusedId));
	const historyLoaded = !snapshot.hasMoreBefore || snapshot.items.some((item) =>
		item.kind === "message" && item.role === "user" && item.turnId === refusedId);
	const queued = resendable && !cloud && historyLoaded && !workedTurnIds.has(refusedId!);
	const account = snapshot.account;
	const loginConfirmed = Boolean(refused?.completedAt && account?.authenticationState === "authenticated" &&
		account.authVerifiedAt && Date.parse(account.authVerifiedAt) >= Date.parse(refused.completedAt));
	const provider = agentAccountProviderFor(snapshot.harness);
	const stored = useUiStore((state) => state.loginReturn);
	const own = stored?.sessionId === snapshot.sessionId ? stored : undefined;
	const loginSpent = Boolean(refusedId && own?.settled && own.turnId !== refusedId);
	const loginReturn = loginSpent ? undefined : own;
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	const forRefused = (turnId?: string) => !turnId || turnId === refusedId;
	const sendError = refusedId && forRefused(retryControl?.turnId) ? retryControl?.error : undefined;
	const loggedIn = loginReturn?.loggedInAt !== undefined || loginConfirmed;
	const [autoSending, setAutoSending] = useState<string>();
	const sending = Boolean(refusedId && (
		(retryControl?.pending && forRefused(retryControl.turnId)) || (autoSending === refusedId && !sendError)));
	const state: ChatLoginBandProps["state"] = sending
		? "sending"
		: sendError
			? "failed"
			: loginReturn?.loggedInAt !== undefined
				? loginReturn.settled ? "loggedIn" : "sending"
				: loginConfirmed ? "loggedIn"
					: loginReturn ? "waiting" : "signedOut";
	const retryTurn = useStableCallback(retryControl?.retry);
	const sendAgain = useStableCallback(() => {
		if (refusedId && !newWorkDisabled) void Promise.resolve(retryTurn(refusedId)).catch(() => undefined);
	});
	const readsAccount = Boolean(provider && !hostId && !cloud && (refused || needsReauth(snapshot)));
	const [sessionAccount, setSessionAccount] = useState<{ sessionId: string; provider: string; accountId?: string }>();
	useEffect(() => {
		if (!readsAccount || !provider) return;
		const sessionId = snapshot.sessionId;
		let current = true;
		void fetchSessionAgentAccounts(sessionId).then((accounts) => {
			if (current) setSessionAccount({ sessionId, provider, accountId: accounts[provider] });
		}).catch(() => undefined);
		return () => {
			current = false;
		};
	}, [provider, readsAccount, snapshot.sessionId]);
	const logIn = useStableCallback(() => {
		const ui = useUiStore.getState();
		const sessionId = snapshot.sessionId;
		const known = sessionAccount?.sessionId === sessionId && sessionAccount.provider === provider ? sessionAccount : undefined;
		const accountId = known?.accountId;
		const reading = !known && readsAccount ? provider : undefined;
		ui.startLoginReturn({
			sessionId,
			agentId: snapshot.harness,
			...(hostId ? { hostId } : {}),
			...(accountId ? { accountId } : {}),
			...(reading ? { accountPending: true } : {}),
		});
		if (reading) {
			const agentId = snapshot.harness;
			const ours = () => {
				const waiting = useUiStore.getState().loginReturn;
				return waiting?.sessionId === sessionId && waiting.agentId === agentId;
			};
			void fetchSessionAgentAccounts(sessionId).then(
				(accounts) => {
					if (!ours()) return;
					const store = useUiStore.getState();
					const found = accounts[reading];
					store.setLoginReturnAccount(sessionId, found);
					if (found && store.settingsModal?.scope === "global") {
						useUiStore.setState({ accountLogin: found });
					}
				},
				() => {
					if (ours()) useUiStore.getState().setLoginReturnAccount(sessionId, undefined);
				},
			);
		}
		ui.openGlobalSettings("harness", {
			focusAgentId: snapshot.harness,
			...(hostId ? { hostId } : {}),
			harnessView: cloud ? "cloud" : "local",
			...(accountId ? { loginAccountId: accountId } : { startLogin: true }),
			preserveProject: true,
		});
	});
	useEffect(() => {
		const ui = useUiStore.getState();
		if (loginSpent) {
			ui.clearLoginReturn(snapshot.sessionId);
			return;
		}
		if (!loginReturn) return;
		if (!refusedId) {
			if (loginReturn.loggedInAt !== undefined || !settingsOpen) ui.clearLoginReturn(snapshot.sessionId);
			return;
		}
		if (loginReturn.loggedInAt === undefined || loginReturn.settled) return;
		if (!queued || Date.now() - loginReturn.loggedInAt > LOGIN_RETRY_WINDOW_MS || activeTurn(snapshot) || retryControl?.pending || newWorkDisabled) {
			ui.settleLoginReturn(snapshot.sessionId, refusedId);
			return;
		}
		ui.settleLoginReturn(snapshot.sessionId, refusedId);
		setAutoSending(refusedId);
		void Promise.resolve(retryTurn(refusedId)).catch(() => undefined).finally(() => {
			setAutoSending((current) => current === refusedId ? undefined : current);
		});
	}, [loginReturn, loginSpent, newWorkDisabled, queued, refusedId, retryControl?.pending, retryTurn, settingsOpen, snapshot]);
	const lines = useMemo(() => {
		const map = new Map<string, TurnOutcomeLogin>();
		for (const turn of snapshot.turns) {
			if (turn.state !== "failed" || turn.failureCode !== "auth_required" || turn.rolledBack) continue;
			const status: TurnOutcomeLogin["status"] = turn.id !== refusedId
				? "stopped"
				: state === "sending" ? "sending"
					: !queued ? "resend"
						: state === "waiting" ? "waiting"
							: state === "signedOut" ? "queued"
								: "stopped";
			map.set(turn.id, {
				status,
				align: workedTurnIds.has(turn.id) ? "start" : "end",
				...(turn.id !== refusedId ? { ownRetry: true } : {}),
			});
		}
		return map;
	}, [queued, refusedId, snapshot.turns, state, workedTurnIds]);
	return {
		lines,
		logIn,
		locked: Boolean(refused) && !cloud && resendable &&
			(state === "signedOut" || state === "waiting" || (state === "failed" && !loggedIn)),
		...(refused ? {
			band: {
				harness: snapshot.harness,
				agent: agentLabel(snapshot.harness),
				...(hostId ? { host: labelForHost(hostId) ?? hostId } : {}),
				state,
				queued,
				onLogin: logIn,
				...(resendable && !newWorkDisabled ? { onSendAgain: sendAgain } : {}),
				signedOut: !loggedIn,
				...(sendError ? { error: sendError } : {}),
			},
		} : {}),
	};
}

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

export function activityRevealScrollDelta(
	rect: { top: number; bottom: number; height: number },
	view: { top: number; bottom: number; height: number },
	hasRange: boolean,
): number {
	const padding = 12;
	if (hasRange) {
		if (rect.top >= view.top + padding && rect.bottom <= view.bottom - padding) return 0;
		return rect.top + rect.height / 2 - (view.top + view.height / 2);
	}
	if (rect.top >= view.top + padding && rect.top <= view.top + view.height / 2) return 0;
	return rect.top - (view.top + view.height / 4);
}

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

const headerSlidSessions = new Set<string>();

function firstTurnSettled(snapshot: ConversationSnapshot): boolean {
	const first = snapshot.turns[0];
	return Boolean(first && first.state !== "queued" && first.state !== "running");
}

function openingTurnSettled(snapshot: ConversationSnapshot, controllerStopped: boolean): boolean {
	return firstTurnSettled(snapshot) || (controllerStopped && snapshot.turns[0]?.state === "running");
}

function groupByTurn(snapshot: ConversationSnapshot, { controllerStopped = false }: { controllerStopped?: boolean } = {}): TimelineGroup[] {
	const byTurn = new Map(snapshot.turns.map((turn) => [turn.id, turn]));
	const groups: TimelineGroup[] = [];
	const groupForTurn = new Map<string, TimelineGroup>();

	for (const item of snapshot.items) {
		const turnId = carriedInto.get(item) ?? item.turnId;
		if (turnId === undefined) {
			const last = groups.at(-1);
			if (last && last.turnId === undefined) {
				last.items.push(item);
				continue;
			}
			groups.push({
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
			key: itemKey(item) === item.id ? byTurn.get(turnId)?.groupKey ?? `${turnId}-${item.sequence}` : itemKey(item),
			turnId,
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
		group.diff = turn.diff;
		group.artifacts = turn.artifacts;
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
			state: turn.notRun ? "not_run" : turn.state,
			usageLimit: turn.usageLimit,
			durationMs: turn.notRun || (turn.state === "interrupted" && !turn.startedAt) ? undefined : settledTurnDuration(turn),
			error: turn.errorMessage || (turn.state === "failed"
				? [...group.items].reverse().find(
					(item): item is ConversationActivity => item.kind === "activity" && item.activityKind === "error",
				)?.summary
				: undefined),
			failureCode: turn.failureCode,
		};
	}

	const conversation = subagentsOfConversation(snapshot.items, groups, controllerStopped);
	if (!conversation) return groups.flatMap(splitAtSteers);
	return groups.flatMap((group) =>
		splitAtSteers(group)
			.map((segment) => withSubagentSteps(segment, conversation))
			.filter((segment) =>
				segment.live
				|| (segment.outcome !== undefined && segment.outcome.state !== "completed")
				|| segment.items.some((item) => !conversation.stepIds.has(item.id))),
	);
}

type ConversationSubagents = {
	steps: ReadonlyMap<string, ConversationActivity[]>;
	stepIds: ReadonlySet<string>;
	live: ReadonlySet<string>;
	stopped: boolean;
};

function subagentsOfConversation(items: ConversationItem[], groups: readonly TimelineGroup[], stopped: boolean): ConversationSubagents | undefined {
	const launches = items.filter(isSubagentLaunch);
	if (launches.length === 0) return undefined;
	const { steps } = partitionSubagentSteps(items);
	const stepIds = new Set([...steps.values()].flat().map((step) => step.id));
	const liveItems = new Set(groups.flatMap((group) => (group.live ? group.items.map((item) => item.id) : [])));
	const live = new Set(
		launches
			.filter((launch) => liveItems.has(launch.id) || steps.get(launch.id)?.some((step) => liveItems.has(step.id)))
			.map((launch) => launch.id),
	);
	return { steps, stepIds, live, stopped };
}

function withSubagentSteps(segment: TimelineGroup, conversation: ConversationSubagents): TimelineGroup {
	const steps = new Map<string, ConversationActivity[]>();
	const stepIds = new Set<string>();
	const live = new Set<string>();
	for (const item of segment.items) {
		if (conversation.stepIds.has(item.id)) stepIds.add(item.id);
		else if (isSubagentLaunch(item)) {
			steps.set(item.id, conversation.steps.get(item.id) ?? []);
			if (conversation.live.has(item.id)) live.add(item.id);
		}
	}
	if (steps.size === 0 && stepIds.size === 0) return segment;
	return { ...segment, subagents: { steps, stepIds, live, stopped: conversation.stopped } };
}

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

function isNoticeRun(run: TimelineRun): boolean {
	return run.items.some(isNoticeItem);
}

function isNoticeItem(item: ConversationItem | undefined): boolean {
	if (item?.kind !== "activity") return false;
	if (item.activityKind === "error") return true;
	if (isOrdinaryActivity(item) && isFailedCall(item)) return true;
	if (item.activityKind === "user_input") return item.status !== "pending";
	return item.detail?.event !== undefined && !isSteer(item) && !isCompaction(item) && item.activityKind !== "plan";
}

const NO_BATCHED_REPORTS: BatchedReports = { under: new Map(), moved: new Set() };

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

function atCarrier(items: ConversationItem[], deliveries: readonly BatchedDelivery[], moved: ReadonlySet<string>): ConversationItem[] {
	if (deliveries.length === 0) return items;
	const steers = new Map(items.flatMap((item) => (item.kind === "activity" && item.turnId && isSteer(item) ? [[item.id, item] as const] : [])));
	const leaders = new Map<string, ConversationItem>();
	for (const item of items) {
		if (item.kind === "message" && item.role === "user" && item.turnId && !leaders.has(item.turnId)) leaders.set(item.turnId, item);
	}
	const carriers = new Map(deliveries.flatMap((delivery) => {
		const carrier = delivery.steerActivityId ? steers.get(delivery.steerActivityId) : leaders.get(delivery.deliveredWithTurnId);
		return carrier && !moved.has(delivery.turnId) ? [[delivery.turnId, carrier] as const] : [];
	}));
	if (carriers.size === 0) return items;
	const sentAt = (turnId: string) => {
		const carrier = carriers.get(turnId);
		const index = carrier?.kind === "activity" ? carrier.detail?.providerInputSourceTurnIds?.indexOf(turnId) ?? -1 : -1;
		return index < 0 ? Number.MAX_SAFE_INTEGER : index;
	};
	const carried = items.filter((item) => item.turnId && carriers.has(item.turnId));
	const blocks = new Map<ConversationItem, ConversationItem[]>();
	for (const item of carried.sort((a, b) => sentAt(a.turnId!) - sentAt(b.turnId!) || a.sequence - b.sequence)) {
		const carrier = carriers.get(item.turnId!)!;
		const block = blocks.get(carrier);
		if (block) block.push(item);
		else blocks.set(carrier, [item]);
	}
	const isCarried = new Set(carried);
	const place = new Map([...blocks].flatMap(([carrier, block]) => {
		const next = items.reduce((min, item) => (!isCarried.has(item) && item.sequence > carrier.sequence && item.sequence < min ? item.sequence : min), carrier.sequence + 1);
		return block.map((item, index) => [item, carrier.sequence + ((next - carrier.sequence) * (index + 1)) / (block.length + 1)] as const);
	}));
	return items
		.map((item) => {
			const carrier = item.turnId ? carriers.get(item.turnId) : undefined;
			const sequence = place.get(item);
			if (!carrier || sequence === undefined) return item;
			const cached = carriedRows.get(item);
			if (cached && carriedInto.get(cached) === carrier.turnId && cached.sequence === sequence) return cached;
			const row = { ...item, sequence };
			carriedRows.set(item, row);
			carriedInto.set(row, carrier.turnId!);
			return row;
		})
		.sort((a, b) => a.sequence - b.sequence);
}

function rendersNothing(run: TimelineRun): boolean {
	const item = run.items[0];
	if (run.kind !== "single" || !item) return false;
	if (item.kind === "message") return item.role === "assistant" && item.text.trim() === "";
	return (item.activityKind === "user_input" || item.activityKind === "approval") && item.status === "pending";
}

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
					exit={{
						height: 0,
						opacity: 0,
						transition: reducedMotion ? { duration: 0 } : {
							height: { duration: 0.32, ease: [0.65, 0, 0.35, 1] },
							opacity: { duration: 0.14, ease: "easeOut" },
						},
					}}
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
