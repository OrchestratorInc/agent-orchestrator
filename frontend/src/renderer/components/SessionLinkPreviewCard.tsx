import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { cn } from "../lib/utils";
import { parseSessionLink } from "../lib/session-links";
import { getSessionStatusDotView, getSessionStatusView } from "../lib/session-presentation";
import { prCardPresentation, sessionPRDisplaySummaries } from "../lib/pr-display";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import type { WorkspaceSession } from "../types/workspace";
import { AgentAvatar } from "./AgentAvatar";
import { HoverCardContent } from "./ui/hover-card";
import { Skeleton } from "./ui/skeleton";

const prToneClasses = {
	neutral: { dot: "bg-status-idle", text: "text-muted-foreground" },
	passive: { dot: "bg-status-idle", text: "text-muted-foreground" },
	success: { dot: "bg-status-ready", text: "text-status-ready" },
	review: { dot: "bg-status-in-review", text: "text-status-in-review" },
	warning: { dot: "bg-status-needs-you", text: "text-status-needs-you" },
	error: { dot: "bg-destructive", text: "text-destructive" },
} as const;

function relativeTime(timestamp: string | undefined, format: (key: "time.justNow" | "shell.updatedAt", values?: Record<string, unknown>) => string): string {
	if (!timestamp) return "";
	const elapsedMinutes = Math.floor((Date.now() - Date.parse(timestamp)) / 60_000);
	if (!Number.isFinite(elapsedMinutes) || elapsedMinutes < 1) return format("shell.updatedAt", { time: format("time.justNow") });
	const unit: Intl.RelativeTimeFormatUnit = elapsedMinutes < 60 ? "minute" : elapsedMinutes < 1_440 ? "hour" : "day";
	const amount = unit === "minute" ? elapsedMinutes : unit === "hour" ? Math.floor(elapsedMinutes / 60) : Math.floor(elapsedMinutes / 1_440);
	return format("shell.updatedAt", { time: new Intl.RelativeTimeFormat(undefined, { numeric: "always" }).format(-amount, unit) });
}

function LoadingCard() {
	return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="max-h-48 overflow-y-auto p-3">
			<div role="status" aria-label="Loading worker details" className="space-y-3">
				<div className="flex items-center gap-2.5">
					<Skeleton className="size-7 shrink-0 rounded-md" />
					<div className="min-w-0 flex-1 space-y-1.5"><Skeleton className="h-3 w-32" /><Skeleton className="h-2.5 w-24" /></div>
				</div>
				<Skeleton className="h-3 w-44" />
				<Skeleton className="h-2.5 w-36" />
			</div>
		</HoverCardContent>
	);
}

function UnavailableCard() {
	const { t } = useTranslation();
	return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="max-h-48 overflow-y-auto p-3">
			<div role="status" className="space-y-1">
				<p className="text-[13px] font-medium text-foreground">{t("session.statusUnavailable")}</p>
				<p className="text-[11px] leading-4 text-muted-foreground">{t("session.notFound")}</p>
			</div>
		</HoverCardContent>
	);
}

function statusDot(session: WorkspaceSession) {
	const dot = getSessionStatusDotView(session);
	return cn("size-1.5 shrink-0 rounded-full", dot.className, dot.breathe && "animate-status-pulse");
}

function compactPRLabel(pr: ReturnType<typeof sessionPRDisplaySummaries>[number], t: TFunction): string {
	const primary = prCardPresentation(pr).primary;
	if (primary.tone === "success" || primary.key === "lifecycle") return primary.label;
	return t("pr.merge.blocked");
}

function SessionCardBody({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const prs = useMemo(() => sessionPRDisplaySummaries(session), [session]);
	const status = session.statusReadiness === "unavailable"
		? "Worker unavailable"
		: session.displayStatus || getSessionStatusView(session.status, t).label;
	return (
		<div className="space-y-3" role="status" aria-label={`${session.title}, ${status}`}>
			<div className="flex min-w-0 items-start gap-2.5">
				<AgentAvatar provider={session.provider} className="size-7 shrink-0" />
				<div className="min-w-0 flex-1">
					<div className="flex min-w-0 items-baseline gap-1.5">
						<p className="min-w-0 truncate text-[13px] font-semibold text-foreground">{session.title}</p>
						<span className="shrink-0 font-mono text-[10px] text-muted-foreground">#{session.id.slice(-3)}</span>
					</div>
					<p className="mt-0.5 truncate text-[11px] text-muted-foreground">{session.workspaceName}</p>
				</div>
			</div>
			<div className="flex items-center gap-1.5 text-xs">
				<span aria-hidden="true" className={statusDot(session)} />
				<span className="min-w-0 truncate font-medium text-popover-foreground">{status}</span>
				<span className="ml-auto shrink-0 text-[10px] text-muted-foreground">{relativeTime(session.updatedAt, t)}</span>
			</div>
			{prs.length > 0 && (
				<div className="border-t border-border pt-2">
					<p className="text-[10px] font-semibold text-muted-foreground">{prs.length} {prs.length === 1 ? "PR" : "PRs"}</p>
					<div className="mt-1.5 space-y-1.5">
						{prs.map((pr) => {
							const presentation = prCardPresentation(pr);
							const tone = prToneClasses[presentation.primary.tone];
							return (
								<div key={`${pr.url}-${pr.number}`} className="flex min-h-5 items-center gap-1.5 text-[10px]">
									<span className="font-mono font-semibold text-popover-foreground">PR #{pr.number}</span>
									<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", tone.dot)} />
									<span className={cn("truncate", tone.text)}>{compactPRLabel(pr, t)}</span>
								</div>
							);
						})}
					</div>
				</div>
			)}
		</div>
	);
}

export function SessionLinkPreviewCard({ href }: { href: string }) {
	const target = parseSessionLink(href);
	const workspaceQuery = useWorkspaceQuery();
	if (workspaceQuery.isLoading || !workspaceQuery.data) return <LoadingCard />;
	if (!target) return <UnavailableCard />;
	const workspace = workspaceQuery.data.find((candidate) => candidate.id === target.projectId);
	const session = workspace?.sessions.find((candidate) => candidate.id === target.sessionId);
	return session ? (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="max-h-48 overflow-y-auto p-3">
			<SessionCardBody session={session} />
		</HoverCardContent>
	) : <UnavailableCard />;
}
