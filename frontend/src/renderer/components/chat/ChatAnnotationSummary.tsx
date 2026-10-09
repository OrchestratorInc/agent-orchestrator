import { MessageSquareQuote, X } from "lucide-react";
import { useRef, useState } from "react";
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
	disabled?: boolean;
	className?: string;
}

/** Compact, keyboard-accessible annotation count with an interactive list. */
export function ChatAnnotationSummary({
	annotations,
	onSelect,
	onRemove,
	disabled = false,
	className,
}: ChatAnnotationSummaryProps) {
	const [open, setOpen] = useState(false);
	const { t } = useTranslation();
	const pointerFocus = useRef(false);
	const closing = useRef(false);
	const triggerRef = useRef<HTMLButtonElement>(null);
	if (annotations.length === 0) return null;
	const label = `${annotations.length} annotation${annotations.length === 1 ? "" : "s"}`;

	return (
		<Popover open={open} onOpenChange={(next) => {
			closing.current = !next;
			setOpen(next);
		}}>
			<PopoverTrigger asChild>
				<button
					ref={triggerRef}
					type="button"
					disabled={disabled}
					aria-label={label}
					onPointerDown={() => { pointerFocus.current = true; }}
					onFocus={() => {
						if (!pointerFocus.current && !closing.current) setOpen(true);
					}}
					onBlur={() => { pointerFocus.current = false; closing.current = false; }}
					className={cn(
						"inline-flex w-fit max-w-full select-none self-start min-w-0 items-center gap-1.5 rounded-[10px] border border-logo-accent/25 bg-logo-accent/5 px-2.5 py-1.5 text-[11px] leading-tight text-muted-foreground transition-colors hover:bg-logo-accent/10 disabled:cursor-not-allowed disabled:opacity-50",
						className,
					)}
				>
					<MessageSquareQuote aria-hidden="true" className="size-3.5 shrink-0 text-logo-accent" />
					<span>{label}</span>
				</button>
			</PopoverTrigger>
			<PopoverContent align="start" side="top" className="w-[min(22rem,90vw)] p-1.5 shadow-xl" onCloseAutoFocus={(event) => {
				// Returning focus to the trigger must not scroll the composer/message
				// back into view after a row has just navigated to its source.
				event.preventDefault();
				closing.current = true;
				triggerRef.current?.focus({ preventScroll: true });
			}}>
				<div className="space-y-1" aria-label={t("chat.annotationReferences")}>
					{annotations.map((annotation, index) => (
						<div key={annotation.id ?? `${annotation.messageId ?? "annotation"}-${index}`} className="flex min-w-0 items-center gap-1 rounded-md border border-border/60 bg-raised/40 p-0.5">
							<button
								type="button"
								className="min-w-0 flex-1 rounded-md px-2 py-1.5 text-left text-xs text-popover-foreground hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-logo-accent"
								onClick={() => {
									onSelect?.(annotation);
									closing.current = true;
									setOpen(false);
								}}
							>
								<span className="block max-w-[280px] truncate">{annotation.text}</span>
							</button>
							{onRemove ? (
								<button
									type="button"
									disabled={disabled}
									aria-label={t("chat.removeAnnotation", { count: index + 1 })}
									className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-interactive-hover hover:text-foreground disabled:opacity-50"
									onClick={() => onRemove(annotation)}
								>
									<X aria-hidden="true" className="size-3.5" />
								</button>
							) : null}
						</div>
					))}
				</div>
			</PopoverContent>
		</Popover>
	);
}
