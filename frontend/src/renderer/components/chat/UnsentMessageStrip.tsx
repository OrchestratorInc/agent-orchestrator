import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, ChevronDown, CircleAlert, Copy, Loader2, RotateCw, Trash2 } from "@/components/app-icons";
import { Button } from "../ui/button";
import { cn } from "../../lib/utils";

export interface UnsentMessage {
	id: string;
	text: string;
	attachmentCount: number;
	/** Its retry is in flight. */
	sending: boolean;
	/** Still unsent, and the composer keeps retrying it by itself. */
	retrying: boolean;
}

/**
 * Sent messages whose delivery was never confirmed, oldest first, shown above the
 * composer. They keep the box free: the person sees exactly what Retry would send,
 * retries them in order with the same delivery ids, or discards one (with a short undo).
 */
export function UnsentMessageStrip({ messages, retryDisabled, onRetry, onDiscard, discarded = false, onUndo }: {
	messages: readonly UnsentMessage[];
	retryDisabled: boolean;
	onRetry: () => void;
	onDiscard: (id: string) => void;
	discarded?: boolean;
	onUndo: () => void;
}) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState<string>();

	return (
		<div className="divide-y divide-border overflow-hidden rounded-[var(--radius-chat-composer)] border border-border bg-surface" data-testid="unsent-message-strip">
			{messages.map(({ id, text, attachmentCount, sending, retrying }, index) => {
				const oneShot = sending && !retrying;
				const open = expanded === id && !oneShot;
				const preview = text.replace(/\s+/g, " ").trim() || t("chat.unsent.attachmentsOnly");
				const row = messages.length > 1 ? ` ${index + 1}` : "";
				return (
					<div key={id}>
						<div className="flex h-10 w-full min-w-0 items-center gap-2.5 overflow-hidden pl-3 pr-1.5">
							{messages.length > 1 ? <span className="w-2 shrink-0 text-xs tabular-nums text-muted-foreground">{index + 1}</span> : null}
							{sending || retrying ? (
								<Loader2 aria-hidden="true" className="size-3.5 shrink-0 animate-spin text-muted-foreground motion-reduce:animate-none" />
							) : (
								<CircleAlert aria-hidden="true" className="size-3.5 shrink-0 text-status-needs-you" />
							)}
							<span aria-live="polite" className={cn("shrink-0 text-xs font-medium", sending || retrying ? "text-muted-foreground" : "text-status-needs-you")}>
								{t(retrying ? "chat.unsent.retrying" : sending ? "chat.unsent.sending" : "chat.unsent.label")}
							</span>
							<p className={cn("min-w-0 flex-1 truncate text-xs leading-relaxed", oneShot ? "text-muted-foreground" : "text-foreground")} title={open ? undefined : text}>
								{open ? t("chat.unsent.retryHint") : preview}
							</p>
							{oneShot ? null : (
								<div className="flex shrink-0 items-center gap-0.5 whitespace-nowrap">
									<Button
										type="button"
										variant="quiet"
										size="icon-sm"
										onClick={() => setExpanded(open ? undefined : id)}
										aria-expanded={open}
										aria-label={t(open ? "chat.unsent.collapse" : "chat.unsent.expand") + row}
										title={t(open ? "chat.unsent.collapse" : "chat.unsent.expand")}
									>
										<ChevronDown aria-hidden="true" className={cn("size-3.5 shrink-0 transition-transform duration-150 motion-reduce:transition-none", open && "rotate-180")} />
									</Button>
									{index === 0 ? (
										<Button type="button" variant="quiet" size="sm" disabled={retryDisabled} onClick={onRetry} title={t("chat.unsent.retryHint")} className="gap-1.5">
											<RotateCw aria-hidden="true" className="size-3.5 shrink-0" />
											{t("chat.unsent.retry")}
										</Button>
									) : null}
									<Button
										type="button"
										variant="destructive-ghost"
										size="icon-sm"
										disabled={sending}
										onClick={() => onDiscard(id)}
										aria-label={t("chat.unsent.discard") + row}
										title={t("chat.unsent.discard")}
									>
										<Trash2 aria-hidden="true" className="size-3.5 shrink-0" />
									</Button>
								</div>
							)}
						</div>
						{open ? (
							<div className="border-t border-border">
								<pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words px-3 py-2.5 font-mono text-xs leading-relaxed text-muted-foreground">{text}</pre>
								<div className="flex items-center justify-between px-3 pb-2 text-xs text-muted-foreground">
									<span>{attachmentCount > 0 ? t("chat.unsent.attachments", { count: attachmentCount }) : null}</span>
									<Button type="button" variant="quiet" size="sm" onClick={() => void navigator.clipboard?.writeText(text)} className="gap-1.5">
										<Copy aria-hidden="true" className="size-3.5 shrink-0" />
										{t("chat.unsent.copy")}
									</Button>
								</div>
							</div>
						) : null}
					</div>
				);
			})}
			{discarded ? (
				<div className="flex h-10 w-full min-w-0 items-center gap-2.5 px-3 text-xs text-muted-foreground" role="status">
					<Check aria-hidden="true" className="size-3.5 shrink-0" />
					<span className="min-w-0 flex-1 truncate">{t("chat.unsent.discarded")}</span>
					<Button type="button" variant="quiet" size="sm" onClick={onUndo}>{t("chat.unsent.undo")}</Button>
				</div>
			) : null}
		</div>
	);
}
