import { InspectorSection, commentTextLines, inspectorEmptyClass } from "@aoagents/product-ui";
import { Reply } from "lucide-react";
import { useEffect, useId, useState, type MouseEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ArrowUpRight, Check, ChevronDown, ChevronRight, ChevronUp, FileCode2, Loader2, MoreVertical, Send } from "@/components/app-icons";
import { usePRDetailsLoad } from "../../hooks/usePRDetailsLoad";
import { agentLabel } from "../../lib/agent-options";
import { formatTimeCompact } from "../../lib/format-time";
import { reviewCommentIsOpen, type ReviewComment, type ReviewPRBlock, type ReviewReviewer, type ReviewReviewerStatus } from "../../lib/review-comments";
import { cn } from "../../lib/utils";
import { AgentAvatar } from "../AgentAvatar";
import { PRAvatar } from "../PRComments";
import { PRCommentComposer } from "../PRCommentComposer";
import { PRMarkdown } from "../PRMarkdown";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Pressable } from "../ui/pressable";

export type ReviewCommentActions = {
	/** Sends one comment to the worker agent. */
	onSend?: (comment: ReviewComment, block: ReviewPRBlock) => Promise<void>;
	/** Sends every open comment on a PR to the worker agent in one message. */
	onSendAll?: (block: ReviewPRBlock, comments: ReviewComment[]) => Promise<void>;
	/** Resolves an inline thread on the provider. */
	onResolve?: (comment: ReviewComment, block: ReviewPRBlock) => Promise<void>;
	/** Posts a reply on the provider: in the thread, or quoting a review or comment. */
	onReply?: (comment: ReviewComment, block: ReviewPRBlock, body: string, files: File[]) => Promise<void>;
	/** Asks a provider reviewer to review again. */
	onRerequest?: (reviewerId: string, block: ReviewPRBlock) => Promise<void>;
	onViewInFile?: (comment: ReviewComment, block: ReviewPRBlock) => void;
	onOpenInBrowser?: (url: string) => void;
	/** The Orchestrator reviewer's row in a PR's reviewer list starts a review. */
	onReviewNow?: () => void;
};

const verdictTone: Record<ReviewReviewerStatus, string> = {
	approved: "text-success",
	changes_requested: "text-status-in-review",
	commented: "text-muted-foreground",
	pending: "text-passive",
};

const verdictKey = {
	approved: "inspector.review.approved",
	changes_requested: "inspector.review.changesRequested",
	commented: "inspector.review.commented",
	pending: "inspector.review.requested",
} as const;

/** A GitLab merge request, from its summary or, before that loads, its URL. */
function onGitLab(block: ReviewPRBlock): boolean {
	return block.provider ? block.provider.provider === "gitlab" : /\/-\/merge_requests\/\d+/.test(block.webUrl);
}

/**
 * A review summary's one-line preview. The verdict already shows beside it, so a
 * body that opens by restating it ("## Changes requested", "Approved. The fix…")
 * previews from what comes after.
 */
