import { CircleX, Eye, GitMerge, GitPullRequest, Hand, LoaderCircle, Plus, Users } from "lucide-react";
import { ScrollArea } from "radix-ui";
import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { useMarkFeedSeen, useTeamCollabSupport, useTeamFeed } from "../hooks/useTeamCollab";
import { awayHasNews, groupFeed, type FeedGroup, type FeedGroupId } from "../lib/cloud-team-collab";
import type { CloudCpFeedAway, CloudCpFeedItem, CloudCpFeedScope, CloudCpFeedStatus } from "../lib/cloud-cp/types";
import { formatTimeCompact } from "../lib/format-time";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { PageTopbar, PageTopbarTitle } from "./PageTopbar";
import { PersonAvatar } from "./teams/TeamVisuals";
import { TopbarButton } from "./TopbarButton";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";
import { Pressable } from "./ui/pressable";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { joinNames } from "./chat/TeamComposerBars";

const TABS = [
	{ scope: "team", label: "feed.tab.all" },
	{ scope: "mine", label: "feed.tab.mine" },
	{ scope: "following", label: "feed.tab.following" },
] as const satisfies ReadonlyArray<{ scope: CloudCpFeedScope; label: string }>;

const EMPTY = {
	team: "feed.empty.team",
	mine: "feed.empty.mine",
	following: "feed.empty.following",
} as const satisfies Record<CloudCpFeedScope, string>;

/** Others shown in a row; the rest are in its tooltip. */
const ROW_PEOPLE = 3;

/**
 * What the team's agents are doing, across projects: All, Mine and
 * Following; what changed since your last visit; then the sessions that need
 * someone, the ones working now, and the rest. A row opens its session.
 */
