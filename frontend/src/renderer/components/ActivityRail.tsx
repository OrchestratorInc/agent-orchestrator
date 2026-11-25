import { Bot, Check, ChevronDown, ChevronRight, CornerDownRight, Eye, FileDiff, GitBranch, GitCommitHorizontal, GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft, MessageCircleQuestion, MessageSquare, MessageSquareCode, Reply, Send, ShieldQuestion, Zap, type LucideIcon } from "lucide-react";
import { Fragment, useContext, useMemo, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { InspectorSectionActionSlot } from "@aoagents/product-ui";
import { Trans, useTranslation } from "react-i18next";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { filterRail, foldRail, localDay, railEvents, railReviews, railWorkItems, NO_RAIL_FILTER, railFilterActive, railGroupCounts, type RailCommit, type RailEvent, type RailFilter, type RailKind, type RailSender, type RailTurn, type RailWork, type RailWorkItem } from "../lib/activity-rail";
import { sessionUiKey } from "../lib/hosts";
import { openPRTab } from "../lib/pr-tabs";
import { sessionBranchPresentation } from "../lib/session-branch";
import { cn } from "../lib/utils";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../types/workspace";
import { ActivityRailFilter } from "./ActivityRailFilter";
import { PRAvatar } from "./PRComments";
import { Pressable } from "./ui/pressable";

type RailTone = "neutral" | "success" | "merged";

// Each kind's glyph. Most are neutral; colour is kept for what it already
// means on a PR: green for opened and approved, purple for merged. Red is
// kept for one thing, a request whose turn failed, so a closed PR and a change
// request stay neutral: the card above says where the PR stands.
const KIND_BADGE: Record<RailKind, { icon: LucideIcon; tone: RailTone }> = {
	created: { icon: GitBranch, tone: "neutral" },
	asked: { icon: CornerDownRight, tone: "neutral" },
	approval: { icon: ShieldQuestion, tone: "neutral" },
	question: { icon: MessageCircleQuestion, tone: "neutral" },
	commits: { icon: GitCommitHorizontal, tone: "neutral" },
	opened: { icon: GitPullRequest, tone: "success" },
	draft: { icon: GitPullRequestDraft, tone: "neutral" },
	merged: { icon: GitMerge, tone: "merged" },
	closed: { icon: GitPullRequestClosed, tone: "neutral" },
	comment: { icon: MessageSquare, tone: "neutral" },
	reviewNotes: { icon: MessageSquareCode, tone: "neutral" },
	reply: { icon: Reply, tone: "neutral" },
	approved: { icon: Check, tone: "success" },
	changes: { icon: FileDiff, tone: "neutral" },
	reviewed: { icon: Eye, tone: "neutral" },
};


const GLYPH_CLASS: Record<RailTone, string> = {
	neutral: "text-muted-foreground",
	success: "text-success",
	merged: "text-status-merged",
};

// A request's glyph says who sent it: the person, Orchestrator, or an automation.
const SENDER_ICON: Record<RailSender, LucideIcon> = { you: CornerDownRight, orchestrator: Send, automation: Zap };

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
		for (const review of railReviews(pr)) logins.add(review.reviewerId);
	}
	return logins;
}

// What people said on a PR: these rows open the PR at its comments.
const isTalk = (kind: RailKind) => kind === "comment" || kind === "reviewNotes" || kind === "reply" || kind === "approved" || kind === "changes" || kind === "reviewed";

// Each session's filter, for as long as the app is open.
const rememberedFilter = new Map<string, RailFilter>();

// Folded commits listed under their row, beyond which the Files tab is the place to look.
const COMMIT_LIST_LIMIT = 10;

// Days of activity shown before the rest fold behind a button.
const RECENT_DAYS = 2;

// Lines an open request row shows before the rest wait behind "Show N more".
const WORK_LIST_PREVIEW = 3;

/**
 * The session's activity as a rail, newest first: what the agent is doing now
 * as the first row, then a badge for each thing that happened and who did it,
 * under day dividers. What
 * people said on the PR is on it too: comments, review notes and replies,
 * approvals and change requests. A row about a PR opens its tab, at the
 * comments when the row is about a comment; a row of commits opens to list them.
 */
