import { ArrowRight, ArrowUpRight, Circle, CircleAlert, CircleCheck, CircleDashed, CircleMinus, CircleX, Contrast, FileText, MessageSquare, X, type LucideIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { usePRDetailsLoad } from "../hooks/usePRDetailsLoad";
import { usePRNoChecksClock } from "../hooks/usePRNoChecksClock";
import { usePRStack } from "../hooks/usePRStack";
import { useSessionScmSummary, type SessionPRSummary } from "../hooks/useSessionScmSummary";
import { formatTimeCompact } from "../lib/format-time";
import { prBrowserUrl, prCanMerge, prChecksUrl, prHasNoChecks, prReviewers, prSummaryBlockReason, sessionPRDisplaySummaries, type PRReviewer } from "../lib/pr-display";
import { stackBlocker, stackLayersToLand } from "../lib/pr-stack";
import type { PRTabSection } from "../lib/pr-tabs";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";
import { GitHubIcon } from "./GitHubIcon";
import { blockReasonLabel, PRActionButton, PRStateMenu } from "./PRActions";
import { AppLink } from "./AppLink";
import { checkTone, sortedChecks } from "./PRCardDetails";
import { PRAvatar, PRComments, prCommentCount } from "./PRComments";
import { PRDescriptionBody } from "./PRDescriptionBody";
import { prStateIcon } from "./PRPills";
import { PRStackSection, usePRStackSection } from "./PRStack";
import { ProductExternalLink } from "./ProductExternalLink";
import { TerminalTabFrame, terminalTabActionClassName } from "./TerminalTabFrame";
import { Pressable } from "./ui/pressable";
import { Skeleton } from "./ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";

/** The session's PR with this URL, as the inspector's card shows it. */
export function useSessionPR(session: WorkspaceSession | undefined, url: string, hostId?: string): SessionPRSummary | undefined {
	const query = useSessionScmSummary(session?.id, Boolean(session) && !session?.cloud, undefined, false, hostId);
	if (!session) return undefined;
	return sessionPRDisplaySummaries(session, query.data?.prs).find((pr) => pr.url === url);
}

/** A PR's tab in the session's tab strip, beside its chat and files. */
export function PRTabChip({ active, hostId, onActivate, onClose, session, url }: {
	active: boolean;
	hostId?: string;
	onActivate: () => void;
	onClose: () => void;
	session?: WorkspaceSession;
	url: string;
}) {
	const { t } = useTranslation();
	const pr = useSessionPR(session, url, hostId);
	const state = prStateIcon[pr?.state ?? "open"];
	const name = pr ? `#${pr.number} ${pr.title}` : t("pr.tab.loading");
	// The close button takes the state icon's slot on hover, like a file tab's.
	const closeAction = (
		<Pressable
			aria-label={t("files.closeTab", { name })}
			className={terminalTabActionClassName}
			onClick={(event) => {
				event.stopPropagation();
				onClose();
			}}
			type="button"
		>
			<X aria-hidden="true" className="size-icon-sm" />
		</Pressable>
	);
	return (
		<TerminalTabFrame
			action={closeAction}
			active={active}
			buttonProps={{
				"aria-label": name,
				"aria-selected": active,
				onClick: onActivate,
				role: "tab",
				tabIndex: active ? 0 : -1,
				title: name,
				type: "button",
			}}
			icon={<state.icon aria-hidden="true" className={cn("size-icon-base shrink-0", state.className)} />}
			label={name}
		/>
	);
}

// Underlined tabs: the word carries the state, and the line sits on the strip's rule.
const tabClass = "relative h-9 flex-none rounded-none border-0 px-0.5 py-0 text-control text-muted-foreground after:absolute after:inset-x-0 after:-bottom-px after:h-0.5 after:rounded-full focus-visible:ring-2 focus-visible:ring-ring/60 data-[state=active]:bg-transparent data-[state=active]:text-foreground data-[state=active]:after:bg-foreground dark:data-[state=active]:border-transparent dark:data-[state=active]:bg-transparent";

// A filled mark is a result: its colour fills the disc and the glyph is cut out in
// the page's colour. A hollow one means nothing has been decided yet.
const resultMark = (fill: string) => cn("text-background", fill);
const markClass = "size-icon-sm shrink-0";
const sideRowClass = "flex min-h-6 min-w-0 max-w-full items-center gap-2 text-control text-foreground";
const sideLinkClass = "min-w-0 truncate underline-offset-2 hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60";

const reviewerMark: Record<PRReviewer["status"], { icon: LucideIcon; className: string; label: "pr.review.approved" | "pr.review.changesRequested" | "pr.review.pending" | "pr.tab.reviewerCommented" }> = {
	approved: { icon: CircleCheck, className: resultMark("fill-success"), label: "pr.review.approved" },
	changes_requested: { icon: CircleAlert, className: resultMark("fill-status-in-review"), label: "pr.review.changesRequested" },
	commented: { icon: Circle, className: "text-passive", label: "pr.tab.reviewerCommented" },
	pending: { icon: Contrast, className: "text-warning", label: "pr.review.pending" },
};

const checkMark: Record<"failed" | "running" | "passed" | "skipped", { icon: LucideIcon; className: string }> = {
	failed: { icon: CircleX, className: resultMark("fill-error") },
	passed: { icon: CircleCheck, className: resultMark("fill-success") },
	running: { icon: CircleDashed, className: "text-muted-foreground" },
	skipped: { icon: CircleMinus, className: "text-passive" },
};

/** Checks named in the side column; the rest are a link to the provider's checks page. */
const PR_TAB_CHECKS_SHOWN = 6;

const failedChecks = (checks: SessionPRSummary["ci"]["checks"] = []) => checks.filter((check) => checkTone[check.status].key === "failed").length;

/** A group of the side column: a quiet label, and its items under it (beside it when narrow). */
function SideGroup({ children, label, note }: { children: React.ReactNode; label?: string; note?: { label: string; text: string } }) {
	return (
		<div className="flex min-w-0 items-start gap-x-3 gap-y-1.5 border-t border-border/60 px-3.5 py-2 first:border-t-0 @3xl:flex-col @3xl:px-0 @3xl:py-3 @3xl:first:pt-0">
			{label ? (
				<h3 className="m-0 w-20 shrink-0 text-control leading-6 font-normal text-muted-foreground @3xl:w-auto">
					{label}
					{note ? <span aria-label={note.label} className="ml-1.5 tabular-nums text-passive" role="img" title={note.label}>{note.text}</span> : null}
				</h3>
			) : null}
			{children}
		</div>
	);
}

const sideListClass = "m-0 flex min-w-0 list-none flex-wrap gap-x-4 p-0 @3xl:w-full @3xl:flex-col @3xl:flex-nowrap";
const SideEmpty = ({ children }: { children: string }) => <span className="min-h-6 text-control leading-6 text-passive">{children}</span>;
const SideLoading = () => <Skeleton aria-hidden="true" className="my-1 h-4 w-32 max-w-full" data-testid="pr-tab-side-loading" />;

/**
 * What the PR waits on at a glance: who reviews it, each check by name, and
 * whether it can merge. Marks are one size and column, an avatar sits level
 * with its mark, and only a real result is filled in, so the eye lands on what
 * was decided. Nothing to report is plain text, a fact still being read is a
 * shape, and merge state stays away until there is one. It is the right
 * column of a wide tab, and a stacked block under the title of a narrow one
 * (with the inspector open beside it).
 */
function PRTabSide({ detailsFailed, hostId, pr, session }: { detailsFailed: boolean; hostId?: string; pr: SessionPRSummary; session: WorkspaceSession }) {
	const { t } = useTranslation();
	const stack = usePRStack(pr, hostId, !session.cloud).data;
	const stackSection = usePRStackSection(pr, session, stack);
	const loaded = Boolean(pr.detailsObservedAt);
	const reviewers = prReviewers(pr);
	const checks = sortedChecks(pr.ci.checks ?? []);
	const hiddenChecks = checks.length - PR_TAB_CHECKS_SHOWN;
	const checksUrl = prChecksUrl(pr);
	// Checks that were skipped have no say, so they don't count against the total.
	const decidingChecks = checks.filter((check) => checkTone[check.status].key !== "skipped");
	const passedChecks = decidingChecks.filter((check) => checkTone[check.status].key === "passed").length;
	const checksNote = decidingChecks.length > 0 ? {
		label: t("pr.tab.checksPassed", { passed: passedChecks, total: decidingChecks.length }),
		text: `${passedChecks}/${decidingChecks.length}`,
	} : undefined;
	const landing = stackLayersToLand(stack?.stack, pr.number);
	const lowerBlock = landing ? stackBlocker(landing, pr.number) : undefined;
	const blockReason = prSummaryBlockReason(pr);
	const merge = pr.state !== "open" && pr.state !== "draft" ? undefined
		: pr.mergeability.state === "conflicting" ? { icon: CircleX, className: resultMark("fill-error"), label: t("pr.card.mergeConflict") }
			: pr.state === "draft" || pr.mergeability.state === "unknown" ? undefined
				: lowerBlock ? { icon: CircleAlert, className: resultMark("fill-warning"), label: t("pr.stack.blocked", { number: lowerBlock.number, reason: t(blockReasonLabel[lowerBlock.reason]) }) }
					: prCanMerge(pr, { noChecks: prHasNoChecks(pr) }) ? { icon: CircleCheck, className: resultMark("fill-success"), label: t("pr.card.readyToMerge") }
						: { icon: CircleAlert, className: resultMark("fill-warning"), label: blockReason === "checks" && failedChecks(checks) > 0 ? t("pr.blocker.checksFailing", { count: failedChecks(checks) }) : t(blockReason ? blockReasonLabel[blockReason] : "pr.card.mergeBlocked") };
	const stackGroup = stackSection ? (
		<SideGroup label={stackSection.label}>
			<PRStackSection hostId={hostId} pr={pr} section={stackSection} session={session} />
		</SideGroup>
	) : null;
	return (
		<aside aria-label={t("pr.tab.status")} className="mb-3 flex min-w-0 flex-col rounded-xl border border-border/60 @3xl:absolute @3xl:top-6 @3xl:right-6 @3xl:mb-0 @3xl:w-[220px] @3xl:rounded-none @3xl:border-0" data-testid="pr-tab-side">
			{stack?.stack ? stackGroup : null}
			<SideGroup label={t("pr.tab.reviewers")}>
				{reviewers.length > 0 ? (
					<ul aria-label={t("pr.tab.reviewers")} className={sideListClass}>
						{reviewers.map((reviewer) => {
							const mark = reviewerMark[reviewer.status];
							return (
								<li className={sideRowClass} key={reviewer.login} title={t(mark.label)}>
									<mark.icon aria-hidden="true" className={cn(markClass, mark.className)} />
									<PRAvatar bot={reviewer.bot} name={reviewer.login} size="sm" />
									<span className="min-w-0 truncate">{reviewer.login}</span>
									<span className="sr-only">{t(mark.label)}</span>
								</li>
							);
						})}
					</ul>
				) : loaded ? <SideEmpty>{t("pr.tab.noReviewers")}</SideEmpty> : detailsFailed ? <SideEmpty>{t("pr.description.unavailable")}</SideEmpty> : <SideLoading />}
			</SideGroup>
			<SideGroup label={t("pr.tab.checks")} note={checksNote}>
				{checks.length > 0 ? (
					<ul aria-label={t("pr.tab.checks")} className={sideListClass}>
						{checks.slice(0, PR_TAB_CHECKS_SHOWN).map((check) => {
							const tone = checkTone[check.status];
							const mark = checkMark[tone.key];
							return (
								<li className={sideRowClass} key={check.name}>
									<mark.icon aria-hidden="true" className={cn(markClass, mark.className, tone.breathe && "animate-status-pulse")} />
									{check.url ? <AppLink className={sideLinkClass} href={check.url} noHoverPreview rel="noopener noreferrer" target="_blank">{check.name}</AppLink> : <span className="min-w-0 truncate">{check.name}</span>}
									<span className="sr-only">{t(`pr.checks.status.${tone.key}`)}</span>
								</li>
							);
						})}
						{hiddenChecks > 0 ? (
							<li className={cn(sideRowClass, "pl-6 text-muted-foreground")}>
								{checksUrl ? <AppLink className={sideLinkClass} href={checksUrl} noHoverPreview rel="noopener noreferrer" target="_blank">{t("pr.checks.more", { count: hiddenChecks })}</AppLink> : t("pr.checks.more", { count: hiddenChecks })}
							</li>
						) : null}
					</ul>
				) : loaded ? <SideEmpty>{t("pr.tab.noChecks")}</SideEmpty> : detailsFailed ? <SideEmpty>{t("pr.description.unavailable")}</SideEmpty> : <SideLoading />}
			</SideGroup>
			{merge ? (
				<SideGroup>
					<span className={sideRowClass}>
						<merge.icon aria-hidden="true" className={cn(markClass, merge.className)} />
						<span className="min-w-0 truncate" title={merge.label}>{merge.label}</span>
					</span>
				</SideGroup>
			) : null}
			{stack?.stack ? null : stackGroup}
		</aside>
	);
}

/** Who opened the PR: the person when AO's GitHub App opened it for them, otherwise its author. */
function PRAuthor({ pr }: { pr: SessionPRSummary }) {
	const { t } = useTranslation();
	const opener = pr.openedBy;
	const login = opener?.githubLogin?.replace(/^@/, "").trim() || opener?.name.trim() || pr.author.replace(/^@/, "");
	if (!login) return null;
	const person = <><PRAvatar name={login} size="lg" /><span className="truncate text-foreground">{login}</span></>;
	return (
		<span className="inline-flex min-w-0 items-center gap-2">
			{pr.provider === "github" && /^[\w-]+$/.test(login) ? (
				<ProductExternalLink className="inline-flex min-w-0 items-center gap-2 underline-offset-2 hover:underline" href={`https://github.com/${encodeURIComponent(login)}`}>{person}</ProductExternalLink>
			) : person}
			{opener ? <span className="shrink-0 text-passive">{t("pr.openedVia")}</span> : null}
		</span>
	);
}

/**
 * A PR in its own tab, beside the chat: its state (changed from the pill), title
 * and who opened it on top, then its description and its conversation as two
 * tabs, the things the sidebar's card only previews, with its reviewers, checks,
 * stack and merge state in a column beside them. Both tabs stay mounted so a
 * half-written comment survives a look at the description. Merge is here so the
 * tab works with the sidebar closed; labels and the branch's work stay on the card.
 */
export function PRTabPane({ hostId, onViewInDiff, request, session, url }: {
	hostId?: string;
	/** Opens a file's diff at a line, for a review thread's "View in diff". */
	onViewInDiff?: (target: { line?: number; path: string }) => void;
	/** The part the card asked to show; a new key asks again. */
	request?: { section: PRTabSection; key: number };
	session: WorkspaceSession;
	url: string;
}) {
	const { t } = useTranslation();
	const pr = useSessionPR(session, url, hostId);
	const detailsFailed = usePRDetailsLoad(pr, session.id, hostId, !session.cloud);
	usePRNoChecksClock([pr]);
	const [section, setSection] = useState<PRTabSection>(request?.section ?? "summary");
	const scrollerRef = useRef<HTMLDivElement>(null);
	const stripRef = useRef<HTMLDivElement>(null);
	const bodyRef = useRef<HTMLDivElement>(null);
	const requestKey = request?.key;
	const requestSection = request?.section;
	useEffect(() => {
		if (requestKey === undefined || !requestSection) return;
		setSection(requestSection);
		if (scrollerRef.current) scrollerRef.current.scrollTop = 0;
	}, [requestKey, requestSection]);
	const ready = Boolean(pr);
	// The tabs stick over the body as it scrolls. In a translucent window the
	// pane's background is clear, so the body would show through them: it is
	// clipped at the tabs' lower edge instead, which holds in every theme.
	useEffect(() => {
		const scroller = scrollerRef.current;
		const strip = stripRef.current;
		const body = bodyRef.current;
		if (!ready || !scroller || !strip || !body) return;
		const clip = () => {
			const under = strip.getBoundingClientRect().bottom - body.getBoundingClientRect().top;
			body.style.clipPath = under > 0 ? `inset(${under}px 0 0 0)` : "";
		};
		clip();
		scroller.addEventListener("scroll", clip, { passive: true });
		const resize = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(clip);
		resize?.observe(scroller);
		resize?.observe(body);
		return () => {
			scroller.removeEventListener("scroll", clip);
			resize?.disconnect();
		};
	}, [ready]);
	// Past the top, the tabs are stuck under the window's edge: the other tab starts at its own top.
	const show = (next: string) => {
		setSection(next as PRTabSection);
		const scroller = scrollerRef.current;
		const body = bodyRef.current;
		if (!scroller || !body) return;
		const stuckAt = scroller.scrollTop + body.getBoundingClientRect().top - scroller.getBoundingClientRect().top - (stripRef.current?.offsetHeight ?? 0);
		scroller.scrollTop = Math.min(scroller.scrollTop, stuckAt);
	};

	if (!pr) {
		return (
			<section className="flex h-full min-h-0 items-center justify-center bg-background" data-testid="pr-tab-pane">
				<p className="m-0 text-sm text-passive">{t("pr.tab.loading")}</p>
			</section>
		);
	}
	const comments = prCommentCount(pr);

	return (
		<section aria-label={t("pr.tab.label", { number: pr.number })} className="@container h-full min-h-0 bg-background" data-testid="pr-tab-pane">
			<div className="h-full overflow-y-auto" ref={scrollerRef}>
				<Tabs className="relative mx-auto w-full max-w-[1080px] min-w-0 px-6 @3xl:pr-[calc(1.5rem+252px)]" onValueChange={show} value={section}>
					<header className="flex min-w-0 flex-col gap-2.5 pt-6 pb-3">
						<div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1">
							<PRStateMenu hostId={hostId} pr={pr} sessionId={session.id} />
							<span className="min-w-0 truncate text-control text-muted-foreground">{pr.repo.split("/").pop()} #{pr.number}</span>
							<ProductExternalLink ariaLabel={t("pr.comments.openOnProvider")} className="ml-auto inline-flex h-7 shrink-0 items-center gap-1.5 rounded-full bg-muted px-2.5 text-control font-medium text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" href={prBrowserUrl(pr)}>
								{pr.provider === "github" ? <GitHubIcon className="size-icon-sm" /> : <ArrowUpRight aria-hidden="true" className="size-icon-sm" />}
								<span className="hidden @md:inline">{t("pr.comments.openOnProvider")}</span>
							</ProductExternalLink>
						</div>
						<div className="flex min-w-0 flex-col items-start gap-2.5 @xl:flex-row @xl:gap-4">
							<h2 className="m-0 min-w-0 flex-1 text-heading font-medium leading-tight text-foreground [overflow-wrap:anywhere]">{pr.title}</h2>
							{(pr.state === "open" || pr.state === "draft") && !session.cloud ? (
								<div className="max-w-full shrink-0">
									<PRActionButton canMerge={prCanMerge(pr, { noChecks: prHasNoChecks(pr) }) && Boolean(pr.url && pr.headSha)} hostId={hostId} pr={pr} sessionId={session.id} />
								</div>
							) : null}
						</div>
						<div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-control text-muted-foreground">
							<PRAuthor pr={pr} />
							{pr.createdAt ? <span className="shrink-0" title={pr.createdAt}>{formatTimeCompact(pr.createdAt, { longUnits: true })}</span> : null}
							{pr.sourceBranch ? (
								<span className="inline-flex min-w-0 items-center gap-2">
									<span aria-hidden="true" className="text-passive">·</span>
									<span className="min-w-0 truncate">{pr.sourceBranch}</span>
									{pr.targetBranch ? <><ArrowRight aria-hidden="true" className="size-icon-sm shrink-0" /><span className="min-w-0 truncate">{pr.targetBranch}</span></> : null}
								</span>
							) : null}
						</div>
					</header>
					<PRTabSide detailsFailed={detailsFailed} hostId={hostId} pr={pr} session={session} />
					<div className="sticky top-0 z-10 border-b border-border bg-background" ref={stripRef}>
						<TabsList aria-label={t("pr.tab.label", { number: pr.number })} className="flex h-auto w-full justify-start gap-5 rounded-none bg-transparent p-0">
							<TabsTrigger className={tabClass} value="summary">
								<FileText aria-hidden="true" className="size-icon-sm" />
								{t("pr.tab.summary")}
							</TabsTrigger>
							<TabsTrigger className={tabClass} value="comments">
								<MessageSquare aria-hidden="true" className="size-icon-sm" />
								{t("pr.tab.comments")}
								{comments > 0 ? <span className="rounded-full bg-muted px-1.5 text-micro font-medium tabular-nums text-muted-foreground">{comments}</span> : null}
							</TabsTrigger>
						</TabsList>
					</div>
					<div className="min-w-0 py-5" ref={bodyRef}>
						<TabsContent className="min-w-0 data-[state=inactive]:hidden" forceMount value="summary">
							<PRDescriptionBody cloud={false} detailsFailed={detailsFailed} pr={pr} />
						</TabsContent>
						<TabsContent className="min-w-0 data-[state=inactive]:hidden" forceMount value="comments">
							<PRComments detailsFailed={detailsFailed} hostId={hostId} onViewInDiff={onViewInDiff} pr={pr} session={session} />
						</TabsContent>
					</div>
				</Tabs>
			</div>
		</section>
	);
}
