import { Feather } from "../icons";
import * as Clipboard from "expo-clipboard";
import * as Linking from "expo-linking";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
	FlatList,
	Image,
	Keyboard,
	Platform,
	Pressable,
	StyleSheet,
	Text,
	View,
} from "react-native";
import { usePreventRemove } from "expo-router/build/react-navigation/core";
import { AppSymbol } from "../AppSymbol";
import { authHeaders, httpBase } from "../config";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import { useApp } from "../store";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { Spinner } from "../Spinner";
import { MascotLamp } from "../ui";
import { FoldBody, FoldChevron, FoldLayout, StatusLabel, StatusSpinner } from "./TurnFoldMotion";
import { ChatMarkdown } from "./ChatMarkdown";
import { FirstLineMarker } from "./FirstLineMarker";
import { RenderFrame } from "./RenderFrame";
import { notifyRenderViewport, readRenderRef } from "./renderFrameModel";
import { HighlightedCodeText } from "./HighlightedCodeText";
import { caretNotation, commandOutputText } from "./ansi";
import { userMessageSurfaceStyle } from "./chatChrome";
import { requestPresentation } from "./chatPresentation";
import { workingElapsedLabel } from "./conversationChrome";
import { errorActivityDuplicatesTurn, providerErrorCopy } from "./providerError";
import { ElicitationAction } from "./elicitation-native-controls";
import { ElicitationInputField } from "./ElicitationInputField";
import {
	elicitationPromptCopy,
	elicitationStepPresentation,
	groupedQuestionInputs,
	humanizeInputName,
	initialInputValue,
	missingRequiredInputs,
	safeHttpURL,
	validateInput,
} from "./elicitationModel";
import { previewFilePath } from "../api";
import { ImageViewer } from "../ImageViewer";
import { attachmentName, attachmentTileSize, editableMessageText, isImageAttachment, isSameAttachmentLoad, stagedAttachmentParts, type AttachmentImageSource } from "./messageAttachments";
import { InlineMessageEditor, type MessageEditDraft } from "./InlineMessageEditor";
import { JumpToLatest } from "./JumpToLatest";
import { useMessageEdit } from "./useMessageEdit";
import { MessageContextMenu } from "./MessageContextMenu";
import type {
	ConversationActivity,
	ConversationItem,
	ConversationMessage,
	ConversationSnapshot,
	ConversationTurn,
	FileChange,
} from "./types";
import {
	activityHierarchy,
	activityNodesRunning,
	activityStartsExpanded,
	canRollbackTurn,
	conversationTimelineRenderPlan,
	editableHumanTurns,
	sameEditableTurns,
	countActivityNodes,
	partitionTurnItems,
	readableConversationItems,
	startupPlacement,
	streamingCursorMessageId,
	summarizeActivities,
	type ActivityNode,
	type ConversationGroup,
} from "./timelineModel";
import { iconSize, radius, type, space, touchTarget } from "../tokens";
import { useFilesNavigation } from "../files/navigation";
import { userFacingError } from "../connectionError";
import { SentResponseQuotes } from "./SentResponseQuotes";
import { TurnRollbackButton } from "./TurnRollbackButton";
import { SessionStartupStatus } from "./SessionStartup";
import type { SessionStartupView } from "../session/sessionStartup";

/** A starting session's checklist and where it sits; see ChatSessionScreen. */
export type TimelineStartup = { view: SessionStartupView; visible: boolean; onRetry?(): void; retrying?: boolean };

type TimelineRow =
	| { kind: "single"; key: string; items: [ConversationItem] }
	| { kind: "activities"; key: string; items: ConversationActivity[] };

