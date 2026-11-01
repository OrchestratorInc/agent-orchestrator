import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronRight, CircleAlert, CircleCheck, Loader2, MessageSquare, MoreHorizontal } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { sessionScmSummaryQueryKey } from "../hooks/useSessionScmSummary";
import { useSendToSessionWorker } from "../hooks/useSendToSessionWorker";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { apiErrorMessage } from "../lib/api-client";
import { formatTimeCompact } from "../lib/format-time";
import { clientForSessionHost } from "../lib/host-clients";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import { cn } from "../lib/utils";
import { sanitizeWorkerMessagePart } from "../lib/worker-message";
import type { WorkspaceSession } from "../types/workspace";
import { PRCommentComposer, type PRAttachmentUpload } from "./PRCommentComposer";
import { PRMarkdown } from "./PRMarkdown";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";

type Thread = NonNullable<SessionPRSummary["threads"]>[number];
type Comment = Thread["comments"][number];
type Review = NonNullable<SessionPRSummary["review"]["reviews"]>[number];

type Item =
	| { kind: "comment"; key: string; at: number; comment: Comment }
	| { kind: "review"; key: string; at: number; review: Review }
	| { kind: "thread"; key: string; at: number; thread: Thread };

const time = (value?: string | null) => {
	const parsed = value ? Date.parse(value) : Number.NaN;
	return Number.isNaN(parsed) ? 0 : parsed;
};

// The Reviews tab's fold header and row action, so the PR's comments read as
// the same family as its open and resolved review comments. A button takes its
// parent's font (styles.css), so the containers carry the text size.
const foldHeaderClass = "flex min-h-8 w-full min-w-0 items-center gap-2 rounded-md px-1.5 py-1 text-left text-xs font-medium leading-none text-muted-foreground transition-colors hover:bg-interactive-hover/20 hover:text-foreground";
const rowActionClass = "inline-flex size-7 shrink-0 items-center justify-center rounded-md border border-border/70 text-muted-foreground transition-colors hover:border-border-strong hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60";