export function ActivityRail({ commits, hostId, prs, session, status, statusAt, statusDot, work }: {
	commits: RailCommit[];
	hostId?: string;
	prs: SessionPRSummary[];
	session: WorkspaceSession;
	/** What the agent is doing now, in words: the rail's first row. */
	status: ReactNode;
	statusAt?: string | null;
	/** The mark in that row's icon column. */
	statusDot?: ReactNode;
	/** What the agent was asked and did, when the app has the session's history. */
	work?: RailWork;
}) {
	const { t, i18n } = useTranslation();
	// A provisional branch stays private until its worktree and name are final.
	const branch = sessionBranchPresentation(session).branch;
	const allEvents = useMemo(
		() => railEvents({ createdAt: session.createdAt ?? session.updatedAt, branch, commits, prs, work }),
		[branch, commits, prs, session.createdAt, session.updatedAt, work],
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
	// The newest approval or question still waiting on you is what the "now" row
	// is waiting for, so it sits there instead of in a row of its own.
	const waitingAsk = useMemo(() => allEvents.find((event) => (event.kind === "approval" || event.kind === "question") && event.waiting && event.detail), [allEvents]);
	// What the filter leaves folds afterwards, so a commit whose request is hidden shows as a commit.
	const events = useMemo(() => foldRail(filterRail(allEvents, filter)).filter((event) => event.key !== waitingAsk?.key), [allEvents, filter, waitingAsk]);
	const [olderState, setOlderState] = useState({ key: sessionKey, shown: false });
	if (olderState.key !== sessionKey) setOlderState({ key: sessionKey, shown: false });
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
	// The "now" row belongs to today, even when nothing else happened today.
	const quietToday = days[0]?.day !== today;
	if (quietToday) days.unshift({ day: today, label: t("inspector.rail.today"), events: [] });
	// The newest days stay open; older ones fold behind one button, which says how many rows it holds.
	const shownDays = olderState.shown ? days : days.slice(0, RECENT_DAYS + (quietToday ? 1 : 0));
	const hiddenRows = days.slice(shownDays.length).reduce((sum, group) => sum + group.events.length, 0);
	const formatTime = (iso: string) => timeFormat.format(new Date(iso));
	const statusTime = statusAt ? (localDay(statusAt) === today ? timeFormat.format(new Date(statusAt)) : dayLabel(statusAt)) : null;

	// The filter sits beside the section's title, as a control over the whole list.
	const actionSlot = useContext(InspectorSectionActionSlot);
	const emptyNote = events.length === 0 && railFilterActive(filter) ? (
		<p className="mt-2 text-xs text-passive" data-testid="activity-rail-empty">
			{t("inspector.rail.filterEmpty")}{" "}
			<button className="font-medium text-muted-foreground underline underline-offset-2 transition-colors hover:text-foreground focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" onClick={() => setFilter(NO_RAIL_FILTER)} type="button">
				{t("inspector.rail.filterShowAll")}
			</button>
		</p>
	) : null;

	return (
		<div className="flex min-w-0 flex-col" data-testid="activity-rail">
			{actionSlot && allEvents.length > 0 ? createPortal(<ActivityRailFilter counts={counts} filter={filter} onChange={setFilter} />, actionSlot) : null}
			{shownDays.map((group, groupIndex) => (
				<Fragment key={group.day}>
					<div className={cn("mb-1 flex items-center gap-2 text-xs text-passive", groupIndex === 0 ? "mt-1" : "mt-4")}>
						{group.label}
						<span aria-hidden="true" className="h-px flex-1 bg-border/60" />
					</div>
					{groupIndex === 0 ? <NowRow dot={statusDot} last={group.events.length === 0} status={status} time={statusTime} waiting={waitingAsk} /> : null}
					<ol className="m-0 flex list-none flex-col p-0">
						{group.events.map((event, index) => (
							<RailRow
								event={event}
								key={event.key}
								face={hasFace(event, logins)}
								last={index === group.events.length - 1}
								onOpen={event.pr && !session.cloud ? () => openPRTab(sessionKey, event.pr!.url, isTalk(event.kind) ? "comments" : "summary") : undefined}
								standalone={standalone}
								time={formatTime(event.at)}
							/>
						))}
					</ol>
					{groupIndex === 0 ? emptyNote : null}
				</Fragment>
			))}
			{hiddenRows > 0 ? (
				<button
					className="mt-3 flex h-8 w-full items-center justify-center gap-1.5 rounded-md text-xs text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					data-testid="activity-rail-earlier"
					onClick={() => setOlderState({ key: sessionKey, shown: true })}
					type="button"
				>
					<ChevronDown aria-hidden="true" className="size-3.5" />
					{t("inspector.rail.showEarlier", { count: hiddenRows })}
				</button>
			) : null}
		</div>
	);
}

/**
 * What the agent is doing now, as the rail's first row: a dot in the icon
 * column like every other row's glyph, the state in plain words, and, when it
 * waits on you, what for. Not an event, so it is not one of the list's items.
 */
function NowRow({ dot, last, status, time, waiting }: { dot?: ReactNode; last: boolean; status: ReactNode; time: string | null; waiting?: RailEvent }) {
	const { t } = useTranslation();
	return (
		<div className="grid min-h-8 w-full grid-cols-[20px_minmax(0,1fr)_auto] gap-x-3 pr-1 py-2" data-testid="activity-rail-now">
			<span className="relative flex justify-center pt-[7px]">
				{dot}
				{last ? null : <span aria-hidden="true" className="absolute top-[20px] -bottom-1.5 w-px bg-border" />}
			</span>
			<span className="flex min-w-0 flex-col gap-0.5">
				<span className="truncate text-sm text-foreground">{status}</span>
				{waiting ? (
					<span className="truncate text-xs text-muted-foreground" data-testid="activity-rail-waiting" title={waiting.detail}>
						<span className="sr-only">{t(waiting.kind === "approval" ? "inspector.rail.approvalWaiting" : "inspector.rail.questionWaiting")}: </span>
						{waiting.detail}
					</span>
				) : null}
			</span>
			{time ? <span className="shrink-0 text-xs text-passive tabular-nums">{time}</span> : null}
		</div>
	);
}

function RailRow({ event, face, last, onOpen, standalone, time }: { event: RailEvent; face: boolean; last: boolean; onOpen?: () => void; standalone: boolean; time: string }) {
	const { t, i18n } = useTranslation();
	const [open, setOpen] = useState(false);
	const badge = KIND_BADGE[event.kind];
	const Icon = event.kind === "asked" && event.sender ? SENDER_ICON[event.sender] : badge.icon;
	// Waiting on you is news, not an alarm: the status above already says input is needed.
	const tone: RailTone = badge.tone;
	const commitList = event.kind === "commits" && (event.commits?.length ?? 0) > 1;
	// A request opens to list the requests folded into it and the commits made while the agent worked.
	const workItems = useMemo(() => railWorkItems(event), [event]);
	const workList = workItems.length > 0;
	const listable = commitList || workList;
	// Your own request leads with what you wrote; its icon already says who asked.
	const mine = event.kind === "asked" && event.sender === "you";
	const title = mine
		? <span className="text-foreground"><span className="sr-only">{t("inspector.rail.asked.you")}: </span>{event.detail}</span>
		: railTitle(event, t(standalone ? "inspector.timeline.createdSession" : "inspector.timeline.createdWorkspace"));
	// Three weights in one stream. What people wrote is loud: their face is the
	// node and their words are readable. A PR milestone keeps its coloured glyph.
	// Machine rows (commits, the session's creation) are quiet one-liners. Each
	// row has one thing on the line, and the line stops short of it.
	// A request to the agent reads like something said, so it is loud too.
	const tier = event.kind === "asked" || (isTalk(event.kind) && event.kind !== "approved" && event.kind !== "changes") ? "talk" : event.kind === "commits" || event.kind === "created" ? "quiet" : "state";
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
				{commitList && open ? (
					<span className="flex flex-col gap-1 pt-0.5 font-mono text-xs text-passive">
						{event.commits!.slice(0, COMMIT_LIST_LIMIT).map((commit) => <span className="truncate" key={commit.sha}>{commit.subject}</span>)}
					</span>
				) : !single && !mine && (event.detail || event.resolved) ? (
					<span className={cn("flex min-w-0 text-xs", tier === "talk" ? "text-muted-foreground" : "text-passive")}>
						{/* A long excerpt gives way; the resolved note stays in view. */}
						{event.detail ? <span className={cn(event.kind === "asked" ? "line-clamp-2 break-words" : "truncate", event.kind === "commits" && "font-mono")}>{event.kind === "commits" ? event.detail.slice(8) : event.detail}</span> : null}
						{event.resolved ? <span className="shrink-0 whitespace-pre text-passive">{event.detail ? " · " : ""}<Check aria-hidden="true" className="inline size-3 align-[-2px]" /> {t("inspector.rail.resolved")}</span> : null}
					</span>
				) : null}
				{event.turn ? <TurnOutcome answered={event.answered} commits={event.commits?.length ?? 0} language={i18n.language} requests={event.requests?.length ?? 1} turn={event.turn} /> : null}
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
			{/* Outside the row's button, so its own "Show more" is a button of its own. */}
			{workList && open ? <WorkList items={workItems} last={last} /> : null}
		</li>
	);
}

