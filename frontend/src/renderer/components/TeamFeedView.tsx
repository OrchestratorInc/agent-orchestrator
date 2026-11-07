import { CircleX, Eye, GitMerge, GitPullRequest, Hand, LoaderCircle, Plus, Users } from "lucide-react";
import { ScrollArea } from "radix-ui";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
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
import { Pressable } from "./ui/pressable";
import { joinNames } from "./chat/TeamComposerBars";

const TABS = [
	{ scope: "team", label: "feed.tab.team" },
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
 * What the team's agents are doing, across projects: Team, Mine and
 * Following; what changed since your last visit; then the sessions that need
 * someone, the ones working now, and the rest. A row opens its session.
 */
export function TeamFeedView() {
	const { t, i18n } = useTranslation();
	const support = useTeamCollabSupport();
	const [scope, setScope] = useState<CloudCpFeedScope>("team");
	const feed = useTeamFeed(scope, support !== "unsupported");
	const markSeen = useMarkFeedSeen();
	const navigateToSession = useNavigateToSession();
	const openBell = useUiStore((state) => state.requestNotificationCenterOpen);
	const listTop = useRef<HTMLDivElement>(null);

	const pages = feed.data?.pages ?? [];
	const items = pages.flatMap((page) => page.items);
	const away = pages[0]?.away;
	const groups = groupFeed(items);

	// Leaving the page is the visit: the next "While you were away" counts from it.
	const seen = useRef({ visited: false, mark: markSeen.mutate });
	seen.current.mark = markSeen.mutate;
	if (feed.isSuccess) seen.current.visited = true;
	useEffect(() => () => {
		if (seen.current.visited) seen.current.mark();
	}, []);

	const openSession = (projectId: string | undefined, sessionId: string) => navigateToSession(projectId, sessionId);
	const projectOf = (sessionId: string) => items.find((item) => item.sessionId === sessionId)?.projectId;

	let body: ReactNode;
	if (support === "unsupported") {
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
						onMarkCaughtUp={() => markSeen.mutate()}
						onNeedsYou={openBell}
						onOpenSession={(sessionId) => openSession(projectOf(sessionId), sessionId)}
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
					<FeedGroups groups={groups} locale={i18n.language} onOpen={(item) => openSession(item.projectId, item.sessionId)} />
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
						<div aria-label={t("feed.tabsLabel")} className="inline-flex shrink-0 items-center gap-0.5" role="tablist">
							{TABS.map((tab) => (
								<TopbarButton
									aria-selected={scope === tab.scope}
									className="data-[active=true]:text-foreground"
									data-active={scope === tab.scope ? "true" : undefined}
									highlightActive={scope === tab.scope}
									key={tab.scope}
									onClick={() => setScope(tab.scope)}
									role="tab"
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
						<div className="mx-auto flex w-full max-w-4xl flex-col gap-5 px-5 py-4" role="tabpanel">
							{body}
						</div>
					</ScrollArea.Viewport>
				</main>
			</ScrollArea.Root>
		</div>
	);
}

/** The feed's groups, each a heading with its count and one shared list of rows. */
export function FeedGroups({ groups, locale, onOpen }: { groups: FeedGroup[]; locale: string; onOpen: (item: CloudCpFeedItem) => void }) {
	const { t } = useTranslation();
	return groups.map((group) => (
		<section aria-label={groupTitle(group.id, group.items.length, t)} className="flex flex-col gap-1.5" data-feed-group={group.id} key={group.id}>
			<h2 className="px-1 text-caption text-muted-foreground">{groupTitle(group.id, group.items.length, t)}</h2>
			<ul className="flex flex-col overflow-hidden rounded-lg bg-card">
				{group.items.map((item) => (
					<FeedRow item={item} key={`${item.orgId}:${item.sessionId}`} locale={locale} onOpen={() => onOpen(item)} />
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
 * its session, and new sessions show the whole team.
 */
export function AwayCard({ away, locale, onMarkCaughtUp, onNeedsYou, onOpenSession, onStarted }: {
	away: CloudCpFeedAway;
	locale: string;
	onMarkCaughtUp: () => void;
	onNeedsYou: () => void;
	onOpenSession: (sessionId: string) => void;
	onStarted: () => void;
}) {
	const { t } = useTranslation();
	const since = sinceLabel(away.since, locale);
	const line = "flex min-w-0 items-center gap-2 text-xs text-muted-foreground";
	return (
		<section aria-label={t("feed.away.title")} className="flex flex-col gap-2 rounded-lg bg-card px-4 py-3" data-feed-away="">
			<div className="flex min-w-0 items-center gap-2">
				<h2 className="text-control font-medium text-foreground">{t("feed.away.title")}</h2>
				{since ? <span className="truncate text-caption text-muted-foreground">{t("feed.away.since", { time: since })}</span> : null}
				<span className="flex-1" />
				<Button onClick={onMarkCaughtUp} size="xs" type="button" variant="quiet">{t("feed.away.markCaughtUp")}</Button>
			</div>
			<ul className="flex flex-col gap-1.5">
				{away.needsYou > 0 ? (
					<li>
						<Pressable className={cn(line, "hover:text-foreground")} data-away="needs" onClick={onNeedsYou} type="button">
							<Hand aria-hidden="true" className="size-3.5 shrink-0 text-status-needs-you" />
							<b className="font-semibold text-foreground tabular-nums">{away.needsYou}</b>
							<span>{t("feed.away.needsYou", { count: away.needsYou })}</span>
						</Pressable>
					</li>
				) : null}
				{away.prsMerged.length > 0 ? (
					<li className={line} data-away="merged">
						<GitMerge aria-hidden="true" className="size-3.5 shrink-0 text-status-merged" />
						<b className="font-semibold text-foreground tabular-nums">{away.prsMerged.length}</b>
						<span className="shrink-0">{t("feed.away.prsMerged", { count: away.prsMerged.length })}</span>
						<span className="flex min-w-0 flex-wrap gap-x-1.5">
							{away.prsMerged.map((pr) => (
								<Button className="h-auto p-0 text-xs" key={`${pr.sessionId}:${pr.number}`} onClick={() => onOpenSession(pr.sessionId)} type="button" variant="link">
									#{pr.number}
								</Button>
							))}
						</span>
					</li>
				) : null}
				{away.failed.length > 0 ? (
					<li className={line} data-away="failed">
						<CircleX aria-hidden="true" className="size-3.5 shrink-0 text-error" />
						<b className="font-semibold text-foreground tabular-nums">{away.failed.length}</b>
						<span className="shrink-0">{t("feed.away.failed", { count: away.failed.length })}</span>
						<span className="flex min-w-0 flex-wrap gap-x-1.5">
							{away.failed.map((session) => (
								<Button className="h-auto max-w-full truncate p-0 text-xs" key={session.sessionId} onClick={() => onOpenSession(session.sessionId)} type="button" variant="link">
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
							<b className="font-semibold text-foreground tabular-nums">{away.started.count}</b>
							<span className="truncate">{t("feed.away.started", { count: away.started.count, names: joinNames(away.started.names, locale) })}</span>
						</Pressable>
					</li>
				) : null}
			</ul>
		</section>
	);
}

function sinceLabel(since: string, locale: string): string | undefined {
	const at = Date.parse(since);
	if (!Number.isFinite(at)) return undefined;
	try {
		return new Intl.DateTimeFormat(locale, { weekday: "short", hour: "numeric", minute: "2-digit" }).format(at);
	} catch {
		return undefined;
	}
}

/** One session: where it stands, whose it is, its title, one line, who else is in it, and when. */
function FeedRow({ item, locale, onOpen }: { item: CloudCpFeedItem; locale: string; onOpen: () => void }) {
	const { t } = useTranslation();
	const others = item.people.slice(0, ROW_PEOPLE);
	const line = item.worker ? `${t("feed.worker", { index: item.worker.index, of: item.worker.of })} · ${item.line}` : item.line;
	return (
		<li className="border-b border-border last:border-b-0">
			<Pressable
				className="grid w-full grid-cols-[1rem_1.25rem_minmax(0,1fr)_auto_3.5rem] items-center gap-x-2.5 px-3 py-2 text-left transition-none hover:bg-interactive-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60"
				data-feed-row={item.sessionId}
				onClick={onOpen}
				title={item.projectName ? `${item.projectName} · ${item.title}` : item.title}
				type="button"
			>
				<FeedStatusMark status={item.status} />
				<PersonAvatar name={item.owner.name} size="sm" />
				<span className="flex min-w-0 flex-col">
					<span className="flex min-w-0 items-center gap-1.5">
						<span className="truncate text-control text-foreground">{item.title}</span>
						{item.following ? <Eye aria-label={t("feed.following")} className="size-3 shrink-0 text-passive" role="img" /> : null}
					</span>
					<span className="truncate text-xs text-muted-foreground">{line}</span>
				</span>
				<span
					aria-label={others.length > 0 ? t("feed.alsoHere", { names: joinNames(item.people.map((person) => person.name), locale) }) : undefined}
					className="flex shrink-0 gap-0.5"
					role={others.length > 0 ? "img" : undefined}
				>
					{others.map((person) => <PersonAvatar key={person.userId} name={person.name} size="xs" />)}
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