export function TeamFeedView() {
	const { t, i18n } = useTranslation();
	const support = useTeamCollabSupport();
	const { userId, signedOut } = useCloudCp();
	const [scope, setScope] = useState<CloudCpFeedScope>("team");
	const feed = useTeamFeed(scope, support !== "unsupported");
	const markSeen = useMarkFeedSeen();
	const navigateToSession = useNavigateToSession();
	const openBell = useUiStore((state) => state.requestNotificationCenterOpen);
	const listTop = useRef<HTMLDivElement>(null);
	const tabsId = useId();
	const tabRefs = useRef<Partial<Record<CloudCpFeedScope, HTMLButtonElement | null>>>({});

	const pages = feed.data?.pages ?? [];
	const items = pages.flatMap((page) => page.items);
	const away = pages[0]?.away;
	const groups = groupFeed(items);

	// Leaving the page is the visit, as of the last read someone could see: the
	// next "While you were away" counts from there. Nothing is recorded when the
	// last read failed, or when the window stayed hidden the whole time.
	const seen = useRef<{ at?: number; failed: boolean; mark: (seenAt?: string) => void }>({ failed: false, mark: markSeen.mutate });
	seen.current.mark = markSeen.mutate;
	seen.current.failed = feed.lastReadFailed;
	const readAt = feed.isSuccess ? feed.readAt : 0;
	useEffect(() => {
		const note = () => {
			if (readAt > 0 && document.visibilityState === "visible") seen.current.at = readAt;
		};
		note();
		document.addEventListener("visibilitychange", note);
		return () => document.removeEventListener("visibilitychange", note);
	}, [readAt]);
	useEffect(() => () => {
		const { at, failed, mark } = seen.current;
		if (at && !failed) mark(new Date(at).toISOString());
	}, []);

	const openSession = (projectId: string | undefined, sessionId: string) => navigateToSession(projectId, sessionId);
	const moveTab = (event: KeyboardEvent<HTMLDivElement>) => {
		const at = TABS.findIndex((tab) => tab.scope === scope);
		const next = event.key === "ArrowRight" ? (at + 1) % TABS.length
			: event.key === "ArrowLeft" ? (at - 1 + TABS.length) % TABS.length
			: event.key === "Home" ? 0
			: event.key === "End" ? TABS.length - 1
			: -1;
		if (next < 0) return;
		event.preventDefault();
		setScope(TABS[next]!.scope);
		tabRefs.current[TABS[next]!.scope]?.focus();
	};

	let body: ReactNode;
	if (signedOut) {
		body = <p className="py-16 text-center text-sm text-muted-foreground" data-feed-signed-out="">{t("feed.signedOut")}</p>;
	} else if (support === "unsupported") {
		body = <p className="py-16 text-center text-sm text-muted-foreground">{t("feed.unavailable")}</p>;
	} else if (feed.isPending) {
		body = <p role="status" className="py-16 text-center text-sm text-muted-foreground">{t("feed.loading")}</p>;
	} else if (feed.isError && items.length === 0) {
		body = (
			<div className="flex flex-col items-center gap-2 py-16 text-center">
				<p role="alert" className="text-sm text-muted-foreground">{t("feed.loadFailed")}</p>
				<Button onClick={() => void feed.refetch()} size="sm" type="button" variant="ghost">{t("feed.retry")}</Button>
			</div>
		);
	} else {
		body = (
			<>
				{awayHasNews(away) && !markSeen.isPending && !markSeen.isSuccess ? (
					<AwayCard
						away={away!}
						locale={i18n.language}
						markFailed={markSeen.isError}
						onMarkCaughtUp={() => markSeen.mutate(undefined)}
						onNeedsYou={openBell}
						onOpenSession={openSession}
						onStarted={() => {
							setScope("team");
							listTop.current?.scrollIntoView({ block: "start" });
						}}
					/>
				) : null}
				<div ref={listTop} />
				{groups.length === 0 ? (
					<p className="py-16 text-center text-sm text-muted-foreground" data-feed-empty={scope}>{t(EMPTY[scope])}</p>
				) : (
					<FeedGroups groups={groups} locale={i18n.language} me={userId} onOpen={(item) => openSession(item.projectId, item.sessionId)} />
				)}
				{feed.hasNextPage ? (
					<div className="flex justify-center">
						<Button disabled={feed.isFetchingNextPage} onClick={() => void feed.fetchNextPage()} size="sm" type="button" variant="ghost">
							{feed.isFetchingNextPage ? <LoaderCircle aria-hidden="true" className="size-icon-sm animate-spin" /> : null}
							{t("feed.loadMore")}
						</Button>
					</div>
				) : null}
			</>
		);
	}

	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<PageTopbar
				lead={
					<>
						<PageTopbarTitle as="h1" icon={<Users className="size-icon-md" aria-hidden="true" />}>{t("feed.title")}</PageTopbarTitle>
						<div aria-label={t("feed.tabsLabel")} className="inline-flex shrink-0 items-center gap-0.5" onKeyDown={moveTab} role="tablist">
							{TABS.map((tab) => (
								<TopbarButton
									aria-controls={`${tabsId}-panel`}
									aria-selected={scope === tab.scope}
									className="data-[active=true]:text-foreground"
									data-active={scope === tab.scope ? "true" : undefined}
									highlightActive={scope === tab.scope}
									id={`${tabsId}-${tab.scope}`}
									key={tab.scope}
									onClick={() => setScope(tab.scope)}
									ref={(node: HTMLButtonElement | null) => { tabRefs.current[tab.scope] = node; }}
									role="tab"
									tabIndex={scope === tab.scope ? 0 : -1}
									variant="secondary"
								>
									{t(tab.label)}
								</TopbarButton>
							))}
						</div>
					</>
				}
			/>
			<ScrollArea.Root asChild type="hover" scrollHideDelay={500}>
				<main className="relative min-h-0 flex-1 overflow-hidden">
					<ScrollArea.Viewport className="h-full w-full overscroll-contain focus-visible:outline-none">
						<div aria-labelledby={`${tabsId}-${scope}`} className="mx-auto flex w-full max-w-4xl flex-col gap-5 px-5 py-4" id={`${tabsId}-panel`} role="tabpanel">
							{body}
						</div>
					</ScrollArea.Viewport>
				</main>
			</ScrollArea.Root>
		</div>
	);
}

/** The feed's groups, each a heading with its count and one shared list of rows. */
export function FeedGroups({ groups, locale, me, onOpen }: { groups: FeedGroup[]; locale: string; me?: string; onOpen: (item: CloudCpFeedItem) => void }) {
	const { t } = useTranslation();
	return groups.map((group) => (
		<section aria-label={groupTitle(group.id, group.items.length, t)} className="flex flex-col gap-1.5" data-feed-group={group.id} key={group.id}>
			<h2 className="px-1 text-caption text-muted-foreground">{groupTitle(group.id, group.items.length, t)}</h2>
			<ul className="flex flex-col overflow-hidden rounded-lg bg-card">
				{group.items.map((item) => (
					<FeedRow item={item} key={`${item.orgId}:${item.sessionId}`} locale={locale} me={me} onOpen={() => onOpen(item)} />
				))}
			</ul>
		</section>
	));
}

function groupTitle(id: FeedGroupId, count: number, t: TFunction): string {
	if (id === "needs") return t("feed.group.needs", { count: String(count) });
	if (id === "working") return t("feed.group.working", { count: String(count) });
	return t("feed.group.recent");
}

/**
 * What changed since the last visit, one line each, each one clickable: the
 * count that needs you opens the bell, a merged PR or a failed session opens
 * its session, and new sessions show the whole team. When it counts from
 * sits in the title's tooltip; ✕ marks it all seen.
 */
