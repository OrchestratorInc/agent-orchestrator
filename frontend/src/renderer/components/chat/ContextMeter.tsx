import { useTranslation } from "react-i18next";
import { AlertTriangle } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import { formatTokenCount } from "../../lib/format-token-count";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../ui/tooltip";
import type { ConversationRateLimits, ConversationUsage } from "../../types/conversation";

const CONTEXT_WARN = 0.7;
const CONTEXT_CRITICAL = 0.9;

const QUOTA_WARN = 75;
const QUOTA_CRITICAL = 90;

type Severity = "normal" | "warn" | "critical";

function contextSeverity(fraction: number): Severity {
	if (fraction >= CONTEXT_CRITICAL) return "critical";
	if (fraction >= CONTEXT_WARN) return "warn";
	return "normal";
}

function quotaSeverity(percent: number): Severity {
	if (percent >= QUOTA_CRITICAL) return "critical";
	if (percent >= QUOTA_WARN) return "warn";
	return "normal";
}

const RING: Record<Severity, string> = {
	normal: "text-logo-accent",
	warn: "text-status-needs-you",
	critical: "text-status-exited",
};

function formatResetIn(seconds: number): string {
	if (seconds <= 0) return "now";
	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) return `${Math.max(1, minutes)}m`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours}h`;
	return `${Math.floor(hours / 24)}d`;
}

type QuotaWindow = { percent: number; resetsIn?: number; kind: "session" | "weekly" };

function worstWindow(
	limits: ConversationRateLimits,
): QuotaWindow | undefined {
	const windows: QuotaWindow[] = [
		{ percent: limits.primaryUsedPercent, resetsIn: limits.primaryResetsInSeconds, kind: "session" as const },
		{ percent: limits.secondaryUsedPercent, resetsIn: limits.secondaryResetsInSeconds, kind: "weekly" as const },
	].filter((w) => typeof w.percent === "number" && Number.isFinite(w.percent) && w.percent >= 0);
	if (windows.length === 0) return undefined;
	return windows.reduce((worst, w) => (w.percent > worst.percent ? w : worst));
}

export function ContextMeter({
	usage,
	rateLimits,
	compactsAt,
	className,
}: {
	usage?: ConversationUsage;
	rateLimits?: ConversationRateLimits;
	compactsAt?: string;
	className?: string;
}) {
	const quota = rateLimits ? worstWindow(rateLimits) : undefined;
	const hasContext = usage !== undefined && (usage.contextUsed > 0 || usage.contextWindow > 0);
	const showQuota = quota !== undefined && quota.percent >= QUOTA_WARN;

	if (!hasContext && !showQuota) return null;

	return (
		<TooltipProvider>
			<div className={cn("flex shrink-0 items-center gap-2", className)}>
				{usage && hasContext ? <ContextReadout usage={usage} compactsAt={compactsAt} /> : null}
				{showQuota && quota ? <QuotaWarning quota={quota} limits={rateLimits} /> : null}
			</div>
		</TooltipProvider>
	);
}

function ContextReadout({ usage, compactsAt }: { usage: ConversationUsage; compactsAt?: string }) {
	const compactsLine = compactsAt ? <p>Compacts on its own at {compactsAt.toUpperCase()}</p> : null;
	const { contextUsed, contextWindow } = usage;
	const tooltipSurface = "border border-(--color-border-overlay) shadow-none";

	if (contextWindow <= 0) {
		const compactUsed = formatTokenCount(contextUsed).replace(/ tok$/, "");
		return (
			<Tooltip>
				<TooltipTrigger asChild>
					<span
						role="img"
						aria-label={`Context ${contextUsed.toLocaleString()} / unknown`}
						className="relative inline-flex size-7 shrink-0 items-center justify-center rounded-full text-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
						data-context-meter=""
						tabIndex={0}
					>
						<svg aria-hidden="true" className="size-5" viewBox="0 0 24 24">
							<circle cx="12" cy="12" fill="none" r="9" stroke="currentColor" strokeWidth="3" />
						</svg>
						<span aria-hidden="true" className="absolute text-micro">?</span>
					</span>
				</TooltipTrigger>
				<TooltipContent className={tooltipSurface}>
					<p className="font-medium">Context {compactUsed} / unknown</p>
					<p>The provider has not reported a context window. Conversation fullness is unknown.</p>
					{compactsLine}
				</TooltipContent>
			</Tooltip>
		);
	}

	const fraction = Math.min(1, Math.max(0, contextUsed / contextWindow));
	const percent = Math.round(fraction * 100);
	const severity = contextSeverity(fraction);
	const exactUsage = `${contextUsed.toLocaleString()} / ${contextWindow.toLocaleString()}`;
	const compactUsage = `${formatTokenCount(contextUsed).replace(/ tok$/, "")} / ${formatTokenCount(contextWindow).replace(/ tok$/, "")}`;

	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<div
					className={cn("inline-flex size-7 shrink-0 items-center justify-center rounded-full focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring", RING[severity])}
					data-context-meter=""
					role="progressbar"
					tabIndex={0}
					aria-valuemin={0}
					aria-valuemax={100}
					aria-valuenow={percent}
					aria-label="Context window used"
					aria-valuetext={`${exactUsage} tokens (${percent}%)`}
				>
					<svg aria-hidden="true" className="size-5 -rotate-90" viewBox="0 0 24 24">
						<circle cx="12" cy="12" fill="none" r="9" stroke="currentColor" strokeWidth="3" className="text-border" />
						<circle
							cx="12" cy="12" fill="none" r="9" stroke="currentColor" strokeWidth="3"
							strokeLinecap="round" pathLength="100"
							strokeDasharray={`${fraction > 0 ? Math.max(fraction * 100, 8) : 0} 100`}
							className="transition-[stroke-dasharray] duration-300"
						/>
					</svg>
				</div>
			</TooltipTrigger>
			<TooltipContent className={tooltipSurface}>
				<p className="font-medium">Context window</p>
				<p className="tabular-nums">{compactUsage} tokens ({percent}%)</p>
				{compactsLine}
				{severity !== "normal" ? (
					<p className="mt-1">
						{severity === "critical"
							? "The next turn may not fit. Compacting or starting a new conversation will reclaim room."
							: "Room is running low. A long task may not fit."}
					</p>
				) : null}
			</TooltipContent>
		</Tooltip>
	);
}

function QuotaWarning({
	quota,
	limits,
}: {
	quota: QuotaWindow;
	limits?: ConversationRateLimits;
}) {
	const { t } = useTranslation();
	const severity = quotaSeverity(quota.percent);
	const percent = Math.min(100, Math.round(quota.percent));
	const resets = quota.resetsIn && quota.resetsIn > 0 ? formatResetIn(quota.resetsIn) : undefined;
	const session = quota.kind === "session";

	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span
					className={cn(
						"flex items-center gap-1 rounded border px-1.5 py-0.5 text-caption tabular-nums",
						severity === "critical"
							? "border-status-exited/40 text-status-exited"
							: "border-status-needs-you/40 text-status-needs-you",
					)}
				>
					<AlertTriangle aria-hidden="true" className="size-3" />
					{t(session ? "chat.quota.badgeSession" : "chat.quota.badgeWeekly", { percent })}
				</span>
			</TooltipTrigger>
			<TooltipContent>
				<p>
					{t(session ? "chat.quota.usedSession" : "chat.quota.usedWeekly", { percent })}
					{limits?.planLabel ? ` ${t("chat.quota.plan", { plan: limits.planLabel })}` : ""}
					{resets ? ` ${t("chat.quota.resetsIn", { time: resets })}` : ""}
				</p>
				<p className="mt-1">
					{t(severity === "critical" ? "chat.quota.critical" : "chat.quota.warn")}
				</p>
			</TooltipContent>
		</Tooltip>
	);
}