export const ChatTimeline = memo(function ChatTimeline({
	snapshot,
	loadingOlder,
	onLoadOlder,
	approvalPending,
	inputPending,
	onDecide,
	onResolveInput,
	onRollback,
	onEditMessage,
	onEditingChange,
	jumpToSequence,
	onJumpHandled,
	answeredBelow,
	startup,
}: {
	snapshot: ConversationSnapshot;
	loadingOlder: boolean;
	onLoadOlder(): void;
	approvalPending: boolean;
	inputPending: boolean;
	onDecide(requestId: string, decisionId: string): Promise<void>;
	onResolveInput(requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>): Promise<void>;
	onRollback?(turnId: string): Promise<number>;
	/** Branches before an earlier human prompt and resends it edited. */
	onEditMessage?(turnId: string, text: string, clientMessageId: string): Promise<void>;
	/** Whether a prompt is open in its inline editor, so the screen can make room. */
	onEditingChange?(editing: boolean): void;
	jumpToSequence?: number;
	onJumpHandled?(): void;
	/**
	 * The request the composer is currently answering. Its card here collapses to
	 * a record of what was asked, so the same decision never has two live sets of
	 * controls that could disagree.
	 */
	answeredBelow?: number;
	/** Setup progress for a session that is still starting. */
	startup?: TimelineStartup;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const olderFeedback = usePressFeedback("button");
	const listRef = useRef<FlatList<ConversationGroup>>(null);
	const followsTail = useRef(true);
	const [showJump, setShowJump] = useState(false);
	// Held above the virtualized list so a "Worked for" fold keeps its state when its row scrolls away and back.
	const [openWorked, setOpenWorked] = useState<ReadonlySet<string>>(() => new Set());
	const toggleWorked = useCallback((key: string) => {
		setOpenWorked((current) => {
			const next = new Set(current);
			if (!next.delete(key)) next.add(key);
			return next;
		});
	}, []);
	// Usage is snapshot state, not conversation. Reasoning stays available in the
	// durable record but hidden on mobile: prose and work are the primary surface.
	const openingTurnId = startup?.view.openingTurnId;
	const items = useMemo(() => readableConversationItems(snapshot, openingTurnId), [openingTurnId, snapshot]);
	const plan = useMemo(() => conversationTimelineRenderPlan(snapshot, items, Platform.OS === "android" ? "android" : "ios"), [items, snapshot.turns]);
	const groups = plan.groups;
	const inverted = plan.inverted;
	// Recomputed per snapshot but kept by identity while its contents are the
	// same, so `extraData` changes (and every row re-renders) only when they do.
	const nextEdits = useMemo(() => onEditMessage ? editableHumanTurns(snapshot) : undefined, [onEditMessage, snapshot]);
	const editsRef = useRef(nextEdits);
	if (!sameEditableTurns(editsRef.current, nextEdits)) editsRef.current = nextEdits;
	const edits = editsRef.current;
	const stopFollowingTail = useCallback(() => { followsTail.current = false; }, []);
	const { editDraft, editing, detached, startEdit, cancelEdit, submitEdit } = useMessageEdit({ items: snapshot.items, onEditMessage, onEditingChange, onStart: stopFollowingTail });
	// Back closes the edit first, as Escape does on the desktop, rather than
	// leaving the conversation with the draft. A JS back listener never hears
	// predictive back on Android, so this guards the screen's removal instead.
	usePreventRemove(editing, cancelEdit);
	const rowState = useMemo(() => ({ openWorked, edits, editDraft }), [editDraft, edits, openWorked]);
	const jumpViewPosition = inverted ? 0.82 : 0.18;
	const scrollToLatest = (animated: boolean) => {
		if (inverted) listRef.current?.scrollToOffset({ offset: 0, animated });
		else listRef.current?.scrollToEnd({ animated });
	};

	useEffect(() => {
		if (jumpToSequence === undefined) return;
		// Match the group that CONTAINS the sequence, not one whose anchor equals
		// it: a group's anchor is its first item, so an activity partway through a
		// turn never matched and the jump silently did nothing.
		const index = groups.findIndex((group) => group.anchor === jumpToSequence || group.items.some((item) => item.sequence === jumpToSequence));
		if (index >= 0) {
			followsTail.current = index === (inverted ? 0 : groups.length - 1);
			setShowJump(!followsTail.current);
			requestAnimationFrame(() => listRef.current?.scrollToIndex({ index, animated: true, viewPosition: jumpViewPosition }));
		}
		onJumpHandled?.();
	}, [groups, inverted, jumpToSequence, jumpViewPosition, onJumpHandled]);

	// The keyboard shrinks the list, so bring the prompt being edited back into view.
	const editingTurnId = editDraft?.turnId;
	const revealEdit = useCallback(() => {
		if (!editingTurnId) return;
		const index = groups.findIndex((group) => group.turnId === editingTurnId);
		if (index < 0) return;
		// A row is the whole turn, so pin its top (the prompt) to the top of the
		// viewport; on an inverted list that is the row's far end.
		requestAnimationFrame(() => listRef.current?.scrollToIndex({ index, animated: true, viewPosition: inverted ? 1 : 0, viewOffset: inverted ? -space.md : space.md }));
	}, [editingTurnId, groups, inverted]);
	useEffect(() => {
		if (!editingTurnId) return;
		revealEdit();
		const subscription = Keyboard.addListener("keyboardDidShow", revealEdit);
		return () => subscription.remove();
		// Only when an edit opens or the keyboard settles: following every refresh
		// would pull the list back while the person scrolls to read.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [editingTurnId]);

	if (plan.kind === "empty" && startup?.visible) {
		// No brief in the timeline (a promptless start): the checklist stands alone.
		return (
			<View style={styles.timelineWrap}>
				<View style={[styles.list, styles.content]}>
					<SessionStartupStatus startup={startup.view} onRetry={startup.onRetry} retrying={startup.retrying} />
				</View>
			</View>
		);
	}
	if (plan.kind === "empty") {
		return (
			<View style={styles.timelineWrap}>
				<View style={styles.emptySurface}>
					<EmptyConversation controller={snapshot.controller.state} />
				</View>
			</View>
		);
	}
	const placement = startupPlacement(groups, openingTurnId, Boolean(startup?.visible));
	// A checklist no loaded turn can host sits at the live edge instead.
	const standaloneStartup = placement === "standalone" && startup ? (
		<SessionStartupStatus startup={startup.view} onRetry={startup.onRetry} retrying={startup.retrying} />
	) : null;
	const olderControl = snapshot.hasMoreBefore ? (
		<Pressable
			accessibilityRole="button"
			accessibilityState={{ disabled: loadingOlder, busy: loadingOlder }}
			disabled={loadingOlder}
			onPress={() => void onLoadOlder()}
			android_ripple={olderFeedback.android_ripple}
			style={({ pressed }) => [styles.older, pressed && olderFeedback.pressed]}
		>
			{loadingOlder ? <Spinner size={iconSize.sm} color={t.textTertiary} /> : <AppSymbol name="history" size={iconSize.sm} color={t.textTertiary} />}
			<Text style={styles.olderText}>{loadingOlder ? "Loading history…" : "Load earlier messages"}</Text>
		</Pressable>
	) : null;

	return (
		<View style={styles.timelineWrap}>
			<FlatList<ConversationGroup>
				ref={listRef}
				data={groups}
				extraData={rowState}
				inverted={plan.inverted}
				keyExtractor={(group) => group.key}
				style={styles.list}
				contentContainerStyle={styles.content}
				keyboardShouldPersistTaps="handled"
				initialNumToRender={4}
				maxToRenderPerBatch={4}
				updateCellsBatchingPeriod={32}
				windowSize={5}
				// Content off screen can resize (a page measured ahead, earlier history
				// loading in); what is on screen stays put on both platforms.
				maintainVisibleContentPosition={{ minIndexForVisible: 0 }}
				onScroll={(event) => {
					const { contentOffset, contentSize, layoutMeasurement } = event.nativeEvent;
					followsTail.current = inverted
						? Math.abs(contentOffset.y) < 120
						: contentSize.height - layoutMeasurement.height - contentOffset.y < 120;
					setShowJump(!followsTail.current);
					notifyRenderViewport();
				}}
				scrollEventThrottle={100}
				onContentSizeChange={() => {
					notifyRenderViewport();
					if (followsTail.current) requestAnimationFrame(() => scrollToLatest(false));
				}}
				onScrollToIndexFailed={({ index, averageItemLength }) => {
					listRef.current?.scrollToOffset({ offset: Math.max(0, index * averageItemLength), animated: true });
					setTimeout(() => listRef.current?.scrollToIndex({ index, animated: true, viewPosition: jumpViewPosition }), 120);
				}}
				ListHeaderComponent={inverted ? standaloneStartup : olderControl}
				ListFooterComponent={inverted ? olderControl : standaloneStartup}
				renderItem={({ item: group }) => <ConversationTurnGroup
					group={group}
					snapshot={snapshot}
					approvalPending={approvalPending}
					inputPending={inputPending}
					onDecide={onDecide}
					onResolveInput={onResolveInput}
					onRollback={onRollback}
					editableTurns={edits?.editable}
					onEditMessage={onEditMessage ? startEdit : undefined}
					editDraft={editDraft}
					onCancelEdit={cancelEdit}
					onSubmitEdit={submitEdit}
					onEditFocus={revealEdit}
					answeredBelow={answeredBelow}
					workedOpen={openWorked.has(group.key)}
					onToggleWorked={toggleWorked}
					startup={placement === "turn" && group.turnId === openingTurnId ? startup : undefined}
				/>}
			/>
			{/* An unresolved edit whose prompt left the conversation keeps its Retry and
			    Abandon controls here, apart from the list, as the desktop does. */}
			{detached && editDraft ? <View style={styles.detachedEditor}>
				<InlineMessageEditor key={editDraft.turnId} draft={editDraft} blocked onCancel={cancelEdit} onEdit={submitEdit} />
			</View> : null}
			{showJump ? <JumpToLatest onPress={() => { followsTail.current = true; setShowJump(false); scrollToLatest(true); }} /> : null}
		</View>
	);
});

function ConversationTurnGroup({ group, snapshot, approvalPending, inputPending, onDecide, onResolveInput, onRollback, editableTurns, onEditMessage, editDraft, onCancelEdit, onSubmitEdit, onEditFocus, answeredBelow, workedOpen, onToggleWorked, startup }: {
	group: ConversationGroup;
	snapshot: ConversationSnapshot;
	approvalPending: boolean;
	inputPending: boolean;
	onDecide(requestId: string, decisionId: string): Promise<void>;
	onResolveInput(requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>): Promise<void>;
	onRollback?(turnId: string): Promise<number>;
	editableTurns?: ReadonlySet<string>;
	onEditMessage?(message: ConversationMessage): void;
	editDraft?: MessageEditDraft;
	onCancelEdit(): void;
	onSubmitEdit(payload: string, body: string): Promise<void>;
	onEditFocus(): void;
	answeredBelow?: number;
	workedOpen: boolean;
	onToggleWorked(key: string): void;
	startup?: TimelineStartup;
}) {
	// A provider failure arrives twice: as an error activity, and again as the
	// turn's errorMessage below it. The turn keeps it — that line carries the
	// outcome and the rollback — so the activity restating it is dropped.
	const turnError = group.turn?.state === "failed" ? group.turn.errorMessage : undefined;
	const items = turnError
		? group.items.filter((item) => !(item.kind === "activity" && errorActivityDuplicatesTurn(item, turnError)))
		: group.items;
	const cursorMessageId = streamingCursorMessageId(items);
	// Only the prompt being edited gets an element, so every other row keeps its memo.
	const editorFor = (item: ConversationItem) => editDraft && item.kind === "message" && item.role === "user" && item.origin === "human" && item.turnId === editDraft.turnId
		? <InlineMessageEditor key={editDraft.turnId} draft={editDraft} blocked={!editableTurns?.has(editDraft.turnId)} onCancel={onCancelEdit} onEdit={onSubmitEdit} onFocus={onEditFocus} />
		: undefined;
	const renderRows = (list: ConversationItem[]) => activityRuns(list).map((row) => row.kind === "activities"
		? <ActivityRun key={row.key} activities={row.items} />
		: <TimelineItem key={row.key} item={row.items[0]} sessionId={snapshot.sessionId} conversationId={snapshot.conversationId} completed={group.turn?.state === "completed" && !group.turn.rolledBack} approvalPending={approvalPending} inputPending={inputPending} onDecide={onDecide} onResolveInput={onResolveInput} onEdit={row.items[0].turnId && editableTurns?.has(row.items[0].turnId) ? onEditMessage : undefined} editor={editorFor(row.items[0])} answeredBelow={answeredBelow} showCursor={row.items[0].id === cursorMessageId} />);
	if (startup) {
		// The checklist sits exactly where the Working row will, under the brief,
		// so the handoff moves nothing.
		const firstWork = items.findIndex((item) => !(item.kind === "message" && item.role === "user"));
		const leading = firstWork < 0 ? items.length : firstWork;
		// The checklist explains the wait, so the brief drops its "Queued" note.
		const brief = items.slice(0, leading).map((item) => item.kind === "message" && item.delivery === "queued" ? { ...item, delivery: undefined } : item);
		return <View>
			{renderRows(brief)}
			<SessionStartupStatus startup={startup.view} onRetry={startup.onRetry} retrying={startup.retrying} />
			{renderRows(items.slice(leading))}
		</View>;
	}
	const partition = partitionTurnItems(items, group.turn);
	// The prompt's editor grows as it opens and as lines are typed. Android's
	// inverted list misplaces rows whose layout transition runs under that growth,
	// so the turn being edited lays out without the fold motion.
	const editing = Boolean(editDraft && group.turnId === editDraft.turnId);
	const WorkBody = editing ? View : FoldBody;
	const BelowFold = editing ? View : FoldLayout;
	const summary = group.turn ? <TurnSummary turn={group.turn} onRollback={canRollbackTurn(snapshot, group.turn) ? onRollback : undefined} /> : null;
	if (!partition || !group.turn) return <View>{renderRows(items)}{summary}</View>;
	// Running: the prompt, a live Working row, then the work as it streams in.
	// Settled: the same row becomes "Worked for", folding the work away and
	// leaving the notices and the final reply in view.
	return <View>
		{renderRows(partition.prompt)}
		<TurnStatusRow turn={group.turn} foldable={partition.settled && partition.work.length > 0} open={workedOpen} onToggle={() => onToggleWorked(group.key)} />
		{partition.settled ? (workedOpen ? <WorkBody style={{ gap: space.xs, paddingBottom: space.xs }}>{renderRows(partition.work)}</WorkBody> : null) : renderRows(partition.work)}
		<BelowFold>{renderRows(partition.notices)}{renderRows(partition.final)}{summary}</BelowFold>
	</View>;
}

const TimelineItem = memo(function TimelineItem({
	item,
	sessionId,
	conversationId,
	completed,
	approvalPending,
	inputPending,
	onDecide,
	onResolveInput,
	onEdit,
	editor,
	answeredBelow,
	showCursor = false,
}: {
	item: ConversationItem;
	sessionId: string;
	conversationId: string;
	completed: boolean;
	approvalPending: boolean;
	inputPending: boolean;
	onDecide(requestId: string, decisionId: string): Promise<void>;
	onResolveInput(requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>): Promise<void>;
	/** Present only on a human prompt the daemon can branch before and resend edited. */
	onEdit?(message: ConversationMessage): void;
	/** Shown in place of the bubble while this prompt is being edited. */
	editor?: ReactNode;
	answeredBelow?: number;
	showCursor?: boolean;
}) {
	const styles = useThemedStyles(makeStyles);
	if (item.kind === "message") {
		if (item.role === "user" && item.origin === "human") {
			if (editor) return <View style={styles.userRow}>{editor}</View>;
			const delivery = deliveryCopy(item.delivery);
			const { body, attachments } = stagedAttachmentParts(item.text);
			// Long-press opens Edit and Copy, so the text is not selectable: a
			// selection would take the same gesture.
			return (
				<View style={styles.userRow}>
					<MessageContextMenu style={styles.userMenu} canEdit={Boolean(onEdit)} onAction={(action) => {
						if (action === "edit") onEdit?.(item);
						else { void Clipboard.setStringAsync(editableMessageText(item.text).body.trim()); haptics.success(); }
					}}>
						<View style={[styles.userBubble, styles.menuBubble, item.content?.some((content) => content.type === "excerpt" && content.text) && styles.quotedBubble]}>
							<SentResponseQuotes content={item.content} />
							{body ? <Text style={styles.userText}>{body}</Text> : null}
							{attachments.length > 0 ? <StagedAttachments sessionId={sessionId} paths={attachments} spaced={Boolean(body)} /> : null}
							{delivery ? <Text style={styles.delivery}>{delivery}</Text> : null}
						</View>
					</MessageContextMenu>
				</View>
			);
		}
		if (item.role === "user") {
			return <OriginMessage message={item} />;
		}
		return (
			<View style={styles.assistantRow}>
				{item.senderLabel ? <Text style={styles.sender}>{item.senderLabel}</Text> : null}
				<ChatMarkdown text={showCursor ? `${item.text || ""} ▍` : item.text} streaming={item.streaming} continuousSelection selectionSource={completed && !item.streaming ? { conversationId, messageId: item.id, revision: item.revision } : undefined} />
				{!item.streaming && item.text ? <View style={styles.responseActions}>
					<CopyResponseButton text={item.text} />
				</View> : null}
			</View>
		);
	}
	if (item.activityKind === "approval") {
		return <ApprovalCard activity={item} busy={approvalPending} onDecide={onDecide} handledBelow={item.sequence === answeredBelow} />;
	}
	if (item.activityKind === "user_input") {
		return <UserInputCard activity={item} busy={inputPending} onResolve={onResolveInput} handledBelow={item.sequence === answeredBelow} />;
	}
	if (item.activityKind === "system" && item.detail?.event === "compaction") {
		return <CompactionMarker activity={item} />;
	}
	if (item.activityKind === "system" && item.detail?.event === "steer") {
		const { body, attachments } = stagedAttachmentParts(item.detail.text || item.summary);
		return <View style={styles.userRow}><View style={[styles.userBubble, styles.steerBubble]}><Text style={styles.steerLabel}>Steered</Text>{body ? <Text selectable style={styles.userText}>{body}</Text> : null}{attachments.length > 0 ? <StagedAttachments sessionId={sessionId} paths={attachments} spaced={Boolean(body)} /> : null}</View></View>;
	}
	// A page the agent published with html_render / ao render.
	const render = item.activityKind === "system" ? readRenderRef(item.detail) : undefined;
	if (render) return <RenderFrame render={render} />;
	if (item.detail?.event === "model.rerouted") return <SystemSignal icon="shuffle" title={`Answered by ${item.detail.toModel || "another model"}`} detail={item.detail.fromModel ? `Instead of ${item.detail.fromModel}${item.detail.reason ? ` · ${item.detail.reason}` : ""}` : item.detail.reason} />;
	if (item.detail?.event === "auth.reauth_required") return <SystemSignal icon="key" danger title="The provider asked you to sign in again" detail={item.detail.reason} />;
	if (item.activityKind === "error") return <ErrorActivity activity={item} />;
	return <ActivityRow activity={item} />;
});

/** How long a copy control shows its checkmark before it turns back. */
const COPIED_MS = 1_400;

/** Copies the whole reply in one tap, without selecting it first. */
function CopyResponseButton({ text }: { text: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("icon");
	const [copied, setCopied] = useState(false);
	useEffect(() => {
		if (!copied) return;
		const timer = setTimeout(() => setCopied(false), COPIED_MS);
		return () => clearTimeout(timer);
	}, [copied]);
	return <Pressable
		accessibilityRole="button"
		accessibilityLabel="Copy response"
		onPress={() => { void Clipboard.setStringAsync(text); haptics.success(); setCopied(true); }}
		android_ripple={feedback.android_ripple}
		style={({ pressed }) => [styles.copy, pressed && feedback.pressed]}
	>
		<AppSymbol name={copied ? "checkmark" : "copy"} size={iconSize.md} color={copied ? t.green : t.textTertiary} />
	</Pressable>;
}

function SystemSignal({ icon, title, detail, danger }: { icon: keyof typeof Feather.glyphMap; title: string; detail?: unknown; danger?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return <View style={[styles.systemSignal, danger && { borderColor: t.red }]}><Feather name={icon} size={iconSize.sm} color={danger ? t.red : t.textTertiary} /><View style={{ flex: 1 }}><Text style={[styles.systemTitle, danger && { color: t.red }]}>{title}</Text>{detail ? <Text style={styles.systemDetail}>{String(detail)}</Text> : null}</View></View>;
}

/**
 * Files AO staged into the worktree for a human message. Images load through the
 * daemon's preview-files route with the connection's Bearer header, the same
 * credential every other mobile request uses; anything else stays a name chip.
 * Images sit in the bubble as center-cropped tiles and open full-screen on tap.
 * RN 0.86 Android forwards HTTP headers only for array sources, on both surfaces.
 * Render it only for messages that carry attachments: it subscribes to the app
 * store, and doing that for every bubble would defeat TimelineItem's memo.
 */
function StagedAttachments({ sessionId, paths, spaced }: { sessionId: string; paths: string[]; spaced: boolean }) {
	const { config } = useApp();
	const styles = useThemedStyles(makeStyles);
	const tileSize = attachmentTileSize(paths.filter(isImageAttachment).length);
	return <View style={[styles.attachments, spaced && styles.attachmentsSpaced]}>
		{paths.map((path) => {
			const source = config && isImageAttachment(path)
				? { uri: `${httpBase(config)}${previewFilePath(sessionId, path)}`, headers: authHeaders(config) }
				: undefined;
			return <StagedAttachment key={path} name={attachmentName(path)} source={source} tileSize={tileSize} />;
		})}
	</View>;
}

function StagedAttachment({ name, source, tileSize }: { name: string; source?: AttachmentImageSource; tileSize: number }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// The image covers the tile, so the ripple has to draw over it.
	const tileFeedback = usePressFeedback("button", { foreground: true });
	const chipFeedback = usePressFeedback("button");
	// The failure belongs to the load that failed, so a reconnect to another address
	// or a rotated password retries on its own; tapping the chip retries in place.
	const [failedLoad, setFailedLoad] = useState<AttachmentImageSource>();
	const [viewerOpen, setViewerOpen] = useState(false);
	if (!source) {
		return <View style={styles.attachmentChip}><Feather name="file-text" size={iconSize.xs} color={t.textTertiary} /><Text numberOfLines={1} style={styles.attachmentName}>{name}</Text></View>;
	}
	if (!isSameAttachmentLoad(failedLoad, source)) {
		// Cover-cropping a square reads as a deliberate thumbnail; the viewer shows the whole image.
		return <>
			<Pressable accessibilityRole="imagebutton" accessibilityLabel={`Open ${name}`} onPress={() => setViewerOpen(true)} android_ripple={tileFeedback.android_ripple} style={({ pressed }) => [styles.attachmentTile, { width: tileSize, height: tileSize }, pressed && tileFeedback.pressed]}>
				<Image accessibilityIgnoresInvertColors source={Platform.OS === "android" ? [source] : source} resizeMode="cover" onError={() => setFailedLoad(source)} style={styles.attachmentTileImage} />
			</Pressable>
			<ImageViewer visible={viewerOpen} name={name} source={source} onClose={() => setViewerOpen(false)} />
		</>;
	}
	return <Pressable accessibilityRole="button" accessibilityLabel={`Retry loading ${name}`} hitSlop={space.md} onPress={() => setFailedLoad(undefined)} android_ripple={chipFeedback.android_ripple} style={({ pressed }) => [styles.attachmentChip, pressed && chipFeedback.pressed]}>
		<AppSymbol name="refresh" size={iconSize.xs} color={t.textTertiary} />
		<Text numberOfLines={1} style={styles.attachmentName}>{name}</Text>
		<Text style={styles.attachmentRetry}>Tap to retry</Text>
	</Pressable>;
}

function deliveryCopy(state?: string): string | undefined {
	switch (state) {
		case "queued": return "Queued — sends when the agent finishes";
		case "sending": return "Sending…";
		case "uncertain": return "Delivery unconfirmed — check the conversation before retrying";
		case "failed": return "Not sent";
		default: return undefined;
	}
}

function OriginMessage({ message }: { message: Extract<ConversationItem, { kind: "message" }> }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("button");
	const long = message.text.length > 600;
	const [expanded, setExpanded] = useState(false);
	return <View style={styles.originMessage}>
		<View style={styles.originHeader}><Feather name="radio" size={iconSize.xs} color={t.textTertiary} /><Text style={styles.originLabel}>{message.senderLabel || (message.origin === "automation" ? "Automation" : "Orchestrator")}</Text></View>
		{long && expanded ? <ChatMarkdown text={message.text} /> : <Text selectable numberOfLines={long ? 5 : undefined} style={styles.originText}>{message.text}</Text>}
		{long ? <Pressable accessibilityRole="button" accessibilityState={{ expanded }} onPress={() => setExpanded((value) => !value)} android_ripple={feedback.android_ripple} style={({ pressed }) => [styles.originMore, pressed && feedback.pressed]}><FoldChevron open={expanded} color={t.accent} /><Text style={styles.originMoreText}>{expanded ? "Hide report" : "Show full report"}</Text></Pressable> : null}
	</View>;
}

/** Collapse consecutive mechanics so agent prose remains the visual hierarchy. */
function activityRuns(items: ConversationItem[]): TimelineRow[] {
	const rows: TimelineRow[] = [];
	for (const item of items) {
		const runnable = item.kind === "activity" &&
			item.activityKind !== "approval" &&
			item.activityKind !== "user_input" &&
			item.activityKind !== "error" &&
			item.activityKind !== "file_change" &&
			item.activityKind !== "reasoning" &&
			item.detail?.event === undefined;
		const previous = rows[rows.length - 1];
		if (runnable && previous?.kind === "activities" && previous.items[0]?.turnId === item.turnId) {
			previous.items.push(item);
		} else if (runnable) {
			rows.push({ kind: "activities", key: `run-${item.sequence}`, items: [item] });
		} else {
			rows.push({ kind: "single", key: `${item.kind}:${item.id}`, items: [item] });
		}
	}
	return rows;
}

function ActivityRow({ activity }: { activity: ConversationActivity }) {
	if (activity.activityKind === "mcp_tool") return <McpToolRow activity={activity} />;
	if (activity.activityKind === "auto_review") return <AutoReviewRow activity={activity} />;
	if (activity.activityKind === "file_change") return <FileChangeActivity activity={activity} />;
	if (activity.activityKind === "plan") return <PlanActivity activity={activity} />;
	return <GenericActivityRow activity={activity} />;
}

function GenericActivityRow({ activity }: { activity: ConversationActivity }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const detail = activity.detail ?? {};
	const output = commandOutputText(detail.output ?? detail.result ?? detail.error ?? detail.patchOutput);
	const [openOverride, setOpenOverride] = useState<boolean | null>(null);
	const open = openOverride ?? activityStartsExpanded(activity);
	const expandable = Boolean(output || detail.cwd || detail.arguments || detail.files || detail.reason || detail.text || detail.terminalInput);
	const meta = activityMeta(activity);
	return (
		<View style={styles.activityWrap}>
			<Pressable
				accessibilityRole={expandable ? "button" : undefined}
				accessibilityState={expandable ? { expanded: open } : undefined}
				disabled={!expandable}
				onPress={() => setOpenOverride(!open)}
				android_ripple={feedback.android_ripple}
				style={({ pressed }) => [styles.activityRow, pressed && feedback.pressed]}
			>
				<Feather name={meta.icon} size={iconSize.xs} color={meta.color(t)} />
				<Text numberOfLines={open ? undefined : 2} style={[styles.activitySummary, activity.status === "failed" && { color: t.red }]}>
					{meta.prefix ? `${meta.prefix} ` : ""}{detail.command || detail.toolName || activity.summary}
				</Text>
				{activity.status === "running" ? <Spinner size={iconSize.sm} color={t.orange} /> : null}
				{activity.status === "cancelled" ? <Text style={styles.activityStopped}>stopped</Text> : null}
				{/* Red alone would be the only sign the command failed. */}
				{activity.status === "failed" ? <Text style={styles.runFailed}>failed</Text> : null}
				{expandable ? <FoldChevron open={open} color={t.textTertiary} /> : null}
			</Pressable>
			{open ? (
				<View style={styles.activityDetail}>
					{detail.cwd ? <LabelValue label="cwd" value={detail.cwd} /> : null}
					{detail.reason || detail.text ? <Text style={styles.detailCopy}>{detail.reason ?? detail.text}</Text> : null}
					{detail.arguments !== undefined ? <CodeOutput value={printable(detail.arguments)} /> : null}
					{detail.terminalInput ? <TerminalInput text={detail.terminalInput} truncated={detail.terminalInputTruncated} /> : null}
					{output ? <CodeOutput value={output} /> : null}
					{detail.outputTruncated || detail.patchOutputTruncated ? <Text style={[styles.partial, { color: t.amber }]}>This output is longer than Orchestrator stores, so it stops early. Open the worktree shell for the full run.</Text> : detail.outputMayBePartial ? <Text style={styles.partial}>{detail.outputSource === "stream" ? "Streamed live; the provider may have omitted the beginning." : "The provider's rolled-up output may omit the beginning."} Open the worktree shell for the full run.</Text> : null}
					{Array.isArray(detail.files) ? <FileList files={detail.files} /> : null}
				</View>
			) : null}
		</View>
	);
}

function TerminalInput({ text, truncated }: { text: string; truncated?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.terminalInput}><View style={styles.terminalInputTitle}><Feather name="corner-down-right" size={iconSize.xs} color={t.textTertiary} /><Text style={styles.detailHeading}>Agent typed</Text></View><CodeOutput value={caretNotation(text)} />{truncated ? <Text style={styles.partial}>Orchestrator stopped recording keystrokes at its cap; more were sent.</Text> : null}</View>;
}

function McpToolRow({ activity }: { activity: ConversationActivity }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const [open, setOpen] = useState(activity.status === "failed");
	const detail = activity.detail ?? {};
	const failed = activity.status === "failed" || detail.success === false || Boolean(detail.error);
	const body = detail.arguments !== undefined || detail.result !== undefined || Boolean(detail.error || detail.progress);
	return <View style={styles.activityWrap}>
		<Pressable disabled={!body} accessibilityRole={body ? "button" : undefined} accessibilityState={body ? { expanded: open } : undefined} onPress={() => setOpen((value) => !value)} android_ripple={feedback.android_ripple} style={({ pressed }) => [styles.activityRow, pressed && feedback.pressed]}>
			<Feather name="tool" size={iconSize.xs} color={failed ? t.red : t.textSecondary} />
			<Text style={[styles.server, failed && { color: t.red }]}>{detail.server ?? detail.namespace ? `${detail.server ?? detail.namespace}/` : ""}</Text>
			<Text numberOfLines={1} style={[styles.activitySummary, failed && { color: t.red }]}>{detail.toolName || activity.summary}</Text>
			{detail.progress ? <Text numberOfLines={1} style={styles.activityProgress}>{lastLine(detail.progress)}</Text> : null}
			{failed && activity.status !== "running" ? <Text style={styles.runFailed}>failed</Text> : null}
			{activity.status === "running" ? <Spinner size={iconSize.sm} color={t.orange} /> : activity.status === "cancelled" ? <Text style={styles.activityStopped}>stopped</Text> : body ? <FoldChevron open={open} color={t.textTertiary} /> : null}
		</Pressable>
		{open && body ? <View style={styles.activityDetail}>{detail.error ? <Text style={[styles.detailCopy, { color: t.red }]}>{detail.error}</Text> : null}{detail.arguments !== undefined ? <JsonPayload label="Arguments" value={detail.arguments} /> : null}{detail.result !== undefined ? <JsonPayload label="Result" value={detail.result} /> : null}{detail.progress ? <View><Text style={styles.detailHeading}>Progress</Text><CodeOutput value={detail.progress} />{detail.progressTruncated ? <Text style={styles.partial}>Progress was longer than Orchestrator stores.</Text> : null}</View> : null}</View> : null}
	</View>;
}

function JsonPayload({ label, value }: { label: string; value: unknown }) {
	const styles = useThemedStyles(makeStyles);
	const note = truncationNote(value);
	return <View><Text style={styles.detailHeading}>{label}</Text>{note ? <Text style={styles.detailCopy}>{note}</Text> : <CodeOutput value={printable(value)} />}</View>;
}

function AutoReviewRow({ activity }: { activity: ConversationActivity }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const [open, setOpen] = useState(false);
	const detail = activity.detail ?? {};
	const denied = String(detail.status ?? "").toLowerCase().includes("den");
	const paths = reviewPaths(detail.files);
	const body = Boolean(detail.rationale || detail.command || detail.cwd || detail.host || detail.decisionSource || paths.length);
	return <View style={styles.activityWrap}><Pressable disabled={!body} accessibilityRole={body ? "button" : undefined} accessibilityState={body ? { expanded: open } : undefined} onPress={() => setOpen((value) => !value)} android_ripple={feedback.android_ripple} style={({ pressed }) => [styles.activityRow, pressed && feedback.pressed]}><Feather name={denied ? "shield-off" : "shield"} size={iconSize.xs} color={denied ? t.red : t.green} /><Text style={[styles.reviewDecision, denied && { color: t.red }]}>{denied ? "Auto-declined" : "Auto-approved"}</Text><Text numberOfLines={1} style={styles.activitySummary}>{activity.summary}</Text>{detail.riskLevel ? <Text style={[styles.risk, ["high", "critical"].includes(detail.riskLevel.toLowerCase()) && { color: t.red }]}>{sentenceCase(detail.riskLevel)}</Text> : null}{body ? <FoldChevron open={open} color={t.textTertiary} /> : null}</Pressable>{open && body ? <View style={styles.activityDetail}><Text style={styles.detailCopy}>{denied ? "The provider declined this on your behalf. You were not asked." : "The provider allowed this on your behalf. You were not asked."}</Text>{detail.rationale ? <Text style={styles.reviewRationale}>{detail.rationale}</Text> : null}{detail.command ? <LabelValue label="cmd" value={detail.command} /> : null}{detail.cwd ? <LabelValue label="cwd" value={detail.cwd} /> : null}{detail.host ? <LabelValue label="host" value={detail.host} /> : null}{paths.length ? <LabelValue label="files" value={paths.join(", ")} /> : null}{detail.decisionSource ? <LabelValue label="by" value={detail.decisionSource} /> : null}</View> : null}</View>;
}

function FileChangeActivity({ activity }: { activity: ConversationActivity }) {
	const files = fileChanges(activity.detail?.files);
	return <View style={useThemedStyles(makeStyles).activityWrap}><ExpandableFileList title={activity.summary || `${files.length} changed files`} files={files} fallbackPatch={activity.detail?.patchOutput} fallbackPatchTruncated={activity.detail?.patchOutputTruncated} live={activity.status === "running"} /></View>;
}

function ExpandableFileList({ title, files, fallbackPatch, fallbackPatchTruncated, live }: { title: string; files: ReturnType<typeof fileChanges>; fallbackPatch?: string; fallbackPatchTruncated?: boolean; live?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const [openOverride, setOpenOverride] = useState<boolean | null>(null);
	const open = openOverride ?? Boolean(live && (fallbackPatch || files.some((file) => file.patch)));
	const expandable = files.length > 0 || Boolean(fallbackPatch);
	return <View><Pressable disabled={!expandable} accessibilityRole={expandable ? "button" : undefined} accessibilityState={expandable ? { expanded: open } : undefined} onPress={() => setOpenOverride(!open)} android_ripple={feedback.android_ripple} style={({ pressed }) => [styles.activityRow, pressed && feedback.pressed]}><Feather name="edit-3" size={iconSize.xs} color={t.accent} /><Text numberOfLines={2} style={styles.activitySummary}>{title}</Text>{expandable ? <FoldChevron open={open} color={t.textTertiary} /> : null}</Pressable>{open ? <View style={styles.activityDetail}>{files.map((file) => <FileChangeRow key={`${file.oldPath ?? ""}:${file.path}`} file={file} live={live} />)}{fallbackPatch ? <PatchBlock patch={fallbackPatch} truncated={fallbackPatchTruncated} /> : null}</View> : null}</View>;
}

function FileChangeRow({ file, live }: { file: ReturnType<typeof fileChanges>[number]; live?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const [open, setOpen] = useState(Boolean(live && file.patch));
	const hasPatch = Boolean(file.patch);
	const mark = file.status === "added" ? "A" : file.status === "deleted" ? "D" : file.status === "renamed" ? "R" : "M";
	return <View><Pressable disabled={!hasPatch} accessibilityRole={hasPatch ? "button" : undefined} accessibilityState={hasPatch ? { expanded: open } : undefined} onPress={() => setOpen((value) => !value)} android_ripple={feedback.android_ripple} style={({ pressed }) => [styles.fileRow, hasPatch && styles.fileRowTap, pressed && feedback.pressed]}><Text style={[styles.fileMark, { color: file.status === "deleted" ? t.red : file.status === "added" ? t.green : t.accent }]}>{mark}</Text><Text selectable numberOfLines={2} style={styles.filePath}>{file.oldPath ? `${file.oldPath} → ${file.path}` : file.path}</Text><Text style={styles.fileStat}>+{file.additions} −{file.deletions}</Text>{hasPatch ? <FoldChevron open={open} color={t.textTertiary} /> : null}</Pressable>{open && file.patch ? <PatchBlock patch={file.patch} truncated={file.patchTruncated} /> : null}</View>;
}

function PatchBlock({ patch, truncated }: { patch: string; truncated?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return <View><Pressable onLongPress={() => { void Clipboard.setStringAsync(patch); haptics.success(); }}><HighlightedCodeText code={patch} language="diff" style={styles.output} /></Pressable>{truncated ? <Text style={[styles.partial, { color: t.amber }]}>This patch is longer than Orchestrator stores. The complete change remains in the worktree.</Text> : null}</View>;
}

function PlanActivity({ activity }: { activity: ConversationActivity }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const [open, setOpen] = useState(activity.status === "running");
	const steps = activity.detail?.steps ?? [];
	return <View style={styles.planCard}><Pressable
			accessibilityRole="button"
			accessibilityState={{ expanded: open }}
			onPress={() => setOpen((value) => !value)}
			android_ripple={feedback.android_ripple}
			style={({ pressed }) => [styles.planHeader, pressed && feedback.pressed]}
			><Feather name="list" size={iconSize.xs} color={t.textTertiary} /><Text style={styles.planTitle}>{activity.summary || "Plan updated"}</Text><Text style={styles.planCount}>{steps.filter((step) => step.status === "completed").length}/{steps.length}</Text><FoldChevron open={open} color={t.textTertiary} /></Pressable>{open ? <View style={styles.planBody}>{activity.detail?.explanation ? <Text style={styles.detailCopy}>{activity.detail.explanation}</Text> : null}{steps.map((step, index) => <View key={index} accessible accessibilityLabel={`${step.status.replace("_", " ")}: ${step.text}`} style={styles.planStep}><PlanStepGlyph status={step.status} /><Text style={[styles.planStepText, step.status === "completed" && styles.planDone]}>{step.text}</Text></View>)}{!steps.length ? <Text style={styles.detailCopy}>{activity.detail?.text || activity.summary}</Text> : null}</View> : null}</View>;
}

/** A plan step's state as a glyph, so it never rests on colour alone. */
function PlanStepGlyph({ status }: { status: string }) {
	const t = useTheme();
	return <Feather name={status === "completed" ? "check-circle" : status === "in_progress" ? "disc" : "circle"} size={iconSize.sm} color={status === "completed" ? t.green : status === "in_progress" ? t.orange : t.textFaint} />;
}

function ActivityRun({ activities }: { activities: ConversationActivity[] }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const [override, setOverride] = useState<boolean | null>(null);
	const hierarchy = activityHierarchy(activities);
	if (activities.length === 1 && hierarchy[0]?.children.length === 0) return <ActivityRow activity={activities[0]} />;
	const running = activities.some((activity) => activity.status === "running");
	const failed = activities.filter((activity) => activity.status === "failed").length;
	const cancelled = activities.filter((activity) => activity.status === "cancelled").length;
	const streaming = activities.some((activity) => activity.status === "running" && Boolean(activity.detail?.output));
	const open = override ?? streaming;
	return <View style={styles.runWrap}>
		<Pressable
			accessibilityRole="button"
			accessibilityState={{ expanded: open }}
			onPress={() => setOverride(!open)}
			android_ripple={feedback.android_ripple}
			style={({ pressed }) => [styles.runSummary, pressed && feedback.pressed]}
		>
			<Text style={styles.runText}>{summarizeActivities(activities)}</Text>
			{failed ? <Text style={styles.runFailed}>{failed} failed</Text> : null}
			{cancelled ? <Text style={styles.runStopped}>{cancelled} stopped</Text> : null}
			{running ? <Spinner size={iconSize.sm} color={t.textTertiary} /> : null}
			<FoldChevron open={open} color={t.textTertiary} />
		</Pressable>
		{open ? <View style={styles.runDetail}>{hierarchy.map((node) => <ActivityTree key={node.activity.id} node={node} />)}</View> : null}
	</View>;
}

function ActivityTree({ node }: { node: ActivityNode }) {
	return <View><ActivityRow activity={node.activity} />{node.children.length ? <NestedAgentRun nodes={node.children} /> : null}</View>;
}

function NestedAgentRun({ nodes }: { nodes: ActivityNode[] }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const [open, setOpen] = useState(false);
	const count = countActivityNodes(nodes);
	const running = activityNodesRunning(nodes);
	return <View style={styles.subagent}>
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`${open ? "Hide" : "Show"} subagent work, ${count} ${count === 1 ? "step" : "steps"}`}
			accessibilityState={{ expanded: open }}
			onPress={() => setOpen((value) => !value)}
			android_ripple={feedback.android_ripple}
			style={({ pressed }) => [styles.subagentHeader, pressed && feedback.pressed]}
		>
			<Feather name="git-branch" size={iconSize.xs} color={t.textTertiary} />
			<Text style={styles.subagentLabel}>Subagent · {count} {count === 1 ? "step" : "steps"}</Text>
			{running ? <Spinner size={iconSize.sm} color={t.textTertiary} /> : null}
			<FoldChevron open={open} color={t.textTertiary} />
		</Pressable>
		{open ? nodes.map((child) => <ActivityTree key={child.activity.id} node={child} />) : null}
	</View>;
}

/**
 * The turn's status row, which sits between the prompt and the work: a ticking
 * "Working · 12s" while the turn runs, then "Worked for 20s" once it settles. When
 * there is folded work the settled row is the toggle that reveals it.
 */
function TurnStatusRow({ turn, foldable, open, onToggle }: { turn: ConversationTurn; foldable: boolean; open: boolean; onToggle(): void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const running = turn.state === "running";
	const [nowMs, setNowMs] = useState(() => Date.now());
	useEffect(() => {
		if (!running) return;
		setNowMs(Date.now());
		const timer = setInterval(() => setNowMs(Date.now()), 1_000);
		return () => clearInterval(timer);
	}, [running, turn.id]);
	const duration = elapsed(turn.startedAt ?? turn.requestedAt, turn.completedAt);
	const workingDuration = running ? workingElapsedLabel(turn.startedAt ?? turn.requestedAt, nowMs) : undefined;
	const label = turn.rolledBack
		? "Rolled back"
		: turn.state === "completed"
			? duration ? `Worked for ${duration}` : "Work completed"
			: turn.state === "failed"
				? duration ? `Failed after ${duration}` : "Turn failed"
				: turn.state === "interrupted"
					? duration ? `Stopped after ${duration}` : "Turn stopped"
					: workingDuration ? `Working · ${workingDuration}` : "Working";
	const text = <Text style={[styles.turnState, turn.state === "failed" && { color: t.red }]}>{label}</Text>;
	// One element throughout, so the spinner can leave and the label slide into
	// its place when the turn settles instead of the whole row remounting. The row
	// keeps the startup checklist header's height, so the slop is what makes the
	// settled toggle a full touch target.
	return <Pressable
		accessibilityRole={foldable ? "button" : undefined}
		accessibilityState={foldable ? { expanded: open } : undefined}
		disabled={!foldable}
		onPress={onToggle}
		hitSlop={foldable ? (touchTarget - STATUS_ROW_HEIGHT) / 2 : undefined}
		android_ripple={feedback.android_ripple}
		style={({ pressed }) => [styles.statusRow, pressed && feedback.pressed]}
	>
		{running ? <StatusSpinner><Spinner size={iconSize.xs} color={t.textTertiary} /></StatusSpinner> : null}
		<StatusLabel>{text}</StatusLabel>
		{foldable ? <FoldChevron open={open} color={t.textTertiary} /> : null}
	</Pressable>;
}

function TurnSummary({ turn, onRollback }: { turn: ConversationTurn; onRollback?(turnId: string): Promise<number> }) {
	const styles = useThemedStyles(makeStyles);
	const settled = turn.state !== "running" && turn.state !== "queued";
	const canRollBack = settled && Boolean(turn.providerTurnId) && !turn.rolledBack;
	return (
		<View style={styles.turnWrap}>
			{turn.plan?.steps.length ? <TurnPlan turn={turn} /> : null}
			{turn.diff?.files.length ? <ChangedFiles turn={turn} /> : null}
			{onRollback && canRollBack ? <View style={styles.turnLine}><TurnRollbackButton turnId={turn.id} onRollback={onRollback} /></View> : null}
			{turn.errorMessage ? <Text style={styles.turnError}>{turn.errorMessage}</Text> : null}
		</View>
	);
}

function TurnPlan({ turn }: { turn: ConversationTurn }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const [open, setOpen] = useState(turn.state === "running");
	const done = turn.plan?.steps.filter((step) => step.status === "completed").length ?? 0;
	return (
		<View style={styles.planCard}>
			<Pressable
				accessibilityRole="button"
				accessibilityState={{ expanded: open }}
				onPress={() => setOpen((value) => !value)}
				android_ripple={feedback.android_ripple}
				style={({ pressed }) => [styles.planHeader, pressed && feedback.pressed]}
				>
				<Feather name="list" size={iconSize.xs} color={t.textTertiary} />
				<Text style={styles.planTitle}>Plan</Text>
				{turn.state === "running" ? <Text style={styles.planLive}>Still changing</Text> : null}
				<Text style={styles.planCount}>{done}/{turn.plan?.steps.length ?? 0}</Text>
				<FoldChevron open={open} color={t.textTertiary} />
			</Pressable>
			{open ? <View style={styles.planBody}>
				{turn.plan?.explanation ? <Text style={styles.detailCopy}>{turn.plan.explanation}</Text> : null}
				{turn.plan?.steps.map((step, index) => <View key={index} accessible accessibilityLabel={`${step.status.replace("_", " ")}: ${step.text}`} style={styles.planStep}>
					<PlanStepGlyph status={step.status} />
					<Text style={[styles.planStepText, step.status === "completed" && styles.planDone]}>{step.text}</Text>
					<Text style={styles.planStepState}>{sentenceCase(step.status.replace("_", " "))}</Text>
				</View>)}
			</View> : null}
		</View>
	);
}

function ChangedFiles({ turn }: { turn: ConversationTurn }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const feedback = usePressFeedback("row");
	const reviewFeedback = usePressFeedback("button");
	const files = useFilesNavigation();
	const [open, setOpen] = useState(false);
	const changed = turn.diff?.files ?? [];
	const additions = changed.reduce((sum, file) => sum + file.additions, 0);
	const deletions = changed.reduce((sum, file) => sum + file.deletions, 0);
	return <View style={styles.planCard}>
		<View style={styles.changedHeader}>
			<Pressable
				accessibilityRole="button"
				accessibilityState={{ expanded: open }}
				accessibilityLabel={`${changed.length} changed ${changed.length === 1 ? "file" : "files"}, ${additions} lines added, ${deletions} removed`}
				onPress={() => setOpen((value) => !value)}
				android_ripple={feedback.android_ripple}
				style={({ pressed }) => [styles.planHeader, styles.changedToggle, pressed && feedback.pressed]}
				>
				<Feather name="file-text" size={iconSize.xs} color={t.textTertiary} />
				<Text numberOfLines={1} style={[styles.planTitle, styles.changedTitle]}>{files ? `${changed.length} ${changed.length === 1 ? "file" : "files"}` : `${changed.length} changed ${changed.length === 1 ? "file" : "files"}`}</Text>
				{turn.state === "running" ? <><Text numberOfLines={1} style={styles.planLive}>Growing</Text><Spinner size={iconSize.sm} color={t.textTertiary} /></> : null}
				<Text style={[styles.changedStat, { color: t.green }]}>+{additions}</Text>
				<Text style={[styles.changedStat, { color: t.red }]}>−{deletions}</Text>
				<FoldChevron open={open} color={t.textTertiary} />
			</Pressable>
			{files ? <Pressable
				accessibilityRole="button"
				accessibilityLabel="Review changes"
				accessibilityHint="Opens every changed file in this session"
				hitSlop={(touchTarget - REVIEW_PILL_HEIGHT) / 2}
				onPress={() => files.openChanges()}
				android_ripple={reviewFeedback.android_ripple}
				style={({ pressed }) => [styles.changedReview, pressed && reviewFeedback.pressed]}
			>
				<AppSymbol name="diff" size={iconSize.sm} color={t.textPrimary} />
				<Text style={styles.changedReviewText}>Review</Text>
			</Pressable> : null}
		</View>
		{open ? <View style={styles.fileBody}>{changed.map((file) => {
			const row = <>
				<Text style={[styles.fileMark, { color: file.status === "deleted" ? t.red : file.status === "added" ? t.green : t.accent }]}>{file.status[0].toUpperCase()}</Text>
				<Text selectable={!files} style={styles.filePath}>{file.oldPath ? `${file.oldPath} → ${file.path}` : file.path}</Text>
				<Text style={styles.fileStat}>+{file.additions} −{file.deletions}</Text>
				{files ? <AppSymbol name="chevronForward" size={iconSize.sm} color={t.textTertiary} /> : null}
			</>;
			return files
				? <Pressable
					key={`${file.oldPath}:${file.path}`}
					accessibilityRole="button"
					accessibilityLabel={`${file.path}, ${file.status}, ${file.additions} added, ${file.deletions} removed`}
					accessibilityHint="Opens the diff"
					onPress={() => files.openFile(file.path, file.oldPath)}
					android_ripple={feedback.android_ripple}
					style={({ pressed }) => [styles.fileRow, styles.fileRowTap, pressed && feedback.pressed]}
				>{row}</Pressable>
				: <View key={`${file.oldPath}:${file.path}`} style={styles.fileRow}>{row}</View>;
		})}{turn.diff?.truncated ? <Text style={[styles.partial, { color: t.amber }]}>{files ? "This turn changed more files than Orchestrator lists here. Review changes shows all of them." : "This turn changed more files than Orchestrator lists here. Open the worktree shell for the complete diff."}</Text> : null}</View> : null}
	</View>;
}

/**
 * What was asked, with no way to answer it — for a request whose controls live
 * in the composer. The timeline stays the record; the composer is the surface.
 */
function RequestEcho({ title, detail }: { title: string; detail?: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.approvalResolved}>
		<View style={[styles.approvalDot, { backgroundColor: t.amber }]} />
		<Text style={styles.approvalResolvedLabel}>{title}</Text>
		{detail ? <Text selectable numberOfLines={1} style={styles.approvalResolvedCommand}>{detail}</Text> : null}
	</View>;
}

function ApprovalCard({ activity, busy, onDecide, handledBelow }: { activity: ConversationActivity; busy: boolean; onDecide(requestId: string, decisionId: string): Promise<void>; handledBelow?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [submitting, setSubmitting] = useState<string>();
	const [submitError, setSubmitError] = useState<string>();
	const pending = activity.status === "pending";
	const presentation = requestPresentation("approval", pending);
	const command = activity.detail?.command ?? activity.summary;
	if (pending && handledBelow) return <RequestEcho title={presentation.title} detail={command} />;
	if (!pending) return <View style={styles.approvalResolved}>
		<Feather name={presentation.icon} size={iconSize.xs} color={t.textFaint} />
		<Text style={styles.approvalResolvedLabel}>{presentation.title}</Text>
		<Text selectable numberOfLines={1} style={styles.approvalResolvedCommand}>{command}</Text>
	</View>;
	return <View style={styles.approvalRequest}>
		<View style={styles.approvalStatus}><View style={styles.approvalDot} /><Text style={styles.approvalStatusText}>{presentation.title}</Text></View>
		{activity.detail?.reason ? <Text selectable style={styles.requestCopy}>{activity.detail.reason}</Text> : null}
		<View style={styles.approvalCommandSurface}>
			<Feather name="terminal" size={iconSize.xs} color={t.textTertiary} />
			<Text selectable style={styles.requestCommand}>{command}</Text>
		</View>
		{activity.detail?.cwd ? <LabelValue label="cwd" value={activity.detail.cwd} /> : null}
		{activity.decisions?.length ? <View style={styles.approvalActions}>{activity.decisions.map((decision, index) => {
			const label = submitting === decision.id ? "Sending…" : decision.label;
			// One haptic per decision, when it lands: the press itself only starts the work.
			return <ElicitationAction key={decision.id} label={label} primary={index === 0} reportsOutcome disabled={busy || Boolean(submitting) || !activity.requestId} onPress={() => {
				setSubmitting(decision.id);
				setSubmitError(undefined);
				void onDecide(activity.requestId ?? "", decision.id)
					.then(() => haptics.success())
					.catch((cause) => {
						setSubmitError(userFacingError(cause));
						haptics.error();
					})
					.finally(() => setSubmitting(undefined));
			}} />;
		})}</View> : <Text style={[styles.partial, { color: t.amber }]}>The agent offered no decisions Orchestrator can present. Open diagnostics from the host.</Text>}
		{submitError ? <Text accessibilityRole="alert" selectable style={styles.validation}>{submitError}</Text> : null}
	</View>;
}

function UserInputCard({ activity, busy, onResolve, handledBelow }: { activity: ConversationActivity; busy: boolean; onResolve(requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>): Promise<void>; handledBelow?: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const schema = activity.detail?.schema;
	const [values, setValues] = useState<Record<string, unknown>>(() => Object.fromEntries(Object.entries(schema?.properties ?? {}).map(([key, property]) => [key, initialInputValue(property)])));
	const [validationError, setValidationError] = useState<string>();
	const [submitting, setSubmitting] = useState(false);
	const [submitError, setSubmitError] = useState<string>();
	const [activeQuestion, setActiveQuestion] = useState(0);
	const pending = activity.status === "pending";
	const url = activity.detail?.inputMode === "url" ? safeHttpURL(activity.detail.url) : undefined;
	const properties = Object.entries(schema?.properties ?? {});
	const questionGroups = groupedQuestionInputs(properties);
	const visibleProperties = questionGroups?.[activeQuestion] ?? properties;
	const hasPreviousQuestion = Boolean(questionGroups && activeQuestion > 0);
	const hasNextQuestion = Boolean(questionGroups && activeQuestion < questionGroups.length - 1);
	const step = elicitationStepPresentation(activeQuestion, questionGroups?.length ?? 1);
	const promptCopy = elicitationPromptCopy(activity.detail?.message || schema?.description || activity.summary);
	if (pending && handledBelow) return <RequestEcho title={step.status} detail={promptCopy} />;
	const resolve = async (action: "accept" | "decline" | "cancel", content?: Record<string, unknown>) => {
		if (submitting || !activity.requestId) return;
		setSubmitting(true);
		setSubmitError(undefined);
		// One haptic per answer, when it lands: the press itself only starts the work.
		try {
			await onResolve(activity.requestId, action, content);
			haptics.success();
		} catch (cause) {
			setSubmitError(userFacingError(cause));
			haptics.error();
		} finally { setSubmitting(false); }
	};
	const submit = () => {
		const visibleRequired = visibleProperties.flatMap(([name]) => schema?.required?.includes(name) ? [name] : []);
		const missing = missingRequiredInputs(visibleRequired, values);
		if (missing.length) { setValidationError(`Complete ${missing.join(", ")} before continuing.`); haptics.error(); return; }
		for (const [name, property] of visibleProperties) {
			const problem = validateInput(property, values[name]);
			if (problem) { setValidationError(`${property.title || humanizeInputName(name)} ${problem}.`); haptics.error(); return; }
		}
		setValidationError(undefined);
		if (hasNextQuestion) {
			setActiveQuestion((current) => current + 1);
			return;
		}
		void resolve("accept", values);
	};
	return <View style={pending ? styles.inputRequest : [styles.requestCard, styles.requestCardResolved]}>
		<View style={styles.inputRequestStatus}><View style={[styles.inputRequestDot, { backgroundColor: pending ? t.amber : t.textFaint }]} /><Text style={styles.inputRequestStatusText}>{pending ? step.status : "Input resolved"}</Text></View>
		{promptCopy ? <Text style={styles.requestCopy}>{promptCopy}</Text> : null}
		{pending && activity.detail?.inputMode === "url" ? <View style={styles.urlBox}><Text selectable style={styles.urlText}>{url?.href ?? "The provider supplied an unsafe or invalid URL."}</Text></View> : null}
		{pending && activity.detail?.inputMode !== "url" && schema?.properties ? <View style={styles.form}>
			{visibleProperties.map(([name, property]) => <ElicitationInputField key={name} name={name} property={property} required={schema.required?.includes(name) ?? false} value={values[name]} onChange={(value) => { setValidationError(undefined); setValues((old) => ({ ...old, [name]: value })); }} />)}
		</View> : null}
		{validationError ? <Text accessibilityRole="alert" style={styles.validation}>{validationError}</Text> : null}
		{submitError ? <Text accessibilityRole="alert" style={styles.validation}>{submitError}</Text> : null}
		{pending && activity.requestId ? <View style={styles.inputActions}>
			<ElicitationAction label="Cancel" reportsOutcome disabled={busy || submitting} onPress={() => void resolve("cancel")} />
			<ElicitationAction label={activity.detail?.inputMode === "url" ? "Decline" : "Skip"} reportsOutcome disabled={busy || submitting} onPress={() => void resolve("decline")} />
			<View style={{ flex: 1 }} />
			{activity.detail?.inputMode !== "url" && hasPreviousQuestion ? <ElicitationAction label="Back" disabled={busy || submitting} onPress={() => { setValidationError(undefined); setActiveQuestion((current) => Math.max(0, current - 1)); }} /> : null}
			{activity.detail?.inputMode === "url" ? <ElicitationAction label={submitting ? "Opening…" : "Open link"} primary reportsOutcome disabled={busy || submitting || !url} onPress={() => {
				if (!url) return;
				void Linking.openURL(url.href)
					.then(() => resolve("accept"))
					.catch(() => {
						setValidationError("This link could not be opened on this device.");
						haptics.error();
					});
			}} /> : <ElicitationAction label={submitting ? "Sending…" : step.primaryLabel} primary reportsOutcome disabled={busy || submitting} onPress={submit} />}
		</View> : pending ? <Text style={[styles.partial, { color: t.amber }]}>This request has no provider identity, so Orchestrator cannot answer it safely. Open diagnostics on the host.</Text> : <Text style={styles.partial}>Already answered. This card is kept for the record.</Text>}
	</View>;
}

function CompactionMarker({ activity }: { activity: ConversationActivity }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const after = activity.detail?.tokensAfter;
	const window = activity.detail?.contextWindow;
	return <View style={styles.compaction}><Feather name="archive" size={iconSize.xs} color={t.textFaint} /><Text style={styles.compactionText}>History compacted{activity.detail?.tokensReclaimed ? ` · −${formatTokens(activity.detail.tokensReclaimed)}` : ""}{after && window ? ` · ${Math.round((after / window) * 100)}% full` : ""}</Text></View>;
}

function ErrorActivity({ activity }: { activity: ConversationActivity }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// Providers set several fields to the same sentence — Codex sends the
	// usage-limit text as both summary and detail.error — so rendering each in
	// turn printed one failure twice inside this card. Same rule as the renderer.
	const { headline, detail } = providerErrorCopy(activity);
	return <View style={[styles.errorCard, { borderColor: t.tintRed }]}><Feather name="alert-triangle" size={iconSize.sm} color={t.red} /><View style={{ flex: 1 }}><Text style={styles.errorTitle}>{headline}</Text>{detail ? <Text selectable style={styles.errorCopy}>{detail}</Text> : null}</View></View>;
}

function EmptyConversation({ controller }: { controller: string }) {
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.empty}><View style={styles.emptyMascot}><MascotLamp size={48} /></View><Text accessibilityRole="header" style={styles.emptyTitle}>{controller === "connecting" ? "Connecting to the agent…" : "Start with a task"}</Text></View>;
}

function LabelValue({ label, value }: { label: string; value: string }) { const styles = useThemedStyles(makeStyles); return <View style={styles.labelValue}><Text style={styles.detailLabel}>{label}</Text><Text selectable style={styles.detailValue}>{value}</Text></View>; }
function CodeOutput({ value }: { value: string }) { const styles = useThemedStyles(makeStyles); return <Pressable onLongPress={() => { void Clipboard.setStringAsync(value); haptics.success(); }}><Text selectable style={styles.output}>{value}</Text></Pressable>; }
function FileList({ files }: { files: unknown[] }) { const styles = useThemedStyles(makeStyles); return <View style={styles.fileBody}>{files.map((file, index) => <View key={index} style={styles.fileListRow}><FirstLineMarker lineStyle={styles.fileLine} style={styles.fileBulletColumn}><View style={styles.fileBullet} /></FirstLineMarker><Text selectable style={styles.filePath}>{typeof file === "string" ? file : printable(file)}</Text></View>)}</View>; }

function printable(value: unknown): string {
	if (value === null || value === undefined) return "";
	if (typeof value === "string") return value;
	try { return JSON.stringify(value, null, 2); } catch { return String(value); }
}

function fileChanges(value: unknown): FileChange[] {
	if (!Array.isArray(value)) return [];
	return value.flatMap((entry): FileChange[] => {
		if (!entry || typeof entry !== "object") return [];
		const file = entry as Record<string, unknown>;
		if (typeof file.path !== "string") return [];
		const status = ["added", "modified", "deleted", "renamed"].includes(String(file.status)) ? String(file.status) as FileChange["status"] : "modified";
		return [{ path: file.path, oldPath: typeof file.oldPath === "string" ? file.oldPath : undefined, status, additions: typeof file.additions === "number" ? file.additions : 0, deletions: typeof file.deletions === "number" ? file.deletions : 0, patch: typeof file.patch === "string" ? file.patch : undefined, patchTruncated: file.patchTruncated === true }];
	});
}

function reviewPaths(value: unknown): string[] {
	if (!Array.isArray(value)) return [];
	return value.flatMap((entry) => typeof entry === "string" ? [entry] : entry && typeof entry === "object" && typeof (entry as { path?: unknown }).path === "string" ? [(entry as { path: string }).path] : []);
}

function truncationNote(value: unknown): string | undefined {
	if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
	const record = value as { truncated?: unknown; bytes?: unknown };
	if (record.truncated !== true) return undefined;
	const bytes = typeof record.bytes === "number" ? ` (${formatBytes(record.bytes)})` : "";
	return `This payload${bytes} was larger than Orchestrator stores, so it was not kept.`;
}

function formatBytes(bytes: number): string {
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/** "high" or "HIGH" as "High": a provider's word, shown in sentence case. */
function sentenceCase(value: string): string { return value ? value[0].toUpperCase() + value.slice(1).toLowerCase() : value; }

function lastLine(value: string): string { return value.trimEnd().split("\n").at(-1) ?? ""; }

function activityMeta(activity: ConversationActivity): { icon: keyof typeof Feather.glyphMap; prefix?: string; color(t: Theme): string } {
	switch (activity.activityKind) {
		case "command": return { icon: "terminal", color: (t) => activity.status === "failed" ? t.red : t.textTertiary };
		case "file_change": return { icon: "edit-3", prefix: "Changed", color: (t) => t.accent };
		case "mcp_tool": return { icon: "tool", prefix: activity.detail?.server ? `${activity.detail.server} ·` : "MCP ·", color: (t) => t.textSecondary };
		case "auto_review": return { icon: "shield", prefix: "Reviewed", color: (t) => t.green };
		default: return { icon: "activity", color: (t) => t.textTertiary };
	}
}

function elapsed(start?: string, end?: string): string | undefined { if (!start || !end) return undefined; const ms = Date.parse(end) - Date.parse(start); if (!Number.isFinite(ms) || ms < 0) return undefined; const seconds = Math.round(ms / 1000); return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`; }
function formatTokens(value: number): string { return value >= 1_000 ? `${(value / 1_000).toFixed(value >= 10_000 ? 0 : 1)}k` : String(value); }
/** The turn status row's height, the same as the startup checklist header it replaces. */
const STATUS_ROW_HEIGHT = 34;
/** The Review pill's drawn height; its hit slop makes up the rest of a touch target. */
const REVIEW_PILL_HEIGHT = 32;

const makeStyles = (t: Theme) => {
	// Every card in the timeline has one shape: the card radius, and the
	// divider's width for its edge.
	const card = { borderRadius: radius.md, borderCurve: "continuous", borderWidth: rowDividerWidth } as const;
	// One line of a file list's path; its bullet is held level with it.
	const fileLine = { fontFamily: t.fontMono, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight };
	return StyleSheet.create({
		timelineWrap: { flex: 1, position: "relative", backgroundColor: t.bgBase },
		detachedEditor: { paddingHorizontal: space.md, paddingTop: space.xs, paddingBottom: space.xs, backgroundColor: t.bgBase },
		emptySurface: { flex: 1, justifyContent: "center" },
		list: { flex: 1, backgroundColor: t.bgBase },
		content: { paddingHorizontal: space.xl, paddingTop: space.md, paddingBottom: space.xxxl },
		older: { alignSelf: "center", minHeight: touchTarget, flexDirection: "row", gap: space.xs, alignItems: "center", paddingHorizontal: space.md, marginBottom: space.xs },
		olderText: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize },
		userRow: { alignItems: "flex-end", paddingTop: space.lg, paddingBottom: space.sm },
		userMenu: { maxWidth: "88%" },
		menuBubble: { maxWidth: "100%" },
		userBubble: { maxWidth: "88%", backgroundColor: userMessageSurfaceStyle(t).backgroundColor, borderRadius: radius.xl, borderCurve: "continuous", paddingHorizontal: space.lg, paddingVertical: space.md },
		quotedBubble: { width: "88%" },
		userText: { fontFamily: "Geist_400Regular", color: userMessageSurfaceStyle(t).foregroundColor, fontSize: type.callout.fontSize, lineHeight: type.callout.lineHeight },
		delivery: { fontFamily: "Geist_400Regular", marginTop: space.xxs, color: t.amber, fontSize: type.footnote.fontSize },
		attachments: { flexDirection: "row", flexWrap: "wrap", gap: space.xs },
		attachmentsSpaced: { marginTop: space.sm },
		attachmentTile: { overflow: "hidden", borderRadius: radius.sm, backgroundColor: t.bgColumn },
		attachmentTileImage: { width: "100%", height: "100%" },
		// alignSelf keeps a chip its own height next to a tile: the row container's
		// default stretch would otherwise blow it up to the tile's 104/160px.
		attachmentChip: { alignSelf: "flex-start", flexDirection: "row", alignItems: "center", gap: space.xs, borderRadius: radius.sm, borderWidth: rowDividerWidth, borderColor: t.borderSubtle, paddingHorizontal: space.sm, paddingVertical: space.xs },
		attachmentName: { fontFamily: "Geist_400Regular", flexShrink: 1, color: t.textSecondary, fontSize: type.footnote.fontSize },
		attachmentRetry: { fontFamily: "Geist_600SemiBold", color: t.accent, fontSize: type.footnote.fontSize, fontWeight: "600" },
		originMessage: { marginVertical: space.sm, borderLeftWidth: 2, borderLeftColor: t.borderStrong, paddingLeft: space.sm, gap: space.xxs },
		originHeader: { flexDirection: "row", alignItems: "center", gap: space.xxs },
		originLabel: { fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.footnote.fontSize, fontWeight: "600" },
		originText: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
		originMore: { alignSelf: "flex-start", minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.xxs },
		originMoreText: { fontFamily: "Geist_600SemiBold", color: t.accent, fontSize: type.footnote.fontSize, fontWeight: "600" },
		steerBubble: { backgroundColor: t.bgSubtle },
		steerLabel: { fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.footnote.fontSize, fontWeight: "600", marginBottom: space.hair },
		assistantRow: { paddingVertical: space.lg },
		sender: { fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.footnote.fontSize, fontWeight: "600", marginBottom: space.xxs },
		responseActions: { flexDirection: "row", alignItems: "center", gap: space.md },
		// The glyph lines up with the reply's left edge. The target reaches into the
		// row's bottom padding rather than up over the text, where a long press selects.
		copy: { alignSelf: "flex-start", alignItems: "center", justifyContent: "center", width: touchTarget, height: touchTarget, marginLeft: -(touchTarget - iconSize.md) / 2, marginBottom: -space.md },
		systemSignal: { ...card, marginVertical: space.xs, flexDirection: "row", alignItems: "flex-start", gap: space.sm, borderColor: t.borderDefault, backgroundColor: t.bgSurface, padding: space.sm },
		systemTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.footnote.fontSize, fontWeight: "600" },
		systemDetail: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, marginTop: space.hair },
		activityWrap: { paddingVertical: space.hair },
		activityRow: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.sm, paddingVertical: space.xs },
		activitySummary: { flex: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontFamily: t.fontMono },
		server: { color: t.textTertiary, fontSize: type.footnote.fontSize, fontFamily: t.fontMono },
		activityProgress: { fontFamily: "Geist_400Regular", maxWidth: "28%", color: t.textFaint, fontSize: type.footnote.fontSize },
		activityStopped: { fontFamily: "Geist_600SemiBold", color: t.textFaint, fontSize: type.footnote.fontSize, fontWeight: "600" },
		activityDetail: { marginLeft: space.xl, marginBottom: space.xs, borderLeftWidth: rowDividerWidth, borderLeftColor: t.borderSubtle, paddingLeft: space.md, gap: space.xs },
		terminalInput: { gap: space.xxs },
		terminalInputTitle: { flexDirection: "row", alignItems: "center", gap: space.xxs },
		reviewDecision: { fontFamily: "Geist_600SemiBold", color: t.green, fontSize: type.footnote.fontSize, fontWeight: "600" },
		risk: { fontFamily: "Geist_600SemiBold", color: t.amber, fontSize: type.footnote.fontSize, fontWeight: "600" },
		reviewRationale: { fontFamily: "Geist_400Regular", color: t.textPrimary, backgroundColor: t.bgColumn, borderRadius: radius.sm, padding: space.sm, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		runWrap: { paddingVertical: space.hair },
		runSummary: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.sm, paddingVertical: space.xs },
		runText: { fontFamily: "Geist_400Regular", flex: 1, color: t.textTertiary, fontSize: type.footnote.fontSize },
		runFailed: { fontFamily: "Geist_600SemiBold", color: t.red, fontSize: type.footnote.fontSize, fontWeight: "600" },
		runStopped: { fontFamily: "Geist_600SemiBold", color: t.textFaint, fontSize: type.footnote.fontSize, fontWeight: "600" },
		runDetail: { ...card, marginBottom: space.xs, borderColor: t.borderSubtle, backgroundColor: t.bgSubtle, paddingHorizontal: space.sm, paddingVertical: space.hair },
		subagent: { marginLeft: space.xl, marginBottom: space.xxs, borderLeftWidth: rowDividerWidth, borderLeftColor: t.borderStrong, paddingLeft: space.sm },
		subagentHeader: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.xs, paddingVertical: space.xs },
		subagentLabel: { fontFamily: "Geist_400Regular", flex: 1, color: t.textFaint, fontSize: type.footnote.fontSize },
		detailCopy: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		labelValue: { flexDirection: "row", gap: space.sm },
		detailLabel: { minWidth: space.huge, color: t.textFaint, fontSize: type.footnote.fontSize, fontFamily: t.fontMono },
		// Names a block of detail: what the agent typed, a tool's arguments or result.
		detailHeading: { fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.footnote.fontSize, fontWeight: "600" },
		detailValue: { flex: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, fontFamily: t.fontMono },
		output: { color: t.textSecondary, backgroundColor: t.bgColumn, borderRadius: radius.sm, padding: space.sm, fontFamily: t.fontMono, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		partial: { fontFamily: "Geist_400Regular", color: t.textFaint, fontSize: type.footnote.fontSize },
		turnWrap: { paddingTop: space.sm, paddingBottom: space.lg, gap: space.sm },
		turnLine: { flexDirection: "row", alignItems: "center", justifyContent: "flex-end", gap: space.sm },
		// Full width with a rule under it, matching the desktop's Working / Worked
		// rows and the setup checklist header, so the line holds through the handoff.
		statusRow: { minHeight: STATUS_ROW_HEIGHT, flexDirection: "row", alignItems: "center", gap: space.xs, paddingVertical: space.xs, borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderDefault },
		turnState: { fontFamily: "Geist_500Medium", color: t.textTertiary, fontSize: type.caption1.fontSize, fontWeight: "500" },
		turnError: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, textAlign: "right" },
		planCard: { ...card, backgroundColor: t.bgSurface, borderColor: t.borderDefault, overflow: "hidden" },
		planHeader: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.md },
		changedHeader: { flexDirection: "row", alignItems: "center", paddingRight: space.sm },
		changedToggle: { flex: 1, minWidth: 0 },
		changedTitle: { flexShrink: 1 },
		changedReview: { minHeight: REVIEW_PILL_HEIGHT, flexDirection: "row", alignItems: "center", gap: space.xxs, paddingHorizontal: space.sm, borderRadius: radius.pill, backgroundColor: t.bgElevatedHover, borderWidth: rowDividerWidth, borderColor: t.borderDefault },
		changedStat: { fontFamily: "Geist_400Regular", fontSize: type.footnote.fontSize },
		changedReviewText: { fontFamily: "Geist_500Medium", fontSize: type.footnote.fontSize, color: t.textPrimary },
		planTitle: { fontFamily: "Geist_600SemiBold", flex: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, fontWeight: "600" },
		planLive: { fontFamily: "Geist_600SemiBold", color: t.orange, fontSize: type.footnote.fontSize, fontWeight: "600" },
		planCount: { color: t.textFaint, fontFamily: t.fontMono, fontSize: type.footnote.fontSize },
		planBody: { paddingHorizontal: space.sm, paddingBottom: space.sm, gap: space.xs },
		planStep: { flexDirection: "row", alignItems: "flex-start", gap: space.sm },
		planStepText: { fontFamily: "Geist_400Regular", flex: 1, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		planStepState: { fontFamily: "Geist_400Regular", color: t.textFaint, fontSize: type.footnote.fontSize },
		planDone: { color: t.textTertiary, textDecorationLine: "line-through" },
		fileBody: { paddingHorizontal: space.sm, paddingBottom: space.sm, gap: space.xxs },
		fileRow: { flexDirection: "row", alignItems: "center", gap: space.xs },
		fileRowTap: { minHeight: touchTarget },
		fileListRow: { flexDirection: "row", alignItems: "flex-start", gap: space.xs },
		fileBulletColumn: { width: 4 },
		fileBullet: { width: 4, height: 4, borderRadius: radius.pill, backgroundColor: t.textTertiary },
		fileMark: { width: 13, fontFamily: t.fontMono, fontSize: type.footnote.fontSize, fontWeight: "600" },
		fileLine,
		filePath: { flex: 1, color: t.textSecondary, ...fileLine },
		fileStat: { color: t.textFaint, fontFamily: t.fontMono, fontSize: type.footnote.fontSize },
		approvalRequest: { marginVertical: space.md, paddingHorizontal: space.hair, paddingTop: space.xs, paddingBottom: space.lg, gap: space.md },
		approvalStatus: { flexDirection: "row", alignItems: "center", gap: space.sm },
		approvalDot: { width: 7, height: 7, borderRadius: radius.pill, backgroundColor: t.amber },
		approvalStatusText: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.footnote.fontSize, fontWeight: "600" },
		approvalCommandSurface: { flexDirection: "row", alignItems: "flex-start", gap: space.sm, borderRadius: radius.md, borderCurve: "continuous", backgroundColor: t.bgSubtle, paddingHorizontal: space.md, paddingVertical: space.md },
		approvalActions: { minHeight: 36, flexDirection: "row", alignItems: "center", justifyContent: "flex-end", flexWrap: "wrap", gap: space.xxs, paddingTop: space.none },
		approvalResolved: { minHeight: 38, marginVertical: space.sm, flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.hair },
		approvalResolvedLabel: { fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.footnote.fontSize, fontWeight: "600" },
		approvalResolvedCommand: { flex: 1, color: t.textFaint, fontSize: type.footnote.fontSize, fontFamily: t.fontMono },
		requestCard: { ...card, marginVertical: space.sm, backgroundColor: t.bgSurface, borderColor: t.borderDefault, padding: space.md, gap: space.md },
		requestCardResolved: { backgroundColor: t.bgSubtle },
		requestCopy: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		inputRequest: { marginVertical: space.md, paddingHorizontal: space.hair, paddingTop: space.xs, paddingBottom: space.xl, gap: space.md },
		inputRequestStatus: { flexDirection: "row", alignItems: "center", gap: space.sm },
		inputRequestDot: { width: 7, height: 7, borderRadius: radius.pill },
		inputRequestStatusText: { fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.footnote.fontSize, fontWeight: "600" },
		inputActions: { minHeight: 36, flexDirection: "row", alignItems: "center", gap: space.hair, paddingTop: space.hair },
		requestCommand: { flex: 1, color: t.textPrimary, fontFamily: t.fontMono, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		form: { gap: space.sm },
		urlBox: { backgroundColor: t.bgColumn, borderRadius: radius.sm, padding: space.sm },
		urlText: { color: t.textSecondary, fontFamily: t.fontMono, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		validation: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		compaction: { alignSelf: "center", flexDirection: "row", alignItems: "center", gap: space.xs, marginVertical: space.sm, paddingHorizontal: space.sm, paddingVertical: space.xs, borderRadius: radius.pill, backgroundColor: t.bgSubtle },
		compactionText: { fontFamily: "Geist_400Regular", color: t.textFaint, fontSize: type.footnote.fontSize },
		errorCard: { ...card, marginVertical: space.sm, flexDirection: "row", gap: space.sm, backgroundColor: t.tintRed, padding: space.md },
		errorTitle: { fontFamily: "Geist_600SemiBold", color: t.red, fontSize: type.footnote.fontSize, fontWeight: "600" },
		errorCopy: { fontFamily: "Geist_400Regular", marginTop: space.xxs, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		empty: { paddingVertical: 90, alignItems: "center", paddingHorizontal: space.xxl },
		emptyMascot: { marginBottom: space.lg },
		emptyTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.body.fontSize, fontWeight: "600" },
	});
};