export function AwayCard({ away, locale, markFailed, onMarkCaughtUp, onNeedsYou, onOpenSession, onStarted }: {
	away: CloudCpFeedAway;
	locale: string;
	/** The last "Mark caught up" didn't go through. */
	markFailed?: boolean;
	onMarkCaughtUp: () => void;
	onNeedsYou: () => void;
	onOpenSession: (projectId: string, sessionId: string) => void;
	onStarted: () => void;
}) {
	const { t } = useTranslation();
	const since = sinceLabel(away.since, locale, t);
	const line = "flex min-w-0 items-center gap-2 text-xs text-muted-foreground";
	const count = <b className="font-semibold text-foreground tabular-nums" />;
	const title = <h2 className="text-control font-medium text-foreground" tabIndex={since ? 0 : undefined}>{t("feed.away.title")}</h2>;
	return (
		<section aria-label={t("feed.away.title")} className="flex flex-col gap-2 rounded-lg bg-card px-4 py-3" data-feed-away="">
			<div className="flex min-w-0 items-center gap-2">
				{since ? (
					<Tooltip>
						<TooltipTrigger asChild>{title}</TooltipTrigger>
						<TooltipContent>{t("feed.away.since", { time: since })}</TooltipContent>
					</Tooltip>
				) : title}
				<span className="flex-1" />
				<Tooltip>
					<TooltipTrigger asChild>
						<CloseButton aria-label={t("feed.away.markCaughtUp")} onClick={onMarkCaughtUp} size="icon-xs" />
					</TooltipTrigger>
					<TooltipContent>{t("feed.away.markCaughtUp")}</TooltipContent>
				</Tooltip>
			</div>
			<ul className="flex flex-col gap-1.5">
				{away.needsYou > 0 ? (
					<li>
						<Pressable className={cn(line, "hover:text-foreground")} data-away="needs" onClick={onNeedsYou} type="button">
							<Hand aria-hidden="true" className="size-3.5 shrink-0 text-status-needs-you" />
							<span><Trans t={t} i18nKey="feed.away.needsYou" count={away.needsYou} components={{ b: count }} /></span>
						</Pressable>
					</li>
				) : null}
				{away.prsMerged.length > 0 ? (
					<li className={line} data-away="merged">
						<GitMerge aria-hidden="true" className="size-3.5 shrink-0 text-status-merged" />
						<span className="shrink-0"><Trans t={t} i18nKey="feed.away.prsMerged" count={away.prsMerged.length} components={{ b: count }} /></span>
						<span className="flex min-w-0 flex-wrap gap-x-1.5">
							{away.prsMerged.map((pr) => (
								<Button className="h-auto p-0 text-xs" key={`${pr.sessionId}:${pr.number}`} onClick={() => onOpenSession(pr.projectId, pr.sessionId)} type="button" variant="link">
									#{pr.number}
								</Button>
							))}
						</span>
					</li>
				) : null}
				{away.failed.length > 0 ? (
					<li className={line} data-away="failed">
						<CircleX aria-hidden="true" className="size-3.5 shrink-0 text-error" />
						<span className="shrink-0"><Trans t={t} i18nKey="feed.away.failed" count={away.failed.length} components={{ b: count }} /></span>
						<span className="flex min-w-0 flex-wrap gap-x-1.5">
							{away.failed.map((session) => (
								<Button className="h-auto max-w-full truncate p-0 text-xs" key={session.sessionId} onClick={() => onOpenSession(session.projectId, session.sessionId)} type="button" variant="link">
									{session.title}
								</Button>
							))}
						</span>
					</li>
				) : null}
				{away.started.count > 0 ? (
					<li>
						<Pressable className={cn(line, "hover:text-foreground")} data-away="started" onClick={onStarted} type="button">
							<Plus aria-hidden="true" className="size-3.5 shrink-0" />
							<span className="truncate">
								{away.started.names.length > 0
									? <Trans t={t} i18nKey="feed.away.started" count={away.started.count} values={{ names: joinNames(away.started.names, locale) }} components={{ b: count }} />
									: <Trans t={t} i18nKey="feed.away.startedNoNames" count={away.started.count} components={{ b: count }} />}
							</span>
						</Pressable>
					</li>
				) : null}
			</ul>
			{markFailed ? <p className="text-xs text-muted-foreground" role="status">{t("feed.away.markFailed")}</p> : null}
		</section>
	);
}

const DAY_MS = 86_400_000;

/**
 * When "While you were away" counts from, as a chat app says it: the time
 * today, "yesterday" and the time, the weekday and time within the week, and
 * the date beyond it, so last Sunday never reads as this morning.
 */
