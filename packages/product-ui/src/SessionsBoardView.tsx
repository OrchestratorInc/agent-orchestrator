import {
	Fragment,
	forwardRef,
	type HTMLAttributes,
	type ReactNode,
} from "react";
import type { ExternalLinkComponent } from "./external-link";
import {
	GitBranchIcon,
	GitPullRequestIcon,
	LoaderCircleIcon,
	MessageSquareIcon,
} from "./icons";
import {
	attentionZone,
	defaultProductUITranslator,
	getDisplayStatusLabel,
	getKanbanColumnView,
	getSessionStatusView,
	toKanbanColumn,
	type KanbanColumnView,
	type ProductUITranslator,
} from "./session-presentation";
import type { KanbanColumn, SessionActivity, SessionStatus } from "./session-models";
import { branchTopic } from "./formatting";
import { UserAvatar } from "./UserAvatar";
import { cn } from "./utils";

export type BoardSessionPresentation = {
	activity?: SessionActivity;
	branch?: string;
	id: string;
	/**
	 * Daemon-derived lane placement. Absent only for fixtures and for a daemon
	 * too old to send one; {@link toKanbanColumn} then keeps the placement the
	 * session's status already implied.
	 */
	kanbanColumn?: KanbanColumn;
	/**
	 * Daemon-derived phrase for what is happening inside {@link kanbanColumn}
	 * ("Fixing CI failures", "Awaiting review"), replacing {@link status} as
	 * the card's status text. Translated via `getDisplayStatusLabel` for known
	 * phrases (see {@link DisplayStatus}); an unrecognized one -- a newer
	 * daemon that shipped a phrase before this build -- renders as the raw,
	 * already-renderable English text the API guarantees. The card styles the
	 * phrase with its daemon-owned Kanban column, so presentation never has to
	 * infer lifecycle semantics from human-readable copy. A daemon too old to
	 * send one falls back to the translated {@link status} label.
	 */
	displayStatus?: string;
	statusReadiness?: "checking" | "ready" | "unavailable";
	/**
	 * Daemon-confirmed termination fact. `status` can already read "merged"
	 * while the session is still live (the SCM merged before the session
	 * exited), so the finished-card PR progress footer requires this in
	 * addition to `status` before it renders.
	 */
	isTerminated?: boolean;
	provider: string;
	status: SessionStatus;
	statusPresentation?: BoardSessionStatusPresentation;
	/** Paint the plain card surface instead of the status's own tint, such as the terminated red. */
	plainSurface?: boolean;
	title: string;
	trackerIssueId?: string;
	updatedAt: string;
	lastUserMessageAt?: string;
};

export type BoardSessionStatusPresentation = {
	className: string;
	indicatorClassName: string;
	label: string;
	tone?: string;
};

export type BoardPullRequestState = "closed" | "open" | "draft" | "merged";

// Display statuses that mean work is still turning, and so earn the spinning
// loader beside the card's status label: the review the PR is waiting on, or an
// AO-driven loop working the PR. Settled phrases ("Mergeable", "Approved",
// "Merged") are deliberately absent — see #4725 and #5081.
const IN_PROGRESS_DISPLAY_STATUSES = new Set<string>([
	"Working",
	"Fixing CI failures",
	"Addressing comments",
	"Reviewing",
	"Retrying review",
]);

export type BoardReviewerPresentation = {
	id: string;
	avatarUrl?: string;
};

export type BoardPullRequestPresentation = {
	commentCount?: number;
	number: number;
	reviewers?: BoardReviewerPresentation[];
	state: BoardPullRequestState;
	url: string;
};

export type BoardPullRequestProgress = Record<BoardPullRequestState, number> & {
	total: number;
};

export type BoardUsagePresentation = {
	accessibleLabel: string;
	compactLabel: string;
};

export type BoardPullRequestLabels = {
	progress?: (progress: BoardPullRequestProgress) => string;
	short: string;
	states: Record<BoardPullRequestState, string>;
};

export type BoardColumnLabels = {
	columnAria: (label: string) => string;
};

export type SessionsBoardGridViewProps<
	TSession extends BoardSessionPresentation = BoardSessionPresentation,
