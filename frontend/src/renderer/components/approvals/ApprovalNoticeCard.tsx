import { CircleAlert } from "@/components/app-icons";
import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { ApprovalDecision, PendingApproval } from "../../lib/approvals";
import { offersSiteRule } from "../../lib/approval-site-rule";
import { AgentAvatar, type AgentAvatarProps } from "../AgentAvatar";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { approvalTimeLeft, useNow } from "./ApprovalPrompt";

/** "shop.example" for "https://shop.example"; the value itself when it is not a URL. */
function siteHost(site: string): string {
	try {
		return new URL(site).host || site;
	} catch {
		return site;
	}
}

/**
 * A request from a session the user is not looking at, in the app's notice
 * card: a status line (what kind of action, where, and when it denies), the
 * session and the action under it, an "always allow" box, and Deny / Preview /
 * Allow once, all on one left edge. Preview opens the
 * element the agent wants to use and why it was flagged.
 *
 * Escape denies. Enter never approves, not even on a focused approval button:
 * a keystroke meant for the composer or terminal must never confirm a
 * payment. Space or a click does.
 */
export function ApprovalNoticeCard({
	request,
	onDecide,
	busy = false,
	error,
	sessionLabel,
	provider,
	onOpenSession,
	more = 0,
}: {
	request: PendingApproval;
	onDecide: (decision: ApprovalDecision) => void;
	busy?: boolean;
	error?: string;
	sessionLabel: string;
	/** The session's agent, for its avatar; unknown for a remote host's session. */
	provider?: AgentAvatarProps["provider"];
	onOpenSession: () => void;
	/** Further requests queued behind this one. */
	more?: number;
}) {
	const { t } = useTranslation();
	const now = useNow();
	const ref = useRef<HTMLDivElement>(null);
	const id = useId();
	const [always, setAlways] = useState(false);
	const [preview, setPreview] = useState(false);
	const host = request.site ? siteHost(request.site) : "";
	const what = request.kind === "credential-use"
		? t("approvals.credentialLabel")
		: t(`approvals.category.${request.category}` as "approvals.category.payment", { defaultValue: request.category });
	const hasPreview = Boolean(request.image || request.reason || request.url);
	// Someone else decides it (a cloud session only its creator may approve).
	const waitingFor = request.waitingFor;

	useEffect(() => {
		const node = ref.current;
		// Take focus only when nothing else has it, so typing elsewhere continues.
		if (waitingFor) return;
		if (node && (document.activeElement === document.body || !document.activeElement)) node.focus();
	}, [request.id, waitingFor]);

	const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		if (waitingFor || busy || event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
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
			role={waitingFor ? "status" : "alertdialog"}
			tabIndex={-1}
			aria-label={t("approvals.region")}
			aria-describedby={`${id}-help`}
			data-testid="approval-prompt"
			data-approval-id={request.id}
			onKeyDown={onKeyDown}
			className="relative px-3 py-2.5 rounded-(--radius-settings-panel) border border-(--color-border-settings-menu) bg-settings-menu text-card-foreground shadow-(--shadow-popover) outline-none"
		>
			{/* The status line leads, with the card's only icon, so the title, the
			    option and the buttons under it all share the card's one left edge. */}
			<p id={`${id}-help`} className="flex h-4 min-w-0 items-center gap-1.5 text-caption leading-4 text-muted-foreground">
				{provider ? (
					<AgentAvatar className="size-3.5 shrink-0" decorative provider={provider} />
				) : (
					<CircleAlert className="size-3.5 shrink-0 text-status-needs-you" aria-hidden="true" />
				)}
				<span className="min-w-0 truncate">
					<span className="font-medium text-status-needs-you">{what}</span>
					{host ? <> · {host}</> : null}
					{more > 0 ? <> · {t("approvals.more", { count: more })}</> : null}
				</span>
				<span className="ml-auto shrink-0 tabular-nums" data-testid="approval-countdown">
					{t("approvals.expiresIn", { time: approvalTimeLeft(request.expiresAt, now) })}
				</span>
			</p>
			<p className="mt-1.5 line-clamp-2 min-w-0 break-words text-control leading-[18px] font-medium" data-testid="approval-action" title={request.action}>
				<button
					type="button"
					className="rounded-sm underline-offset-2 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring/60"
					title={t("approvals.openSession")}
					onClick={onOpenSession}
				>
					{sessionLabel}
				</button>
				{": "}
				{request.action}
			</p>

			{preview ? (
				<div className="mt-2 flex flex-col gap-1.5" data-testid="approval-preview">
					{request.reason ? <p className="break-words text-caption text-muted-foreground">{request.reason}</p> : null}
					{request.image ? (
						<img
							src={`data:image/png;base64,${request.image}`}
							alt={request.element ? `${t("approvals.elementAlt")}: ${request.element}` : t("approvals.elementAlt")}
							className="max-h-36 self-start rounded-md bg-background object-contain"
							data-testid="approval-image"
						/>
					) : null}
					{request.url ? <p className="min-w-0 truncate font-mono text-caption text-passive" title={request.url}>{request.url}</p> : null}
				</div>
			) : null}

			{offersSiteRule(request) && !waitingFor ? (
				<label className="mt-2.5 flex items-center gap-2 text-caption leading-4 text-muted-foreground">
					<Checkbox checked={always} disabled={busy} onCheckedChange={(value) => setAlways(value === true)} />
					{t("approvals.allowSite", { site: host })}
				</label>
			) : null}

			{error ? (
				<p role="alert" className="mt-2 text-caption text-destructive">
					{t("approvals.decisionFailed", { message: error })}
				</p>
			) : null}

			<div className="mt-2.5 flex items-center gap-2">
				{waitingFor ? (
					<p className="text-caption text-muted-foreground" data-testid="approval-waiting">
						{waitingFor.name ? t("approvals.waitingFor", { name: waitingFor.name }) : t("approvals.waitingForOwner")}
					</p>
				) : (
					<Button type="button" variant="secondary" size="sm" disabled={busy} onClick={() => onDecide("deny")}>
						{t("approvals.deny")}
					</Button>
				)}
				<span className="flex-1" />
				{hasPreview ? (
					<Button type="button" variant="ghost" size="sm" aria-expanded={preview} onClick={() => setPreview((open) => !open)}>
						{t("approvals.preview")}
					</Button>
				) : null}
				{waitingFor ? null : <Button
					type="button"
					size="sm"
					className="min-w-20"
					disabled={busy}
					data-approval-allow=""
					onClick={() => onDecide(always ? "allow-site" : "allow-once")}
				>
					{always ? t("approvals.allow") : t("approvals.allowOnce")}
				</Button>}
			</div>
		</div>
	);
}
