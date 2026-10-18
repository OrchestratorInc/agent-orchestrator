import { MessageSquareQuote, X } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";

export interface ChatAnnotationSummaryItem {
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
	const removeClass = "absolute right-1 top-1/2 -translate-y-1/2 inline-flex size-5 items-center justify-center text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50";

	return (
		<Popover open={open} onOpenChange={setOpen}>
			<div className={cn("relative inline-flex w-fit max-w-full min-w-0 self-start rounded bg-interactive-hover transition-colors", !disabled && "hover:bg-interactive-active", className)}>
				<PopoverTrigger asChild>
					<button
						ref={triggerRef}
						type="button"
						disabled={disabled}
						aria-label={label}
						className={cn("inline-flex h-7 w-full min-w-0 select-none items-center gap-1.5 rounded px-1.5 text-xs leading-tight text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50", onRemoveAll && "pr-7")}
					>
						<MessageSquareQuote aria-hidden="true" className="size-3.5 shrink-0" />
						<span className="truncate">{label}</span>
					</button>
				</PopoverTrigger>
				{onRemoveAll ? (
					<button
						type="button"
						disabled={disabled}
						aria-label={t("chat.removeAllAnnotations")}
						className={removeClass}
						onClick={() => {
							setOpen(false);
							onRemoveAll();
						}}
					>
						<X aria-hidden="true" className="size-3" />
					</button>
				) : null}
			</div>
			<PopoverContent
				align="start"
				side="top"
				className="w-[min(22rem,90vw)] border-0 p-2 shadow-md"
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
							<button
								type="button"
								disabled={disabled}
								className={cn("block w-full min-w-0 rounded bg-transparent px-2.5 py-1.5 text-left text-xs leading-5 text-popover-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50", onRemove && "pr-7")}
								onClick={() => {
									onSelect?.(annotation);
									setOpen(false);
								}}
							>
								<span className="block truncate">{annotation.text}</span>
							</button>
							{onRemove ? (
								<button
									type="button"
									ref={(node) => { removeRefs.current[index] = node; }}
									disabled={disabled}
									aria-label={t("chat.removeAnnotation", { count: index + 1 })}
									className={removeClass}
									onClick={() => {
										pendingRemoval.current = { annotation, index };
										onRemove(annotation);
									}}
								>
									<X aria-hidden="true" className="size-3" />
								</button>
							) : null}
						</div>
					))}
				</div>
			</PopoverContent>
		</Popover>
	);
}
