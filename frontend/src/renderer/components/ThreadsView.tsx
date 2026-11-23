import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { BellOff, Loader2, MessagesSquare } from "@/components/app-icons";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCanPromptInOrg, useCommentThreadList } from "../hooks/useCommentThreads";
import { useWindowAttention } from "../hooks/useWindowAttention";
import { closeThreadInView, cloudCommentThreadsRootKey, openThreadInView, useThreadInView, type ThreadInView } from "../lib/cloud-asides";
import type { CloudCpCommentThread, CloudCpSession } from "../lib/cloud-cp/types";
import { formatTimeCompact } from "../lib/format-time";
import { cn } from "../lib/utils";
import { useOpenThreadInSession } from "./CloudMentionRuntime";
import { CloudAsidePane } from "./chat/AsidePane";
import { PageTopbar, PageTopbarTitle } from "./PageTopbar";
import { PersonAvatar } from "./teams/TeamVisuals";
import { Button } from "./ui/button";
import { Pressable } from "./ui/pressable";

// The Threads page: every comment thread the person is in (they started it,
// replied, or were mentioned), across their organizations and sessions, as
// Slack's Threads view lists conversations. Open and Resolved tabs, an
// Unread only filter, unread threads first. A row opens the thread beside the
// list in the same pane the session uses, so it can be answered, resolved,
// muted or sent to the agent without leaving; "Open in session" goes to its
// message.

type ThreadStatus = "open" | "resolved";

export function ThreadsView() {
	const { t } = useTranslation();
	const [status, setStatus] = useState<ThreadStatus>("open");
	const [unreadOnly, setUnreadOnly] = useState(false);
	const list = useCommentThreadList(status, unreadOnly);
	const selected = useThreadInView();
	// Leaving the page leaves no thread selected for the next visit.
	useEffect(() => () => closeThreadInView(), []);
	const statuses: ReadonlyArray<{ value: ThreadStatus; label: string }> = [
		{ value: "open", label: t("threads.tab.open") },
		{ value: "resolved", label: t("threads.tab.resolved") },
	];
	const empty = unreadOnly ? t("threads.empty.unread") : status === "open" ? t("threads.empty.open") : t("threads.empty.resolved");
	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background" data-testid="threads-view">
			<PageTopbar lead={<PageTopbarTitle as="h1" icon={<MessagesSquare className="size-icon-md" aria-hidden="true" />}>{t("threads.title")}</PageTopbarTitle>} />
			<div className="flex min-h-0 flex-1">
				<section aria-label={t("threads.list")} className="flex min-h-0 min-w-0 flex-1 flex-col">
					<div className="flex min-h-control-md shrink-0 items-center gap-1 pt-3 pr-4 pl-(--space-4_5)">
						<div aria-label={t("threads.tabs")} className="flex items-center gap-1" role="tablist">
							{statuses.map((option) => (
								<Button
									aria-selected={status === option.value}
									data-active={status === option.value}
									key={option.value}
									onClick={() => setStatus(option.value)}
									role="tab"
									size="sm"
									type="button"
									variant="ghost"
								>
									{option.label}
								</Button>
							))}
						</div>
						<Button
							aria-pressed={unreadOnly}
							className="ms-auto"
							data-threads-unread-only=""
							onClick={() => setUnreadOnly((value) => !value)}
							size="sm"
							type="button"
							variant="ghost"
						>
							{t("threads.unreadOnly")}
						</Button>
					</div>
					<div className="min-h-0 flex-1 overflow-y-auto px-2 pt-2 pb-4">
						{list.isPending ? (
							<p className="flex items-center gap-2 px-3 py-6 text-xs text-muted-foreground" role="status">
								<Loader2 aria-hidden="true" className="size-icon-sm animate-spin" />
								{t("threads.loading")}
							</p>
						) : list.isError && list.rows.length === 0 ? (
							<div className="flex flex-col items-start gap-2 px-3 py-6">
								<p className="text-xs text-muted-foreground" role="alert">{t("threads.loadFailed")}</p>
								<Button onClick={() => void list.refetch()} size="sm" type="button" variant="secondary">{t("threads.retry")}</Button>
							</div>
						) : list.rows.length === 0 ? (
							<p className="mx-auto mt-16 max-w-72 text-center text-xs text-muted-foreground" data-threads-empty="">{empty}</p>
						) : (
							<ul className="flex flex-col gap-0.5" role="list">
								{list.rows.map((row) => (
									<li key={row.aside.asideId}>
										<ThreadRow
											onOpen={() => openThreadInView(threadTarget(row))}
											row={row}
											selected={selected?.asideId === row.aside.asideId}
										/>
									</li>
								))}
								{list.hasNextPage ? (
									<li className="flex justify-center pt-2">
										<Button data-threads-load-more="" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()} size="sm" type="button" variant="quiet">
											{list.isFetchingNextPage ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : null}
											{t("threads.loadMore")}
										</Button>
									</li>
								) : null}
							</ul>
						)}
					</div>
				</section>
				{selected ? <ThreadPane key={selected.asideId} thread={selected} /> : null}
			</div>
		</div>
	);
}

/** What the threads view needs to show a row's thread beside the list. */
function threadTarget(row: CloudCpCommentThread): ThreadInView {
	return {
		orgId: row.orgId,
		sessionId: row.sessionId,
		asideId: row.aside.asideId,
		projectId: row.projectId,
		sessionTitle: row.sessionTitle,
		ended: row.sessionTerminated,
	};
}

/**
 * One thread: where it is (session and project), what it's about, its newest
 * comment, who is in it and how many comments, and when it last moved. An
 * unread thread has a dot and a bolder title; a muted one says so.
 */
