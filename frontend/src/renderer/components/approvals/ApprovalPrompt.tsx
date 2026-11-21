import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { ApprovalDecision, PendingApproval } from "../../lib/approvals";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";

/** "1:05" until the request is denied for lack of an answer. */
export function approvalTimeLeft(expiresAt: string, now: number): string {
	const seconds = Math.max(0, Math.ceil((Date.parse(expiresAt) - now) / 1000));
	return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

function useNow(intervalMs = 1000): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		const timer = setInterval(() => setNow(Date.now()), intervalMs);
		return () => clearInterval(timer);
	}, [intervalMs]);
	return now;
}

/**
 * One agent action waiting for the user: what will happen, where, and why it
 * was flagged, with the element in its page. Shared by every approval source
 * (risky browser actions, a saved login's first use on a site).
 *
 * Escape denies. Enter never approves, not even on a focused approval button:
 * a keystroke meant for the composer or terminal must never confirm a
 * payment. Space or a click does.
 */
export function ApprovalPrompt({
	request,
	onDecide,
	busy = false,
	error,
	sessionLabel,
	onOpenSession,
	more = 0,
}: {
	request: PendingApproval;
	onDecide: (decision: ApprovalDecision) => void;
	busy?: boolean;
	error?: string;
	/** Shown when the prompt appears outside its session. */
	sessionLabel?: string;
	onOpenSession?: () => void;
	/** Further requests queued behind this one. */
	more?: number;
}) {
	const { t } = useTranslation();
	const now = useNow();
	const ref = useRef<HTMLDivElement>(null);
	const categoryKey = `approvals.category.${request.category}` as const;
	const category = t(categoryKey as "approvals.category.payment", { defaultValue: request.category });
	const label = request.kind === "credential-use" ? t("approvals.credentialLabel") : t("approvals.label");

	useEffect(() => {
		const node = ref.current;
		// Take focus only when nothing else has it, so typing elsewhere continues.
		if (node && (document.activeElement === document.body || !document.activeElement)) node.focus();
	}, [request.id]);

	const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		if (busy || event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
		if (event.key === "Escape") {
			event.preventDefault();
			onDecide("deny");
			return;
		}
		// Enter on a focused button would click it. Approval buttons ignore
		// Enter; Space (or a click) approves.
		if (event.key === "Enter" && event.target instanceof HTMLElement && event.target.closest("[data-approval-allow]")) {
			event.preventDefault();
		}
	};

	return (
		<div
			ref={ref}
			role="alertdialog"
			tabIndex={-1}
			aria-label={t("approvals.region")}
			aria-describedby={`approval-reason-${request.id}`}
			data-testid="approval-prompt"
			data-approval-id={request.id}
			onKeyDown={onKeyDown}
			className="flex flex-col gap-2 px-4 py-3 outline-none"
		>
			<div className="flex items-baseline gap-2 text-caption leading-snug text-muted-foreground">
				<span className="size-1.5 shrink-0 self-center rounded-full bg-status-needs-you" aria-hidden="true" />
				<span className="min-w-0 truncate">
					{/* A saved login's category repeats its label; show it once. */}
					{label}
					{request.kind === "credential-use" ? null : <> · {category}</>} · {request.site || request.url}
					{sessionLabel ? <> · {sessionLabel}</> : null}
				</span>
				<span className="ml-auto shrink-0 tabular-nums text-passive" data-testid="approval-countdown">
					{t("approvals.expiresIn", { time: approvalTimeLeft(request.expiresAt, now) })}
				</span>
			</div>

			<p className="break-words text-sm font-medium leading-snug text-foreground" data-testid="approval-action">
				{request.action}
			</p>
			<p id={`approval-reason-${request.id}`} className="break-words text-sm leading-snug text-muted-foreground">
				{request.reason}
			</p>

			{request.image ? (
				<img
					src={`data:image/png;base64,${request.image}`}
					alt={request.element ? `${t("approvals.elementAlt")}: ${request.element}` : t("approvals.elementAlt")}
					className="max-h-44 self-start rounded-md bg-background object-contain"
					data-testid="approval-image"
				/>
			) : null}

			{request.url || request.pageTitle ? (
				<p className="min-w-0 truncate font-mono text-caption text-passive" title={request.url}>
					{request.pageTitle ? `${request.pageTitle} — ` : ""}
					{request.url}
				</p>
			) : null}

			{error ? (
				<p role="alert" className="text-xs leading-snug text-destructive">
					{t("approvals.decisionFailed", { message: error })}
				</p>
			) : null}

			<div className={cn("flex flex-wrap items-center gap-1.5", onOpenSession || more ? "justify-between" : "justify-end")}>
				{onOpenSession || more ? (
					<div className="flex items-center gap-2">
						{onOpenSession ? (
							<Button type="button" variant="link" size="sm" onClick={onOpenSession}>
								{t("approvals.openSession")}
							</Button>
						) : null}
						{more > 0 ? <span className="text-caption text-passive">{t("approvals.more", { count: more })}</span> : null}
					</div>
				) : null}
				<div className="flex flex-wrap justify-end gap-1.5">
					<Button type="button" variant="secondary" size="sm" disabled={busy} onClick={() => onDecide("deny")}>
						{t("approvals.deny")}
						<kbd className="rounded-full bg-foreground/10 px-1.5 py-0.5 font-sans text-caption leading-none text-muted-foreground">
							Esc
						</kbd>
					</Button>
					{request.site ? (
						<Button type="button" variant="ghost" size="sm" disabled={busy} data-approval-allow="" onClick={() => onDecide("allow-site")}>
							{t("approvals.allowSite", { site: request.site })}
						</Button>
					) : null}
					<Button type="button" variant="accent" size="sm" disabled={busy} data-approval-allow="" onClick={() => onDecide("allow-once")}>
						{t("approvals.allowOnce")}
					</Button>
				</div>
			</div>
		</div>
	);
}
