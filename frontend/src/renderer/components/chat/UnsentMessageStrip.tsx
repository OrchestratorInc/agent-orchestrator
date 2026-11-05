import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, ChevronDown, CircleAlert, Copy, Loader2, RotateCw, Trash2 } from "lucide-react";
import { Button } from "../ui/button";
import { cn } from "../../lib/utils";

/**
 * A sent message whose delivery was never confirmed, shown above the composer.
 * It keeps the box free: the person sees exactly what Retry would send, retries
 * it with the same delivery id, or discards it (with a short undo).
 */
export function UnsentMessageStrip(props:
	| {
		discarded?: false;
		text: string;
		attachmentCount: number;
		/** The retry is in flight. */
		sending: boolean;
		retryDisabled: boolean;
		onRetry: () => void;
		onDiscard: () => void;
	}
	| { discarded: true; onUndo: () => void },
) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);

	if (props.discarded) {
		return (
			<div className="overflow-hidden rounded-[var(--radius-chat-composer)] border border-border bg-surface" data-testid="unsent-message-strip" role="status">
				<div className="flex h-10 w-full min-w-0 items-center gap-2.5 px-3 text-xs text-muted-foreground">
					<Check aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2} />
					<span className="min-w-0 flex-1 truncate">{t("chat.unsent.discarded")}</span>
					<Button type="button" variant="quiet" size="sm" onClick={props.onUndo}>{t("chat.unsent.undo")}</Button>
				</div>
			</div>
		);
	}

	const { text, attachmentCount, sending, retryDisabled, onRetry, onDiscard } = props;
	const preview = text.replace(/\s+/g, " ").trim() || t("chat.unsent.attachmentsOnly");
	return (
		<div className="overflow-hidden rounded-[var(--radius-chat-composer)] border border-border bg-surface" data-testid="unsent-message-strip">
			<div className="flex h-10 w-full min-w-0 items-center gap-2.5 overflow-hidden pl-3 pr-1.5">
				{sending ? (
					<Loader2 aria-hidden="true" className="size-3.5 shrink-0 animate-spin text-muted-foreground motion-reduce:animate-none" strokeWidth={2} />
				) : (
					<CircleAlert aria-hidden="true" className="size-3.5 shrink-0 text-status-needs-you" strokeWidth={2} />
				)}
				<span className={cn("shrink-0 text-xs font-medium", sending ? "text-muted-foreground" : "text-status-needs-you")}>
					{t(sending ? "chat.unsent.sending" : "chat.unsent.label")}
				</span>
				<p className={cn("min-w-0 flex-1 truncate text-xs leading-relaxed", sending ? "text-muted-foreground" : "text-foreground")} title={expanded ? undefined : text}>
					{expanded ? t("chat.unsent.retryHint") : preview}
				</p>
				{sending ? null : (
					<div className="flex shrink-0 items-center gap-0.5 whitespace-nowrap">
						<Button
							type="button"
							variant="quiet"
							size="icon-sm"
							onClick={() => setExpanded((open) => !open)}
							aria-expanded={expanded}
							aria-label={t(expanded ? "chat.unsent.collapse" : "chat.unsent.expand")}
							title={t(expanded ? "chat.unsent.collapse" : "chat.unsent.expand")}
						>
							<ChevronDown aria-hidden="true" className={cn("size-3.5 shrink-0 transition-transform duration-150 motion-reduce:transition-none", expanded && "rotate-180")} strokeWidth={2} />
						</Button>
						<Button type="button" variant="quiet" size="sm" disabled={retryDisabled} onClick={onRetry} title={t("chat.unsent.retryHint")} className="gap-1.5">
							<RotateCw aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2} />
							{t("chat.unsent.retry")}
						</Button>
						<Button
							type="button"
							variant="destructive-ghost"
							size="icon-sm"
							onClick={onDiscard}
							aria-label={t("chat.unsent.discard")}
							title={t("chat.unsent.discard")}
						>
							<Trash2 aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2} />
						</Button>
					</div>
				)}
			</div>
			{expanded && !sending ? (
				<div className="border-t border-border">
					<pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words px-3 py-2.5 font-mono text-xs leading-relaxed text-muted-foreground">{text}</pre>
					<div className="flex items-center justify-between px-3 pb-2 text-xs text-muted-foreground">
						<span>{attachmentCount > 0 ? t("chat.unsent.attachments", { count: attachmentCount }) : null}</span>
						<Button type="button" variant="quiet" size="sm" onClick={() => void navigator.clipboard?.writeText(text)} className="gap-1.5">
							<Copy aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2} />
							{t("chat.unsent.copy")}
						</Button>
					</div>
				</div>
			) : null}
		</div>
	);
}