> = {
	columns: KanbanColumnView[];
	labels: BoardColumnLabels;
	renderSessionCard: (session: TSession) => ReactNode;
	sessions: TSession[];
};

export function SessionsBoardGridView<TSession extends BoardSessionPresentation>({
	columns,
	labels,
	renderSessionCard,
	sessions,
}: SessionsBoardGridViewProps<TSession>) {
	const byColumn = new Map<KanbanColumn, TSession[]>();
	for (const session of sessions) {
		const column = toKanbanColumn(session.kanbanColumn, session.status);
		const sessionsForColumn = byColumn.get(column);
		if (sessionsForColumn) sessionsForColumn.push(session);
		else byColumn.set(column, [session]);
	}

	return (
		<div
			className="board-horizontal-scrollbar h-full overflow-x-auto overflow-y-hidden"
			data-testid="board-horizontal-scroll"
		>
			<div className="relative grid h-full min-w-[64rem] grid-cols-4 divide-x divide-border-strong xl:min-w-0">
				<div
					aria-hidden="true"
					className="pointer-events-none absolute inset-x-0 top-12 z-10 border-t border-border-strong"
				/>
				{columns.map((column) => (
					<BoardColumnView
						column={column}
						key={column.column}
						labels={labels}
						renderSessionCard={renderSessionCard}
						sessions={byColumn.get(column.column) ?? []}
					/>
				))}
			</div>
		</div>
	);
}

function BoardColumnView<TSession extends BoardSessionPresentation>({
	column,
	labels,
	renderSessionCard,
	sessions,
}: {
	column: KanbanColumnView;
	labels: BoardColumnLabels;
	renderSessionCard: (session: TSession) => ReactNode;
	sessions: TSession[];
}) {
	const ordered = [...sessions].sort((left, right) => {
		const attentionPriority =
			Number(boardSessionNeedsAttention(right)) - Number(boardSessionNeedsAttention(left));
		return attentionPriority || right.updatedAt.localeCompare(left.updatedAt);
	});
	return (
		<section
			aria-label={labels.columnAria(column.label)}
			className="flex min-w-0 flex-col overflow-hidden"
			data-testid="board-column"
			data-column={column.column}
		>
			<div className="flex h-12 shrink-0 items-center gap-2.5 px-4">
				<span
					data-testid="board-column-swatch"
					className="size-[var(--size-swatch)] rounded-full"
					style={{ backgroundColor: column.dot }}
				/>
				<span className={cn("text-xs font-medium", column.titleClassName)}>
					{column.label}
				</span>
				<span className="ml-auto tabular-nums text-xs leading-none text-passive">{ordered.length}</span>
			</div>
			<div className="board-scrollbar min-h-0 flex-1 overflow-y-auto pl-3 pr-2 pb-3 pt-3">
				<div className="flex min-h-full flex-col gap-2.5">
					{ordered.map((session) => (
						<Fragment key={session.id}>{renderSessionCard(session)}</Fragment>
					))}
				</div>
			</div>
		</section>
	);
}

export type SessionCardViewProps = {
		action?: ReactNode;
	/** Latest session artifacts, shown as evidence below the PRs. */
	artifacts?: ReactNode;
	branchAction?: ReactNode;
	branchIcon?: ReactNode;
	branchLabel?: ReactNode;
	/** The dense layout for a grid of many cards (the archive): branch, PRs and
	 * time share one line, with no status footer strip. */
	compact?: boolean;
	error?: string;
	externalLink: ExternalLinkComponent;
	footer?: ReactNode;
	interactive?: boolean;
	labels: {
		formatTime: (timestamp: string) => string;
		intakeIssue: (id: string) => string;
		pr: BoardPullRequestLabels;
		updatedAt: (timestamp: string) => string;
	};
	onOpen?: () => void;
	overlay?: ReactNode;
	prs?: BoardPullRequestPresentation[];
	renderAvatar: (provider: string) => ReactNode;
	/** A static rendering of the card (a hover preview): no attention pulse. */
	still?: boolean;
	renderUsage?: (usage: BoardUsagePresentation) => ReactNode;
	/** Live cost on the machine (memory, CPU while working), shown before
	 * token usage. Grey unless this card is part of the fix for a tight machine. */
	resource?: BoardUsagePresentation & { tone?: "neutral" | "warning" | "critical" };
	/** Set by a list that owns one selection (the archive): the card's fill
	 * follows it instead of its own hover and focus, so only one card is ever
	 * lit, and the card is not a focus target of its own. */
	selected?: boolean;
	session: BoardSessionPresentation;
	/** A short label above the title, such as whose session this is. */
	tag?: ReactNode;
	translate?: ProductUITranslator;
	usage?: BoardUsagePresentation;
};