// 45s, 8m, 1h 12m: short enough to sit on a row's last line.
function shortDuration(ms: number, language: string): string {
	const unit = (value: number, name: "second" | "minute" | "hour") => new Intl.NumberFormat(language, { style: "unit", unit: name, unitDisplay: "narrow" }).format(value);
	const seconds = Math.max(1, Math.round(ms / 1000));
	if (seconds < 60) return unit(seconds, "second");
	const minutes = Math.round(seconds / 60);
	if (minutes < 60) return unit(minutes, "minute");
	return minutes % 60 ? `${unit(Math.floor(minutes / 60), "hour")} ${unit(minutes % 60, "minute")}` : unit(minutes / 60, "hour");
}

/**
 * How the turn a request started went: still working, how long it worked and
 * what it changed, or why it ended early. A row standing for several requests
 * says how many, and counts what folded into it: commits, approvals, questions
 * and earlier requests that failed. Red is kept for a newest request that
 * failed; an earlier one you followed up on is history.
 */
function TurnOutcome({ answered, commits, language, requests, turn }: { answered?: RailEvent["answered"]; commits: number; language: string; requests: number; turn: RailTurn }) {
	const { t } = useTranslation();
	const duration = turn.ms === undefined ? undefined : shortDuration(turn.ms, language);
	const state = duration && (turn.state === "completed" || turn.state === "stopped") ? t(`inspector.rail.turn.${turn.state}For`, { duration }) : t(`inspector.rail.turn.${turn.state}`);
	const approvals = answered?.filter((ask) => ask.kind === "approval").length ?? 0;
	const questions = answered?.filter((ask) => ask.kind === "question").length ?? 0;
	const parts: string[] = [];
	if (requests > 1) parts.push(t("inspector.rail.turn.requests", { count: requests }));
	// An earlier failure comes before the line can run out of room.
	if (turn.failures) parts.push(t("inspector.rail.turn.failures", { count: turn.failures }));
	parts.push(state);
	if (turn.files) parts.push(t("inspector.rail.turn.files", { count: turn.files }));
	if (commits) parts.push(t("inspector.rail.turn.commits", { count: commits }));
	if (approvals) parts.push(t("inspector.rail.turn.approvals", { count: approvals }));
	if (questions) parts.push(t("inspector.rail.turn.questions", { count: questions }));
	if (turn.error) parts.push(turn.error);
	return (
		<span className={cn("flex min-w-0 items-center gap-1.5 pt-0.5 text-xs", turn.state === "failed" ? "text-error" : "text-passive")} data-testid="rail-turn-outcome" data-turn-state={turn.state}>
			<Bot aria-hidden="true" className="size-3.5 shrink-0" />
			<span className="truncate">{parts.join(" · ")}</span>
		</span>
	);
}

