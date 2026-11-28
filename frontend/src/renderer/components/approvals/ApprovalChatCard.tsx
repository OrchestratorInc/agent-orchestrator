import { ChevronDown, KeyRound } from "@/components/app-icons";
import { useEffect, useId, useRef, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { ApprovalDecision, PendingApproval } from "../../lib/approvals";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { approvalTimeLeft, useNow } from "./ApprovalPrompt";
import { offersSiteRule } from "../../lib/approval-site-rule";

/** "shop.example" for "https://shop.example"; the value itself when it is not a URL. */
function siteHost(site: string): string {
	try {
		return new URL(site).host || site;
	} catch {
		return site;
	}
}

/** "shop.example/checkout" for "https://shop.example/checkout?step=2". */
function pageLocation(url: string): string {
	try {
		const parsed = new URL(url);
		return `${parsed.host}${parsed.pathname === "/" ? "" : parsed.pathname}`;
	} catch {
		return url;
	}
}

/**
 * The session's own approval request, at the end of its chat: the element the
 * agent wants to use, what will happen, and Deny / Allow once. "Always allow
 * on this site" sits behind Allow's menu so it is never clicked by mistake.
 *
 * The agent's browser command waits until this is answered or times out, so
 * the card goes away as soon as it is decided. Escape denies; Enter never
 * approves, so a keystroke meant for the composer cannot confirm a payment.
 */
export function ApprovalChatCard({
	request,
	onDecide,
	busy = false,
	error,
	more = 0,
}: {
	request: PendingApproval;
	onDecide: (decision: ApprovalDecision) => void;
	busy?: boolean;
	error?: string;
	/** Further requests queued behind this one. */
	more?: number;
}) {
	const { t } = useTranslation();
	const now = useNow();
	const ref = useRef<HTMLDivElement>(null);
	const id = useId();
	const host = request.site ? siteHost(request.site) : "";
	const what = request.kind === "credential-use"
		? t("approvals.credentialLabel")
		: t(`approvals.category.${request.category}` as "approvals.category.payment", { defaultValue: request.category });
	const where = request.url ? pageLocation(request.url) : host;

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
			aria-describedby={request.reason ? `${id}-reason` : undefined}
			data-testid="approval-prompt"
			data-approval-id={request.id}
			onKeyDown={onKeyDown}
			className="cursor-chat-activity-panel rounded-lg border border-border px-3 py-2.5 outline-none"
		>
			<div className="flex items-center gap-3">
				{request.image ? (
					<img
						src={`data:image/png;base64,${request.image}`}
						alt={request.element ? `${t("approvals.elementAlt")}: ${request.element}` : t("approvals.elementAlt")}
						className="h-10 w-30 shrink-0 rounded-md border border-border/70 bg-background object-cover"
						data-testid="approval-image"
					/>
				) : request.kind === "credential-use" ? (
					<span className="grid size-10 shrink-0 place-items-center rounded-md border border-border/70 bg-background/45" aria-hidden="true">
						<KeyRound className="size-icon-md text-muted-foreground" />
					</span>
				) : null}
				<div className="min-w-0 flex-1">
					<p className="break-words text-sm leading-[1.4] text-foreground/90" data-testid="approval-action">
						{request.action}
					</p>
					<p className="mt-0.5 truncate text-xs text-muted-foreground" title={request.reason || request.url}>
						{what}
						{where ? <> · {where}</> : null}
					</p>
					{request.reason ? <p id={`${id}-reason`} className="sr-only">{request.reason}</p> : null}
				</div>
			</div>

			{error ? (
				<p role="alert" className="mt-2 text-xs leading-snug text-destructive">
					{t("approvals.decisionFailed", { message: error })}
				</p>
			) : null}

			<div className="mt-2 flex items-center gap-1.5">
				<span className="text-xs tabular-nums text-muted-foreground" data-testid="approval-countdown">
					{t("approvals.expiresIn", { time: approvalTimeLeft(request.expiresAt, now) })}
					{more > 0 ? <> · {t("approvals.more", { count: more })}</> : null}
				</span>
				<span className="flex-1" />
				<Button type="button" variant="secondary" size="sm" shape="pill" disabled={busy} onClick={() => onDecide("deny")}>
					{t("approvals.deny")}
					<kbd className="rounded-full bg-foreground/10 px-1.5 py-0.5 font-sans text-caption leading-none text-muted-foreground">
						Esc
					</kbd>
				</Button>
				<div className="flex overflow-hidden rounded-full">
					<Button
						type="button"
						variant="primary"
						size="sm"
						className={offersSiteRule(request) ? "rounded-none" : "rounded-full"}
						disabled={busy}
						data-approval-allow=""
						onClick={() => onDecide("allow-once")}
					>
						{t("approvals.allowOnce")}
					</Button>
					{offersSiteRule(request) ? (
						<DropdownMenu>
							<DropdownMenuTrigger asChild>
								<Button
									type="button"
									variant="primary"
									size="icon-sm"
									aria-label={t("approvals.moreOptions")}
									className="rounded-none border-l border-l-primary-foreground/20"
									disabled={busy}
								>
									<ChevronDown aria-hidden="true" className="size-3.5" />
								</Button>
							</DropdownMenuTrigger>
							<DropdownMenuContent
								align="end"
								side="bottom"
								className="min-w-52"
								onEscapeKeyDown={(event) => event.stopPropagation()}
							>
								<DropdownMenuItem disabled={busy} onSelect={() => onDecide("allow-site")}>
									{t("approvals.allowSite", { site: host })}
								</DropdownMenuItem>
							</DropdownMenuContent>
						</DropdownMenu>
					) : null}
				</div>
			</div>
		</div>
	);
}