export function summaryPreview(lines: string[], verdictLabels: string[]): string {
	const [first = "", ...rest] = lines;
	const labels = [...new Set(verdictLabels.map((label) => label.trim()).filter(Boolean))];
	for (const label of labels) {
		const lead = new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(?:\\s*[:.!—–-]\\s*|\\s*$)`, "i");
		if (!lead.test(first)) continue;
		const remainder = first.replace(lead, "").trim();
		if (remainder) return remainder.charAt(0).toUpperCase() + remainder.slice(1);
		return rest[0] ?? first;
	}
	return first;
}

/**
 * The Reviews tab's Comments: one list per open PR holding the Orchestrator
 * reviewer's comments and the provider's together, Open first and Resolved
 * behind a tab. A row is two lines (who and when, then where and the first
 * line of the text) and opens in place to the whole text and its actions.
 */
export function ReviewCommentsSection({
	actions,
	blocks,
	hostId,
	loading,
	noPR,
	readOnly,
	sessionId,
}: {
	actions: ReviewCommentActions;
	blocks: ReviewPRBlock[];
	hostId?: string;
	loading: boolean;
	noPR: boolean;
	/** Reached through a share: the comments show, but only the owner acts on them. */
	readOnly: boolean;
	sessionId: string;
}) {
	const { t } = useTranslation();
	const total = blocks.reduce((count, block) => count + block.openCount, 0);
	return (
		<InspectorSection meta={total > 0 ? t("inspector.review.openCount", { count: total }) : undefined} surface={false} title={t("inspector.review.comments")}>
			{noPR ? (
				<p className={cn(inspectorEmptyClass, "m-0")}>{t("inspector.review.commentsEmptyNoPR")}</p>
			) : loading && blocks.length === 0 ? (
				<p className={cn(inspectorEmptyClass, "m-0")}>{t("inspector.loadingReviews")}</p>
			) : (
				<div className="flex min-w-0 flex-col gap-2" data-testid="review-comments">
					{blocks.map((block) => (
						<ReviewPRBlockView
							actions={readOnly ? readOnlyActions(actions) : actions}
							block={block}
							hostId={hostId}
							key={block.key}
							sessionId={sessionId}
						/>
					))}
				</div>
			)}
		</InspectorSection>
	);
}

/** A shared session reads everything and opens links, but changes nothing. */
function readOnlyActions(actions: ReviewCommentActions): ReviewCommentActions {
	return { onViewInFile: actions.onViewInFile, onOpenInBrowser: actions.onOpenInBrowser };
}

function ReviewPRBlockView({
	actions,
	block,
	hostId,
	sessionId,
}: {
	actions: ReviewCommentActions;
	block: ReviewPRBlock;
	hostId?: string;
	sessionId: string;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(true);
	const [view, setView] = useState<"open" | "resolved">("open");
	// A row the person opened or folded keeps that; otherwise an open comment not yet
	// sent to the worker starts open, so its Send to agent is in reach, and the rest fold.
	const [expandedHere, setExpandedHere] = useState<ReadonlyMap<string, boolean>>(() => new Map());
	// Threads resolved from here move to Resolved at once, until the provider's state says so itself.
	const [resolvedHere, setResolvedHere] = useState<ReadonlySet<string>>(() => new Set());
	const [sent, setSent] = useState<ReadonlySet<string>>(() => new Set());
	const [sendingAll, setSendingAll] = useState(false);
	const [sendAllError, setSendAllError] = useState(false);
	const listId = useId();
	// The SCM observer reads a PR's threads only for PRs it follows; ask once for the rest.
	usePRDetailsLoad(block.provider, sessionId, hostId, open);
	useEffect(() => {
		const confirmed = new Set(block.comments.filter((comment) => comment.resolved).map((comment) => comment.key));
		setResolvedHere((current) => ([...current].some((key) => confirmed.has(key)) ? new Set([...current].filter((key) => !confirmed.has(key))) : current));
	}, [block.comments]);

	const comments = block.comments.map((comment) => (resolvedHere.has(comment.key) ? { ...comment, resolved: true } : comment));
	const openComments = comments.filter(reviewCommentIsOpen);
	const resolvedComments = comments.filter((comment) => !reviewCommentIsOpen(comment));
	const shown = view === "open" ? openComments : resolvedComments;
	const unsent = openComments.filter((comment) => !comment.delivered && !sent.has(comment.key));
	const markSent = (keys: string[]) => setSent((current) => new Set([...current, ...keys]));
	const awaitingSend = (comment: ReviewComment) =>
		Boolean(actions.onSend) && reviewCommentIsOpen(comment) && Boolean(comment.body) && !comment.delivered && !sent.has(comment.key);
	const isExpanded = (comment: ReviewComment) => expandedHere.get(comment.key) ?? (view === "open" && awaitingSend(comment));
	const meta = block.repository ? `#${block.number} · ${block.repository}` : `#${block.number}`;

	const sendAll = async () => {
		if (!actions.onSendAll || unsent.length === 0) return;
		setSendingAll(true);
		setSendAllError(false);
		try {
			await actions.onSendAll(block, unsent);
			markSent(unsent.map((comment) => comment.key));
		} catch {
			setSendAllError(true);
		} finally {
			setSendingAll(false);
		}
	};

	return (
		<article className="min-w-0 rounded-lg border border-border/60" data-testid="review-pr-block">
			<Pressable
				aria-controls={open ? listId : undefined}
				aria-expanded={open}
				className="group flex w-full min-w-0 select-none items-start gap-2 rounded-lg px-3 py-2.5"
				data-testid="review-pr-row"
				onClick={() => setOpen((current) => !current)}
			>
				{open ? <ChevronDown aria-hidden="true" className="mt-0.5 size-icon-xs shrink-0 text-passive transition-colors group-hover:text-foreground" /> : <ChevronRight aria-hidden="true" className="mt-0.5 size-icon-xs shrink-0 text-passive transition-colors group-hover:text-foreground" />}
				<span className="flex min-w-0 flex-1 flex-col gap-0.5">
					<span className="break-words text-sm-md font-semibold leading-snug text-foreground">{block.title}</span>
					<span className="text-micro tabular-nums text-passive transition-colors group-hover:text-muted-foreground">{meta}</span>
				</span>
				{/* Open, the tabs carry the counts; folded, the header says what is waiting. */}
				{!open && comments.length > 0 ? (
					<span className={cn("mt-0.5 shrink-0 whitespace-nowrap text-2xs", openComments.length > 0 ? "font-medium text-status-in-review" : "text-passive")}>
						{openComments.length > 0 ? t("inspector.review.openCount", { count: openComments.length }) : t("inspector.review.allResolved")}
					</span>
				) : null}
			</Pressable>
			{open ? (
				<div id={listId}>
					<div className="flex min-w-0 select-none items-center gap-1 border-b border-border/60 pr-2 pb-1.5 pl-3">
						{comments.length > 0 ? (
							<div aria-label={t("inspector.review.comments")} className="flex items-center gap-0.5" role="tablist">
								<Button aria-selected={view === "open"} data-active={view === "open"} onClick={() => { setView("open"); setExpandedHere(new Map()); }} role="tab" size="xs" variant="quiet">
									{t("inspector.review.tabOpen")}
									<span className="tabular-nums text-passive">{openComments.length}</span>
								</Button>
								<Button aria-selected={view === "resolved"} data-active={view === "resolved"} onClick={() => { setView("resolved"); setExpandedHere(new Map()); }} role="tab" size="xs" variant="quiet">
									{t("inspector.review.tabResolved")}
									<span className="tabular-nums text-passive">{resolvedComments.length}</span>
								</Button>
							</div>
						) : null}
						<span className="ml-auto" />
						<ReviewersMenu actions={actions} block={block} />
						<PRActionsMenu
							block={block}
							onOpenInBrowser={actions.onOpenInBrowser}
							onSendAll={actions.onSendAll && unsent.length > 0 ? () => void sendAll() : undefined}
							sendAllError={sendAllError}
							sendingAll={sendingAll}
						/>
					</div>
					{shown.length > 0 ? (
						<div className="divide-y divide-border/60" role="tabpanel">
							{shown.map((comment) => (
								<ReviewCommentRow
									actions={actions}
									block={block}
									comment={comment}
									expanded={isExpanded(comment)}
									key={comment.key}
									onExpandedChange={(next) => setExpandedHere((current) => new Map(current).set(comment.key, next))}
									onResolved={() => setResolvedHere((current) => new Set(current).add(comment.key))}
									onSent={() => {
										// Stays open so "Sent to agent" shows where it was pressed.
										setExpandedHere((current) => (current.has(comment.key) ? current : new Map(current).set(comment.key, true)));
										markSent([comment.key]);
									}}
									sent={comment.delivered || sent.has(comment.key)}
								/>
							))}
						</div>
					) : (
						<p className={cn(inspectorEmptyClass, "m-0 px-3 py-3.5")} role="tabpanel">
							{comments.length === 0 ? t("pr.comments.none") : view === "open" ? t("inspector.review.noOpenComments") : t("inspector.review.nothingResolved")}
						</p>
					)}
				</div>
			) : null}
		</article>
	);
}

function ReviewerAvatar({ reviewer }: { reviewer: Pick<ReviewReviewer, "orchestrator" | "harness" | "id" | "isBot"> }) {
	return reviewer.orchestrator
		? <AgentAvatar className="size-4 shrink-0" decorative provider={reviewer.harness || "reviewer"} />
		: <PRAvatar bot={reviewer.isBot} name={reviewer.id} size="sm" />;
}

/** Who reviewed the PR and where each stands; picking one asks them again. */
function ReviewersMenu({ actions, block }: { actions: ReviewCommentActions; block: ReviewPRBlock }) {
	const { t } = useTranslation();
	const [asked, setAsked] = useState<ReadonlySet<string>>(() => new Set());
	const [failed, setFailed] = useState<ReadonlySet<string>>(() => new Set());
	if (block.reviewers.length === 0) return null;
	const rerequest = async (reviewer: ReviewReviewer) => {
		if (!actions.onRerequest) return;
		setFailed((current) => { const next = new Set(current); next.delete(reviewer.id); return next; });
		try {
			await actions.onRerequest(reviewer.id, block);
			setAsked((current) => new Set(current).add(reviewer.id));
		} catch {
			setFailed((current) => new Set(current).add(reviewer.id));
		}
	};
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button aria-label={t("inspector.review.reviewers")} className="gap-0 px-1" size="xs" title={t("inspector.review.reviewers")} variant="quiet">
					{block.reviewers.slice(0, 4).map((reviewer, index) => (
						<span className={cn("inline-flex rounded-full ring-2 ring-background", index > 0 && "-ml-1")} key={reviewer.id}>
							<ReviewerAvatar reviewer={reviewer} />
						</span>
					))}
					{block.reviewers.length > 4 ? <span className="pl-1 text-micro tabular-nums text-passive">+{block.reviewers.length - 4}</span> : null}
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-64">
				<DropdownMenuLabel>{t("inspector.review.reviewersHint")}</DropdownMenuLabel>
				{block.reviewers.map((reviewer) => {
					const status = asked.has(reviewer.id) ? "pending" : reviewer.status;
					const canAct = reviewer.orchestrator ? Boolean(actions.onReviewNow) : reviewer.canRerequest && !asked.has(reviewer.id) && Boolean(actions.onRerequest);
					return (
						<DropdownMenuItem
							disabled={!canAct}
							key={reviewer.id}
							onSelect={() => (reviewer.orchestrator ? actions.onReviewNow?.() : void rerequest(reviewer))}
						>
							<ReviewerAvatar reviewer={reviewer} />
							<span className="min-w-0 truncate">{reviewer.orchestrator ? t("inspector.review.orchestrator") : reviewer.id}</span>
							<span className={cn("ml-auto inline-flex shrink-0 items-center gap-1.5 text-2xs font-medium", failed.has(reviewer.id) ? "text-error" : verdictTone[status])}>
								{failed.has(reviewer.id) ? t("inspector.rereviewRequestFailed") : (
									<>
										<span aria-hidden="true" className="size-1.5 rounded-full bg-current" />
										{t(verdictKey[status])}
									</>
								)}
							</span>
						</DropdownMenuItem>
					);
				})}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

function PRActionsMenu({
	block,
	onOpenInBrowser,
	onSendAll,
	sendAllError,
	sendingAll,
}: {
	block: ReviewPRBlock;
	onOpenInBrowser?: (url: string) => void;
	onSendAll?: () => void;
	sendAllError: boolean;
	sendingAll: boolean;
}) {
	const { t } = useTranslation();
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button aria-label={t("inspector.review.prActions")} size="icon-xs" title={t("inspector.review.prActions")} variant="quiet">
					{sendingAll ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : <MoreVertical aria-hidden="true" className="size-icon-sm" />}
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="min-w-52">
				{onSendAll ? (
					<DropdownMenuItem disabled={sendingAll} onSelect={onSendAll}>
						<Send aria-hidden="true" />
						{sendAllError ? t("inspector.sendToWorkerAgentError") : t("inspector.review.sendAllOpen")}
					</DropdownMenuItem>
				) : null}
				<DropdownMenuItem onSelect={() => window.open(block.webUrl, "_blank", "noopener,noreferrer")}>
					<ArrowUpRight aria-hidden="true" />
					{onGitLab(block) ? t("inspector.review.openMROnGitLab") : t("inspector.review.openPROnProvider")}
				</DropdownMenuItem>
				{onOpenInBrowser ? (
					<DropdownMenuItem onSelect={() => onOpenInBrowser(block.webUrl)}>{t("inspector.openInAOBrowser")}</DropdownMenuItem>
				) : null}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

type Busy = "idle" | "busy" | "error";

function ReviewCommentRow({
	actions,
	block,
	comment,
	expanded,
	onExpandedChange,
	onResolved,
	onSent,
	sent,
}: {
	actions: ReviewCommentActions;
	block: ReviewPRBlock;
	comment: ReviewComment;
	expanded: boolean;
	onExpandedChange: (expanded: boolean) => void;
	onResolved: () => void;
	onSent: () => void;
	sent: boolean;
}) {
	const { t } = useTranslation();
	const bodyId = useId();
	const [sendState, setSendState] = useState<Busy>("idle");
	const [resolveState, setResolveState] = useState<Busy>("idle");
	const [replying, setReplying] = useState(false);
	const [menuOpen, setMenuOpen] = useState(false);
	const [replyState, setReplyState] = useState<{ busy: boolean; error: string | null }>({ busy: false, error: null });
	useEffect(() => {
		if (!expanded) setReplying(false);
	}, [expanded]);

	const orchestrator = comment.source === "orchestrator";
	const name = orchestrator ? t("inspector.review.orchestrator") : comment.author;
	const location = comment.file ? `${comment.file}${comment.line ? `:${comment.line}` : ""}` : "";
	const shortLocation = comment.file ? `${comment.file.split("/").pop()}${comment.line ? `:${comment.line}` : ""}` : "";
	const preview = summaryPreview(
		commentTextLines(comment.body),
		comment.kind === "summary" && comment.verdict ? [t(verdictKey[comment.verdict]), t(verdictKey[comment.verdict], { lng: "en" })] : [],
	);
	const canSend = Boolean(actions.onSend) && !comment.resolved && !comment.earlier && Boolean(comment.body);
	const canResolve = Boolean(actions.onResolve) && comment.kind === "inline" && !comment.resolved && Boolean(comment.url);
	// A thread takes a reply in place; a provider review or comment is answered by quoting it.
	const replyOffered = Boolean(actions.onReply) && block.provider?.provider === "github"
		&& (comment.kind === "inline" ? Boolean(comment.threadId) : comment.source === "provider");

	const send = async () => {
		if (!actions.onSend || sendState === "busy" || sent) return;
		setSendState("busy");
		try {
			await actions.onSend(comment, block);
			setSendState("idle");
			onSent();
		} catch {
			setSendState("error");
		}
	};
	const resolve = async () => {
		if (!actions.onResolve || resolveState === "busy") return;
		setResolveState("busy");
		try {
			await actions.onResolve(comment, block);
			setResolveState("idle");
			onResolved();
			onExpandedChange(false);
		} catch {
			setResolveState("error");
		}
	};
	const reply = async (body: string, files: File[]) => {
		if (!actions.onReply) return;
		setReplyState({ busy: true, error: null });
		try {
			await actions.onReply(comment, block, body, files);
			setReplyState({ busy: false, error: null });
			setReplying(false);
		} catch (error) {
			setReplyState({ busy: false, error: error instanceof Error ? error.message : t("pr.comments.postFailed") });
			throw error;
		}
	};
	const copy = (value: string) => void navigator.clipboard?.writeText(value).catch(() => {});
	// A click anywhere on a folded row opens it; its buttons and links act on their own.
	const openFromRow = (event: MouseEvent<HTMLElement>) => {
		if (expanded || (event.target as HTMLElement).closest("button, a, [role='menuitem']")) return;
		onExpandedChange(true);
	};

	const where = comment.kind === "summary" && comment.verdict ? (
		<span className={cn("inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap text-2xs font-medium", verdictTone[comment.verdict])}>
			<span aria-hidden="true" className="size-1.5 rounded-full bg-current" />
			{t(verdictKey[comment.verdict])}
		</span>
	) : location ? (
		<span className="inline-flex min-w-0 max-w-full shrink-0 items-center gap-1 rounded-sm bg-muted/70 px-1.5 py-px text-2xs text-foreground/80" title={location}>
			<FileCode2 aria-hidden="true" className="size-icon-2xs shrink-0" />
			<span className="truncate">{expanded ? location : shortLocation}</span>
		</span>
	) : null;

	const menuItems: ReactNode[] = [];
	if (canSend && !sent) menuItems.push(<DropdownMenuItem disabled={sendState === "busy"} key="send" onSelect={() => void send()}>{t("pr.comments.sendToAgent")}</DropdownMenuItem>);
	if (canResolve) menuItems.push(<DropdownMenuItem disabled={resolveState === "busy"} key="resolve" onSelect={() => void resolve()}>{t("pr.comments.resolve")}</DropdownMenuItem>);
	if (comment.file && actions.onViewInFile) menuItems.push(<DropdownMenuItem key="file" onSelect={() => actions.onViewInFile?.(comment, block)}>{t("inspector.viewInFile")}</DropdownMenuItem>);
	if (replyOffered && !comment.resolved) menuItems.push(<DropdownMenuItem key="reply" onSelect={() => { onExpandedChange(true); setReplying(true); }}>{t("pr.comments.reply")}</DropdownMenuItem>);
	if (comment.rerequestReviewerId && actions.onRerequest) {
		const reviewerId = comment.rerequestReviewerId;
		menuItems.push(<DropdownMenuItem key="rerequest" onSelect={() => void actions.onRerequest?.(reviewerId, block).catch(() => {})}>{t("inspector.requestRereviewPR")}</DropdownMenuItem>);
	}
	if (comment.url) {
		const url = comment.url;
		if (menuItems.length > 0) menuItems.push(<DropdownMenuSeparator key="links" />);
		menuItems.push(<DropdownMenuItem key="provider" onSelect={() => window.open(url, "_blank", "noopener,noreferrer")}>{onGitLab(block) ? t("inspector.review.openOnGitLab") : t("pr.comments.openOnProvider")}</DropdownMenuItem>);
		if (actions.onOpenInBrowser) menuItems.push(<DropdownMenuItem key="browser" onSelect={() => actions.onOpenInBrowser?.(url)}>{t("inspector.openInAOBrowser")}</DropdownMenuItem>);
		menuItems.push(<DropdownMenuItem key="copy" onSelect={() => copy(url)}>{t("pr.comments.copyLink")}</DropdownMenuItem>);
	}
	const menu = menuItems.length > 0 ? (
		<DropdownMenu onOpenChange={setMenuOpen}>
			<DropdownMenuTrigger asChild>
				<Button
					aria-label={t("pr.comments.actionsFor", { name })}
					className={cn("-mr-1", !expanded && "opacity-0 group-hover/review-row:opacity-100 group-focus-within/review-row:opacity-100 data-[state=open]:opacity-100")}
					size="icon-xs"
					variant="quiet"
				>
					<MoreVertical aria-hidden="true" className="size-icon-sm" />
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="min-w-44">{menuItems}</DropdownMenuContent>
		</DropdownMenu>
	) : null;

	return (
		<div
			className={cn("group/review-row relative min-w-0 select-none px-3 py-2 text-xs transition-colors", expanded ? "bg-interactive-hover/40" : "cursor-pointer hover:bg-interactive-hover/30")}
			data-kind={comment.kind}
			data-testid="review-comment"
			onClick={openFromRow}
		>
			<div className="flex min-w-0 items-center gap-1.5">
				<Pressable
					aria-controls={expanded ? bodyId : undefined}
					aria-expanded={expanded}
					className="flex min-w-0 flex-1 items-center gap-1.5 rounded-sm focus-visible:ring-2 focus-visible:ring-ring/60"
					onClick={() => onExpandedChange(!expanded)}
				>
					{orchestrator ? <AgentAvatar className="size-4 shrink-0" decorative provider={comment.harness || "reviewer"} /> : <PRAvatar bot={comment.isBot} name={comment.author} size="sm" />}
					{/* The agent logo says which agent reviewed; its name is on hover. */}
					<span className="truncate font-medium text-foreground" title={orchestrator && comment.harness ? `${name} · ${agentLabel(comment.harness)}` : undefined}>{name}</span>
					{!orchestrator && comment.isBot ? <span className="shrink-0 text-micro text-passive">{t("pr.comments.bot")}</span> : null}
					{comment.resolved ? <span className="shrink-0 text-micro text-success">{t("pr.comments.resolvedTag")}</span> : null}
					{comment.earlier ? <span className="shrink-0 text-micro text-passive">{t("inspector.review.earlier")}</span> : null}
					{comment.requestedByAgent ? <span className="min-w-0 truncate text-micro text-passive">{t("inspector.review.requestedByAgent")}</span> : null}
				</Pressable>
				{sent && !expanded ? (
					<span aria-label={t("pr.comments.sentToAgent")} className="shrink-0 whitespace-nowrap text-micro text-passive" role="status">{t("pr.comments.sentToAgent")}</span>
				) : null}
				{/* The time holds the row's right edge; on hover or focus its actions take its place, so nothing moves. */}
				<span className={cn("relative flex h-6 shrink-0 items-center justify-end gap-1", menu && "min-w-6")}>
					{comment.at ? (
						<span
							className={cn("text-micro tabular-nums text-passive", menu && !expanded && (menuOpen ? "invisible" : "group-hover/review-row:invisible group-focus-within/review-row:invisible"))}
							title={comment.at}
						>
							{formatTimeCompact(comment.at)}
						</span>
					) : null}
					{menu ? <span className={cn("flex items-center", !expanded && "absolute inset-y-0 right-0")}>{menu}</span> : null}
				</span>
			</div>
			{expanded ? (
				<div className="flex min-w-0 flex-col gap-1.5 pt-1 pl-5.5" id={bodyId}>
					{where ? <div className="flex min-w-0">{where}</div> : null}
					<div className="min-w-0 cursor-text select-text text-muted-foreground">
						<PRMarkdown body={comment.body} />
					</div>
					{comment.replies.map((replyItem) => (
						<div className="flex min-w-0 flex-col gap-1 border-l-2 border-border-strong pl-2.5" data-testid="review-comment-reply" key={replyItem.id}>
							<span className="flex min-w-0 items-center gap-1.5">
								<PRAvatar bot={replyItem.isBot} name={replyItem.author} size="sm" />
								<span className="truncate font-medium text-foreground">{replyItem.author}</span>
								{replyItem.createdAt ? <span className="text-micro tabular-nums text-passive">{formatTimeCompact(replyItem.createdAt)}</span> : null}
							</span>
							<div className="min-w-0 cursor-text select-text"><PRMarkdown body={replyItem.body} /></div>
						</div>
					))}
					<div className="-ml-1.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 pt-0.5">
						{canSend && sent ? (
							// Done, not an action: reads as a fact where Send to agent was.
							<span aria-label={t("pr.comments.sentToAgent")} className="inline-flex h-6 items-center gap-1.5 px-1.5 text-xs font-medium text-muted-foreground" role="status">
								<Check aria-hidden="true" className="size-icon-sm text-success" />
								{t("pr.comments.sentToAgent")}
							</span>
						) : canSend ? (
							<FootAction disabled={sendState === "busy"} icon={sendState === "busy" ? <Loader2 className="animate-spin" /> : <Send />} onClick={() => void send()}>
								{t("pr.comments.sendToAgent")}
							</FootAction>
						) : null}
						{replyOffered && !replying ? <FootAction icon={<Reply />} onClick={() => setReplying(true)}>{t("pr.comments.reply")}</FootAction> : null}
						{comment.url ? (
							<FootAction icon={<ArrowUpRight />} onClick={() => window.open(comment.url, "_blank", "noopener,noreferrer")}>{onGitLab(block) ? t("inspector.review.openOnGitLab") : t("pr.comments.openOnProvider")}</FootAction>
						) : null}
						<FootAction className="ml-auto" iconAfter={<ChevronUp />} onClick={() => onExpandedChange(false)}>{t("pr.comments.collapse")}</FootAction>
					</div>
					{replying ? (
						<PRCommentComposer
							autoFocus
							busy={replyState.busy}
							error={replyState.error}
							initial={comment.kind === "inline" ? "" : replyQuote(comment.author, comment.body)}
							onCancel={() => setReplying(false)}
							onEdit={() => setReplyState((current) => (current.error ? { ...current, error: null } : current))}
							onSubmit={reply}
							placeholder={t("pr.comments.replyPlaceholder")}
							submitLabel={t("pr.comments.reply")}
						/>
					) : null}
					{sendState === "error" ? <p className="m-0 text-2xs font-medium text-error" role="status">{t("pr.comments.sendFailed")}</p> : null}
					{resolveState === "error" ? <p className="m-0 text-2xs font-medium text-error" role="status">{t("pr.comments.resolveFailed")}</p> : null}
				</div>
			) : (
				<div className="flex min-w-0 items-center gap-1.5 pt-0.5 pl-5.5 text-muted-foreground">
					{where}
					<span className="min-w-0 truncate">{preview}</span>
				</div>
			)}
			{!expanded && (sendState === "error" || resolveState === "error") ? (
				<p className="m-0 pt-1 pl-5.5 text-2xs font-medium text-error" role="status">
					{sendState === "error" ? t("pr.comments.sendFailed") : t("pr.comments.resolveFailed")}
				</p>
			) : null}
		</div>
	);
}

/** A quiet text action under an opened comment, as on the PR tab's cards. */
function FootAction({
	children,
	className,
	disabled,
	icon,
	iconAfter,
	onClick,
}: {
	children: ReactNode;
	className?: string;
	disabled?: boolean;
	icon?: ReactNode;
	iconAfter?: ReactNode;
	onClick: () => void;
}) {
	return (
		<Button
			className={cn("gap-1.5 px-1.5 font-medium [&_svg]:size-icon-sm", className)}
			disabled={disabled}
			onClick={onClick}
			size="xs"
			variant="quiet"
		>
			{icon}
			{children}
			{iconAfter}
		</Button>
	);
}

/**
 * What a reply to a review or top-level comment opens with: its first
 * paragraph quoted (cut at a word past 240 characters) and an @mention, as the
 * PR tab's Reply writes it, so the reply nests under it on the provider.
 */
function replyQuote(author: string, body: string): string {
	let excerpt = body.trim().split(/\n\s*\n/)[0] ?? "";
	if (excerpt.length > 240) {
		const space = excerpt.lastIndexOf(" ", 240);
		excerpt = excerpt.slice(0, space > 0 ? space : 240);
	}
	const quoted = excerpt.split("\n").map((line) => `> ${line}`.trimEnd()).join("\n");
	return `${quoted}\n\n@${author} `;
}
