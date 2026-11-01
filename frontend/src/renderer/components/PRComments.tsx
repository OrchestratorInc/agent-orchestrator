import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowUpRight, Bot, CheckCheck, CircleAlert, CircleCheck, FileCode2, Loader2, MessageSquare, Reply, Send } from "lucide-react";
import { useId, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
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
import { AppLink } from "./AppLink";
import { PRDescriptionImage } from "./PRDescriptionImage";
import { ProductExternalLink } from "./ProductExternalLink";
import { Button } from "./ui/button";

type Thread = NonNullable<SessionPRSummary["threads"]>[number];
type Comment = Thread["comments"][number];
type Filter = "all" | "humans" | "bots";

/** Items listed before "Show N earlier"; the newest stay in sight. */
export const PR_COMMENTS_SHOWN = 4;

type Item =
	| { kind: "comment"; key: string; at: number; bot: boolean; comment: Comment }
	| { kind: "review"; key: string; at: number; bot: boolean; review: NonNullable<SessionPRSummary["review"]["reviews"]>[number] }
	| { kind: "thread"; key: string; at: number; bot: boolean; thread: Thread };

const time = (value?: string | null) => {
	const parsed = value ? Date.parse(value) : Number.NaN;
	return Number.isNaN(parsed) ? 0 : parsed;
};

// Review-summary typography, so a comment reads like the description above it.
const bodyClass = cn(
	"min-w-0 select-text break-words text-xs leading-relaxed text-muted-foreground",
	"[&_a]:font-medium [&_a]:text-foreground [&_a]:underline [&_a]:underline-offset-2",
	"[&_code]:rounded [&_code]:bg-muted/55 [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-foreground",
	"[&_li]:my-0.5 [&_ol]:my-1 [&_ol]:list-decimal [&_ol]:pl-4 [&_p]:my-1 [&_pre]:my-1.5 [&_pre]:overflow-x-auto [&_pre]:rounded-md",
	"[&_pre]:border [&_pre]:border-border [&_pre]:bg-muted/35 [&_pre]:p-2 [&_pre_code]:bg-transparent [&_pre_code]:p-0",
	"[&_strong]:text-foreground [&_ul]:my-1 [&_ul]:list-disc [&_ul]:pl-4 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
	"[&_blockquote]:border-l-2 [&_blockquote]:border-border [&_blockquote]:pl-2",
);

// Outside the component, so a re-render keeps an open image lightbox open.
const markdownComponents: Components = {
	a: ({ href, children }) => <AppLink href={href} rel="noopener noreferrer" target="_blank">{children}</AppLink>,
	img: ({ alt, src }) => <PRDescriptionImage alt={alt} src={typeof src === "string" ? src : undefined} />,
};

function CommentBody({ body }: { body: string }) {
	const [open, setOpen] = useState(false);
	const id = useId();
	const { t } = useTranslation();
	const long = body.length > 280 || body.split("\n").length > 5;
	return (
		<>
			<div className={cn(bodyClass, long && !open && "line-clamp-4")} id={id}>
				{/* Provider content: react-markdown never renders raw HTML. */}
				<ReactMarkdown components={markdownComponents} remarkPlugins={[remarkGfm]}>
					{body}
				</ReactMarkdown>
			</div>
			{long ? (
				<span className="text-micro"><button aria-controls={id} aria-expanded={open} className="self-start text-micro font-medium text-passive hover:text-foreground" onClick={() => setOpen(!open)} type="button">
					{open ? t("inspector.showLess") : t("inspector.showMore")}
				</button></span>
			) : null}
		</>
	);
}

function Author({ name, bot, at, url }: { name: string; bot: boolean; at?: string | null; url?: string }) {
	const { t } = useTranslation();
	return (
		<div className="flex min-w-0 items-center gap-1.5 text-xs">
			<span aria-hidden="true" className="flex size-4 shrink-0 items-center justify-center rounded-full bg-muted text-[9px] font-semibold uppercase text-muted-foreground">
				{bot ? <Bot className="size-2.5" /> : name.slice(0, 1)}
			</span>
			<span className="min-w-0 truncate font-medium text-foreground">{name}</span>
			{bot ? <span className="shrink-0 text-micro text-passive">{t("pr.comments.bot")}</span> : null}
			{at ? <span className="shrink-0 text-micro text-passive">{formatTimeCompact(at)}</span> : null}
			{url ? (
				<ProductExternalLink ariaLabel={t("pr.comments.openOnProvider")} className="ml-auto shrink-0 text-passive hover:text-foreground" href={url}>
					<ArrowUpRight aria-hidden="true" className="size-icon-2xs" />
				</ProductExternalLink>
			) : null}
		</div>
	);
}

function Composer({ autoFocus, busy, error, initial = "", onCancel, onSubmit, placeholder, submitLabel }: {
	autoFocus?: boolean;
	busy: boolean;
	error?: string | null;
	initial?: string;
	onCancel?: () => void;
	onSubmit: (body: string) => Promise<unknown>;
	placeholder: string;
	submitLabel: string;
}) {
	const { t } = useTranslation();
	const [value, setValue] = useState(initial);
	const submit = async () => {
		if (!value.trim() || busy) return;
		try {
			await onSubmit(value.trim());
			setValue("");
		} catch {
			// The mutation shows the error; the draft stays so nothing typed is lost.
		}
	};
	const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
		if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
			event.preventDefault();
			void submit();
		} else if (event.key === "Escape" && onCancel) {
			event.preventDefault();
			event.stopPropagation();
			onCancel();
		}
	};
	return (
		<div className="flex min-w-0 flex-col gap-1.5">
			<textarea
				aria-label={placeholder}
				autoFocus={autoFocus}
				className="min-h-16 w-full resize-y rounded-md border border-border bg-background px-2 py-1.5 text-xs leading-relaxed text-foreground placeholder:text-passive focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
				disabled={busy}
				onChange={(event) => setValue(event.target.value)}
				onKeyDown={onKeyDown}
				placeholder={placeholder}
				value={value}
			/>
			{error ? <p className="m-0 text-xs text-error" role="status">{error}</p> : null}
			<div className="flex items-center justify-end gap-1.5">
				<span className="mr-auto text-micro text-passive">{t("pr.comments.markdownHint")}</span>
				{onCancel ? <Button className="px-2 text-xs" onClick={onCancel} size="sm" type="button" variant="ghost">{t("pr.comments.cancel")}</Button> : null}
				<Button className="gap-1 px-2 text-xs" disabled={busy || !value.trim()} onClick={() => void submit()} size="sm" type="button" variant="secondary">
					{busy ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : null}
					{submitLabel}
				</Button>
			</div>
		</div>
	);
}

