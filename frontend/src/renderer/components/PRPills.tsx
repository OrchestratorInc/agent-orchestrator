import { ChevronDown, GitBranch, GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { prCanMerge, prHasNoChecks, prRepositoryFromUrl, prRepositoryLabels } from "../lib/pr-display";
import { usePRNoChecksClock } from "../hooks/usePRNoChecksClock";
import { cn } from "../lib/utils";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";

/** A PR state's icon and colour, shared by the pills and the PR card. */
export const prStateIcon: Record<SessionPRSummary["state"], { icon: LucideIcon; className: string }> = {
	open: { icon: GitPullRequest, className: "text-success" },
	draft: { icon: GitPullRequestDraft, className: "text-passive" },
	merged: { icon: GitMerge, className: "text-status-merged" },
	closed: { icon: GitPullRequestClosed, className: "text-error" },
};

/** A PR's identity in a session: its URL, or its number for PRs without one. */
export function prFocusKey(pr: SessionPRSummary): string {
	return pr.url || pr.htmlUrl || String(pr.number);
}

/** Open PRs, then drafts, newest first in each; merged and closed ones after. */
export function sortSessionPRs(prs: SessionPRSummary[]): SessionPRSummary[] {
	const byNewest = [...prs].sort((a, b) => b.number - a.number);
	const rank = { open: 0, draft: 1, merged: 2, closed: 2 } as const;
	return byNewest.sort((a, b) => rank[a.state] - rank[b.state]);
}

/** The one fact a folded PR shows as its dot: what stands in its way, or that it is ready or done. */
export function prStatusDot(pr: SessionPRSummary): { className: string; labelKey: "pr.card.mergeConflict" | "pr.card.checksFailing" | "pr.review.changesRequested" | "pr.card.readyToMerge" | "pr.card.checksPending" | "pr.card.reviewRequired" | "pr.state.merged" | "pr.state.closed" | "pr.state.draft" | "pr.state.open"; breathe?: boolean } {
	if (pr.state === "merged") return { className: "bg-status-merged", labelKey: "pr.state.merged" };
	if (pr.state === "closed") return { className: "bg-error", labelKey: "pr.state.closed" };
	if (pr.state === "draft") return { className: "bg-passive", labelKey: "pr.state.draft" };
	if (pr.mergeability.state === "conflicting") return { className: "bg-error", labelKey: "pr.card.mergeConflict" };
	if (pr.ci.state === "failing") return { className: "bg-error", labelKey: "pr.card.checksFailing" };
	if (pr.review.decision === "changes_requested") return { className: "bg-warning", labelKey: "pr.review.changesRequested" };
	if (prCanMerge(pr, { noChecks: prHasNoChecks(pr) })) return { className: "bg-success", labelKey: "pr.card.readyToMerge" };
	if (pr.ci.state === "pending") return { className: "bg-muted-foreground", labelKey: "pr.card.checksPending", breathe: true };
	if (pr.review.decision === "review_required") return { className: "bg-passive", labelKey: "pr.card.reviewRequired" };
	return { className: "bg-passive", labelKey: "pr.state.open" };
}

/**
 * The session's PRs as a row of pills over the PR card, one per open or
 * draft PR with its status dot; merged and closed ones wait behind one
 * "+N earlier" pill. The pill of the PR on screen is pressed, and the PR the
 * branch updates carries a branch mark. Picking a pill shows that PR.
 */
export function PRPills({ branchPR, focused, onFocus, prs, showRepository }: {
	/** The PR the branch updates; its pill carries a branch mark. */
	branchPR?: SessionPRSummary;
	focused: SessionPRSummary;
	onFocus: (pr: SessionPRSummary) => void;
	prs: SessionPRSummary[];
	showRepository?: boolean;
}) {
	const { t } = useTranslation();
	usePRNoChecksClock(prs);
	if (prs.length < 2) return null;
	const sorted = sortSessionPRs(prs);
	const active = sorted.filter((pr) => pr.state === "open" || pr.state === "draft");
	const earlier = sorted.filter((pr) => pr.state === "merged" || pr.state === "closed");
	const focusKey = prFocusKey(focused);
	// A merged or closed PR on screen gets its own pill, so the pressed pill always names it.
	const shown = earlier.some((pr) => prFocusKey(pr) === focusKey) ? [...active, focused] : active;
	const folded = earlier.filter((pr) => prFocusKey(pr) !== focusKey);
	const branchKey = branchPR ? prFocusKey(branchPR) : undefined;
	const repositoryLabels = prRepositoryLabels(prs);
	return (
		<div aria-label={t("pr.others.label")} className="flex min-w-0 flex-wrap items-center gap-1.5 text-xs" data-testid="pr-pills" role="group">
			{shown.map((pr) => {
				const dot = prStatusDot(pr);
				const status = t(dot.labelKey);
				const pressed = prFocusKey(pr) === focusKey;
				const branch = prFocusKey(pr) === branchKey;
				// The repository comes from the PR, or from its URL when the PR doesn't say.
				const fullRepository = showRepository ? pr.repo || prRepositoryFromUrl(pr.htmlUrl || pr.url) : undefined;
				const repository = fullRepository ? repositoryLabels.get(fullRepository) ?? fullRepository : undefined;
				return (
					<button
						aria-pressed={pressed}
						className={cn(
							"inline-flex h-6 max-w-[11rem] min-w-0 items-center gap-1.5 rounded-full border px-2 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
							pressed
								? "border-foreground/70 bg-background text-foreground"
								: "border-(--color-border-settings-input) bg-(--color-bg-settings-input) text-muted-foreground hover:border-border-strong hover:text-foreground",
						)}
						key={prFocusKey(pr)}
						onClick={() => onFocus(pr)}
						title={`${fullRepository ? `${fullRepository} · ` : ""}${pr.title} · ${status}${branch ? ` · ${t("pr.others.checkedOut")}` : ""}`}
						type="button"
					>
						<span aria-hidden="true" className={cn("size-dot-sm shrink-0 rounded-full", dot.className, dot.breathe && "animate-status-pulse")} />
						{/* A long repository name gives way; the number always shows. */}
						{repository ? <span className="min-w-0 shrink truncate font-medium">{repository}</span> : null}
						<span className="shrink-0 font-medium">{t("pr.others.short", { number: pr.number })}</span>
						{pr.title && !repository ? <span className="min-w-0 truncate">{pr.title}</span> : null}
						{branch ? <GitBranch aria-hidden="true" className="size-icon-2xs shrink-0 text-passive" /> : null}
						<span className="sr-only">, {status}{branch ? `, ${t("pr.others.checkedOut")}` : ""}</span>
					</button>
				);
			})}
			{folded.length > 0 ? (
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<button
							className="inline-flex h-6 shrink-0 items-center gap-0.5 rounded-full border border-dashed border-(--color-border-settings-input) px-2 text-passive transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
							data-testid="pr-pills-earlier"
							type="button"
						>
							{t("pr.others.earlier", { count: folded.length })}
							<ChevronDown aria-hidden="true" className="size-icon-2xs shrink-0" />
						</button>
					</DropdownMenuTrigger>
					<DropdownMenuContent align="start" className="w-72 max-w-[calc(100vw-2rem)]">
						{folded.map((pr) => {
							const state = prStateIcon[pr.state];
							return (
								<DropdownMenuItem className="min-w-0 gap-2" key={prFocusKey(pr)} onSelect={() => onFocus(pr)}>
									<state.icon aria-hidden="true" className={cn("size-icon-sm shrink-0", state.className)} />
									<span className="min-w-0 flex-1 truncate"><span className="font-medium">{t("pr.others.short", { number: pr.number })}</span> <span className="text-muted-foreground">{pr.title}</span></span>
									<span className="shrink-0 text-micro text-passive">{t(prStatusDot(pr).labelKey)}</span>
								</DropdownMenuItem>
							);
						})}
					</DropdownMenuContent>
				</DropdownMenu>
			) : null}
		</div>
	);
}
