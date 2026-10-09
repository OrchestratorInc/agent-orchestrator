import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { LoaderCircle, Play } from "lucide-react";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { cn } from "../lib/utils";
import { parseSessionLink } from "../lib/session-links";
import { getSessionStatusDotView, getSessionStatusView } from "../lib/session-presentation";
import { prCanMerge, prCardPresentation, sessionPRDisplaySummaries, type PRDisplayTone } from "../lib/pr-display";
import { useSessionLinkSource } from "../lib/use-session-link-navigation";
import { LOCAL_HOST } from "../lib/hosts";
import type { WorkspaceSession } from "../types/workspace";
import { AgentAvatar } from "./AgentAvatar";
import { Button } from "./ui/button";
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
	const { t } = useTranslation();
	return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="max-h-48 overflow-y-auto p-3">
			<div role="status" aria-label={t("session.statusChecking")} className="space-y-3">
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
			<p role="status" className="text-sm font-medium text-foreground">{t("session.statusUnavailable")}</p>
		</HoverCardContent>
	);
}

function ErrorCard() {
	const { t } = useTranslation();
	return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="max-h-48 overflow-y-auto p-3">
			<div role="alert" className="space-y-1">
				<p className="text-sm font-medium text-foreground">{t("session.statusUnavailable")}</p>
				<p className="text-xs leading-4 text-muted-foreground">{t("shell.couldNotLoadSessions")}</p>
			</div>
		</HoverCardContent>
	);
}

function statusDot(session: WorkspaceSession) {
	const dot = getSessionStatusDotView(session);
	return cn("size-1.5 shrink-0 rounded-full", dot.className, dot.breathe && "animate-status-pulse");
}

function compactPRStatus(
	pr: ReturnType<typeof sessionPRDisplaySummaries>[number],
	t: TFunction,
): { label: string; tone: PRDisplayTone } {
	const primary = prCardPresentation(pr).primary;
	if (pr.state === "merged" || pr.state === "closed" || pr.state === "draft") return primary;
	if (prCanMerge(pr)) return { label: t("pr.card.readyToMerge"), tone: "success" };
	const blocked = pr.ci.state === "failing"
		|| pr.mergeability.state === "conflicting"
		|| pr.mergeability.state === "blocked"
		|| pr.mergeability.state === "unstable"
		|| pr.review.decision === "changes_requested"
		|| pr.review.decision === "review_required"
		|| pr.review.hasUnresolvedHumanComments;
	return blocked
		? { label: t("pr.merge.blocked"), tone: primary.tone }
		: { label: t("pr.card.open"), tone: "neutral" };
}

function SessionCardBody({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const prs = useMemo(() => sessionPRDisplaySummaries(session), [session]);
	const status = session.statusReadiness === "unavailable"
		? t("session.statusUnavailable")
		: session.displayStatus || getSessionStatusView(session.status, t).label;
	return (
		<div className="space-y-3" role="status" aria-label={`${session.title}, ${status}`}>
			<div className="flex min-w-0 items-start gap-2.5">
				<AgentAvatar provider={session.provider} className="size-7 shrink-0" />
				<div className="min-w-0 flex-1">
					<div className="flex min-w-0 items-baseline gap-1.5">
						<p className="min-w-0 truncate text-sm font-semibold text-foreground">{session.title}</p>
						<span className="shrink-0 font-mono text-xs text-muted-foreground">#{session.id.slice(-3)}</span>
					</div>
					<p className="mt-0.5 truncate text-xs text-muted-foreground">{session.workspaceName}</p>
				</div>
			</div>
			<div className="flex items-center gap-1.5 text-xs">
				<span aria-hidden="true" className={statusDot(session)} />
				<span className="min-w-0 truncate font-medium text-popover-foreground">{status}</span>
				<span className="ml-auto shrink-0 text-xs text-muted-foreground">{relativeTime(session.updatedAt, t)}</span>
			</div>
			{prs.length > 0 && (
				<div className="border-t border-border pt-2">
					<p className="text-xs font-semibold text-muted-foreground">{t("pr.count", { count: prs.length })}</p>
					<div className="mt-1.5 space-y-1.5">
						{prs.map((pr) => {
							const status = compactPRStatus(pr, t);
							const tone = prToneClasses[status.tone];
							return (
								<div key={`${pr.url}-${pr.number}`} className="flex min-h-5 items-center gap-1.5 text-xs">
									<span className="font-mono font-semibold text-popover-foreground">{t("pr.short")} #{pr.number}</span>
									<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", tone.dot)} />
									<span className={cn("truncate", tone.text)}>{status.label}</span>
								</div>
							);
						})}
					</div>
				</div>
			)}
		</div>
	);
}

function TerminatedCard({ session, hostId }: { session: WorkspaceSession; hostId?: string }) {
	const { t } = useTranslation();
	const restoreSession = useRestoreSession();
	const [restoring, setRestoring] = useState(false);
	const [error, setError] = useState<string>();
	const resumeAgent = async () => {
		if (restoring) return;
		setRestoring(true);
		setError(undefined);
		try {
			const result = await restoreSession(session.id, hostId);
			if (result.status !== "success") setError(result.message);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : t("terminal.unableRestore"));
		} finally {
			setRestoring(false);
		}
	};
	return (
		<div className="space-y-3" role="status" aria-label={t("session.agentTerminated")}>
			<p className="text-sm font-semibold text-popover-foreground">{t("session.agentTerminated")}</p>
			<Button
				className="w-full"
				disabled={restoring}
				onClick={(event) => {
					event.preventDefault();
					event.stopPropagation();
					void resumeAgent();
				}}
				size="sm"
				type="button"
			>
				{restoring
					? <LoaderCircle aria-hidden="true" className="size-icon-sm animate-spin" />
					: <Play aria-hidden="true" className="size-icon-sm" />}
				{t(restoring ? "inspector.resumingAgent" : "inspector.resumeAgent")}
			</Button>
			{error ? <p className="text-xs leading-4 text-destructive" role="alert">{error}</p> : null}
		</div>
	);
}

export function SessionLinkPreviewCard({
	href,
	sourceHostId,
	sourceKind,
}: {
	href: string;
	sourceHostId?: string;
	sourceKind?: "cloud";
}) {
	const target = parseSessionLink(href);
	const source = useSessionLinkSource(sourceHostId, sourceKind);
	const workspace = target ? source.workspaces.find((candidate) => candidate.id === target.projectId) : undefined;
	const session = workspace?.sessions.find((candidate) => candidate.id === target?.sessionId);
	const restoreHostId = sourceHostId && sourceHostId !== LOCAL_HOST ? sourceHostId : undefined;
	if (session) return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="max-h-48 overflow-y-auto p-3">
			{session.isTerminated === true || session.status === "terminated"
				? <TerminatedCard session={session} hostId={restoreHostId} />
				: <SessionCardBody session={session} />}
		</HoverCardContent>
	);
	if (source.isLoading) return <LoadingCard />;
	if (source.isError) return <ErrorCard />;
	return <UnavailableCard />;
}
