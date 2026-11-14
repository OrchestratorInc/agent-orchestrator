import { memo, useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { flushSync } from "react-dom";
import {
	DndContext,
	KeyboardSensor,
	PointerSensor,
	closestCenter,
	type DragEndEvent,
	type DragStartEvent,
	type Modifier,
	useSensor,
	useSensors,
} from "@dnd-kit/core";
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { ChevronDown, Circle, CornerDownLeft, GripVertical, Pencil, Trash2 } from "@/components/app-icons";
import { apiErrorMessage } from "../../lib/api-client";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";
import type { ConversationMessage } from "../../types/conversation";
import { useTranslation } from "react-i18next";
import { labelInlineImages } from "./messageAttachments";
import { parseBrowserAnnotationMessage, type ParsedBrowserAnnotationItem } from "../../../shared/browser-annotations";

export type QueuedMessage = {
	turnId: string;
	message: ConversationMessage;
	/** Sent but not yet confirmed by the daemon: shown at once, with no actions until it has a turn id. */
	pending?: boolean;
};

const QUEUE_DOCK_VISIBLE_ROWS = 5;
const REORDER_ACTIVATION_DISTANCE = 4;

/** Keep queue drags inside the scroll list, matching sidebar session rows. */
const restrictToQueueDockBounds: Modifier = ({ activeNodeRect, containerNodeRect, transform }) => {
	if (!activeNodeRect || !containerNodeRect) return transform;
	const minY = containerNodeRect.top - activeNodeRect.top;
	const maxY = containerNodeRect.bottom - activeNodeRect.bottom;
	return {
		...transform,
		x: 0,
		y: Math.min(maxY, Math.max(minY, transform.y)),
	};
};

type DragRowSnapshot = {
	showPersistentSteer: boolean;
	showHoverSteer: boolean;
};

function reorderById(ids: string[], activeId: string, overId: string): string[] | null {
	if (activeId === overId) return null;
	const from = ids.indexOf(activeId);
	const to = ids.indexOf(overId);
	if (from < 0 || to < 0) return null;
	const next = [...ids];
	const [moved] = next.splice(from, 1);
	next.splice(to, 0, moved);
	return next;
}

function sortableRowStyle({
	transform,
	transition,
	isDragging,
	isReordering,
}: {
	transform: { x: number; y: number; scaleX: number; scaleY: number } | null;
	transition: string | undefined;
	isDragging: boolean;
	isReordering: boolean;
}): CSSProperties {
	return {
		transform: transform ? CSS.Transform.toString({ ...transform, x: 0, scaleX: 1, scaleY: 1 }) : undefined,
		transition: isDragging || isReordering ? "none" : transition,
	};
}

function QueuedMessageRowContent({
	message,
	showPersistentSteer,
	enterSteers = false,
	showHoverSteer = false,
	canSteer,
	suppressHoverSteer,
	onPromoteQueuedTurn,
	onBeginQueuedEdit,
	canEditMessage,
	onCancelQueuedTurn,
	canCancelMessage,
	turnId,
	busy,
	steady = false,
	onRunAction,
	showGrip,
	reorderEnabled,
	dragHandleRef,
	dragHandleProps,
}: {
	message: ConversationMessage;
	showPersistentSteer?: boolean;
	/** Cmd/Ctrl+Enter in the empty composer steers this row; the button shows the key. */
	enterSteers?: boolean;
	showHoverSteer?: boolean;
	canSteer?: boolean;
	suppressHoverSteer?: boolean;
	onPromoteQueuedTurn?: (turnId: string) => Promise<unknown>;
	onBeginQueuedEdit?: (turnId: string, text: string) => void;
	canEditMessage?: (message: ConversationMessage) => boolean;
	onCancelQueuedTurn?: (turnId: string) => Promise<unknown>;
	canCancelMessage?: (message: ConversationMessage) => boolean;
	turnId: string;
	busy?: boolean;
	/** Busy only while the composer sends: the actions wait without dimming. */
	steady?: boolean;
	onRunAction: (turnId: string, action: () => Promise<unknown>) => void;
	/** The grip stays put whenever the dock can reorder; only dragging is gated. */
	showGrip?: boolean;
	reorderEnabled?: boolean;
	dragHandleRef?: (element: HTMLButtonElement | null) => void;
	dragHandleProps?: Record<string, unknown>;
}) {
	const { t } = useTranslation();
	const annotations = message.origin === "human" ? null : parseBrowserAnnotationMessage(message.text);
	// Lead with what the user wrote: page titles and counts alone leave two
	// batches from the same page indistinguishable.
	const annotationLabel = (item: ParsedBrowserAnnotationItem) =>
		item.comment || (item.changes.length > 0 ? t("chat.automation.visualChanges", { count: item.changes.length }) : item.target);
	const text = annotations
		? [annotations.items[0] ? annotationLabel(annotations.items[0]) : annotations.pageTitle,
			t("browser.annotationCount", { count: annotations.items.length })].join(" · ")
		: labelInlineImages(message.text, (index) => t("chat.image.numbered", { index }));
	const tooltip = annotations
		? [annotations.pageTitle, ...annotations.items.map((item) => `${item.number}. ${annotationLabel(item)}`)].join("\n")
		: text;
	// Relayed user-authored text is generated, so it can be steered or cancelled
	// but not edited; the chat may narrow which human messages it can edit.
	const editable = message.origin === "human" && (canEditMessage?.(message) ?? true);
	const showHoverSteerButton =
		showHoverSteer ||
		Boolean(onPromoteQueuedTurn && canSteer && !showPersistentSteer && !suppressHoverSteer);
	// Each send briefly disables every row. Dimming them on every Enter made the
	// dock flicker and showed the hidden hover Steer on every row.
	// Row actions change state instantly: no fades on hover, disable or key hint.
	const steadyClass = cn("transition-none", steady && "disabled:opacity-100");

	return (
		<div className="queue-dock-row-content flex h-10 w-full min-w-0 items-center gap-2.5 overflow-hidden px-3">
			<Circle
				aria-hidden="true"
				className="size-3 shrink-0 text-muted-foreground/60"
			/>
			<div className="min-w-0 flex-1 overflow-hidden">
				<p
					className="queue-dock-row-text truncate text-xs leading-relaxed text-foreground"
					title={tooltip}
				>
					{text}
				</p>
			</div>
			<div className="queue-dock-actions flex shrink-0 items-center gap-0.5 whitespace-nowrap">
				{showHoverSteerButton ? (
					<Button
						type="button"
						variant="quiet"
						size="sm"
						disabled={busy}
						onClick={() => {
							void onRunAction(turnId, () => onPromoteQueuedTurn!(turnId));
						}}
						className={cn(
							"transition-none focus-visible:pointer-events-auto focus-visible:opacity-100",
							showHoverSteer
								? "pointer-events-none opacity-100"
								: busy
									? "pointer-events-none opacity-0 disabled:opacity-0"
									: "pointer-events-none opacity-0 group-hover/queued-row:pointer-events-auto group-hover/queued-row:opacity-100",
						)}
						aria-label="Steer this queued message into the running turn"
						title="Steer into running turn"
					>
						Steer
					</Button>
				) : null}
				{showPersistentSteer && onPromoteQueuedTurn ? (
					<Button
						type="button"
						variant="quiet"
						size="sm"
						disabled={busy}
						className={steadyClass}
						onClick={() => {
							void onRunAction(turnId, () => onPromoteQueuedTurn(turnId));
						}}
						aria-label="Steer this queued message into the running turn"
						title="Steer into running turn"
					>
						{/* The key hint hides in place on other rows, so every Steer button is the same width. */}
						<span
							className={cn(
								"inline-flex shrink-0 items-center gap-1 text-muted-foreground",
								!enterSteers && "opacity-0",
							)}
							data-enter-steers={enterSteers ? "true" : undefined}
						>
							<CornerDownLeft aria-hidden="true" className="size-3.5 shrink-0" />
						</span>
						Steer
					</Button>
				) : null}
				{onBeginQueuedEdit && editable ? (
					<Button
						type="button"
						variant="quiet"
						size="icon-sm"
						disabled={busy}
						className={steadyClass}
						onClick={() => onBeginQueuedEdit(turnId, message.text)}
						aria-label="Edit queued message"
						title="Edit"
					>
						<Pencil aria-hidden="true" className="size-3.5 shrink-0" />
					</Button>
				) : null}
				{onCancelQueuedTurn && (canCancelMessage?.(message) ?? true) ? (
					<Button
						type="button"
						variant="destructive-ghost"
						size="icon-sm"
						disabled={busy}
						className={steadyClass}
						onClick={() => {
							void onRunAction(turnId, () => onCancelQueuedTurn(turnId));
						}}
						aria-label="Delete queued message"
						title="Delete"
					>
						<Trash2 aria-hidden="true" className="size-3.5 shrink-0" />
					</Button>
				) : null}
				{showGrip ? (
					<Pressable
						ref={dragHandleRef}
						{...dragHandleProps}
						disabled={busy || !reorderEnabled}
						className="flex size-7 cursor-grab items-center justify-center rounded-md text-muted-foreground touch-none transition-none active:cursor-grabbing disabled:pointer-events-none"
						aria-label="Drag to reorder queued message"
						title="Drag to reorder"
					>
						<GripVertical aria-hidden="true" className="size-3.5 shrink-0" />
					</Pressable>
				) : null}
			</div>
		</div>
	);
}

function SortableQueuedMessageRow({
	turnId,
	message,
	hiddenFromView,
	showPersistentSteer,
	enterSteers,
	canSteer,
	suppressHoverSteer,
	onPromoteQueuedTurn,
	onBeginQueuedEdit,
	canEditMessage,
	onCancelQueuedTurn,
	canCancelMessage,
	busy,
	steady,
	error,
	onRunAction,
	showGrip,
	reorderEnabled,
	isReordering,
	dragSnapshot,
	onRowHoverChange,
}: {
	turnId: string;
	message: ConversationMessage;
	hiddenFromView?: boolean;
	showPersistentSteer?: boolean;
	enterSteers?: boolean;
	canSteer?: boolean;
	suppressHoverSteer?: boolean;
	onPromoteQueuedTurn?: (turnId: string) => Promise<unknown>;
	onBeginQueuedEdit?: (turnId: string, text: string) => void;
	canEditMessage?: (message: ConversationMessage) => boolean;
	onCancelQueuedTurn?: (turnId: string) => Promise<unknown>;
	canCancelMessage?: (message: ConversationMessage) => boolean;
	busy?: boolean;
	steady?: boolean;
	error?: string;
	onRunAction: (turnId: string, action: () => Promise<unknown>) => void;
	showGrip?: boolean;
	reorderEnabled?: boolean;
	isReordering?: boolean;
	dragSnapshot?: DragRowSnapshot | null;
	onRowHoverChange?: (turnId: string | null) => void;
}) {
	const { attributes, listeners, setActivatorNodeRef, setNodeRef, transform, transition, isDragging } =
		useSortable({
			id: turnId,
			disabled: !reorderEnabled,
		});

	const frozenActions = isDragging && dragSnapshot ? dragSnapshot : null;

	return (
		<div
			ref={setNodeRef}
			className={cn(
				"queue-dock-row group/queued-row w-full min-w-0 max-w-full overflow-hidden",
				isDragging && "relative cursor-grabbing opacity-60",
			)}
			style={sortableRowStyle({
				transform,
				transition,
				isDragging,
				isReordering: Boolean(isReordering),
			})}
			data-dragging={isDragging ? "true" : undefined}
			data-testid={`queued-message-${turnId}`}
			aria-hidden={hiddenFromView ? true : undefined}
			inert={hiddenFromView ? true : undefined}
			onPointerEnter={() => {
				if (!isReordering) onRowHoverChange?.(turnId);
			}}
			onPointerLeave={() => {
				if (!isReordering) onRowHoverChange?.(null);
			}}
		>
			<QueuedMessageRowContent
				busy={busy}
				steady={steady}
				canSteer={canSteer}
				dragHandleProps={{ ...attributes, ...listeners }}
				dragHandleRef={setActivatorNodeRef}
				enterSteers={enterSteers}
				message={message}
				onBeginQueuedEdit={onBeginQueuedEdit}
				canEditMessage={canEditMessage}
				onCancelQueuedTurn={onCancelQueuedTurn}
				canCancelMessage={canCancelMessage}
				onPromoteQueuedTurn={onPromoteQueuedTurn}
				onRunAction={onRunAction}
				showGrip={showGrip}
				reorderEnabled={reorderEnabled}
				showHoverSteer={frozenActions?.showHoverSteer}
				showPersistentSteer={frozenActions?.showPersistentSteer ?? showPersistentSteer}
				suppressHoverSteer={suppressHoverSteer && !isDragging}
				turnId={turnId}
			/>
			{error ? (
				<p role="status" className="px-3 pb-2 text-xs text-warning">
					{error}
				</p>
			) : null}
		</div>
	);
}

export const QueuedMessageDock = memo(function QueuedMessageDock({
	messages,
	disabled,
	sending,
	editingTurnId,
	canSteer,
	canSteerNext,
	steerNextRequest,
	onPromoteQueuedTurn,
	onBeginQueuedEdit,
	canEditMessage,
	onCancelQueuedTurn,
	canCancelMessage,
	onReorderQueuedTurns,
	promotePendingTurnId,
	cancelPendingTurnId,
	steerErrors,
}: {
	messages: QueuedMessage[];
	disabled?: boolean;
	/** The composer is sending: row actions wait for it, without dimming. */
	sending?: boolean;
	editingTurnId?: string;
	canSteer?: boolean;
	canSteerNext?: boolean;
	steerNextRequest?: number;
	onPromoteQueuedTurn?: (turnId: string) => Promise<unknown>;
	onBeginQueuedEdit?: (turnId: string, text: string) => void;
	/** Which human messages Edit is offered on; every one when absent. */
	canEditMessage?: (message: ConversationMessage) => boolean;
	onCancelQueuedTurn?: (turnId: string) => Promise<unknown>;
	/** Which messages Delete is offered on; every one when absent. */
	canCancelMessage?: (message: ConversationMessage) => boolean;
	onReorderQueuedTurns?: (turnIds: string[]) => Promise<unknown>;
	promotePendingTurnId?: string;
	cancelPendingTurnId?: string;
	steerErrors?: Readonly<Record<string, string>>;
}) {
	const [expanded, setExpanded] = useState(true);
	const [errors, setErrors] = useState<Record<string, string>>({});
	const [displayOrder, setDisplayOrder] = useState<string[] | null>(null);
	const [hoverSteerSuppressed, setHoverSteerSuppressed] = useState(false);
	const [reorderError, setReorderError] = useState<string | undefined>();
	const [isReordering, setIsReordering] = useState(false);
	const [dragSnapshot, setDragSnapshot] = useState<DragRowSnapshot | null>(null);
	const [hoveredTurnId, setHoveredTurnId] = useState<string | null>(null);
	const scrollRef = useRef<HTMLDivElement>(null);
	const hoverSteerSuppressTimer = useRef<number | undefined>(undefined);
	const [handledSteerNextRequest, setHandledSteerNextRequest] = useState(steerNextRequest ?? 0);
	const steerNextInFlight = useRef(false);
	const reorderSensors = useSensors(
		useSensor(PointerSensor, {
			activationConstraint: { distance: REORDER_ACTIVATION_DISTANCE },
		}),
		useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
	);

	const runAction = useCallback(
		async (turnId: string, action: () => Promise<unknown>) => {
			setErrors((current) => {
				if (!current[turnId]) return current;
				const next = { ...current };
				delete next[turnId];
				return next;
			});
			try {
				await action();
			} catch (error) {
				setErrors((current) => ({
					...current,
					[turnId]: apiErrorMessage(error, "That action failed."),
				}));
			}
		},
		[],
	);

	const count = messages.length;
	const hasMore = count > 1;
	const isOpen = !hasMore || expanded;
	const expandedRows = Math.min(count, QUEUE_DOCK_VISIBLE_ROWS);
	const messagesByTurnId = useMemo(
		() => new Map(messages.map((message) => [message.turnId, message])),
		[messages],
	);
	const pendingMessages = useMemo(() => messages.filter((entry) => entry.pending), [messages]);
	const fifoTurnIds = useMemo(
		() => messages.filter((entry) => !entry.pending).map(({ turnId }) => turnId),
		[messages],
	);
	// Rows read in send order, like the transcript: the next message on top, the
	// newest at the bottom, and a send not yet confirmed after everything queued.
	const displayTurnIds = useMemo(() => {
		if (!displayOrder) return fifoTurnIds;
		const known = new Set(fifoTurnIds);
		const ordered = displayOrder.filter((turnId) => known.has(turnId));
		const missing = fifoTurnIds.filter((turnId) => !ordered.includes(turnId));
		return [...ordered, ...missing];
	}, [displayOrder, fifoTurnIds]);
	const displayMessages = useMemo(
		() => [
			...displayTurnIds.flatMap((turnId) => {
				const message = messagesByTurnId.get(turnId);
				return message ? [message] : [];
			}),
			...pendingMessages,
		],
		[displayTurnIds, messagesByTurnId, pendingMessages],
	);
	const reorderEnabled =
		Boolean(onReorderQueuedTurns) && fifoTurnIds.length > 1 && isOpen;
	// Grips appear with the second row, a send still in flight included, and
	// then stay put while sends come and go; only dragging waits.
	const showGrip = Boolean(onReorderQueuedTurns) && displayMessages.length > 1 && isOpen;
	const nextQueuedTurnId = fifoTurnIds[0];

	const suppressHoverSteer = useCallback(() => {
		setHoverSteerSuppressed(true);
		if (hoverSteerSuppressTimer.current !== undefined) {
			window.clearTimeout(hoverSteerSuppressTimer.current);
		}
		hoverSteerSuppressTimer.current = window.setTimeout(() => {
			setHoverSteerSuppressed(false);
			hoverSteerSuppressTimer.current = undefined;
		}, 200);
	}, []);

	useEffect(
		() => () => {
			if (hoverSteerSuppressTimer.current !== undefined) {
				window.clearTimeout(hoverSteerSuppressTimer.current);
			}
		},
		[],
	);

	const fifoTurnIdSetKey = useMemo(
		() => [...fifoTurnIds].sort().join("\0"),
		[fifoTurnIds],
	);

	useEffect(() => {
		setDisplayOrder(null);
	}, [fifoTurnIdSetKey]);

	useEffect(() => {
		if (!displayOrder) return;
		if (
			displayOrder.length === fifoTurnIds.length &&
			displayOrder.every((turnId, index) => turnId === fifoTurnIds[index])
		) {
			setDisplayOrder(null);
		}
	}, [displayOrder, fifoTurnIds]);

	useEffect(() => {
		if (steerNextRequest === undefined || steerNextRequest <= handledSteerNextRequest) return;
		if (!canSteerNext || !onPromoteQueuedTurn) {
			setHandledSteerNextRequest(steerNextRequest);
			return;
		}
		if (steerNextInFlight.current) return;

		const next = displayMessages.find((message) => message.turnId === nextQueuedTurnId);
		if (!next) {
			setHandledSteerNextRequest(steerNextRequest);
			return;
		}
		steerNextInFlight.current = true;
		void runAction(next.turnId, () => onPromoteQueuedTurn(next.turnId)).finally(() => {
			steerNextInFlight.current = false;
			setHandledSteerNextRequest((request) => request + 1);
		});
	}, [
		canSteerNext,
		displayMessages,
		handledSteerNextRequest,
		nextQueuedTurnId,
		onPromoteQueuedTurn,
		runAction,
		steerNextRequest,
	]);

	useEffect(() => {
		if (!isOpen || count < QUEUE_DOCK_VISIBLE_ROWS) return;
		const scroll = scrollRef.current;
		if (scroll) scroll.scrollTop = scroll.scrollHeight;
	}, [count, isOpen]);

	const onDragCancel = useCallback(() => {
		setIsReordering(false);
		setDragSnapshot(null);
		setHoveredTurnId(null);
		suppressHoverSteer();
	}, [suppressHoverSteer]);

	const onDragStart = useCallback(
		({ active }: DragStartEvent) => {
			const turnId = String(active.id);
			const showPersistentSteer = Boolean(canSteer);
			setIsReordering(true);
			setDragSnapshot({
				showPersistentSteer,
				showHoverSteer: Boolean(
					canSteer && !showPersistentSteer && hoveredTurnId === turnId,
				),
			});
			suppressHoverSteer();
		},
		[canSteer, hoveredTurnId, suppressHoverSteer],
	);

	const onDragEnd = useCallback(
		({ active, over }: DragEndEvent) => {
			setIsReordering(false);
			setDragSnapshot(null);
			setHoveredTurnId(null);
			suppressHoverSteer();
			if (!over || !onReorderQueuedTurns) return;
			const nextDisplay = reorderById(displayTurnIds, String(active.id), String(over.id));
			if (!nextDisplay) return;
			setReorderError(undefined);
			flushSync(() => setDisplayOrder(nextDisplay));
			void onReorderQueuedTurns(nextDisplay).catch((error) => {
				setDisplayOrder(null);
				setReorderError(
					apiErrorMessage(error, "Could not reorder queued messages."),
				);
			});
		},
		[displayTurnIds, onReorderQueuedTurns, suppressHoverSteer],
	);

	const rowProps = {
		canSteer,
		onPromoteQueuedTurn,
		onBeginQueuedEdit,
		canEditMessage,
		onCancelQueuedTurn,
		canCancelMessage,
		onRunAction: runAction,
		suppressHoverSteer: hoverSteerSuppressed || isReordering,
	};

	return (
		<div
			className="queue-dock overflow-hidden rounded-[var(--radius-chat-composer)] border border-border bg-surface"
			data-testid="queued-message-dock"
			data-collapsible={hasMore ? "true" : "false"}
			data-expanded={isOpen ? "true" : "false"}
			data-reordering={isReordering ? "true" : undefined}
			style={{ "--queue-dock-expanded-rows": expandedRows } as CSSProperties}
		>
			<Pressable
				onClick={() => hasMore && setExpanded((open) => !open)}
				disabled={!hasMore}
				className={cn(
					"queue-dock-toggle relative z-[1] flex w-full items-center gap-2 bg-surface px-3 py-2 text-left transition-none",
					hasMore && "cursor-pointer",
					!hasMore && "cursor-default",
				)}
				aria-expanded={hasMore ? expanded : undefined}
				data-testid="queued-message-toggle"
			>
				<span
					aria-hidden="true"
					className={cn(
						"grid h-3.5 shrink-0 overflow-hidden",
						hasMore ? "w-3.5" : "w-0",
					)}
				>
					<ChevronDown
						className={cn(
							"size-3.5 shrink-0 text-muted-foreground",
							expanded ? "rotate-0" : "-rotate-90",
						)}
					/>
				</span>
				<span className="queue-dock-label text-xs font-medium text-muted-foreground">
					<span
						className="inline-block w-fit"
					>
						{count} Queued {count === 1 ? "Message" : "Messages"}
					</span>
					{editingTurnId ? " · editing" : ""}
				</span>
			</Pressable>
			{count > 0 ? (
				<div
					className="queue-dock-body bg-surface"
					data-expanded={isOpen ? "true" : "false"}
					data-collapsible={hasMore ? "true" : "false"}
					style={
						{
							"--queue-dock-expanded-rows": expandedRows,
						} as CSSProperties
					}
				>
					<DndContext
						collisionDetection={closestCenter}
						id="queue-dock-reorder"
						modifiers={[restrictToQueueDockBounds]}
						onDragCancel={onDragCancel}
						onDragEnd={onDragEnd}
						onDragStart={onDragStart}
						sensors={reorderSensors}
					>
						<SortableContext items={displayTurnIds} strategy={verticalListSortingStrategy}>
							<div
								ref={scrollRef}
								className={cn(
									"queue-dock-scroll",
									isOpen && count >= QUEUE_DOCK_VISIBLE_ROWS && "queue-dock-scroll-active",
								)}
							>
								{displayMessages.map(({ turnId, message, pending }, index) => {
									const rowBusy =
										disabled || promotePendingTurnId === turnId || cancelPendingTurnId === turnId;
									const busy = rowBusy || Boolean(sending);
									const isNextQueuedTurn = turnId === nextQueuedTurnId;
									return (
										<SortableQueuedMessageRow
											// The send's own id outlives the swap from pending to confirmed, so the
											// row stays mounted instead of replaying its entrance.
											key={message.clientMessageId ?? turnId}
											turnId={turnId}
											message={message}
											// Folded, the dock keeps the next message to send in view.
											hiddenFromView={!isOpen && index !== 0}
											// Every row's Steer, and the next row's key hint, stay put while a
											// turn can be steered: typing or sending never makes them blink.
											showPersistentSteer={Boolean(canSteer)}
											enterSteers={Boolean(canSteer && isNextQueuedTurn)}
											busy={busy}
											steady={busy && !rowBusy}
											dragSnapshot={dragSnapshot}
											error={errors[turnId] ?? steerErrors?.[turnId]}
											isReordering={isReordering}
											onRowHoverChange={setHoveredTurnId}
											showGrip={showGrip}
											reorderEnabled={reorderEnabled && !busy && !pending}
											{...rowProps}
											// A send the daemon has not confirmed yet shows the same actions,
											// held until it has a turn, so confirming it shifts nothing.
											{...(pending ? { busy: true, steady: true } : null)}
										/>
									);
								})}
							</div>
						</SortableContext>
					</DndContext>
					{reorderError ? (
						<p role="status" className="px-3 pb-2 text-xs text-warning">
							{reorderError}
						</p>
					) : null}
				</div>
			) : null}
		</div>
	);
});
