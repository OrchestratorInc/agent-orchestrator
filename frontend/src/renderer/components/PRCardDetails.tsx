import { ArrowUpRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { prChecksUrl } from "../lib/pr-display";
import { cn } from "../lib/utils";
import { ProductExternalLink } from "./ProductExternalLink";

type Check = SessionPRSummary["ci"]["checks"][number];

/** Checks listed under the Checks row; the rest are a link to the provider's checks page. */
export const PR_CHECKS_SHOWN = 3;

// Only what needs a look: a passing or skipped check adds nothing to "Checks passing".
const needsAttention = new Set<Check["status"]>(["failed", "cancelled", "in_progress", "queued"]);

// Failures first, then what is still running, then the settled rest.
const checkOrder: Record<Check["status"], number> = {
	failed: 0,
	cancelled: 1,
	in_progress: 2,
	queued: 3,
	unknown: 4,
	passed: 5,
	skipped: 6,
};

const checkTone: Record<Check["status"], { className: string; key: "failed" | "running" | "passed" | "skipped"; breathe?: boolean }> = {
	failed: { className: "text-error", key: "failed" },
	cancelled: { className: "text-passive", key: "skipped" },
	in_progress: { className: "text-muted-foreground", key: "running", breathe: true },
	queued: { className: "text-muted-foreground", key: "running" },
	unknown: { className: "text-passive", key: "skipped" },
	passed: { className: "text-success", key: "passed" },
	skipped: { className: "text-passive", key: "skipped" },
};

export function sortedChecks(checks: Check[]): Check[] {
	return [...checks].sort((a, b) => checkOrder[a.status] - checkOrder[b.status] || a.name.localeCompare(b.name));
}

/**
 * Labels and the people asked to review, as text under the PR's author and
 * diff stats. A label keeps its provider colour as a dot, not a pill.
 */
export function PRLabelsAndReviewers({ pr }: { pr: SessionPRSummary }) {
	const { t } = useTranslation();
	// Details are absent until the observer loads them, and from older daemons.
	const labels = pr.labels ?? [];
	const reviewers = pr.requestedReviewers ?? [];
	if (labels.length === 0 && reviewers.length === 0) return null;
	return (
		<div className="mt-1.5 flex min-w-0 flex-col gap-1 text-xs leading-4" data-testid="pr-labels-reviewers">
			{labels.length > 0 ? (
				<ul aria-label={t("pr.labels")} className="m-0 flex min-w-0 list-none flex-wrap gap-x-2.5 gap-y-1 p-0 text-muted-foreground">
					{labels.map((label) => (
						<li className="inline-flex min-w-0 items-center gap-1" key={label.name}>
							<span
								aria-hidden="true"
								className="size-dot-sm shrink-0 rounded-full bg-passive"
								style={label.color && /^[0-9a-f]{6}$/i.test(label.color) ? { backgroundColor: `#${label.color}` } : undefined}
							/>
							<span className="truncate">{label.name}</span>
						</li>
					))}
				</ul>
			) : null}
			{reviewers.length > 0 ? (
				<p className="m-0 min-w-0 truncate text-muted-foreground">
					{t("pr.reviewRequestedFrom", { names: reviewers.map((reviewer) => reviewer.login).join(", ") })}
				</p>
			) : null}
		</div>
	);
}

/**
 * The checks that need a look, failures first, linked to their runs: a few at
 * a time, the rest one link to the provider. Nothing when every check passed.
 */
export function PRChecksList({ pr }: { pr: SessionPRSummary }) {
	const { t } = useTranslation();
	const checks = sortedChecks((pr.ci.checks ?? []).filter((check) => needsAttention.has(check.status)));
	if (checks.length === 0) return null;
	const hidden = checks.length - PR_CHECKS_SHOWN;
	const checksUrl = prChecksUrl(pr);
	return (
		<ul className="m-0 flex min-w-0 list-none flex-col gap-1 p-0 text-xs leading-4" data-testid="pr-checks">
			{checks.slice(0, PR_CHECKS_SHOWN).map((check) => {
				const tone = checkTone[check.status];
				const name = (
					<>
						<span className="min-w-0 truncate">{check.name}</span>
						<span className="sr-only"> {t(`pr.checks.status.${tone.key}`)}</span>
					</>
				);
				return (
					<li className="flex min-w-0 items-center gap-2 text-muted-foreground" key={check.name}>
						<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full bg-current", tone.className, tone.breathe && "animate-status-pulse")} />
						{check.url ? (
							<ProductExternalLink className="inline-flex min-w-0 items-center gap-0.5 underline-offset-2 hover:text-foreground hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" href={check.url}>
								{name}
								<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" />
							</ProductExternalLink>
						) : (
							<span className="inline-flex min-w-0 items-center">{name}</span>
						)}
					</li>
				);
			})}
			{hidden > 0 ? (
				<li className="pl-3.5 text-passive">
					{checksUrl ? (
						<ProductExternalLink className="inline-flex items-center gap-0.5 underline-offset-2 hover:text-foreground hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" href={checksUrl}>
							{t("pr.checks.more", { count: hidden })}
							<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" />
						</ProductExternalLink>
					) : t("pr.checks.more", { count: hidden })}
				</li>
			) : null}
		</ul>
	);
}