export function SessionCardView({
	action,
	artifacts,
	branchAction,
	branchIcon,
	branchLabel,
	compact = false,
	error,
	externalLink,
	footer,
	interactive = true,
	labels,
	onOpen,
	overlay,
	prs = [],
	renderAvatar,
	renderUsage = (usage) => <SessionUsageMetricView usage={usage} />,
	resource,
	selected,
	session,
	tag,
	still = false,
	translate,
	usage,
}: SessionCardViewProps) {
	const translateStatus = translate ?? defaultProductUITranslator;
	const badge = getSessionStatusView(session.status, translate);
	const statusPresentation = session.statusPresentation;
	const needsAttention = boardSessionNeedsAttention(session);
	const needsAttentionChip = needsAttention;
	const column = getKanbanColumnView(toKanbanColumn(session.kanbanColumn, session.status), translate);
	const statusClassName =
		session.displayStatus === "Closed without merge"
			? "text-status-exited"
			: session.displayStatus === "Merged"
				? "text-status-merged"
				: session.status === "mergeable" || session.displayStatus === "Mergeable"
				? "text-success"
				: (session.statusPresentation?.className ?? column.titleClassName);
	const branch = session.branch ?? "";
	const topic = branchTopic(branch);
	const showBranch = Boolean(branchLabel) || (branch !== "" && !sameLabel(topic, session.title) && !sameLabel(topic, session.id));
	const renderedStatusLabel =
		session.statusReadiness === "checking"
			? translateStatus("session.statusChecking")
			: session.statusReadiness === "unavailable"
				? translateStatus("session.statusUnavailable")
				: (statusPresentation?.label ??
					(session.displayStatus ? getDisplayStatusLabel(session.displayStatus, translate) : badge.label));
	// Additive summary footer, not a replacement for renderedStatusLabel: it
	// only appears once the daemon confirms the session is actually finished
	// ("terminated", or "merged" with isTerminated true -- a live session can
	// already read "merged" before it exits and gain more PRs).
	const isFinishedForPullRequestProgress =
		session.status === "terminated" ||
		(session.status === "merged" && session.isTerminated === true);
	const pullRequestProgressLabel =
		prs.length > 0 && isFinishedForPullRequestProgress
			? labels.pr.progress?.(countBoardPullRequests(prs))
			: undefined;
	// The loader follows the label: it spins only when the phrase itself names
	// work in progress, so a card never spins beside a settled phrase. Fall back
	// to `status` only for a daemon too old to send `displayStatus`.
	const showStatusLoader =
		session.statusReadiness === "checking" || (session.statusReadiness !== "unavailable" &&
		!needsAttention &&
		(session.displayStatus
			? IN_PROGRESS_DISPLAY_STATUSES.has(session.displayStatus)
			: session.status === "working" || session.status === "review_pending"));

	const listSelection = selected !== undefined;
	const statusLabel = (
		<span
			className={cn(
				"inline-flex min-w-0 max-w-full items-center text-2xs font-medium",
				needsAttentionChip
					? "text-status-needs-you"
					: statusClassName,
			)}
			data-kanban-column={statusPresentation ? undefined : column.column}
			data-testid="session-status"
		>
			{showStatusLoader ? <LoaderCircleIcon aria-hidden="true" className="mr-1 size-icon-2xs animate-spin" /> : null}
			<span className="min-w-0 truncate">{renderedStatusLabel}</span>
		</span>
	);
	const metrics = (
		<>
			{resource ? (
				<SessionUsageMetricView
					className={cn(resource.tone === "critical" && "text-destructive", resource.tone === "warning" && "text-warning")}
					data-resource-tone={resource.tone ?? "neutral"}
					data-testid="session-resource"
					usage={resource}
				/>
			) : null}
			{resource ? <span aria-hidden="true" className="text-border-strong">·</span> : null}
			{usage ? renderUsage(usage) : null}
			{usage ? <span aria-hidden="true" className="text-border-strong">·</span> : null}
			<span className="tabular-nums text-muted-foreground" title={labels.updatedAt(session.updatedAt)}>
				{labels.formatTime(session.updatedAt)}
			</span>
		</>
	);
	const prGroups = groupBoardPullRequests(prs).flatMap((group) => {
		const compactGroup = group.prs.filter((pr) => (pr.commentCount ?? 0) === 0);
		const commented = group.prs.filter((pr) => (pr.commentCount ?? 0) > 0);
		return [
			...(compactGroup.length > 0 ? [{ ...group, prs: compactGroup }] : []),
			...commented.map((pr) => ({ ...group, prs: [pr] })),
		];
	}).map((group) => (
		<BoardPullRequestGroup
			externalLink={externalLink}
			group={group}
			key={`${group.state}-${group.prs.map((pr) => pr.url || pr.number).join("-")}`}
			labels={labels.pr}
		/>
	));
	const branchRow = showBranch ? (
		<>
			{branchIcon ?? <GitBranchIcon aria-hidden="true" className="size-icon-2xs shrink-0" />}
			{branchLabel ?? <span className="truncate text-muted-foreground" title={branch}>{topic}</span>}
			{branchAction}
		</>
	) : null;

	return (
		<div
			onClick={interactive ? onOpen : undefined}
			role={interactive || still ? undefined : "listitem"}
			className={cn(
				still
					? // A hover preview: one elevated surface, no outline, no motion.
						"relative w-full rounded-lg bg-popover text-left"
					: listSelection
						? // The list draws the one selection; it switches instantly.
							"group relative w-full rounded-lg border border-border text-left"
						: "group relative w-full rounded-lg border border-border text-left transition-[background-color,box-shadow,transform] duration-[120ms] ease-out",
				!still && ((session.plainSurface ? undefined : badge.cardClassName) ?? "border-border bg-surface"),
				interactive &&
					(listSelection
						? "cursor-pointer"
						: "cursor-pointer hover:bg-interactive-hover focus-within:bg-interactive-hover active:scale-[0.99] has-[button:active]:scale-100 has-[a:active]:scale-100"),
				needsAttention &&
					(still
						? "bg-[color-mix(in_srgb,var(--color-status-needs-you)_10%,var(--popover))]"
						: "animate-attention-card-pulse border-status-needs-you bg-[color-mix(in_srgb,var(--color-status-needs-you)_8%,var(--color-surface))]"),
				selected && "border-border-strong bg-interactive-hover",
				// A grid of compact cards lines each row up: every card fills it.
				compact && "h-full",
			)}
			data-selected={selected || undefined}
			data-testid="board-session-card"
			data-session-id={session.id}
		>
			{interactive && onOpen && !listSelection ? (
				<button
					aria-label={session.title}
					className="pointer-events-none absolute inset-0 outline-none"
					type="button"
				/>
			) : null}
			<div className={still ? "px-3 pb-1.5 pt-2.5" : compact ? "px-3 py-2" : "px-3.5 pb-2.5 pt-2.5"}>
				{tag ? (
					<div className="mb-1.5 flex min-w-0" data-testid="board-session-tag">
						{tag}
					</div>
				) : null}
				<div className={cn("flex min-w-0 items-center", still ? "gap-2" : "gap-2.5")}>
					{renderAvatar(session.provider)}
					<div
						className="min-w-0 flex-1 line-clamp-2 overflow-hidden text-balance text-sm-md font-semibold leading-tight tracking-tight text-foreground"
						title={session.title}
					>
						{session.title}
					</div>
					{overlay || action ? (
						<div className="relative z-10 -mr-1 shrink-0 self-center">
							{overlay}
							{action}
						</div>
					) : null}
				</div>
				{compact ? (
					// Every compact card has the same shape: branch and time on one
					// line, then the PRs on their own.
					<div className="mt-1 flex min-w-0 flex-col gap-1 text-2xs text-muted-foreground" data-testid="session-card-meta">
						<div className="flex min-h-5 min-w-0 items-center gap-2">
							<div className="flex min-w-0 flex-1 items-center gap-1.5 font-sans">{branchRow}</div>
							{/* A long status truncates rather than squeezing out the branch;
							    only the time and metrics keep their full width. */}
							<div className="flex min-w-0 max-w-[70%] items-center justify-end gap-2 whitespace-nowrap">
								{statusLabel}
								<div className="flex shrink-0 items-center gap-2">{metrics}</div>
							</div>
						</div>
						{prGroups.length > 0 ? (
							<div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-0.5">{prGroups}</div>
						) : null}
					</div>
				) : showBranch ? (
					<div className={cn("flex min-w-0 items-center gap-1.5 font-sans text-2xs text-muted-foreground", still ? "mt-1" : "mt-1.5")}>
						{branchRow}
					</div>
				) : null}
				{compact && pullRequestProgressLabel ? (
					<div
						className="mt-1 min-w-0 truncate text-2xs text-muted-foreground"
						data-testid="session-pr-progress"
						title={pullRequestProgressLabel}
					>
						{pullRequestProgressLabel}
					</div>
				) : null}
			</div>
			{!compact && prs.length > 0 && (
				<div className={cn("flex min-w-0 flex-col gap-1.5 pb-1", still ? "px-3" : "px-3.5")}>
					<div className="flex min-w-0 flex-col gap-y-1 font-sans text-2xs text-muted-foreground">
						{prGroups}
					</div>
				</div>
			)}
			{artifacts ? <div className={cn("relative z-10 min-w-0 pb-2", compact ? "px-3" : "px-3.5")}>{artifacts}</div> : null}
			{compact ? null : (
				<div
					className={cn(
						"grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2",
						still ? "px-3 pb-2.5 pt-1" : "border-t border-border px-3.5 py-2.5",
					)}
				>
					<div className="flex min-w-0 flex-1">
						{statusLabel}
					</div>
					<div className="ml-auto flex shrink-0 items-center gap-2 whitespace-nowrap text-2xs text-muted-foreground">
						{metrics}
					</div>
					{pullRequestProgressLabel ? (
						<div
							className="col-span-2 min-w-0 truncate text-2xs text-muted-foreground"
							data-testid="session-pr-progress"
							title={pullRequestProgressLabel}
						>
							{pullRequestProgressLabel}
						</div>
					) : null}
				</div>
			)}
			{error ? (
				<div className="border-t border-border px-3.5 py-1.5 text-2xs text-destructive" role="alert">
					{error}
				</div>
			) : null}
			{footer}
		</div>
	);
}

