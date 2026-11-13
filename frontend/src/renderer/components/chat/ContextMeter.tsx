/**
 * How full the conversation is.
 *
 * This replaces a bare token total in the chat composer. A total on its own is a
 * number with no scale: it cannot answer the question a user actually has, which
 * is "when will this conversation stop working". That failure is otherwise
 * undiagnosable from the UI, which is what makes it worth a permanent readout
 * rather than an error message after the fact.
 *
 * State is encoded in form as well as in number: a fill that grows, and a colour
 * that shifts at thresholds. Reading the digits is then optional rather than
 * required, which matters for something glanced at rather than studied.
 */

import { cn } from "../../lib/utils";
import { formatTokenCount } from "../../lib/format-token-count";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../ui/tooltip";
import type { ConversationUsage } from "../../types/conversation";

/**
 * Where the fill changes colour.
 *
 * Chosen for what a user can still do at each point rather than for round
 * numbers. Below 70% a long conversation is unremarkable. From 70% it is worth
 * knowing before starting a large task, since the remaining room is no longer
 * most of the window. From 90% the next turn is genuinely at risk, which is a
 * different message from "getting full" and gets the alarm colour.
 */
const CONTEXT_WARN = 0.7;
const CONTEXT_CRITICAL = 0.9;

type Severity = "normal" | "warn" | "critical";

function contextSeverity(fraction: number): Severity {
	if (fraction >= CONTEXT_CRITICAL) return "critical";
	if (fraction >= CONTEXT_WARN) return "warn";
	return "normal";
}

/**
 * Normal usage is informational rather than session activity, so it uses AO's
 * logo blue. Warning is the caution amber, distinct from the red that means the
 * next turn is at risk.
 */
const RING: Record<Severity, string> = {
	normal: "text-logo-accent",
	warn: "text-status-in-review",
	critical: "text-status-exited",
};

/* -------------------------------------------------------------------------- */

export function ContextMeter({
	usage,
	compactsAt,
	className,
}: {
	usage?: ConversationUsage;
	/** The size at which the agent compacts on its own, such as Claude Code's "200k". */
	compactsAt?: string;
	className?: string;
}) {
	if (usage === undefined || (usage.contextUsed <= 0 && usage.contextWindow <= 0)) return null;

	return (
		// Scoped provider, as IntakeFields does: this component is rendered in surfaces
		// that do not all sit under the route-level one, and a tooltip with no provider
		// throws rather than degrading.
		<TooltipProvider>
			<div className={cn("flex shrink-0 items-center gap-2", className)}>
				<ContextReadout usage={usage} compactsAt={compactsAt} />
			</div>
		</TooltipProvider>
	);
}

function ContextReadout({ usage, compactsAt }: { usage: ConversationUsage; compactsAt?: string }) {
	const compactsLine = compactsAt ? <p>Compacts on its own at {compactsAt.toUpperCase()}</p> : null;
	const { contextUsed, contextWindow } = usage;
	// A border separates the tooltip without the layered shadow's hover halos.
	const tooltipSurface = "border border-(--color-border-overlay) shadow-none";

	// Failed turns can report 0 used with a valid model limit. That draws as an
	// empty 0% ring until the next turn reports a real reading. Without a window
	// there is no fraction to draw, so only the used count is shown.
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

	// Clamped because a provider that reports slightly over its own window should
	// render as full rather than overflow the track.
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
					// A progressbar rather than a bare span: the fill is the primary
					// encoding, so a screen reader has to get the same value sighted
					// users get from its length.
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
							// A nearly-empty conversation keeps a visible sliver; a zero reading
							// stays an empty track so it does not look like measured usage.
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