export function ThreadRow({ row, selected, onOpen }: { row: CloudCpCommentThread; selected: boolean; onOpen: () => void }) {
	const { t } = useTranslation();
	const anchor = row.aside.quote
		? `“${row.aside.quote.replace(/\s+/g, " ")}”`
		: row.aside.anchor.kind === "agent_message" ? t("comment.onAgentAnswer") : t("comment.wholeMessage");
	return (
		<Pressable
			aria-current={selected ? "true" : undefined}
			className={cn(
				"group grid w-full grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-0.5 rounded-lg px-3 py-2.5 transition-none hover:bg-interactive-hover focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60",
				selected && "bg-interactive-active hover:bg-interactive-active",
			)}
			data-thread-row={row.aside.asideId}
			data-unread={row.unread || undefined}
			onClick={onOpen}
		>
			<span className="flex min-w-0 items-baseline gap-1.5">
				<span className={cn("truncate text-control text-foreground", row.unread && "font-semibold")}>{row.sessionTitle}</span>
				<span className="shrink-0 truncate text-caption text-muted-foreground">{row.projectName}</span>
			</span>
			<span className="flex items-center justify-end gap-1.5">
				<time className="text-caption tabular-nums text-muted-foreground" dateTime={row.lastActivityAt}>{formatTimeCompact(row.lastActivityAt)}</time>
				{row.unread ? <span aria-label={t("threads.unread")} className="size-2 rounded-full bg-status-working" data-thread-unread="" role="img" /> : null}
			</span>
			<span className="col-span-2 truncate border-l-2 border-[var(--comment-quote-edge)] pl-2 text-caption text-foreground/80" data-thread-anchor="">{anchor}</span>
			{row.lastComment ? (
				<span className="col-span-2 truncate text-control text-muted-foreground" data-thread-last="">
					<span className="font-medium text-foreground/90">{row.lastComment.authorName}</span>
					{": "}
					{row.lastComment.preview}
				</span>
			) : null}
			<span className="col-span-2 mt-1 flex items-center gap-2 text-caption text-muted-foreground">
				<span className="flex -space-x-1" data-thread-participants="">
					{row.participants.slice(0, 3).map((person) => <PersonAvatar className="ring-2 ring-background" key={person.userId} name={person.name} size="sm" />)}
				</span>
				<span className="tabular-nums">{t("comment.count", { count: row.commentCount })}</span>
				{row.sentToAgentAt ? <span className="rounded-full bg-raised px-1.5 py-px text-foreground/80" data-thread-sent="">{t("threads.sentToAgent")}</span> : null}
				{row.muted ? (
					<span className="inline-flex items-center gap-1" data-thread-muted="">
						<BellOff aria-hidden="true" className="size-3" />
						{t("threads.muted")}
					</span>
				) : null}
			</span>
		</Pressable>
	);
}

/**
 * The thread open beside the list: the session's comment pane, with "Open in
 * session" where the session shows "Jump to message". A thread opened from a
 * notification may not be on the tab shown, so what the pane needs (the
 * session's title, its project, whether it ended) comes from the
 * notification, a cached row, or the session itself.
 */
function ThreadPane({ thread }: { thread: ThreadInView }) {
	const { t } = useTranslation();
	const { client, ready, baseUrl, userId } = useCloudCp();
	const queryClient = useQueryClient();
	const attentive = useWindowAttention();
	const canPromptInOrg = useCanPromptInOrg();
	const openInSession = useOpenThreadInSession();
	const cachedRow = useMemo(() => {
		for (const [, data] of queryClient.getQueriesData<{ pages: Array<{ items: CloudCpCommentThread[] }> }>({ queryKey: cloudCommentThreadsRootKey(baseUrl, userId) })) {
			const row = data?.pages.flatMap((page) => page.items).find((item) => item.aside.asideId === thread.asideId);
			if (row) return row;
		}
		return undefined;
	}, [baseUrl, queryClient, thread.asideId, userId]);
	const known = { ...(cachedRow ? threadTarget(cachedRow) : {}), ...definedOf(thread) };
	const session = useQuery<CloudCpSession | undefined>({
		queryKey: ["cloud-thread-session", baseUrl, thread.orgId, thread.sessionId],
		enabled: ready && (known.sessionTitle === undefined || known.projectId === undefined),
		staleTime: 60_000,
		queryFn: async ({ signal }) => (await client.getSession(thread.orgId, thread.sessionId, { signal })).session,
	});
	const sessionTitle = known.sessionTitle ?? session.data?.displayName ?? "";
	const projectId = known.projectId ?? session.data?.projectId;
	const ended = known.ended ?? false;
	const onOpenInSession = useCallback((sequence: number) => {
		if (!projectId) return;
		openInSession({ orgId: thread.orgId, projectId, sessionId: thread.sessionId, asideId: thread.asideId, sequence });
	}, [openInSession, projectId, thread.asideId, thread.orgId, thread.sessionId]);
	return (
		<CloudAsidePane
			asideId={thread.asideId}
			attentive={attentive}
			canPrompt={!ended && canPromptInOrg(thread.orgId)}
			ended={ended}
			focusKey={thread.focusKey}
			onClose={closeThreadInView}
			onOpenInSession={projectId ? onOpenInSession : undefined}
			orgId={thread.orgId}
			sessionId={thread.sessionId}
			sessionTitle={sessionTitle || t("threads.title")}
		/>
	);
}

/** The fields that are set, so they override what a cached row says. */
function definedOf(thread: ThreadInView): Partial<ThreadInView> {
	return Object.fromEntries(Object.entries(thread).filter(([, value]) => value !== undefined)) as Partial<ThreadInView>;
}
