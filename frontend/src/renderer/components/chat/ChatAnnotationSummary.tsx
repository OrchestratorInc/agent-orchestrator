import type { ChatExcerptSnapshot } from "../../../shared/chat-excerpt";
import { FileAnnotationPreview } from "./FileAnnotationPreview";
import type { ChatFileReference } from "../../../shared/chat-file-reference";
import { MessageSquareQuote, X } from "lucide-react";
import { useId, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";

export interface ChatAnnotationSummaryItem {
	snapshot?: ChatExcerptSnapshot;
	kind?: "chat" | "file";
	fileReference?: ChatFileReference;
	order?: number;
	text: string;
	conversationId?: string;
	id?: string;
	messageId?: string;
	revision?: number;
}

interface ChatAnnotationSummaryProps {
	annotations: readonly ChatAnnotationSummaryItem[];
	onSelect?: (annotation: ChatAnnotationSummaryItem) => void;
	onRemove?: (annotation: ChatAnnotationSummaryItem) => void;
	onRemoveAll?: () => void;
	disabled?: boolean;
	className?: string;
}

/** Compact, keyboard-accessible annotation count with an interactive list. */
export function ChatAnnotationSummary({
	annotations,
	onSelect,
	onRemove,
	onRemoveAll,
	disabled = false,
	className,
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

	return (
		<Popover open={open} onOpenChange={setOpen}>
			<div className={cn("relative inline-flex w-fit max-w-full min-w-0 self-start rounded bg-interactive-hover transition-colors", !disabled && "has-[>button:first-child:hover]:bg-interactive-active", className)}>
				<PopoverTrigger asChild>
					<Pressable
						ref={triggerRef}
						disabled={disabled}
						aria-label={label}
						className={cn("inline-flex h-7 w-full min-w-0 select-none items-center gap-1.5 rounded px-1.5 text-xs leading-tight text-muted-foreground transition-colors hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50", onRemoveAll && "pr-7")}
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
			<PopoverContent
				onClick={(event) => event.stopPropagation()}
				align="start"
				side="top"
				className="w-[min(22rem,90vw)] p-2"
				onInteractOutside={() => { interactedOutside.current = true; }}
				onCloseAutoFocus={(event) => {
					// Outside interaction already chose a focus target. Stealing it back
					// would dismiss another annotation popover that is just opening.
					event.preventDefault();
					if (!interactedOutside.current) triggerRef.current?.focus({ preventScroll: true });
					interactedOutside.current = false;
				}}
			>
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
			</PopoverContent>
		</Popover>
	);
}

function ChatExcerptPreview({ annotation, index, disabled, onNavigate }: { annotation: ChatAnnotationSummaryItem; index: number; disabled: boolean; onNavigate: () => void }) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);
	const [overflow, setOverflow] = useState(false);
	const preview = useRef<HTMLPreElement>(null);
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
	return <>
		<Pressable disabled={disabled} onClick={onNavigate} aria-label={annotation.text || t("chat.excerpt.flowchartAttached")}
			className="mb-1 flex w-full min-w-0 items-center gap-2 border-b border-border/60 pb-1.5 text-left text-popover-foreground hover:text-foreground">
			<span className="min-w-0 flex-1 font-semibold">{index}. {t("chat.excerpt.selectedText")}</span>
			{annotation.snapshot?.containsFlowchart ? <span className="shrink-0 rounded bg-background/60 px-1.5 py-0.5 font-mono text-[10px]">{t("chat.excerpt.flowchartAttached")}</span> : null}
		</Pressable>
		{text ? <pre ref={preview} id={previewId} className={cn("whitespace-pre-wrap break-words font-sans text-xs leading-5 select-text", expanded ? "max-h-48 overflow-auto" : "line-clamp-6")}>{expanded ? text : shortened + (clipped ? "…" : "")}</pre> : null}
		{clipped || overflow ? <Button type="button" variant="link" disabled={disabled} className="mt-1 text-xs text-muted-foreground" aria-expanded={expanded} aria-controls={previewId} onClick={() => setExpanded(value => !value)}>{t(expanded ? "chat.fileAnnotationShowLess" : "chat.fileAnnotationShowMore")}</Button> : null}
	</>;
}