export function sinceLabel(since: string, locale: string, t: TFunction, now = new Date()): string | undefined {
	const at = new Date(since);
	if (!Number.isFinite(at.getTime())) return undefined;
	const day = (date: Date) => new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
	const days = Math.round((day(now) - day(at)) / DAY_MS);
	try {
		const time = new Intl.DateTimeFormat(locale, { hour: "numeric", minute: "2-digit" }).format(at);
		if (days <= 0) return time;
		if (days === 1) return t("feed.away.yesterday", { time });
		if (days < 7) return new Intl.DateTimeFormat(locale, { weekday: "short", hour: "numeric", minute: "2-digit" }).format(at);
		return new Intl.DateTimeFormat(locale, at.getFullYear() === now.getFullYear()
			? { month: "short", day: "numeric" }
			: { month: "short", day: "numeric", year: "numeric" }).format(at);
	} catch {
		return undefined;
	}
}

/**
 * One session: where it stands, whose it is, its title, one line, who else is
 * in it, and when. Its tooltip says where it is, who started it and who else
 * is in it; the eye marks a session you follow but didn't start.
 */
function FeedRow({ item, locale, me, onOpen }: { item: CloudCpFeedItem; locale: string; me?: string; onOpen: () => void }) {
	const { t } = useTranslation();
	const others = item.people.slice(0, ROW_PEOPLE);
	const more = item.people.length - others.length;
	const line = item.worker ? t("feed.workerLine", { index: item.worker.index, of: item.worker.of, line: item.line }) : item.line;
	const everyone = item.people.map((person) => person.name);
	const tooltip = [
		item.projectName ? t("feed.row.where", { project: item.projectName, title: item.title }) : item.title,
		t("feed.row.owner", { name: item.owner.name }),
		...(everyone.length > 0 ? [t("feed.alsoHere", { names: joinNames(everyone, locale) })] : []),
	].join("\n");
	return (
		<li className="border-b border-border last:border-b-0">
			<Pressable
				className="grid w-full grid-cols-[1rem_1.25rem_minmax(0,1fr)_auto_3.5rem] items-center gap-x-2.5 px-3 py-2 text-left transition-none hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60"
				data-feed-row={item.sessionId}
				onClick={onOpen}
				title={tooltip}
				type="button"
			>
				<FeedStatusMark status={item.status} />
				<span aria-label={item.owner.name} className="inline-flex" data-feed-owner="" role="img">
					<PersonAvatar name={item.owner.name} size="sm" />
				</span>
				<span className="flex min-w-0 flex-col">
					<span className="flex min-w-0 items-center gap-1.5">
						<span className="truncate text-control text-foreground">{item.title}</span>
						{item.following && item.owner.userId !== me ? <Eye aria-label={t("feed.following")} className="size-3 shrink-0 text-muted-foreground" role="img" /> : null}
					</span>
					<span className="truncate text-xs text-muted-foreground">{line}</span>
				</span>
				<span
					aria-label={others.length > 0 ? t("feed.alsoHere", { names: joinNames(everyone, locale) }) : undefined}
					className="flex shrink-0 items-center gap-0.5"
					role={others.length > 0 ? "img" : undefined}
				>
					{others.map((person) => <PersonAvatar key={person.userId} name={person.name} size="xs" />)}
					{more > 0 ? <span aria-hidden="true" className="ml-0.5 text-caption tabular-nums text-muted-foreground" data-feed-more="">+{more}</span> : null}
				</span>
				<time className="text-right text-caption tabular-nums text-muted-foreground" dateTime={item.updatedAt}>
					{formatTimeCompact(item.updatedAt)}
				</time>
			</Pressable>
		</li>
	);
}

const STATUS_LABEL = {
	needs: "feed.status.needs",
	working: "feed.status.working",
	review: "feed.status.review",
	ready: "feed.status.ready",
	merged: "feed.status.merged",
	failed: "feed.status.failed",
	idle: "feed.status.idle",
} as const satisfies Record<CloudCpFeedStatus, string>;

/** One status slot, as the sidebar and board draw it: a spinner, a PR mark, or a dot. */
function FeedStatusMark({ status }: { status: CloudCpFeedStatus }) {
	const { t } = useTranslation();
	const label = t(STATUS_LABEL[status] ?? STATUS_LABEL.idle);
	let mark: ReactNode;
	if (status === "working") mark = <LoaderCircle className="size-3 animate-spin text-muted-foreground motion-reduce:animate-none" />;
	else if (status === "review") mark = <GitPullRequest className="size-3.5 text-status-in-review" />;
	else if (status === "ready") mark = <GitPullRequest className="size-3.5 text-status-ready" />;
	else if (status === "merged") mark = <GitMerge className="size-3.5 text-status-merged" />;
	else if (status === "failed") mark = <CircleX className="size-3.5 text-error" />;
	else if (status === "needs") mark = <span className="size-2 rounded-full bg-status-needs-you" />;
	else mark = <span className="size-2 rounded-full border border-muted-foreground/60" />;
	return (
		<span aria-label={label} className="inline-flex size-4 items-center justify-center" data-feed-status={status} role="img" title={label}>
			{mark}
		</span>
	);
}