function boardSessionNeedsAttention(session: BoardSessionPresentation): boolean {
	if (session.statusReadiness && session.statusReadiness !== "ready") return false;
	if (session.statusPresentation) return false;
	switch (session.displayStatus) {
		case "Blocked":
		case "Needs input":
		case "CI failing":
		case "Merge conflicts":
		case "Changes requested":
			return true;
		case undefined:
			return (
				attentionZone(session.status) === "action" || session.activity?.state === "blocked"
			);
		default:
			return false;
	}
}

export const SessionUsageMetricView = forwardRef<
	HTMLSpanElement,
	{ usage: BoardUsagePresentation } & HTMLAttributes<HTMLSpanElement>
>(({ className, usage, ...props }, ref) => (
	<span
		{...props}
		className={cn(
			"inline-flex shrink-0 items-center gap-1 whitespace-nowrap font-sans text-2xs text-muted-foreground",
			className,
		)}
		ref={ref}
	>
		{/* aria-label on a generic span is not reliably exposed, so the full
		    label is real text placed off-screen and the compact form is hidden
		    from assistive technology rather than read out twice. */}
		<span className="sr-only">{usage.accessibleLabel}</span>
		<span aria-hidden="true">{usage.compactLabel}</span>
	</span>
));
SessionUsageMetricView.displayName = "SessionUsageMetricView";

