import { useState } from "react";
import { ArrowUpRight, ChevronDown } from "lucide-react";
import { useTranslation } from "react-i18next";
import { CheckStateGlyph, type PRCheckState } from "@aoagents/product-ui";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { prChecksUrl, prCheckState, prCheckStates } from "../lib/pr-display";
import { cn } from "../lib/utils";
import { ProductExternalLink } from "./ProductExternalLink";

type Check = SessionPRSummary["ci"]["checks"][number];

/** Checks listed in one group; the rest are a link to the provider's checks page. */
const PR_CHECKS_SHOWN = 8;
/** More passes than this fold behind their group's heading. */
const PR_PASSED_SHOWN = 3;

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

export const checkTone: Record<Check["status"], { className: string; key: "failed" | "running" | "passed" | "skipped"; breathe?: boolean }> = {
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
 * A PR's checks grouped by where they stand, failing first, each linked to its
 * run. Passes fold away when there are many and skipped checks always do; a
 * long group lists a few and links the rest to the provider.
 */
export function PRChecksGroups({ pr }: { pr: SessionPRSummary }) {
	const checks = pr.ci.checks ?? [];
	const checksUrl = prChecksUrl(pr);
	const groups = prCheckStates
		.map((state) => ({ state, checks: sortedChecks(checks.filter((check) => prCheckState[check.status] === state)) }))
		.filter((group) => group.checks.length > 0);
	if (groups.length === 0) return null;
	return (
		<div className="flex min-w-0 flex-col gap-1.5" data-testid="pr-checks">
			{groups.map((group) => (
				<PRChecksGroup
					checks={group.checks}
					checksUrl={checksUrl}
					folded={group.state === "skipped" || (group.state === "passed" && group.checks.length > PR_PASSED_SHOWN)}
					key={group.state}
					state={group.state}
				/>
			))}
		</div>
	);
}

/** A check's status in words after its name, as GitHub's list says it. */
const checkStatusWord: Record<Check["status"], "failed" | "cancelled" | "in_progress" | "queued" | "passed" | "skipped"> = {
	failed: "failed",
	cancelled: "cancelled",
	in_progress: "in_progress",
	queued: "queued",
	unknown: "queued",
	passed: "passed",
	skipped: "skipped",
};

/**
 * One state's checks, laid out like GitHub's merge box: a foldable sentence
 * heading ("2 failing checks"), then a row per check with a line between rows,
 * its glyph, its name (which links to the run) and its status in words.
 */
function PRChecksGroup({ checks, checksUrl, folded, state }: { checks: Check[]; checksUrl?: string; folded: boolean; state: PRCheckState }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(!folded);
	const hidden = checks.length - PR_CHECKS_SHOWN;
	return (
		<section aria-label={t(`pr.checks.count.${state}`, { count: checks.length })} className="min-w-0" data-state={state}>
			<button
				aria-expanded={open}
				className="inline-flex min-w-0 items-center gap-1 rounded-sm text-caption text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
				onClick={() => setOpen((value) => !value)}
				type="button"
			>
				<span className="tabular-nums">{t(`pr.checks.heading.${state}`, { count: checks.length })}</span>
				<ChevronDown aria-hidden="true" className={cn("size-icon-2xs shrink-0 transition-transform", open && "rotate-180")} />
			</button>
			{open ? (
				<ul className="m-0 mt-0.5 flex min-w-0 list-none flex-col p-0 text-xs leading-4">
					{checks.slice(0, PR_CHECKS_SHOWN).map((check) => {
						const name = <span className="min-w-0 truncate">{check.name}</span>;
						return (
							<li className="flex min-w-0 items-center gap-2 border-t border-border py-1.5 first:border-t-0" key={check.name}>
								<CheckStateGlyph state={state} />
								{check.url ? (
									<ProductExternalLink className="inline-flex min-w-0 items-center text-foreground underline-offset-2 hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" href={check.url}>
										{name}
									</ProductExternalLink>
								) : (
									<span className="inline-flex min-w-0 items-center text-foreground">{name}</span>
								)}
								<span className="shrink-0 text-micro text-muted-foreground">{t(`pr.checks.row.${checkStatusWord[check.status]}`)}</span>
							</li>
						);
					})}
					{hidden > 0 ? (
						<li className="border-t border-border py-1.5 pl-5 text-passive">
							{checksUrl ? (
								<ProductExternalLink className="inline-flex items-center gap-0.5 underline-offset-2 hover:text-foreground hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" href={checksUrl}>
									{t("pr.checks.more", { count: hidden })}
									<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" />
								</ProductExternalLink>
							) : t("pr.checks.more", { count: hidden })}
						</li>
					) : null}
				</ul>
			) : null}
		</section>
	);
}
