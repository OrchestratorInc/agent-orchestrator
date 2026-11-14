import { Check, ChevronRight, Eye, FileDiff, GitBranch, GitCommitHorizontal, GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft, MessageSquare, MessageSquareCode, Reply, type LucideIcon } from "lucide-react";
import { Fragment, useMemo, useState, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { buildActivityRail, filterRail, localDay, NO_RAIL_FILTER, railFilterActive, railGroupCounts, type RailCommit, type RailEvent, type RailFilter, type RailKind } from "../lib/activity-rail";
import { sessionUiKey } from "../lib/hosts";
import { openPRTab } from "../lib/pr-tabs";
import { sessionBranchPresentation } from "../lib/session-branch";
import { cn } from "../lib/utils";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../types/workspace";
import { ActivityRailFilter } from "./ActivityRailFilter";
import { PRAvatar } from "./PRComments";
import { Pressable } from "./ui/pressable";

type RailTone = "neutral" | "success" | "merged" | "error";

// Each kind's glyph. Most are neutral; colour is kept
// for what it already means on a PR: green for opened and approved, purple
// for merged, red for closed and changes requested.
const KIND_BADGE: Record<RailKind, { icon: LucideIcon; tone: RailTone }> = {
	created: { icon: GitBranch, tone: "neutral" },
	commits: { icon: GitCommitHorizontal, tone: "neutral" },
	opened: { icon: GitPullRequest, tone: "success" },
	draft: { icon: GitPullRequestDraft, tone: "neutral" },
	merged: { icon: GitMerge, tone: "merged" },
	closed: { icon: GitPullRequestClosed, tone: "error" },
	comment: { icon: MessageSquare, tone: "neutral" },
	reviewNotes: { icon: MessageSquareCode, tone: "neutral" },
	reply: { icon: Reply, tone: "neutral" },
	approved: { icon: Check, tone: "success" },
	changes: { icon: FileDiff, tone: "error" },
	reviewed: { icon: Eye, tone: "neutral" },
};


const GLYPH_CLASS: Record<RailTone, string> = {
	neutral: "text-muted-foreground",
	success: "text-success",
	merged: "text-status-merged",
	error: "text-error",
};

// A row shows its author's face when the author is a provider login. PR
// events always carry one. A commit's author is a git name, which may be
// nobody's login or someone else's, so it gets a face only when it matches
// someone already on this session's PRs.
const hasFace = (event: RailEvent, logins: ReadonlySet<string>) => {
	if (!event.author || event.kind === "created" || event.kind === "merged" || event.kind === "closed") return false;
	return event.kind === "commits" ? logins.has(event.author) : true;
};

/** Everyone the session's PRs name by login: authors, commenters and reviewers. */
function prLogins(prs: SessionPRSummary[]): Set<string> {
	const logins = new Set<string>();
	for (const pr of prs) {
		if (pr.author) logins.add(pr.author);
		for (const comment of pr.conversation ?? []) logins.add(comment.author);
		for (const thread of pr.threads ?? []) for (const comment of thread.comments) logins.add(comment.author);
		for (const review of pr.review.reviews ?? []) logins.add(review.reviewerId);
	}
	return logins;
}

// What people said on a PR: these rows open the PR at its comments.
const isTalk = (kind: RailKind) => kind === "comment" || kind === "reviewNotes" || kind === "reply" || kind === "approved" || kind === "changes" || kind === "reviewed";

// Each session's filter, for as long as the app is open.
const rememberedFilter = new Map<string, RailFilter>();

// Folded commits listed under their row, beyond which the Files tab is the place to look.
const COMMIT_LIST_LIMIT = 10;

/**
 * The session's activity as a rail, newest first, under the live status: a
 * badge for each thing that happened and who did it, under day dividers. What
 * people said on the PR is on it too: comments, review notes and replies,
 * approvals and change requests. A row about a PR opens its tab, at the
 * comments when the row is about a comment; a row of commits opens to list them.
 */
export function ActivityRail({ commits, hostId, prs, session, status, statusAt }: {
	commits: RailCommit[];
	hostId?: string;
	prs: SessionPRSummary[];
	session: WorkspaceSession;
	/** The live state: the agent's activity and the PR signals that need a look. */
	status: ReactNode;
	statusAt?: string | null;
}) {
	const { t, i18n } = useTranslation();
	// A provisional branch stays private until its worktree and name are final.
	const branch = sessionBranchPresentation(session).branch;
	const allEvents = useMemo(
		() => buildActivityRail({ createdAt: session.createdAt ?? session.updatedAt, branch, commits, prs }),
		[branch, commits, prs, session.createdAt, session.updatedAt],
	);
	const sessionKey = sessionUiKey(session.id, hostId);
	// The filter is this session's: it stays while the app is open, so leaving
	// the session and coming back keeps what was hidden.
	const [filterState, setFilterState] = useState(() => ({ key: sessionKey, filter: rememberedFilter.get(sessionKey) ?? NO_RAIL_FILTER }));
	if (filterState.key !== sessionKey) setFilterState({ key: sessionKey, filter: rememberedFilter.get(sessionKey) ?? NO_RAIL_FILTER });
	const filter = filterState.filter;
	const setFilter = (next: RailFilter) => {
		rememberedFilter.set(sessionKey, next);
		setFilterState({ key: sessionKey, filter: next });
	};
	const counts = useMemo(() => railGroupCounts(allEvents), [allEvents]);
	const events = useMemo(() => filterRail(allEvents, filter), [allEvents, filter]);
	const logins = useMemo(() => prLogins(prs), [prs]);
	const standalone = session.workspaceId === STANDALONE_WORKSPACE_ID;
	const timeFormat = useMemo(() => new Intl.DateTimeFormat(i18n.language, { hour: "numeric", minute: "2-digit" }), [i18n.language]);
	const dayFormat = useMemo(() => new Intl.DateTimeFormat(i18n.language, { day: "numeric", month: "short" }), [i18n.language]);
	const today = localDay(new Date().toISOString());
	const yesterday = localDay(new Date(Date.now() - 86_400_000).toISOString());
	const dayLabel = (iso: string) => {
		const day = localDay(iso);
		return day === today ? t("inspector.rail.today") : day === yesterday ? t("inspector.rail.yesterday") : dayFormat.format(new Date(iso));
	};

	const days: { day: string; label: string; events: RailEvent[] }[] = [];
	for (const event of events) {
		const day = localDay(event.at);
		if (days[days.length - 1]?.day !== day) days.push({ day, label: dayLabel(event.at), events: [] });
		days[days.length - 1]!.events.push(event);
	}
	const statusTime = statusAt ? (localDay(statusAt) === today ? timeFormat.format(new Date(statusAt)) : dayLabel(statusAt)) : null;

	return (
		<div className="flex min-w-0 flex-col" data-testid="activity-rail">
			<div className="flex min-w-0 items-center gap-2 text-xs">
				<span className="min-w-0 flex-1">{status}</span>
				{statusTime ? <span className="shrink-0 text-passive tabular-nums">{statusTime}</span> : null}
				{allEvents.length > 0 ? <ActivityRailFilter counts={counts} filter={filter} onChange={setFilter} /> : null}
			</div>
			{events.length === 0 && railFilterActive(filter) ? (
				<p className="mt-4 text-xs text-passive" data-testid="activity-rail-empty">
					{t("inspector.rail.filterEmpty")}{" "}
					<button className="font-medium text-muted-foreground underline underline-offset-2 transition-colors hover:text-foreground focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" onClick={() => setFilter(NO_RAIL_FILTER)} type="button">
						{t("inspector.rail.filterShowAll")}
					</button>
				</p>
			) : null}
			{days.map((group) => (
				<Fragment key={group.day}>
					<div className="mt-4 mb-1 flex items-center gap-2 text-xs text-passive">
						{group.label}
						<span aria-hidden="true" className="h-px flex-1 bg-border/60" />
					</div>
					<ol className="m-0 flex list-none flex-col p-0">
						{group.events.map((event, index) => (
							<RailRow
								event={event}
								key={event.key}
								face={hasFace(event, logins)}
								last={index === group.events.length - 1}
								onOpen={event.pr && !session.cloud ? () => openPRTab(sessionKey, event.pr!.url, isTalk(event.kind) ? "comments" : "summary") : undefined}
								standalone={standalone}
								time={timeFormat.format(new Date(event.at))}
							/>
						))}
					</ol>
				</Fragment>
			))}
		</div>
	);
}

function RailRow({ event, face, last, onOpen, standalone, time }: { event: RailEvent; face: boolean; last: boolean; onOpen?: () => void; standalone: boolean; time: string }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const { icon: Icon, tone } = KIND_BADGE[event.kind];
	const listable = event.kind === "commits" && (event.commits?.length ?? 0) > 1;
	const title = railTitle(event, t(standalone ? "inspector.timeline.createdSession" : "inspector.timeline.createdWorkspace"));
	// Three weights in one stream. What people wrote is loud: their face is the
	// node and their words are readable. A PR milestone keeps its coloured glyph.
	// Machine rows (commits, the session's creation) are quiet one-liners. Each
	// row has one thing on the line, and the line stops short of it.
	const tier = isTalk(event.kind) && event.kind !== "approved" && event.kind !== "changes" ? "talk" : event.kind === "commits" || event.kind === "created" ? "quiet" : "state";
	const faceNode = tier === "talk" && face;
	// Without a face a commit still has to say who, so it keeps its sentence.
	const single = face && event.kind === "commits" && !listable ? event.commits?.[0] : undefined;
	const content = (
		<>
			<span className="relative flex justify-center pt-0.5" data-tier={tier}>
				{faceNode
					? <span className="mt-px inline-flex" data-tone={tone}><PRAvatar bot={event.bot} name={event.author!} size="sm" /></span>
					: <Icon aria-hidden="true" className={cn(tier === "quiet" ? "mt-px size-3.5 text-passive" : cn("size-4", GLYPH_CLASS[tone]))} data-tone={tone} strokeWidth={2} />}
				{last ? null : <span aria-hidden="true" className={cn("absolute w-px bg-border", tier === "quiet" ? "top-[22px] -bottom-1" : "top-[26px] -bottom-1.5")} />}
			</span>
			<span className="flex min-w-0 flex-col gap-0.5">
				<span className={cn("flex min-w-0 items-center gap-1.5", tier === "quiet" ? "text-xs text-passive" : "text-sm text-muted-foreground")}>
					{tier === "quiet" && face ? <PRAvatar bot={event.bot} name={event.author!} size="sm" /> : null}
					{single ? <span className="truncate font-mono"><span className="sr-only">{event.author}: </span>{single.subject}</span> : <span className="truncate">{title}</span>}
					{listable ? <ChevronRight aria-hidden="true" className={cn("size-3 shrink-0 text-passive transition-transform duration-150", open && "rotate-90")} /> : null}
				</span>
				{listable && open ? (
					<span className="flex flex-col gap-1 pt-0.5 font-mono text-xs text-passive">
						{event.commits!.slice(0, COMMIT_LIST_LIMIT).map((commit) => <span className="truncate" key={commit.sha}>{commit.subject}</span>)}
					</span>
				) : !single && (event.detail || event.resolved) ? (
					<span className={cn("flex min-w-0 text-xs", tier === "talk" ? "text-muted-foreground" : "text-passive")}>
						{/* A long excerpt gives way; the resolved note stays in view. */}
						{event.detail ? <span className={cn("truncate", event.kind === "commits" && "font-mono")}>{event.kind === "commits" ? event.detail.slice(8) : event.detail}</span> : null}
						{event.resolved ? <span className="shrink-0 whitespace-pre text-passive">{event.detail ? " · " : ""}<Check aria-hidden="true" className="inline size-3 align-[-2px]" /> {t("inspector.rail.resolved")}</span> : null}
					</span>
				) : null}
			</span>
			<span className="shrink-0 text-xs text-passive tabular-nums">{time}</span>
		</>
	);
	const action = onOpen ?? (listable ? () => setOpen((value) => !value) : undefined);
	// Quiet rows are denser, but every row stays a 32px target.
	const rowClass = cn("grid min-h-8 w-full grid-cols-[20px_minmax(0,1fr)_auto] gap-x-3 rounded-md pr-1", tier === "quiet" ? "py-1.5" : "py-2");
	return (
		<li className="min-w-0">
			{action ? (
				<Pressable
					aria-expanded={listable ? open : undefined}
					className={cn(rowClass, "transition-colors hover:bg-muted/40 focus-visible:ring-2 focus-visible:ring-ring/60")}
					onClick={action}
				>
					{content}
				</Pressable>
			) : (
				<div className={rowClass}>{content}</div>
			)}
		</li>
	);
}

function railTitle(event: RailEvent, createdLabel: string): ReactNode {
	const values = { name: event.author ?? "", count: event.count, number: event.pr?.number };
	const bold = { b: <b className="font-medium text-foreground" /> };
	switch (event.kind) {
		case "created":
			return createdLabel;
		case "commits":
			return event.author
				? <Trans components={bold} i18nKey="inspector.rail.pushed" values={values} />
				: <Trans components={bold} i18nKey="inspector.rail.commits" values={values} />;
		case "opened":
			return <Trans components={bold} i18nKey="inspector.rail.opened" values={values} />;
		case "draft":
			return <Trans components={bold} i18nKey="inspector.rail.draft" values={values} />;
		case "merged":
			return <Trans components={bold} i18nKey="inspector.rail.merged" values={values} />;
		case "closed":
			return <Trans components={bold} i18nKey="inspector.rail.closed" values={values} />;
		case "comment":
			return <Trans components={bold} i18nKey="inspector.rail.commented" values={values} />;
		case "reviewNotes":
			return <Trans components={bold} i18nKey="inspector.rail.reviewNotes" values={values} />;
		case "reply":
			return <Trans components={bold} i18nKey="inspector.rail.replied" values={values} />;
		case "approved":
			return <Trans components={bold} i18nKey="inspector.rail.approved" values={values} />;
		case "changes":
			return <Trans components={bold} i18nKey="inspector.rail.changesRequested" values={values} />;
		case "reviewed":
			return <Trans components={bold} i18nKey="inspector.rail.reviewed" values={values} />;
	}
}