type BoardPullRequestGroupModel = {
	prs: BoardPullRequestPresentation[];
	state: BoardPullRequestState;
};

function BoardPullRequestGroup({
	externalLink: ExternalLink,
	group,
	labels,
}: {
	externalLink: ExternalLinkComponent;
	group: BoardPullRequestGroupModel;
	labels: BoardPullRequestLabels;
}) {
	const statusLabel = labels.states[group.state];
	const linkClassName = "pr-link hover:underline";
	return (
		// Wraps so a session with several PRs of one state stays inside the card
		// instead of shrinking its gaps away and spilling past the edge.
		<div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
			{group.prs.map((pr) => {
				const hasComments = (pr.commentCount ?? 0) > 0;
				return (
					<Fragment key={pr.url || pr.number}>
						<ExternalLink
							ariaLabel={`PR #${pr.number} ${statusLabel}`}
							// `shrink-0` keeps wrapping the only way a crowded row can resolve:
							// without it the row squeezes entries into each other, and the
							// number below — which has no box of its own to be clipped by —
							// paints straight over its neighbour and past the card's edge.
							// `max-w-full` is the floor for the one case wrapping cannot fix,
							// a single entry wider than the row, which truncates instead.
							className={cn(
								"inline-flex min-w-0 max-w-full shrink-0 items-center gap-x-2 py-0.5",
								linkClassName,
							)}
							href={pr.url}
							stopPropagation
						>
			<PullRequestLifecycleIcon state={group.state} />
			<span className="sr-only">{labels.short}</span>
			<span className="truncate font-sans text-xs font-medium text-foreground">#{pr.number}</span>
			<span className="sr-only">{statusLabel}</span>
			{hasComments ? (
				<div className="-ml-0.5 flex shrink-0 items-center pl-1">
					{(pr.reviewers ?? [])
						.slice(0, 3)
						.map((reviewer, index) => (
							<UserAvatar
								className={cn("size-5 rounded-full border-2 border-surface object-cover ring-1 ring-border", index > 0 && "-ml-1.5")}
								imageUrl={reviewer.avatarUrl}
								key={`${reviewer.id}-${index}`}
								name={reviewer.id}
							/>
						))}
				</div>
			) : null}
						</ExternalLink>
						{hasComments ? (
							<ExternalLink
								ariaLabel={`${pr.commentCount} comments on PR #${pr.number}`}
								className={cn("ml-auto inline-flex shrink-0 items-center gap-1 text-xs tabular-nums text-muted-foreground", linkClassName)}
								href={pr.url}
								stopPropagation
							>
								<MessageSquareIcon aria-hidden="true" className="size-icon-2xs" />
								{pr.commentCount}
							</ExternalLink>
						) : null}
					</Fragment>
				);
			})}
		</div>
	);
}

