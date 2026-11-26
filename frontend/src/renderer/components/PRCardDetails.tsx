import { useState } from "react";
import { ChevronDown } from "lucide-react";
import { useTranslation } from "react-i18next";
import { CheckStateGlyph, type PRCheckState } from "@aoagents/product-ui";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { prChecksUrl, prCheckState } from "../lib/pr-display";
import { cn } from "../lib/utils";
import { ProductExternalLink } from "./ProductExternalLink";

type Check = SessionPRSummary["ci"]["checks"][number];

/** Checks listed in one group; the rest are a link to the provider's checks page. */
const PR_CHECKS_SHOWN = 8;

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
 * A PR's checks, opened from the card's Checks row. What still needs a look
 * (failing, running, pending) is listed, worst first, with its state in words
 * at the right; passed and skipped checks are one line each that opens. Passes
 * show at once when nothing else is listed. A check's name links to its run.
 */
export function PRChecksGroups({ pr }: { pr: SessionPRSummary }) {
	const checks = pr.ci.checks ?? [];
	const checksUrl = prChecksUrl(pr);
	if (checks.length === 0) return null;
	const needsLook = sortedChecks(checks.filter((check) => attentionStates.includes(prCheckState[check.status])));
	const settled = settledStates
		.map((state) => ({ state, checks: sortedChecks(checks.filter((check) => prCheckState[check.status] === state)) }))
		.filter((group) => group.checks.length > 0);
	return (
		<div className="flex min-w-0 flex-col gap-1.5" data-testid="pr-checks">
			{needsLook.length > 0 ? <PRCheckList checks={needsLook} checksUrl={checksUrl} withState /> : null}
			{settled.map((group) => (
				<PRChecksFold
					checks={group.checks}
					checksUrl={checksUrl}
					defaultOpen={group.state === "passed" && needsLook.length === 0}
					key={group.state}
					state={group.state}
				/>
			))}
		</div>
	);
}

// Listed at once: what still needs a look. Folded: what is settled.
const attentionStates: PRCheckState[] = ["failed", "running", "pending"];
const settledStates = ["passed", "skipped"] as const;

function PRChecksFold({ checks, checksUrl, defaultOpen, state }: { checks: Check[]; checksUrl?: string; defaultOpen: boolean; state: PRCheckState }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(defaultOpen);
	return (
		<section aria-label={t(`pr.checks.count.${state}`, { count: checks.length })} className="min-w-0" data-state={state}>
			<button
				aria-expanded={open}
				className="inline-flex min-w-0 items-center gap-1 rounded-sm text-caption text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
				onClick={() => setOpen((value) => !value)}
				type="button"
			>
				<span className="tabular-nums">{t(`pr.checks.count.${state}`, { count: checks.length })}</span>
				<ChevronDown aria-hidden="true" className={cn("size-icon-2xs shrink-0 transition-transform", open && "rotate-180")} />
			</button>
			{open ? <PRCheckList checks={checks} checksUrl={checksUrl} className="mt-1" /> : null}
		</section>
	);
}

/** Checks as lines: glyph, then the name, which links to the run. A long list ends in one link to the provider's checks page. */
function PRCheckList({ checks, checksUrl, className, withState = false }: { checks: Check[]; checksUrl?: string; className?: string; withState?: boolean }) {
	const { t } = useTranslation();
	const hidden = checks.length - PR_CHECKS_SHOWN;
	return (
		<ul className={cn("m-0 flex min-w-0 list-none flex-col gap-1 p-0 text-xs leading-4", className)}>
			{checks.slice(0, PR_CHECKS_SHOWN).map((check) => {
				const state = prCheckState[check.status];
				const name = <span className="min-w-0 truncate">{check.name}</span>;
				return (
					<li className="flex min-w-0 items-center gap-2 text-muted-foreground" key={check.name}>
						<CheckStateGlyph state={state} />
						{check.url ? <ProductExternalLink className={linkClass} href={check.url}>{name}</ProductExternalLink> : name}
						{/* The state in words: shown at the right for what needs a look, read out for the rest. */}
						{withState
							? <span className={cn("ml-auto shrink-0 pl-2 text-micro", state === "failed" ? "text-error" : "text-passive")}>{t(`pr.checks.group.${state}`)}</span>
							: <span className="sr-only"> {t(`pr.checks.status.${checkTone[check.status].key}`)}</span>}
					</li>
				);
			})}
			{hidden > 0 ? (
				<li className="text-passive">
					{checksUrl ? <ProductExternalLink className={linkClass} href={checksUrl}>{t("pr.checks.more", { count: hidden })}</ProductExternalLink> : t("pr.checks.more", { count: hidden })}
				</li>
			) : null}
		</ul>
	);
}

// The name is the link: underlined on hover, no arrow after it.
const linkClass = "inline-flex min-w-0 items-center underline-offset-2 hover:text-foreground hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60";
