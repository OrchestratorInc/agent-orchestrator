import type { ChatExcerptSnapshot } from "../../../shared/chat-excerpt";
import { FileAnnotationPreview } from "./FileAnnotationPreview";
import { fileReferenceLabel, type ChatFileReference } from "../../../shared/chat-file-reference";
import { ChevronLeft, ChevronRight, MessageSquareQuote, Trash2, X } from "@/components/app-icons";
import { useId, useLayoutEffect, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";
import { ComposerTileRemove } from "./ComposerAttachmentTile";

export interface ChatAnnotationSummaryItem {
	snapshot?: ChatExcerptSnapshot;
	kind?: "chat" | "file";
	fileReference?: ChatFileReference;
	order?: number;
	text: string;
	comment?: string;
	conversationId?: string;
	id?: string;
	messageId?: string;
	revision?: number;
	/** Who wrote the message a chat excerpt quotes. */
	role?: "user" | "assistant";
}

interface ChatAnnotationSummaryProps {
	annotations: readonly ChatAnnotationSummaryItem[];
	onSelect?: (annotation: ChatAnnotationSummaryItem) => void;
	onRemove?: (annotation: ChatAnnotationSummaryItem) => void;
	onRemoveAll?: () => void;
	disabled?: boolean;
	className?: string;
	/** `card` draws the composer's attachment card: the quote and where it came from. */
	appearance?: "chip" | "card";
}

/** Compact, keyboard-accessible annotation count with an interactive list. */
export function ChatAnnotationSummary({
	annotations,
	onSelect,
	onRemove,
	onRemoveAll,
	disabled = false,
	className,
	appearance = "chip",
}: ChatAnnotationSummaryProps) {
	const [open, setOpen] = useState(false);
	annotations = [...annotations].sort((a, b) => (a.order ?? 0) - (b.order ?? 0) || Number(Boolean(a.fileReference)) - Number(Boolean(b.fileReference)));
	const { t } = useTranslation();
	const triggerRef = useRef<HTMLButtonElement>(null);
	const interactedOutside = useRef(false);
	const removeRefs = useRef<(HTMLButtonElement | null)[]>([]);
	const pendingRemoval = useRef<{ annotation: ChatAnnotationSummaryItem; index: number } | null>(null);
	// Wait for the caller to remove the item before focusing the updated list.
	useLayoutEffect(() => {
		const pending = pendingRemoval.current;
		if (!pending || annotations.some((item) => (pending.annotation.id ? item.id === pending.annotation.id : item === pending.annotation))) return;
		pendingRemoval.current = null;
		if (annotations.length === 0) setOpen(false);
		else removeRefs.current[Math.min(pending.index, annotations.length - 1)]?.focus({ preventScroll: true });
	}, [annotations]);
	if (annotations.length === 0) return null;
	const label = `${annotations.length} annotation${annotations.length === 1 ? "" : "s"}`;
	const removeClass = "absolute right-1 top-1/2 -translate-y-1/2";

	const trigger = appearance === "card" ? (
		<div
			className={cn("relative flex h-14 w-50 min-w-0 shrink-0 self-start rounded-lg border border-border bg-interactive-hover transition-colors", !disabled && "has-[>button:first-child:hover]:bg-interactive-active", className)}
		>
			<PopoverTrigger asChild>
				<Pressable
					ref={triggerRef}
					disabled={disabled}
					aria-label={label}
					className="flex h-full w-full min-w-0 select-none items-center rounded-[inherit] px-3.5 text-left leading-tight focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50"
				>
					<span className="flex min-w-0 flex-col gap-1">
						<span className="truncate text-control text-foreground">{annotationQuote(annotations[0]!, t)}</span>
						<span className="truncate text-sm-md text-muted-foreground">
							{annotations.length > 1 ? t("browser.annotationCount", { count: annotations.length }) : annotationSource(annotations[0]!, t)}
						</span>
					</span>
				</Pressable>
			</PopoverTrigger>
			{onRemoveAll ? (
				<ComposerTileRemove
					label={t("chat.removeAllAnnotations")}
					disabled={disabled}
					onRemove={() => {
						setOpen(false);
						onRemoveAll();
					}}
				/>
			) : null}
		</div>
	) : (
			<div
				className={cn("relative inline-flex w-fit max-w-full min-w-0 self-start rounded-md bg-interactive-hover transition-colors", !disabled && "has-[>button:first-child:hover]:bg-interactive-active", className)}
			>
				<PopoverTrigger asChild>
					<Pressable
						ref={triggerRef}
						disabled={disabled}
						aria-label={label}
						className={cn(
							"inline-flex h-7 w-full min-w-0 select-none items-center gap-1.5 rounded-md px-1.5 text-xs leading-tight text-muted-foreground transition-colors hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50",
							onRemoveAll && "pr-7",
						)}
					>
						<MessageSquareQuote aria-hidden="true" className="size-3.5 shrink-0" />
						<span className="truncate">{label}</span>
					</Pressable>
				</PopoverTrigger>
				{onRemoveAll ? (
					<Button
						type="button"
						variant="quiet"
						size="icon-xs"
						disabled={disabled}
						aria-label={t("chat.removeAllAnnotations")}
						className={cn(removeClass, "before:hidden transition-none focus-visible:ring-1 focus-visible:ring-ring")}
						onClick={() => {
							setOpen(false);
							onRemoveAll();
						}}
					>
						<X aria-hidden="true" className="size-3" />
					</Button>
				) : null}
			</div>
	);

	return (
		<Popover open={open} onOpenChange={setOpen}>
			{trigger}
			<PopoverContent
				onClick={(event) => event.stopPropagation()}
				align="start"
				side="top"
				className={appearance === "card" ? "w-[min(25rem,90vw)] p-0" : "w-[min(22rem,90vw)] p-2"}
				onInteractOutside={() => { interactedOutside.current = true; }}
				onCloseAutoFocus={(event) => {
					// Outside interaction already chose a focus target. Stealing it back
					// would dismiss another annotation popover that is just opening.
					event.preventDefault();
					if (!interactedOutside.current) triggerRef.current?.focus({ preventScroll: true });
					interactedOutside.current = false;
				}}
			>
				{appearance === "card" ? (
					<AnnotationPager
						annotations={annotations}
						disabled={disabled}
						onClose={() => setOpen(false)}
						onNavigate={(annotation) => { onSelect?.(annotation); setOpen(false); }}
						onRemove={onRemove ? (annotation, index) => {
							pendingRemoval.current = { annotation, index };
							onRemove(annotation);
						} : undefined}
					/>
				) : (
				<div className="max-h-[min(24rem,60vh)] space-y-1.5 overflow-y-auto p-0.5" aria-label={t("chat.annotationReferences")}>
					{annotations.map((annotation, index) => (
						<div key={annotation.id ?? `${annotation.messageId ?? "annotation"}-${index}`} className="relative min-w-0 rounded bg-interactive-hover">
							<div className={cn("px-2.5 py-1.5 text-xs", onRemove && "pr-7")}>
								{annotation.fileReference ? <FileAnnotationPreview reference={annotation.fileReference} index={index + 1} onNavigate={() => setOpen(false)} /> :
									<ChatExcerptPreview annotation={annotation} index={index + 1} disabled={disabled} onNavigate={() => { onSelect?.(annotation); setOpen(false); }} />}
							</div>
							{onRemove ? (
								<Button
									type="button"
									variant="quiet"
									size="icon-xs"
									ref={(node) => { removeRefs.current[index] = node; }}
									disabled={disabled}
									aria-label={t("chat.removeAnnotation", { count: index + 1 })}
									className="absolute right-1 top-1 before:hidden transition-none focus-visible:ring-1 focus-visible:ring-ring"
									onClick={() => {
										pendingRemoval.current = { annotation, index };
										onRemove(annotation);
									}}
								>
									<X aria-hidden="true" className="size-3" />
								</Button>
							) : null}
						</div>
					))}
				</div>
				)}
			</PopoverContent>
		</Popover>
	);
}

/**
 * The composer card's popover: one annotation at a time. The header names it
 * and holds its actions (remove, close); the pager under it moves between
 * annotations, and so do the arrow keys.
 */
function AnnotationPager({ annotations, disabled, onClose, onNavigate, onRemove }: {
	annotations: readonly ChatAnnotationSummaryItem[];
	disabled: boolean;
	onClose: () => void;
	onNavigate: (annotation: ChatAnnotationSummaryItem) => void;
	onRemove?: (annotation: ChatAnnotationSummaryItem, index: number) => void;
}) {
	const { t } = useTranslation();
	const [position, setPosition] = useState(0);
	const total = annotations.length;
	// A removal shortens the list under the pager: stay on the same slot, or the last one.
	const index = Math.min(position, total - 1);
	const annotation = annotations[index]!;
	const previous = useRef<HTMLButtonElement>(null);
	const next = useRef<HTMLButtonElement>(null);
	const handOff = useRef<"previous" | "next" | null>(null);
	const go = (target: number) => {
		const clamped = Math.max(0, Math.min(total - 1, target));
		// An arrow that reaches the end disables itself: once it has, focus the other so focus stays in the popover.
		if (clamped === total - 1 && document.activeElement === next.current) handOff.current = "previous";
		if (clamped === 0 && document.activeElement === previous.current) handOff.current = "next";
		setPosition(clamped);
	};
	useLayoutEffect(() => {
		const target = handOff.current === "previous" ? previous.current : handOff.current === "next" ? next.current : null;
		handOff.current = null;
		target?.focus({ preventScroll: true });
	}, [index]);
	return (
		<div
			onKeyDown={(event) => {
				if (event.key === "ArrowLeft") { event.preventDefault(); go(index - 1); }
				if (event.key === "ArrowRight") { event.preventDefault(); go(index + 1); }
			}}
		>
			<div className="flex h-10 items-center gap-2 pl-3.5 pr-1.5">
				{/* The number matches the marker beside this text in the chat. */}
				<span aria-hidden="true" className="inline-flex size-5 shrink-0 items-center justify-center rounded-full rounded-bl-none bg-primary text-caption font-medium tabular-nums text-primary-foreground">{index + 1}</span>
				<span className="min-w-0 flex-1 truncate text-control font-medium">
					{t(annotation.fileReference ? "chat.annotation.selectedCode" : "chat.annotation.selectedText")}
				</span>
				{onRemove ? (
					<Button
						type="button"
						variant="destructive-ghost"
						size="icon-sm"
						disabled={disabled}
						aria-label={t("chat.removeAnnotation", { count: index + 1 })}
						onClick={() => onRemove(annotation, index)}
					>
						<Trash2 aria-hidden="true" className="size-3.5" />
					</Button>
				) : null}
				<Button type="button" variant="quiet" size="icon-sm" aria-label={t("common.close")} onClick={onClose}>
					<X aria-hidden="true" className="size-3.5" />
				</Button>
			</div>
			<div className="max-h-[min(20rem,50vh)] overflow-y-auto px-4 pb-3.5">
				{annotation.fileReference
					? <FileAnnotationPreview key={annotation.id ?? index} reference={annotation.fileReference} onNavigate={onClose} />
					: <ChatExcerptPreview key={annotation.id ?? index} bare annotation={annotation} index={index + 1} disabled={disabled} onNavigate={() => onNavigate(annotation)} />}
			</div>
			{total > 1 ? (
				<div className="flex items-center justify-center gap-0.5 pb-2.5 text-xs text-muted-foreground tabular-nums">
					<Button ref={previous} type="button" variant="quiet" size="icon-xs" disabled={index === 0} aria-label={t("chat.annotation.previous")} onClick={() => go(index - 1)}>
						<ChevronLeft aria-hidden="true" className="size-3.5" />
					</Button>
					<span aria-live="polite" className="min-w-12 text-center">{t("chat.annotation.position", { current: index + 1, total })}</span>
					<Button ref={next} type="button" variant="quiet" size="icon-xs" disabled={index === total - 1} aria-label={t("chat.annotation.next")} onClick={() => go(index + 1)}>
						<ChevronRight aria-hidden="true" className="size-3.5" />
					</Button>
				</div>
			) : null}
		</div>
	);
}

/** The card's first line: the start of what was selected, in quotes. */
function annotationQuote(annotation: ChatAnnotationSummaryItem, t: TFunction): string {
	const text = annotation.fileReference
		? annotation.fileReference.lines.map((row) => row.text.trim()).find(Boolean) ?? ""
		: annotation.snapshot?.badgeOnly ? "" : annotation.text;
	const line = text.split("\n").map((part) => part.trim()).find(Boolean);
	return line ? t("chat.excerpt.quoted", { text: line }) : t("chat.excerpt.flowchartAttached");
}

/** The card's second line: whose message, or which file, the selection came from. */
function annotationSource(annotation: ChatAnnotationSummaryItem, t: TFunction): string {
	if (annotation.fileReference) return fileReferenceLabel(annotation.fileReference);
	return annotation.role === "user" ? t("chat.excerpt.fromUser") : t("chat.excerpt.fromAgent");
}

/**
 * One chat excerpt in the popover. `bare` drops its numbered heading (the
 * pager's header names it) and makes the quote itself the way back to it.
 */
function ChatExcerptPreview({ annotation, index, disabled, onNavigate, bare = false }: { annotation: ChatAnnotationSummaryItem; index: number; disabled: boolean; onNavigate: () => void; bare?: boolean }) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);
	const [overflow, setOverflow] = useState(false);
	const preview = useRef<HTMLElement | null>(null);
	const card = useRef<HTMLDivElement>(null);
	const pointerStart = useRef<{ x: number; y: number } | null>(null);
	const dragged = useRef(false);
	const previewId = useId();
	const text = annotation.snapshot?.badgeOnly ? "" : annotation.text;
	const shortened = Array.from(text.split("\n").slice(0, 6).join("\n")).slice(0, 600).join("");
	const clipped = shortened !== text;
	useLayoutEffect(() => {
		const element = preview.current;
		if (!element || expanded) return;
		const measure = () => setOverflow(element.scrollHeight > element.clientHeight);
		measure();
		const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(measure);
		observer?.observe(element);
		return () => observer?.disconnect();
	}, [text, expanded]);
	const navigate = () => {
		if (disabled || dragged.current) return;
		const selection = window.getSelection();
		if (selection && !selection.isCollapsed && selection.rangeCount && card.current && selection.getRangeAt(0).intersectsNode(card.current)) return;
		onNavigate();
	};
	return <div
		ref={card}
		className={disabled ? undefined : "cursor-pointer text-popover-foreground hover:text-foreground"}
		onPointerDownCapture={event => {
			pointerStart.current = { x: event.clientX, y: event.clientY };
			dragged.current = false;
		}}
		onPointerMoveCapture={event => {
			const start = pointerStart.current;
			if (start && Math.hypot(event.clientX - start.x, event.clientY - start.y) > 4) dragged.current = true;
		}}
		onPointerUpCapture={() => { pointerStart.current = null; }}
		onClick={event => {
			if (event.target instanceof Element && event.target.closest("button, a, input, textarea, select")) return;
			navigate();
		}}
	>
		{bare ? (
			<Pressable disabled={disabled} onClick={event => { if (event.detail === 0) dragged.current = false; navigate(); }} aria-label={annotation.text || t("chat.excerpt.flowchartAttached")}
				className="block w-full min-w-0 select-text text-left text-popover-foreground hover:text-foreground">
				{text ? (
					<span ref={(node) => { preview.current = node; }} id={previewId} className={cn("block whitespace-pre-wrap break-words text-sm leading-[1.55]", expanded ? "max-h-48 overflow-auto" : "line-clamp-5")}>
						{t("chat.excerpt.quoted", { text: expanded ? text : shortened + (clipped ? "…" : "") })}
					</span>
				) : <span className="text-sm text-muted-foreground">{t("chat.excerpt.flowchartAttached")}</span>}
			</Pressable>
		) : (
			<>
			<Pressable disabled={disabled} onClick={event => { if (event.detail === 0) dragged.current = false; navigate(); }} aria-label={annotation.text || t("chat.excerpt.flowchartAttached")}
				className="mb-1 flex w-full min-w-0 items-center gap-2 border-b border-border/60 pb-1.5 text-left text-popover-foreground hover:text-foreground">
				<span className="min-w-0 flex-1 font-semibold">{index}. {t("chat.excerpt.selectedText")}</span>
				{annotation.snapshot?.containsFlowchart ? <span className="shrink-0 rounded bg-background/60 px-1.5 py-0.5 font-mono text-[10px]">{t("chat.excerpt.flowchartAttached")}</span> : null}
			</Pressable>
			{text ? <pre ref={(node) => { preview.current = node; }} id={previewId} className={cn("whitespace-pre-wrap break-words font-sans text-xs leading-5 select-text", expanded ? "max-h-48 overflow-auto" : "line-clamp-6")}>{expanded ? text : shortened + (clipped ? "…" : "")}</pre> : null}
			</>
		)}
		{clipped || overflow ? <Button type="button" variant="link" disabled={disabled} className="mt-1 text-xs text-muted-foreground" aria-expanded={expanded} aria-controls={previewId} onClick={() => setExpanded(value => !value)}>{t(expanded ? "chat.fileAnnotationShowLess" : "chat.fileAnnotationShowMore")}</Button> : null}
		{annotation.comment ? <p className={cn("flex gap-1.5 whitespace-pre-wrap break-words text-foreground select-text", bare ? "mt-2.5 rounded-md bg-interactive-hover px-2.5 py-2 text-sm-md leading-5" : "mt-1.5 text-xs font-medium leading-5")}>
			<MessageSquareQuote aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
			<span className="min-w-0">{annotation.comment}</span>
		</p> : null}
	</div>;
}