function PullRequestLifecycleIcon({ state }: { state: BoardPullRequestState }) {
	const className = cn("size-icon-sm shrink-0", lifecycleClassName(state));
	return <GitPullRequestIcon aria-hidden="true" className={className} />;
}

export function groupBoardPullRequests(
	prs: BoardPullRequestPresentation[],
): BoardPullRequestGroupModel[] {
	const groups = new Map<BoardPullRequestState, BoardPullRequestGroupModel>();
	for (const pr of prs) {
		const group = groups.get(pr.state);
		if (group) group.prs.push(pr);
		else groups.set(pr.state, { state: pr.state, prs: [pr] });
	}
	return Array.from(groups.values());
}

function countBoardPullRequests(prs: BoardPullRequestPresentation[]): BoardPullRequestProgress {
	const progress: BoardPullRequestProgress = {
		closed: 0,
		draft: 0,
		merged: 0,
		open: 0,
		total: prs.length,
	};
	for (const pr of prs) progress[pr.state] += 1;
	return progress;
}

function lifecycleClassName(state: BoardPullRequestState): string {
	switch (state) {
		case "draft":
			return "text-passive";
		case "merged":
			return "text-status-merged";
		case "closed":
			return "text-error";
		case "open":
			return "text-success";
	}
}

function sameLabel(a: string, b: string): boolean {
	const normalize = (value: string) =>
		value
			.toLowerCase()
			.replace(/^(feat|fix|chore|refactor|session)\//, "")
			.replace(/[^a-z0-9]+/g, "");
	return normalize(a) === normalize(b);
}
