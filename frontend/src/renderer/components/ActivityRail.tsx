import { Check, ChevronRight, Eye, FileDiff, GitBranch, GitCommitHorizontal, GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft, MessageSquare, MessageSquareCode, Reply, type LucideIcon } from "lucide-react";
import { Fragment, useMemo, useState, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { buildActivityRail, localDay, type RailCommit, type RailEvent, type RailKind } from "../lib/activity-rail";
import { sessionUiKey } from "../lib/hosts";
import { openPRTab } from "../lib/pr-tabs";
import { sessionBranchPresentation } from "../lib/session-branch";
import { cn } from "../lib/utils";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSession } from "../types/workspace";

type RailTone = "neutral" | "success" | "merged" | "error";

// Each kind's badge: a glyph on a soft disc. Most are neutral; colour is kept
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

const TONE_CLASS: Record<RailTone, string> = {
	neutral: "bg-muted text-muted-foreground",
	success: "bg-success/15 text-success",
	merged: "bg-status-merged/15 text-status-merged",
	error: "bg-error/15 text-error",
};

// What people said on a PR: these rows open the PR at its comments.
const isTalk = (kind: RailKind) => kind === "comment" || kind === "reviewNotes" || kind === "reply" || kind === "approved" || kind === "changes" || kind === "reviewed";

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
	const events = useMemo(
		() => buildActivityRail({ createdAt: session.createdAt ?? session.updatedAt, branch, commits, prs }),
		[branch, commits, prs, session.createdAt, session.updatedAt],
	);
	const standalone = session.workspaceId === STANDALONE_WORKSPACE_ID;
	const timeFormat = useMemo(() => new Intl.DateTimeFormat(i18n.language, { hour: "numeric", minute: "2-digit" }), [i18n.language]);
	const dayFormat = useMemo(() => new Intl.DateTimeFormat(i18n.language, { day: "numeric", month: "short" }), [i18n.language]);
	const today = localDay(new Date().toISOString());
	const yesterday = localDay(new Date(Date.now() - 86_400_000).toISOString());
	const dayLabel = (iso: string) => {
		const day = localDay(iso);
		return day === today ? t("inspector.rail.today") : day === yesterday ? t("inspector.rail.yesterday") : dayFormat.format(new Date(iso));
	};
	const sessionKey = sessionUiKey(session.id, hostId);

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
			</div>
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
								last={index === group.events.length - 1}
								onOpen={event.pr ? () => openPRTab(sessionKey, event.pr!.url, isTalk(event.kind) ? "comments" : "summary") : undefined}
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

function RailRow({ event, last, onOpen, standalone, time }: { event: RailEvent; last: boolean; onOpen?: () => void; standalone: boolean; time: string }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const { icon: Icon, tone } = KIND_BADGE[event.kind];
	const listable = event.kind === "commits" && (event.commits?.length ?? 0) > 1;
	const title = railTitle(event, t(standalone ? "inspector.timeline.createdSession" : "inspector.timeline.createdWorkspace"));
	const detail = event.kind === "commits" && event.detail ? <><span className="font-mono">{event.detail.slice(0, 7)}</span>{event.detail.slice(7)}</> : event.detail;
	const content = (
		<>
			<span className="relative flex justify-center pt-px">
				<span aria-hidden="true" className={cn("inline-flex size-6 items-center justify-center rounded-full", TONE_CLASS[tone])} data-tone={tone}>
					<Icon className="size-3.5" strokeWidth={2.25} />
				</span>
				{last ? null : <span aria-hidden="true" className="absolute top-7 -bottom-2 w-px bg-border/60" />}
			</span>
			<span className="flex min-w-0 flex-col pt-0.5">
				<span className="flex min-w-0 items-center gap-1 text-sm text-muted-foreground">
					<span className="truncate">{title}</span>
					{listable ? <ChevronRight aria-hidden="true" className={cn("size-3 shrink-0 text-passive transition-transform duration-150", open && "rotate-90")} /> : null}
				</span>
				{listable && open ? (
					<span className="flex flex-col gap-0.5 pt-0.5 text-xs text-passive">
						{event.commits!.slice(0, COMMIT_LIST_LIMIT).map((commit) => (
							<span className="truncate" key={commit.sha}>
								<span className="font-mono">{commit.sha.slice(0, 7)}</span> {commit.subject}
							</span>
						))}
					</span>
				) : event.detail || event.resolved ? (
					<span className="flex min-w-0 text-xs text-passive">
						{/* A long excerpt gives way; the resolved note stays in view. */}
						{event.detail ? <span className="truncate">{detail}</span> : null}
						{event.resolved ? <span className="shrink-0 whitespace-pre">{event.detail ? " · " : ""}<Check aria-hidden="true" className="inline size-3 align-[-2px]" /> {t("inspector.rail.resolved")}</span> : null}
					</span>
				) : null}
			</span>
			<span className="shrink-0 pt-1 text-xs text-passive tabular-nums">{time}</span>
		</>
	);
	const action = onOpen ?? (listable ? () => setOpen((value) => !value) : undefined);
	const rowClass = "grid w-full grid-cols-[24px_minmax(0,1fr)_auto] gap-x-2.5 rounded-md py-1.5 pr-1 text-left";
	return (
		<li className="min-w-0">
			{action ? (
				<button
					aria-expanded={listable ? open : undefined}
					className={cn(rowClass, "transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60")}
					onClick={action}
					type="button"
				>
					{content}
				</button>
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