function SendToAgent({ message }: { message: () => Promise<void> }) {
	const { t } = useTranslation();
	const send = useMutation({ mutationFn: message });
	return (
		<button
			className="inline-flex items-center gap-1 text-micro font-medium text-passive transition-colors hover:text-foreground disabled:opacity-60"
			disabled={send.isPending || send.isSuccess}
			onClick={() => send.mutate()}
			title={send.error instanceof Error ? send.error.message : undefined}
			type="button"
		>
			{send.isPending ? <Loader2 aria-hidden="true" className="size-icon-2xs animate-spin" /> : <Send aria-hidden="true" className="size-icon-2xs" />}
			{send.isSuccess ? t("pr.comments.sentToAgent") : send.isError ? t("pr.comments.sendFailed") : t("pr.comments.sendToAgent")}
		</button>
	);
}

/**
 * The PR's conversation in the sidebar, like a PR page's: review summaries,
 * review threads on a line, and top-level comments, filtered to humans or
 * bots. A thread takes a reply and can be resolved; any item can go to the
 * session's agent; a new comment posts from the box under the list. Reading
 * is free (the daemon already stored it); only writing calls the provider.
 */
export function PRComments({ hostId, pr, session }: { hostId?: string; pr: SessionPRSummary; session: WorkspaceSession }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [filter, setFilter] = useState<Filter>("all");
	const [showAll, setShowAll] = useState(false);
	const [replying, setReplying] = useState<string | null>(null);
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
	const resolve = useMutation({
		mutationFn: async (thread: Thread) => {
			const first = thread.comments[0];
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/comments/resolve", {
				params: { path: { sessionId: session.id } },
				body: { pullRequestUrl: pr.url, commentUrl: first?.url ?? "" },
			});
			if (error) throw new Error(apiErrorMessage(error, t("pr.comments.resolveFailed")));
		},
		onSuccess: refresh,
	});

	const items: Item[] = [
		...(pr.conversation ?? []).map((comment): Item => ({ kind: "comment", key: `c:${comment.id}`, at: time(comment.createdAt), bot: comment.isBot, comment })),
		...(pr.review.reviews ?? []).filter((review) => review.body?.trim()).map((review): Item => ({ kind: "review", key: `r:${review.reviewUrl ?? review.reviewerId + review.submittedAt}`, at: time(review.submittedAt), bot: review.isBot === true, review })),
		...(pr.threads ?? []).filter((thread) => thread.comments.length > 0).map((thread): Item => ({ kind: "thread", key: `t:${thread.id}`, at: time(thread.comments[0]?.createdAt), bot: thread.comments[0]?.isBot === true, thread })),
	].sort((a, b) => a.at - b.at);
	const humans = items.filter((item) => !item.bot).length;
	const bots = items.length - humans;
	const filtered = items.filter((item) => filter === "all" || (filter === "bots") === item.bot);
	const hidden = showAll ? 0 : Math.max(0, filtered.length - PR_COMMENTS_SHOWN);
	const shown = filtered.slice(hidden);
	const unresolved = (pr.threads ?? []).filter((thread) => !thread.resolved).length;

	const sendComment = (author: string, body: string, url?: string, location?: string) => () => sendToWorker(
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

	const tabs: { id: Filter; label: string; count: number }[] = [
		{ id: "all", label: t("pr.comments.filter.all"), count: items.length },
		{ id: "humans", label: t("pr.comments.filter.humans"), count: humans },
		{ id: "bots", label: t("pr.comments.filter.bots"), count: bots },
	];

	return (
		<div className="flex min-w-0 flex-col gap-2 border-t border-(--color-border-settings-input) pt-2 pb-2" data-testid="pr-comments">
			<div className="flex min-w-0 items-center gap-1.5 text-micro font-medium text-muted-foreground">
				<MessageSquare aria-hidden="true" className="size-icon-2xs shrink-0" />
				<span>{t("pr.comments.title")}</span>
				<span className="text-passive">{items.length}</span>
				{unresolved > 0 ? <span className="ml-auto text-status-in-review">{t("pr.comments.unresolved", { count: unresolved })}</span> : null}
			</div>
			{items.length > 0 ? (
				<div aria-label={t("pr.comments.filterLabel")} className="grid grid-cols-3 rounded-md bg-muted/50 p-0.5 text-micro" role="tablist">
					{tabs.map((tab) => (
						<button
							aria-selected={filter === tab.id}
							className={cn("rounded-sm px-1.5 py-1 font-medium transition-colors", filter === tab.id ? "bg-background text-foreground shadow-xs" : "text-muted-foreground hover:text-foreground")}
							key={tab.id}
							onClick={() => { setFilter(tab.id); setShowAll(false); }}
							role="tab"
							type="button"
						>
							{tab.label} <span className="text-passive">{tab.count}</span>
						</button>
					))}
				</div>
			) : null}
			{hidden > 0 ? (
				<span className="self-center text-micro">
					<button className="font-medium text-passive hover:text-foreground" onClick={() => setShowAll(true)} type="button">
						{t("pr.comments.showEarlier", { count: hidden })}
					</button>
				</span>
			) : null}
			{filtered.length === 0 ? (
				<p className="m-0 text-xs text-passive">{items.length === 0 ? (pr.detailsObservedAt ? t("pr.comments.none") : t("pr.description.pending")) : t("pr.comments.noneHere")}</p>
			) : null}
			<ol className="m-0 flex min-w-0 list-none flex-col gap-2 p-0">
				{shown.map((item) => (
					<li className={cn("flex min-w-0 flex-col gap-1 rounded-md border border-border/60 bg-background px-2.5 py-2", item.kind === "thread" && item.thread.resolved && "opacity-70")} data-kind={item.kind} key={item.key}>
						{item.kind === "comment" ? (
							<>
								<Author at={item.comment.createdAt} bot={item.bot} name={item.comment.author} url={item.comment.url} />
								<CommentBody body={item.comment.body} />
								<div className="flex items-center gap-3 text-micro">
									{canWrite ? (
										<button className="inline-flex items-center gap-1 text-micro font-medium text-passive hover:text-foreground" onClick={() => setReplying(item.key)} type="button">
											<Reply aria-hidden="true" className="size-icon-2xs" />
											{t("pr.comments.quoteReply")}
										</button>
									) : null}
									<SendToAgent message={sendComment(item.comment.author, item.comment.body, item.comment.url)} />
								</div>
								{replying === item.key ? (
									<Composer
										autoFocus
										busy={post.isPending}
										error={post.error instanceof Error ? post.error.message : null}
										initial={`${item.comment.body.trim().split("\n").map((line) => `> ${line}`).join("\n")}\n\n@${item.comment.author} `}
										onCancel={() => { setReplying(null); post.reset(); }}
										onSubmit={(body) => post.mutateAsync({ body })}
										placeholder={t("pr.comments.replyPlaceholder")}
										submitLabel={t("pr.comments.comment")}
									/>
								) : null}
							</>
						) : item.kind === "review" ? (
							<>
								<Author at={item.review.submittedAt} bot={item.bot} name={item.review.reviewerId} url={item.review.reviewUrl} />
								<span className={cn("inline-flex items-center gap-1 text-micro font-medium", item.review.verdict === "approved" ? "text-success" : item.review.verdict === "changes_requested" ? "text-status-in-review" : "text-passive")}>
									{item.review.verdict === "approved" ? <CircleCheck aria-hidden="true" className="size-icon-2xs" /> : item.review.verdict === "changes_requested" ? <CircleAlert aria-hidden="true" className="size-icon-2xs" /> : <MessageSquare aria-hidden="true" className="size-icon-2xs" />}
									{t(`pr.comments.verdict.${item.review.verdict === "approved" || item.review.verdict === "changes_requested" ? item.review.verdict : "commented"}`)}
								</span>
								<CommentBody body={item.review.body ?? ""} />
								<div className="text-micro"><SendToAgent message={sendComment(item.review.reviewerId, item.review.body ?? "", item.review.reviewUrl)} /></div>
							</>
						) : (
							<>
								<div className="flex min-w-0 items-center gap-1 font-mono text-micro text-muted-foreground">
									<FileCode2 aria-hidden="true" className="size-icon-2xs shrink-0" />
									<span className="min-w-0 truncate">{item.thread.path ? `${item.thread.path}${item.thread.line ? `:${item.thread.line}` : ""}` : t("pr.comments.general")}</span>
									{item.thread.resolved ? <span className="ml-auto shrink-0 font-sans text-success">{t("pr.comments.resolved")}</span> : null}
								</div>
								<ol className="m-0 flex list-none flex-col gap-2 p-0">
									{item.thread.comments.map((comment, index) => (
										<li className={cn("flex min-w-0 flex-col gap-1", index > 0 && "border-l-2 border-border pl-2")} key={comment.id}>
											<Author at={comment.createdAt} bot={comment.isBot} name={comment.author} url={comment.url} />
											<CommentBody body={comment.body} />
										</li>
									))}
								</ol>
								<div className="flex flex-wrap items-center gap-3 text-micro">
									{canWrite ? (
										<button className="inline-flex items-center gap-1 text-micro font-medium text-passive hover:text-foreground" onClick={() => setReplying(item.key)} type="button">
											<Reply aria-hidden="true" className="size-icon-2xs" />
											{t("pr.comments.reply")}
										</button>
									) : null}
									{canWrite && !item.thread.resolved ? (
										<button
											className="inline-flex items-center gap-1 text-micro font-medium text-passive hover:text-foreground disabled:opacity-60"
											disabled={resolve.isPending}
											onClick={() => resolve.mutate(item.thread)}
											type="button"
										>
											{resolve.isPending && resolve.variables?.id === item.thread.id ? <Loader2 aria-hidden="true" className="size-icon-2xs animate-spin" /> : <CheckCheck aria-hidden="true" className="size-icon-2xs" />}
											{t("pr.comments.resolve")}
										</button>
									) : null}
									{!item.thread.resolved ? (
										<SendToAgent
											message={sendComment(
												item.thread.comments[0]?.author ?? "",
												item.thread.comments.map((comment) => `@${comment.author}: ${comment.body}`).join("\n\n"),
												item.thread.comments[0]?.url,
												item.thread.path ? `${item.thread.path}${item.thread.line ? `:${item.thread.line}` : ""}` : undefined,
											)}
										/>
									) : null}
								</div>
								{resolve.error instanceof Error && resolve.variables?.id === item.thread.id ? <p className="m-0 text-xs text-error" role="status">{resolve.error.message}</p> : null}
								{replying === item.key ? (
									<Composer
										autoFocus
										busy={post.isPending}
										error={post.error instanceof Error ? post.error.message : null}
										onCancel={() => { setReplying(null); post.reset(); }}
										onSubmit={(body) => post.mutateAsync({ body, threadId: item.thread.id })}
										placeholder={t("pr.comments.replyPlaceholder")}
										submitLabel={t("pr.comments.reply")}
									/>
								) : null}
							</>
						)}
					</li>
				))}
			</ol>
			{canWrite ? (
				<Composer
					busy={post.isPending && replying === null}
					error={replying === null && post.error instanceof Error ? post.error.message : null}
					onSubmit={(body) => post.mutateAsync({ body })}
					placeholder={t("pr.comments.addPlaceholder")}
					submitLabel={t("pr.comments.comment")}
				/>
			) : null}
		</div>
	);
}