/** A first line to stand for the comment while its row is folded. */
function preview(body: string): string {
	const line = body.split("\n").map((value) => value.replace(/^[>#\-*\s]+/, "").trim()).find(Boolean) ?? "";
	return line.replace(/[*_`]/g, "");
}

function Fold({ children, count, defaultOpen, label, testId }: { children: ReactNode; count: number; defaultOpen: boolean; label: string; testId: string }) {
	const [open, setOpen] = useState(defaultOpen);
	const id = useId();
	if (count === 0) return null;
	return (
		<section className="min-w-0 text-xs" data-testid={testId}>
			<button aria-controls={open ? id : undefined} aria-expanded={open} className={foldHeaderClass} onClick={() => setOpen(!open)} type="button">
				<ChevronRight aria-hidden="true" className={cn("size-icon-2xs shrink-0 transition-transform motion-reduce:transition-none", open && "rotate-90")} />
				<span>{label} · {count}</span>
			</button>
			{open ? <div className="divide-y divide-border/60 pl-1.5" id={id}>{children}</div> : null}
		</section>
	);
}

type RowAction = { label: string; onSelect: () => void; disabled?: boolean } | "separator";

/**
 * One comment, review or thread as a row: where or who, when, and its first
 * line; opening the row shows all of it. Its actions sit in a menu, like an
 * inline review comment's in the Reviews tab.
 */
function Row({ actions, at, author, bot, children, label, location, note, sent, status, text }: {
	actions: RowAction[];
	at?: string | null;
	author: string;
	bot?: boolean;
	children?: ReactNode;
	/** Names the row for its action menu. */
	label: string;
	/** A review thread's file and line, which leads the row. */
	location?: string;
	/** Under the row whether or not it is open, such as a reply box. */
	note?: ReactNode;
	sent?: boolean;
	/** A review's verdict, beside the author. */
	status?: ReactNode;
	text: string;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const bodyId = useId();
	return (
		<div className="flex min-w-0 flex-col gap-1.5 py-2 text-xs">
			<div className="flex min-w-0 items-start gap-2">
				<button
					aria-controls={open ? bodyId : undefined}
					aria-expanded={open}
					className="flex min-w-0 flex-1 flex-col gap-1 rounded-md py-0.5 text-left text-xs transition-colors hover:bg-interactive-hover/20"
					onClick={() => setOpen(!open)}
					type="button"
				>
					<span className="flex w-full min-w-0 items-center gap-1.5">
						<ChevronRight aria-hidden="true" className={cn("size-icon-2xs shrink-0 text-passive transition-transform motion-reduce:transition-none", open && "rotate-90")} />
						{location ? <span className="min-w-0 truncate font-mono text-2xs font-semibold text-foreground" title={location}>{location}</span> : null}
						<span className={cn("min-w-0 truncate", location ? "text-muted-foreground" : "font-medium text-foreground")}>{author}</span>
						{bot ? <span className="shrink-0 text-micro text-passive">{t("pr.comments.bot")}</span> : null}
						{status}
						{at ? <span className="ml-auto shrink-0 text-micro text-passive">{formatTimeCompact(at)}</span> : null}
					</span>
					{open ? null : <span className="block w-full min-w-0 truncate pl-4 text-muted-foreground">{text}</span>}
				</button>
				{sent ? (
					<span aria-label={t("pr.comments.sentToAgent")} className="inline-flex size-7 shrink-0 items-center justify-center text-success" role="status" title={t("pr.comments.sentToAgent")}>
						<Check aria-hidden="true" className="size-icon-xs" />
					</span>
				) : null}
				{actions.length > 0 ? (
					<DropdownMenu>
						<DropdownMenuTrigger asChild>
							<button aria-label={t("pr.comments.actionsFor", { name: label })} className={rowActionClass} type="button">
								<MoreHorizontal aria-hidden="true" className="size-icon-xs" />
							</button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" className="min-w-40">
							{actions.map((action, index) => action === "separator"
								? <DropdownMenuSeparator key={`separator-${index}`} />
								: <DropdownMenuItem disabled={action.disabled} key={action.label} onSelect={action.onSelect}>{action.label}</DropdownMenuItem>)}
						</DropdownMenuContent>
					</DropdownMenu>
				) : null}
			</div>
			{open ? <div className="flex min-w-0 flex-col gap-2 pl-4" id={bodyId}>{children}</div> : null}
			{note ? <div className="flex min-w-0 flex-col gap-1 pl-4">{note}</div> : null}
		</div>
	);
}

/**
 * The PR's conversation under its card, folded the way the Reviews tab folds
 * review comments: open threads and the conversation first, then what came
 * from bots and what is resolved, closed until asked for. Each row opens to
 * the whole comment or thread; its menu replies, resolves, sends it to the
 * session's agent, or opens it on the provider. Reading is free (the daemon
 * already stored it); only posting calls the provider.
 */
export function PRComments({ hostId, pr, session }: { hostId?: string; pr: SessionPRSummary; session: WorkspaceSession }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [replying, setReplying] = useState<string | null>(null);
	const [sent, setSent] = useState<Set<string>>(() => new Set());
	const [actionError, setActionError] = useState<{ key: string; message: string } | null>(null);
	const sendToWorker = useSendToSessionWorker(session, hostId);
	const canWrite = !session.cloud && !usePreviewData && pr.provider === "github" && Boolean(pr.url);
	const refresh = () => Promise.all([
		queryClient.invalidateQueries({ queryKey: sessionScmSummaryQueryKey(session.id, hostId) }),
		queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) }),
	]);

	const post = useMutation({
		mutationFn: async ({ body, threadId }: { body: string; threadId?: string }) => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/comments", {
				params: { path: { id: String(pr.number) } },
				body: { prUrl: pr.url, body, ...(threadId ? { threadId } : {}) },
			});
			if (error) throw new Error(apiErrorMessage(error, t("pr.comments.postFailed")));
		},
		onSuccess: async () => {
			setReplying(null);
			await refresh();
		},
	});
	const upload: PRAttachmentUpload | undefined = canWrite ? async (file) => {
		const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/attachments", {
			params: { path: { id: String(pr.number) } },
			body: { prUrl: pr.url, ...file },
		});
		if (error || !data) throw new Error(apiErrorMessage(error, t("pr.comments.attach.failed", { name: file.name })));
		return data.markdown;
	} : undefined;
	const resolve = useMutation({
		mutationFn: async (thread: Thread) => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/comments/resolve", {
				params: { path: { sessionId: session.id } },
				body: { pullRequestUrl: pr.url, commentUrl: thread.comments[0]?.url ?? "" },
			});
			if (error) throw new Error(apiErrorMessage(error, t("pr.comments.resolveFailed")));
		},
		onSuccess: refresh,
		onError: (error, thread) => setActionError({ key: `t:${thread.id}`, message: error.message }),
	});

	const send = async (key: string, author: string, body: string, url?: string, location?: string) => {
		setActionError(null);
		try {
			await sendToWorker(
				[
					"A comment on your pull request may need a change. Read it, and if it asks for one, make it, run the relevant tests, commit, and push the branch.",
					"",
					`From: @${sanitizeWorkerMessagePart(author)}`,
					...(location ? [`Location: ${sanitizeWorkerMessagePart(location)}`] : []),
					"",
					"Comment:",
					sanitizeWorkerMessagePart(body.trim()),
					...(url ? ["", `Comment URL: ${sanitizeWorkerMessagePart(url)}`] : []),
				].join("\n"),
				t("pr.comments.sendFailed"),
			);
			setSent((current) => new Set(current).add(key));
		} catch (error) {
			setActionError({ key, message: error instanceof Error ? error.message : t("pr.comments.sendFailed") });
		}
	};
	const linkActions = (url?: string): RowAction[] => url ? [
		"separator",
		{ label: t("pr.comments.openOnProvider"), onSelect: () => window.open(url, "_blank", "noopener,noreferrer") },
		{ label: t("pr.comments.copyLink"), onSelect: () => void navigator.clipboard?.writeText(url).catch(() => {}) },
	] : [];

	const items: Item[] = [
		...(pr.conversation ?? []).map((comment): Item => ({ kind: "comment", key: `c:${comment.id}`, at: time(comment.createdAt), comment })),
		...(pr.review.reviews ?? []).filter((review) => review.body?.trim()).map((review): Item => ({ kind: "review", key: `r:${review.reviewUrl ?? review.reviewerId + review.submittedAt}`, at: time(review.submittedAt), review })),
		...(pr.threads ?? []).filter((thread) => thread.comments.length > 0).map((thread): Item => ({ kind: "thread", key: `t:${thread.id}`, at: time(thread.comments[0]?.createdAt), thread })),
	].sort((a, b) => a.at - b.at);
	const isBot = (item: Item) => item.kind === "comment" ? item.comment.isBot : item.kind === "review" ? item.review.isBot === true : item.thread.comments[0]?.isBot === true;
	const resolved = items.filter((item) => item.kind === "thread" && item.thread.resolved);
	const live = items.filter((item) => !(item.kind === "thread" && item.thread.resolved));
	const openThreads = live.filter((item) => item.kind === "thread" && !isBot(item));
	const conversation = live.filter((item) => item.kind !== "thread" && !isBot(item));
	const bots = live.filter(isBot);

	const replyBox = (item: Item, threadId?: string, initial?: string) => replying === item.key ? (
		<PRCommentComposer
			autoFocus
			busy={post.isPending}
			error={post.error instanceof Error ? post.error.message : null}
			initial={initial}
			onCancel={() => { setReplying(null); post.reset(); }}
			onSubmit={(body) => post.mutateAsync({ body, threadId })}
			placeholder={t("pr.comments.replyPlaceholder")}
			submitLabel={threadId ? t("pr.comments.reply") : t("pr.comments.comment")}
			upload={upload}
		/>
	) : null;
	const errorFor = (key: string) => actionError?.key === key ? <p className="m-0 text-2xs font-medium text-error" role="status">{actionError.message}</p> : null;

	const row = (item: Item) => {
		if (item.kind === "thread") {
			const { thread } = item;
			const first = thread.comments[0];
			const location = thread.path ? `${thread.path}${thread.line ? `:${thread.line}` : ""}` : t("pr.comments.general");
			const replies = thread.comments.length - 1;
			const resolving = resolve.isPending && resolve.variables?.id === thread.id;
			return (
				<div data-kind="thread" key={item.key}>
					<Row
						actions={[
							...(canWrite ? [{ label: t("pr.comments.reply"), onSelect: () => setReplying(item.key) }] : []),
							...(!thread.resolved ? [{ label: t("pr.comments.sendToAgent"), onSelect: () => void send(item.key, first?.author ?? "", thread.comments.map((comment) => `@${comment.author}: ${comment.body}`).join("\n\n"), first?.url, location), disabled: sent.has(item.key) }] : []),
							...(canWrite && !thread.resolved ? [{ label: t("pr.comments.resolve"), onSelect: () => { setActionError(null); resolve.mutate(thread); }, disabled: resolve.isPending }] : []),
							...linkActions(first?.url),
						]}
						at={first?.createdAt}
						author={replies > 0 ? t("pr.comments.authorWithReplies", { name: first?.author ?? "", count: replies }) : first?.author ?? ""}
						bot={first?.isBot}
						label={location}
						location={location}
						note={replying === item.key || actionError?.key === item.key || resolving ? (
							<>
								{replyBox(item, thread.id)}
								{errorFor(item.key)}
								{resolving ? <p className="m-0 inline-flex items-center gap-1 text-2xs text-passive"><Loader2 aria-hidden="true" className="size-icon-2xs animate-spin" />{t("pr.comments.resolving")}</p> : null}
							</>
						) : null}
						sent={sent.has(item.key)}
						text={preview(first?.body ?? "")}
					>
						{thread.comments.map((comment, index) => (
							<div className={cn("flex min-w-0 flex-col gap-0.5", index > 0 && "border-l-2 border-border pl-2")} key={comment.id}>
								{index > 0 ? <span className="text-micro text-passive"><span className="font-medium text-muted-foreground">{comment.author}</span>{comment.createdAt ? ` · ${formatTimeCompact(comment.createdAt)}` : ""}</span> : null}
								<PRMarkdown body={comment.body} />
							</div>
						))}
					</Row>
				</div>
			);
		}
		if (item.kind === "review") {
			const { review } = item;
			const verdict = review.verdict === "approved" || review.verdict === "changes_requested" ? review.verdict : "commented";
			return (
				<div data-kind="review" key={item.key}>
					<Row
						actions={[
							{ label: t("pr.comments.sendToAgent"), onSelect: () => void send(item.key, review.reviewerId, review.body ?? "", review.reviewUrl), disabled: sent.has(item.key) },
							...linkActions(review.reviewUrl),
						]}
						at={review.submittedAt}
						author={review.reviewerId}
						bot={review.isBot}
						label={review.reviewerId}
						note={errorFor(item.key)}
						sent={sent.has(item.key)}
						status={
							<span className={cn("inline-flex shrink-0 items-center gap-0.5 text-micro font-medium", verdict === "approved" ? "text-success" : verdict === "changes_requested" ? "text-status-in-review" : "text-passive")}>
								{verdict === "approved" ? <CircleCheck aria-hidden="true" className="size-icon-2xs" /> : verdict === "changes_requested" ? <CircleAlert aria-hidden="true" className="size-icon-2xs" /> : null}
								{t(`pr.comments.verdict.${verdict}`)}
							</span>
						}
						text={preview(review.body ?? "")}
					>
						<PRMarkdown body={review.body ?? ""} />
					</Row>
				</div>
			);
		}
		const { comment } = item;
		return (
			<div data-kind="comment" key={item.key}>
				<Row
					actions={[
						...(canWrite ? [{ label: t("pr.comments.quoteReply"), onSelect: () => setReplying(item.key) }] : []),
						{ label: t("pr.comments.sendToAgent"), onSelect: () => void send(item.key, comment.author, comment.body, comment.url), disabled: sent.has(item.key) },
						...linkActions(comment.url),
					]}
					at={comment.createdAt}
					author={comment.author}
					bot={comment.isBot}
					label={comment.author}
					note={replying === item.key || actionError?.key === item.key ? (
						<>
							{replyBox(item, undefined, `${comment.body.trim().split("\n").map((line) => `> ${line}`).join("\n")}\n\n@${comment.author} `)}
							{errorFor(item.key)}
						</>
					) : null}
					sent={sent.has(item.key)}
					text={preview(comment.body)}
				>
					<PRMarkdown body={comment.body} />
				</Row>
			</div>
		);
	};

	return (
		<div className="flex min-w-0 flex-col gap-1 border-t border-(--color-border-settings-input) pt-2 pb-2 text-xs" data-testid="pr-comments">
			<h4 className="m-0 flex min-w-0 items-center gap-1 px-1.5 text-micro font-medium text-muted-foreground">
				<MessageSquare aria-hidden="true" className="size-icon-2xs shrink-0" />
				{t("pr.comments.title")}
			</h4>
			{items.length === 0 ? (
				<p className="m-0 px-1.5 text-xs text-passive">{pr.detailsObservedAt ? t("pr.comments.none") : t("pr.description.pending")}</p>
			) : null}
			<Fold count={openThreads.length} defaultOpen label={t("pr.comments.openThreads")} testId="pr-comments-open">{openThreads.map(row)}</Fold>
			<Fold count={conversation.length} defaultOpen label={t("pr.comments.conversation")} testId="pr-comments-conversation">{conversation.map(row)}</Fold>
			<Fold count={bots.length} defaultOpen={false} label={t("pr.comments.fromBots")} testId="pr-comments-bots">{bots.map(row)}</Fold>
			<Fold count={resolved.length} defaultOpen={false} label={t("pr.comments.resolvedThreads")} testId="pr-comments-resolved">{resolved.map(row)}</Fold>
			{canWrite ? (
				<div className="mt-1 px-1.5">
					<PRCommentComposer
						busy={post.isPending && replying === null}
						compact
						error={replying === null && post.error instanceof Error ? post.error.message : null}
						onSubmit={(body) => post.mutateAsync({ body })}
						placeholder={t("pr.comments.addPlaceholder")}
						submitLabel={t("pr.comments.comment")}
						upload={upload}
					/>
				</div>
			) : null}
		</div>
	);
}