/**
 * An open request row's list: its requests, newest first, then the commits
 * made while the agent worked. The first few show; the rest wait behind one
 * button. Commits read in code type, so no line needs a mark in front.
 */
function WorkList({ items, last }: { items: RailWorkItem[]; last: boolean }) {
	const { t } = useTranslation();
	const [all, setAll] = useState(false);
	// Commits come last; past the first few, the Files tab is the place to look.
	const firstCommit = items.findIndex((item) => item.kind === "commit");
	const listed = firstCommit < 0 ? items : items.slice(0, firstCommit + COMMIT_LIST_LIMIT);
	// A button that would hide a single line costs as much room as the line, so that line just shows.
	const shown = all || listed.length <= WORK_LIST_PREVIEW + 1 ? listed : listed.slice(0, WORK_LIST_PREVIEW);
	const hidden = listed.length - shown.length;
	return (
		<div className="grid w-full grid-cols-[20px_minmax(0,1fr)_auto] gap-x-3 pr-1 pb-2">
			<span aria-hidden="true" className="relative flex justify-center">{last ? null : <span className="absolute inset-y-0 w-px bg-border" />}</span>
			<ul className="m-0 flex min-w-0 list-none flex-col gap-1 border-l border-border/70 p-0 pl-2.5 text-xs" data-testid="rail-work-list">
				{shown.map((item) => (
					<li className="flex min-w-0 gap-2" data-kind={item.kind} key={item.key}>
						<span className={cn("min-w-0 truncate", item.kind === "commit" ? "font-mono text-passive" : "text-muted-foreground")}>{item.text}</span>
						{item.failed ? <span className="shrink-0 text-passive">{t("inspector.rail.turn.failed")}</span> : null}
					</li>
				))}
				{hidden > 0 ? (
					<li>
						<button
							className="rounded-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
							data-testid="rail-work-more"
							onClick={() => setAll(true)}
							type="button"
						>
							{t("inspector.rail.showMore", { count: hidden })}
						</button>
					</li>
				) : null}
			</ul>
		</div>
	);
}

function railTitle(event: RailEvent, createdLabel: string): ReactNode {
	const values = { name: event.author ?? "", count: event.count, number: event.pr?.number };
	const bold = { b: <b className="font-medium text-foreground" /> };
	switch (event.kind) {
		case "created":
			return createdLabel;
		case "asked":
			return <Trans components={bold} i18nKey={`inspector.rail.asked.${event.sender ?? "you"}`} />;
		case "approval":
			return event.waiting ? <span className="text-foreground"><Trans i18nKey="inspector.rail.approvalWaiting" /></span> : <Trans components={bold} i18nKey="inspector.rail.approval" />;
		case "question":
			return event.waiting ? <span className="text-foreground"><Trans i18nKey="inspector.rail.questionWaiting" /></span> : <Trans components={bold} i18nKey="inspector.rail.question" />;
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
