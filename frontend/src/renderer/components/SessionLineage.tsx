import { useQuery } from "@tanstack/react-query";
import { ChevronRight, GitBranch } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { sessionWorkspaceHistoryQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { formatTimeCompact } from "../lib/format-time";
import { sessionBranchPresentation } from "../lib/session-branch";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";

/** Commits listed on the branch lane; the rest collapse into "+N more". */
export const LINEAGE_COMMITS_SHOWN = 4;

const ROW = 26;
const MAIN_X = 8;
const BRANCH_X = 26;
const TEXT_X = 44;
const WIDTH = 56;

// The base branch is always the first lane and always the same colour; the
// session's branch takes another. A colour never depends on lane position.
const MAIN_COLOR = "var(--color-status-ready)";
const BRANCH_COLOR = "var(--color-status-merged)";

type LineageCommit = { sha: string; subject: string };

/** Lineage folded or open per session; outlives tab switches like the PR sections. */
const lineageExpansion = new Map<string, boolean>();

export function resetLineageExpansion() {
	lineageExpansion.clear();
}

/**
 * Where the session's branch split from its base, the commits it carries, and
 * (once its PR merged) where it came back. It draws from the commits the
 * Activity timeline already loaded, so it adds no request of its own.
 */
/** Whether the checked-out branch has a split to draw. */
export function sessionLineageDrawable(session: WorkspaceSession): boolean {
	return !session.cloud && session.kind !== "orchestrator" && (session.branchState?.commits ?? 0) > 0 && (session.branchState?.base ?? "") !== "" && !sessionBranchPresentation(session).naming;
}

/** Whether a PR's provider commits give it a lineage of its own. */
export function prLineageDrawable(pr: SessionPRSummary): boolean {
	return Boolean(pr.targetBranch) && (pr.commits?.length ?? 0) > 0;
}

export function SessionLineage({
	hostId,
	label,
	mergedAt,
	session,
}: {
	hostId?: string;
	label?: string;
	/** When the PR whose head is the branch tip merged; absent while the branch carries unshipped work. */
	mergedAt?: string;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const commitCount = session.branchState?.commits ?? 0;
	// The base the daemon counted against; without one there is no split to draw.
	const base = session.branchState?.base ?? "";
	const behind = session.branchState?.behind ?? 0;
	const drawable = sessionLineageDrawable(session);
	// Same key as the Activity timeline's read, so the two share one request.
	const history = useQuery({
		queryKey: ["session-activity-commits", hostId ?? "", session.id, commitCount],
		queryFn: sessionWorkspaceHistoryQueryOptions(session.id, undefined, hostId).queryFn,
		staleTime: Infinity,
		placeholderData: (previous) => previous,
		enabled: drawable,
	});
	const commits: LineageCommit[] = (history.data?.commits ?? []).map((commit) => ({ sha: commit.sha, subject: commit.subject }));
	if (!drawable || commits.length === 0) return null;
	const topLabel = mergedAt
		? t("inspector.git.mergedInto", { base, time: formatTimeCompact(mergedAt) })
		: behind > 0
			? t("inspector.git.baseMoved", { count: behind })
			: undefined;
	return <LineageGraph base={base} className="" commits={commits} label={label} mergedAt={mergedAt} stateKey={`${hostId ?? ""}:${session.id}`} topLabel={topLabel} total={commitCount} />;
}

/**
 * A PR's lineage when its branch is not the one checked out: the commits the
 * provider reported for it, splitting from its target branch. How far the
 * target moved since is only known for the checked-out branch.
 */
export function PRLineage({ className, hostId, label, pr, sessionId }: { className?: string; hostId?: string; label?: string; pr: SessionPRSummary; sessionId: string }) {
	const { t } = useTranslation();
	const base = pr.targetBranch;
	// The provider lists them oldest first; the lane reads newest first. Daemons
	// that predate PR details send none.
	const commits: LineageCommit[] = [...(pr.commits ?? [])].reverse().map((commit) => ({ sha: commit.sha, subject: commit.headline }));
	if (!base || commits.length === 0) return null;
	const mergedAt = pr.state === "merged" ? pr.stateChangedAt ?? undefined : undefined;
	return (
		<LineageGraph
			base={base}
			className={className}
			commits={commits}
			label={label}
			mergedAt={mergedAt}
			stateKey={`${hostId ?? ""}:${sessionId}:${pr.url || pr.number}`}
			topLabel={mergedAt ? t("inspector.git.mergedInto", { base, time: formatTimeCompact(mergedAt) }) : undefined}
			total={pr.commitCount}
		/>
	);
}

function LineageGraph({
	base,
	className = "mt-3",
	commits: all,
	label,
	mergedAt,
	stateKey,
	topLabel,
	total,
}: {
	base: string;
	className?: string;
	/** Newest first. */
	commits: LineageCommit[];
	/** Names the lineage when several share a list; defaults to "Lineage". */
	label?: string;
	mergedAt?: string;
	/** Remembers whether this lineage is folded. */
	stateKey: string;
	topLabel?: string;
	/** Commits on the branch, which can exceed the ones listed. */
	total: number;
}) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const bodyId = useId();
	const [expanded, setExpanded] = useState(() => lineageExpansion.get(stateKey) ?? true);
	const toggle = () => {
		lineageExpansion.set(stateKey, !expanded);
		setExpanded(!expanded);
	};

	const shown = all.slice(0, LINEAGE_COMMITS_SHOWN);
	const hidden = Math.max(total, all.length) - shown.length;
	const lastRow = shown.length + 1;
	const y = (row: number) => row * ROW + ROW / 2;
	const height = (lastRow + 1) * ROW;
	const firstCommitY = y(1);
	const lastCommitY = y(shown.length);
	const splitY = y(lastRow);
	const topY = y(0);

	return (
		<div className={cn("flex min-w-0 flex-col", className)} data-testid="session-lineage">
			<button
				aria-controls={expanded ? bodyId : undefined}
				aria-expanded={expanded}
				className="flex min-w-0 items-center gap-2 rounded-sm py-1 text-left text-xs text-passive transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
				onClick={toggle}
				type="button"
			>
				<span className={label ? "min-w-0 truncate text-foreground" : "shrink-0"}>{label ?? t("inspector.git.lineage")}</span>
				{/* Folded, the one fact that matters still shows. */}
				{!expanded && topLabel ? <span className="min-w-0 truncate">{topLabel}</span> : null}
				<ChevronRight aria-hidden="true" className={cn("ml-auto size-icon-sm shrink-0 transition-transform motion-reduce:transition-none", expanded ? "-rotate-90" : "rotate-90")} />
			</button>
			<AnimatePresence initial={false}>
				{expanded ? (
					<motion.div
						animate={{ height: "auto", opacity: 1, transitionEnd: { overflow: "visible" } }}
						className="min-w-0"
						exit={{ height: 0, opacity: 0, overflow: "hidden" }}
						id={bodyId}
						initial={{ height: 0, opacity: 0, overflow: "hidden" }}
						transition={reduceMotion ? { duration: 0 } : { duration: 0.22, ease: [0.4, 0, 0.2, 1] }}
					>
						<div className="relative" style={{ height }}>
							<svg aria-hidden="true" className="absolute left-0 top-0" height={height} width={WIDTH}>
								<line stroke={MAIN_COLOR} strokeWidth="2" x1={MAIN_X} x2={MAIN_X} y1={topY} y2={splitY} />
								<line stroke={BRANCH_COLOR} strokeWidth="2" x1={BRANCH_X} x2={BRANCH_X} y1={firstCommitY} y2={lastCommitY} />
								<path
									d={`M${MAIN_X} ${splitY} C${MAIN_X} ${lastCommitY + 8}, ${BRANCH_X} ${splitY - 8}, ${BRANCH_X} ${lastCommitY}`}
									fill="none"
									stroke={BRANCH_COLOR}
									strokeWidth="2"
								/>
								{mergedAt ? (
									<path
										d={`M${BRANCH_X} ${firstCommitY} C${BRANCH_X} ${topY + 8}, ${MAIN_X} ${firstCommitY - 8}, ${MAIN_X} ${topY}`}
										fill="none"
										stroke={BRANCH_COLOR}
										strokeWidth="2"
									/>
								) : null}
								<circle cx={MAIN_X} cy={topY} fill={MAIN_COLOR} r="4" />
								<circle cx={MAIN_X} cy={splitY} fill={MAIN_COLOR} r="4" />
								{shown.map((commit, index) => (
									<circle cx={BRANCH_X} cy={y(index + 1)} fill={BRANCH_COLOR} key={commit.sha} r="3.5" />
								))}
							</svg>
							<ol className="m-0 list-none p-0 text-xs" aria-label={label ?? t("inspector.git.lineage")}>
								<li className="flex items-center gap-2" style={{ height: ROW, paddingLeft: TEXT_X }}>
									<span className="font-mono" style={{ color: MAIN_COLOR }}>{base}</span>
									{topLabel ? <span className="min-w-0 truncate text-passive">{topLabel}</span> : null}
								</li>
								{shown.map((commit) => (
									<li className="flex items-center" key={commit.sha} style={{ height: ROW, paddingLeft: TEXT_X }}>
										<span className="min-w-0 truncate text-foreground" title={commit.subject}>{commit.subject}</span>
									</li>
								))}
								<li className="flex items-center gap-2" style={{ height: ROW, paddingLeft: TEXT_X }}>
									<span className="text-passive">{t("inspector.git.splitFrom", { base })}</span>
									{hidden > 0 ? <span className="shrink-0 text-passive">{t("inspector.git.moreCommits", { count: hidden })}</span> : null}
								</li>
							</ol>
						</div>
					</motion.div>
				) : null}
			</AnimatePresence>
		</div>
	);
}

/**
 * The lineage button pinned to the bottom of the summary. It opens a panel
 * above it with one lineage per branch: the checked-out branch drawn from
 * the worktree, every other PR from the commits its provider reported.
 */
export function SessionLineageFooter({
	branchPRNumber,
	hostId,
	mergedAt,
	prs,
	session,
}: {
	/** The PR whose branch is checked out; its lineage comes from the worktree. */
	branchPRNumber?: number;
	hostId?: string;
	mergedAt?: string;
	prs: SessionPRSummary[];
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const reduceMotion = useReducedMotion();
	const panelId = useId();
	const stateKey = `footer:${hostId ?? ""}:${session.id}`;
	const [open, setOpen] = useState(() => lineageExpansion.get(stateKey) ?? false);
	const toggle = () => {
		lineageExpansion.set(stateKey, !open);
		setOpen(!open);
	};
	const branchName = sessionBranchPresentation(session).branch;
	const prName = (pr: SessionPRSummary) => `#${pr.number} ${pr.title}`;
	const branchPR = prs.find((pr) => pr.number === branchPRNumber);
	const worktree = sessionLineageDrawable(session)
		? { key: "worktree", node: <SessionLineage hostId={hostId} label={branchPR ? prName(branchPR) : branchName || undefined} mergedAt={mergedAt} session={session} /> }
		: undefined;
	const others = prs
		.filter((pr) => pr.number !== branchPRNumber && prLineageDrawable(pr))
		.map((pr) => ({ key: pr.url || String(pr.number), node: <PRLineage className="" hostId={hostId} label={prName(pr)} pr={pr} sessionId={session.id} /> }));
	const lineages = [...(worktree ? [worktree] : []), ...others];
	if (lineages.length === 0) return null;
	return (
		<div className="border-t border-border bg-background" data-testid="lineage-footer">
			<AnimatePresence initial={false}>
				{open ? (
					<motion.div
						animate={{ height: "auto", opacity: 1 }}
						className="overflow-hidden"
						exit={{ height: 0, opacity: 0 }}
						id={panelId}
						initial={{ height: 0, opacity: 0 }}
						transition={reduceMotion ? { duration: 0 } : { duration: 0.22, ease: [0.4, 0, 0.2, 1] }}
					>
						<div className="board-scrollbar flex max-h-[45vh] flex-col gap-2 divide-y divide-(--color-border-settings-input) overflow-y-auto px-3 pt-2 pb-1" role="region" aria-label={t("inspector.git.lineage")}>
							{lineages.map((lineage) => <div className="min-w-0 pt-1 first:pt-0" key={lineage.key}>{lineage.node}</div>)}
						</div>
					</motion.div>
				) : null}
			</AnimatePresence>
			<button
				aria-controls={open ? panelId : undefined}
				aria-expanded={open}
				className="flex h-9 w-full min-w-0 items-center gap-2 px-3 text-left text-xs text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60"
				onClick={toggle}
				type="button"
			>
				<GitBranch aria-hidden="true" className="size-icon-sm shrink-0" />
				<span className="shrink-0 font-medium">{t("inspector.git.lineage")}</span>
				{lineages.length > 1 ? <span className="shrink-0 text-passive">{t("inspector.git.lineageBranches", { count: lineages.length })}</span> : null}
				<ChevronRight aria-hidden="true" className={cn("ml-auto size-icon-sm shrink-0 transition-transform motion-reduce:transition-none", open ? "rotate-90" : "-rotate-90")} />
			</button>
		</div>
	);
}
