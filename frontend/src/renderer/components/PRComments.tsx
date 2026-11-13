import { apiErrorException } from "../lib/api-error-exception";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, ChevronRight, CircleAlert, CircleCheck, FileCode2, Loader2, MoreHorizontal, Reply } from "lucide-react";
import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { sessionScmSummaryQueryKey } from "../hooks/useSessionScmSummary";
import { useSendToSessionWorker } from "../hooks/useSendToSessionWorker";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { useAddTextToChat } from "../lib/chat-context-bus";
import { formatTimeCompact } from "../lib/format-time";
import { getGitHubStatus } from "../lib/github-daemon";
import { clientForSessionHost } from "../lib/host-clients";
import { prCheckedOut, prThreadDiffUrl } from "../lib/pr-display";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import { cn } from "../lib/utils";
import { sanitizeWorkerMessagePart } from "../lib/worker-message";
import type { WorkspaceSession } from "../types/workspace";
import { ConfirmDialog } from "./ConfirmDialog";
import { PRCommentComposer, readAsBase64 } from "./PRCommentComposer";
import { PRMarkdown } from "./PRMarkdown";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";

type Thread = NonNullable<SessionPRSummary["threads"]>[number];
type Comment = Thread["comments"][number];
type Review = NonNullable<SessionPRSummary["review"]["reviews"]>[number];

/** Everything the Comments section lists for a PR: top-level comments, reviews with a body, and review threads. */
export function prCommentCount(pr: SessionPRSummary): number {
	return (pr.conversation?.length ?? 0)
		+ (pr.review.reviews ?? []).filter((review) => review.body?.trim()).length
		+ (pr.threads ?? []).filter((thread) => thread.comments.length > 0).length;
}

type Item =
	| { kind: "comment"; key: string; at: number; comment: Comment }
	| { kind: "review"; key: string; at: number; review: Review }
	| { kind: "thread"; key: string; at: number; thread: Thread };

const time = (value?: string | null) => {
	const parsed = value ? Date.parse(value) : Number.NaN;
	return Number.isNaN(parsed) ? 0 : parsed;
};

// The Reviews tab's fold header, so the PR's folded groups read as the same
// family as its open and resolved review comments. A button takes its parent's
// font (styles.css), so the containers carry the text size.
const foldHeaderClass = "flex min-h-8 w-full min-w-0 items-center gap-2 rounded-md px-1.5 py-1 text-left text-xs font-medium leading-none text-muted-foreground transition-colors hover:bg-interactive-hover/20 hover:text-foreground";
const iconActionClass = "inline-flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60";
// A card's action is a quiet word under its text.
const footActionClass = "inline-flex h-6 items-center gap-1.5 rounded-md font-medium text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 disabled:opacity-60";
const showMoreClass = "inline-flex items-center rounded-sm font-medium text-muted-foreground underline-offset-2 transition-colors hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60";

/** Quotes a comment as markdown for the chat: who, where, and what they wrote. */
function quoteForChat(author: string, body: string, where: string, url?: string): string {
	const head = `**@${sanitizeWorkerMessagePart(author)}** ${sanitizeWorkerMessagePart(where)}${url ? ` (${sanitizeWorkerMessagePart(url)})` : ""}:`;
	return [head, ...sanitizeWorkerMessagePart(body.trim()).split("\n")].map((line) => `> ${line}`).join("\n");
}

/** How long a comment's menu says "Added to chat" before offering it again. */
export const ADDED_TO_CHAT_MS = 2500;

const squash = (text: string) => text.replace(/\s+/g, " ").trim();

/** The text a comment opens by quoting ("> …"), as GitHub's Quote reply writes it. */
function leadingQuote(body: string): string {
	const quoted: string[] = [];
	for (const line of body.split("\n")) {
		if (!line.startsWith(">")) break;
		quoted.push(line.replace(/^>\s?/, ""));
	}
	return squash(quoted.join(" "));
}

/** A quote reply's own words: the quote it opens with and the @mention after it go. */
function ownWords(body: string, author: string): string {
	const lines = body.split("\n");
	let start = 0;
	while (start < lines.length && lines[start]?.startsWith(">")) start++;
	const words = lines.slice(start).join("\n").trim();
	const mention = `@${author.toLowerCase()}`;
	return words.toLowerCase().startsWith(mention) ? words.slice(mention.length).trimStart() : words;
}

/**
 * What a reply to a top-level comment opens with: its first paragraph quoted
 * (cut at a word past 240 characters) and an @mention, so the reply nests
 * under it and the author is told, without pasting a whole bot summary back.
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

/** A GitHub login's avatar, or its first letter when the picture can't load. */
export function PRAvatar({ name, bot, size = "md" }: { name: string; bot?: boolean; size?: "sm" | "md" | "lg" }) {
	const [failed, setFailed] = useState(false);
	const login = name.replace(/\[bot\]$/, "");
	// GitHub Actions has no profile picture under its login.
	const src = login === "github-actions" ? "https://avatars.githubusercontent.com/in/15368?s=40&v=4" : `https://github.com/${login}.png?size=40`;
	const box = size === "sm" ? "size-4 text-[8px]" : size === "md" ? "size-5 text-[10px]" : "size-6 text-xs";
	if (failed || !/^[A-Za-z0-9-]+$/.test(login)) {
		return <span aria-hidden="true" className={cn("inline-flex shrink-0 items-center justify-center rounded-full bg-muted font-semibold uppercase text-muted-foreground", box)}>{login.slice(0, 1)}</span>;
	}
	return (
		<img
			alt=""
			aria-hidden="true"
			className={cn("shrink-0 bg-muted", bot ? "rounded-sm" : "rounded-full", box)}
			onError={() => setFailed(true)}
			referrerPolicy="no-referrer"
			src={src}
		/>
	);
}

const isLong = (body: string) => body.length > 360 || body.split("\n").length > 6;
const clampClass = "line-clamp-6 max-h-[6lh] overflow-hidden";
const roomyTextClass = "text-sm leading-[1.6]";

/** A reply's text: rendered markdown, clamped to six lines until opened. */
function Body({ body, roomy }: { body: string; roomy?: boolean }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const id = useId();
	const long = isLong(body);
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<div className={cn("min-w-0", long && !open && clampClass)} id={id}>
				<PRMarkdown body={body} className={roomy ? roomyTextClass : undefined} />
			</div>
			{long ? (
				<span className="mt-0.5 text-micro">
					<button aria-controls={id} aria-expanded={open} className={showMoreClass} onClick={() => setOpen(!open)} type="button">
						{open ? t("inspector.showLess") : t("inspector.showMore")}
					</button>
				</span>
			) : null}
		</div>
	);
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
			{open ? <div className="flex min-w-0 flex-col gap-2 pt-1" id={id}>{children}</div> : null}
		</section>
	);
}

type MenuAction = { label: string; onSelect: () => void; disabled?: boolean } | "separator";
type FootAction = { label: string; icon?: ReactNode; onClick: () => void; disabled?: boolean; busy?: boolean };

/**
 * One comment, review or review thread as a card: who and when, its text, and
 * Reply in plain sight, with Expand for long text across from it. Everything
 * else sits in its menu.
 */
function Card({ author, at, body, bot, children, foot, label, location, menu, muted, note, reply, roomy, sent, status }: {
	author: string;
	at?: string | null;
	body: string;
	bot?: boolean;
	/** A thread's replies, under its text. */
	children?: ReactNode;
	foot: FootAction[];
	/** Names the card for its menu. */
	label: string;
	/** A review thread's file and line, shown over its text. */
	location?: string;
	menu: MenuAction[];
	/** A settled card (a resolved thread) sits back on a tinted ground. */
	muted?: boolean;
	/** Under the card, such as an error. */
	note?: ReactNode;
	/** The reply box, open in place under the card's actions. */
	reply?: ReactNode;
	/** Reading size, for the PR's tab; the sidebar stays compact. */
	roomy?: boolean;
	sent?: boolean;
	/** A review's verdict or a thread's state, beside the name. */
	status?: ReactNode;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const id = useId();
	const long = isLong(body);
	const menuItems = menu[0] === "separator" ? menu.slice(1) : menu;
	return (
		<article aria-label={label} className={cn("flex min-w-0 flex-col rounded-xl border", roomy ? "gap-2.5 px-4 py-3.5 text-sm" : "gap-2 px-3 py-2.5 text-xs", muted ? "border-(--color-border-settings-input) bg-(--color-bg-settings-input)" : "border-border bg-background")}>
			<div className="flex min-w-0 items-center gap-2">
				<PRAvatar bot={bot} name={author} size={roomy ? "lg" : "md"} />
				<span className="min-w-0 truncate font-medium text-foreground">{author}</span>
				{bot ? <span className="shrink-0 rounded-sm border border-border px-1 text-micro font-medium text-muted-foreground">{t("pr.comments.bot")}</span> : null}
				{status}
				<span className="ml-auto" />
				{sent ? (
					<span aria-label={t("pr.comments.sentToAgent")} className="inline-flex size-6 shrink-0 items-center justify-center text-success" role="status" title={t("pr.comments.sentToAgent")}>
						<Check aria-hidden="true" className="size-icon-xs" />
					</span>
				) : null}
				{menuItems.length > 0 ? (
					<DropdownMenu>
						<DropdownMenuTrigger asChild>
							<button aria-label={t("pr.comments.actionsFor", { name: label })} className={iconActionClass} type="button">
								<MoreHorizontal aria-hidden="true" className="size-icon-xs" />
							</button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" className="min-w-40">
							{menuItems.map((action, index) => action === "separator"
								? <DropdownMenuSeparator key={`separator-${index}`} />
								: <DropdownMenuItem disabled={action.disabled} key={action.label} onSelect={action.onSelect}>{action.label}</DropdownMenuItem>)}
						</DropdownMenuContent>
					</DropdownMenu>
				) : null}
				{at ? <span className={cn("shrink-0 text-muted-foreground", roomy ? "text-control" : "text-micro")} title={at}>{formatTimeCompact(at)}</span> : null}
			</div>
			{location ? (
				<span className="inline-flex min-w-0 max-w-full items-center gap-1 self-start rounded-sm bg-muted/70 px-1.5 py-0.5 font-mono text-2xs text-foreground/80" title={location}>
					<FileCode2 aria-hidden="true" className="size-icon-2xs shrink-0" />
					<span className="truncate">{location}</span>
				</span>
			) : null}
			<div className={cn("min-w-0", long && !open && clampClass)} id={id}>
				<PRMarkdown body={body} className={roomy ? roomyTextClass : undefined} />
			</div>
			{children}
			{foot.length > 0 || long ? (
				<div className={cn("flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1", roomy ? "text-control" : "text-micro")}>
					{foot.map((action) => (
						<button className={footActionClass} disabled={action.disabled || action.busy} key={action.label} onClick={action.onClick} type="button">
							{action.busy ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin" /> : action.icon}
							{action.label}
						</button>
					))}
					{long ? (
						<button aria-controls={id} aria-expanded={open} className={cn(footActionClass, "ml-auto")} onClick={() => setOpen(!open)} type="button">
							{open ? t("pr.comments.collapse") : t("pr.comments.expand")}
							<ChevronDown aria-hidden="true" className={cn("size-icon-sm transition-transform motion-reduce:transition-none", open && "rotate-180")} />
						</button>
					) : null}
				</div>
			) : null}
			{reply}
			{note}
		</article>
	);
}

/**
 * The PR's conversation as cards: reviews, top-level comments and open review
 * threads (bots among them). The PR's tab reads it in order, oldest first, with
 * resolved threads folded after it and the comment box at the end. Each
 * card's text is there to read;
 * Reply (on other people's comments, as on GitHub) opens the comment box at
 * the card, and its menu holds Resolve, Add to chat (the comment quoted in the
 * session's chat, with room for your own words), View in diff, Send to agent
 * now and the GitHub links. Reading is free (the daemon already stored it); only
 * posting calls the provider.
 */
/** Repositories the person agreed to keep comment files in, so it is asked once each. */
const ATTACHMENT_CONSENT_KEY = "ao.prAttachmentsConsent";

function attachmentConsentGiven(location: string): boolean {
	try {
		const stored: unknown = JSON.parse(window.localStorage.getItem(ATTACHMENT_CONSENT_KEY) ?? "[]");
		return Array.isArray(stored) && stored.includes(location);
	} catch {
		return false;
	}
}

function rememberAttachmentConsent(location: string) {
	try {
		const stored: unknown = JSON.parse(window.localStorage.getItem(ATTACHMENT_CONSENT_KEY) ?? "[]");
		const list = Array.isArray(stored) ? stored.filter((item): item is string => typeof item === "string") : [];
		window.localStorage.setItem(ATTACHMENT_CONSENT_KEY, JSON.stringify([...new Set([...list, location])]));
	} catch {
		// Without storage the person is asked again next time; nothing else depends on it.
	}
}

/** A comment whose files the person chose not to store; it stays a draft. */
class AttachmentConsentDeclined extends Error {}

/**
 * Asks once per repository before a comment's files are stored there: they go
 * into the repository, where anyone who can read it can open them for good.
 */
function useAttachmentConsent(location: string) {
	const { t } = useTranslation();
	const [pending, setPending] = useState<((agreed: boolean) => void) | null>(null);
	const confirm = useCallback(() => {
		if (!location || attachmentConsentGiven(location)) return Promise.resolve(true);
		return new Promise<boolean>((resolve) => setPending(() => resolve));
	}, [location]);
	const settle = (agreed: boolean) => {
		if (agreed) rememberAttachmentConsent(location);
		pending?.(agreed);
		setPending(null);
	};
	const dialog = (
		<ConfirmDialog
			confirmLabel={t("pr.comments.attach.consentConfirm")}
			description={t("pr.comments.attach.consentDescription", { repo: location })}
			onConfirm={() => settle(true)}
			onOpenChange={(open) => { if (!open) settle(false); }}
			open={pending !== null}
			title={t("pr.comments.attach.consentTitle", { repo: location })}
		/>
	);
	return { confirm, dialog };
}

export function PRComments({ detailsFailed, hostId, onViewInDiff, pr, session }: {
	/** The one read of a PR's missing details failed (see usePRDetailsLoad). */
	detailsFailed?: boolean;
	hostId?: string;
	/** Opens a review thread's file in the diff at its line. */
	onViewInDiff?: (target: { line?: number; path: string }) => void;
	pr: SessionPRSummary;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [replying, setReplying] = useState<string | null>(null);
	// Threads resolved from here, shown as resolved at once rather than after the next summary.
	const [resolvedHere, setResolvedHere] = useState<ReadonlySet<string>>(() => new Set());
	// Once the summary says so itself, the provider owns the state again (a thread
	// unresolved on GitHub later shows open).
	useEffect(() => {
		const confirmed = new Set((pr.threads ?? []).filter((thread) => thread.resolved).map((thread) => thread.id));
		setResolvedHere((current) => [...current].some((id) => confirmed.has(id)) ? new Set([...current].filter((id) => !confirmed.has(id))) : current);
	}, [pr.threads]);
	const [sent, setSent] = useState<Set<string>>(() => new Set());
	// "Added to chat" is a moment's confirmation, not a lock: once it fades the
	// comment can be added again (after the draft was sent or cleared, say).
	const [added, setAdded] = useState<ReadonlySet<string>>(() => new Set());
	const addedTimers = useRef(new Map<string, ReturnType<typeof setTimeout>>());
	useEffect(() => () => { for (const timer of addedTimers.current.values()) clearTimeout(timer); }, []);
	const markAdded = (key: string) => {
		setAdded((current) => new Set(current).add(key));
		clearTimeout(addedTimers.current.get(key));
		addedTimers.current.set(key, setTimeout(() => {
			addedTimers.current.delete(key);
			setAdded((current) => { const next = new Set(current); next.delete(key); return next; });
		}, ADDED_TO_CHAT_MS));
	};
	const [actionError, setActionError] = useState<{ key: string; message: string } | null>(null);
	const sendToWorker = useSendToSessionWorker(session, hostId);
	const addToChat = useAddTextToChat(session.id, hostId);
	const canWrite = !session.cloud && !usePreviewData && pr.provider === "github" && Boolean(pr.url);
	// Who AO posts as. Reply is for other people's comments, as on GitHub; until
	// the login is known, every comment offers it.
	const login = useQuery({ queryKey: ["github-status"], queryFn: getGitHubStatus, staleTime: 5 * 60_000, enabled: canWrite }).data?.login;
	const viewer = login?.toLowerCase();
	const mine = (author: string) => viewer !== undefined && author.replace(/^@/, "").toLowerCase() === viewer;
	const refresh = () => Promise.all([
		queryClient.invalidateQueries({ queryKey: sessionScmSummaryQueryKey(session.id, hostId) }),
		queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) }),
	]);

	const post = useMutation({
		// from: the reply box that posted, by its card's key; a new comment has none.
		mutationFn: async ({ body, threadId, files = [] }: { body: string; threadId?: string; files?: File[]; from?: string }) => {
			// The files travel with the comment: the daemon stores them only now.
			const attachments = await Promise.all(files.map(async (file) => ({
				name: file.name,
				contentType: file.type || "application/octet-stream",
				data: await readAsBase64(file),
			})));
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/comments", {
				params: { path: { id: String(pr.number) } },
				body: { prUrl: pr.url, body, ...(threadId ? { threadId } : {}), ...(attachments.length ? { attachments } : {}) },
				headers: attachments.length ? { "X-AO-Attachment-Upload": "1" } : undefined,
			});
			if (error) throw apiErrorException(error, t("pr.comments.postFailed"));
		},
		onSuccess: async (_data, { from }) => {
			// Only the reply that posted closes: another open reply keeps its draft.
			if (from) setReplying((current) => (current === from ? null : current));
			await refresh();
		},
	});
	// Whether files can be attached here, asked before any is picked.
	const access = useQuery({
		queryKey: ["pr-attachment-access", hostId ?? "", pr.url],
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/prs/{id}/attachment-access", {
				params: { path: { id: String(pr.number) }, query: { prUrl: pr.url } },
			});
			if (error || !data) throw apiErrorException(error, t("pr.comments.postFailed"));
			return data;
		},
		enabled: canWrite,
		staleTime: 10 * 60_000,
	});
	const repository = access.data?.location || pr.repo || "";
	const attachBlocked = access.data && !access.data.allowed
		? access.data.reason === "no_write_access"
			? t("pr.comments.attach.noWriteAccess", { repo: repository })
			: t("pr.comments.attach.unsupported")
		: undefined;
	const attachments = canWrite ? { blockedReason: attachBlocked } : undefined;
	const consent = useAttachmentConsent(repository);
	// Files are stored in the repository: asked once per repository, before the first.
	const postWithFiles = async (input: { body: string; threadId?: string; files: File[]; from?: string }) => {
		if (input.files.length > 0 && !(await consent.confirm())) throw new AttachmentConsentDeclined();
		return post.mutateAsync(input);
	};
	const resolve = useMutation({
		mutationFn: async (thread: Thread) => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/comments/resolve", {
				params: { path: { sessionId: session.id } },
				body: { pullRequestUrl: pr.url, commentUrl: thread.comments[0]?.url ?? "" },
			});
			if (error) throw apiErrorException(error, t("pr.comments.resolveFailed"));
		},
		onSuccess: async (_data, thread) => {
			setResolvedHere((previous) => new Set(previous).add(thread.id));
			await refresh();
		},
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
	// Add to chat drafts the comment in the session's chat; a session without a
	// chat (a terminal agent) gets it sent to the agent instead.
	const addOne = (key: string, quote: string, sendArgs: Parameters<typeof send>) => {
		setActionError(null);
		if (addToChat) {
			if (addToChat(quote)) markAdded(key);
			else setActionError({ key, message: t("pr.comments.addToChatFailed") });
			return;
		}
		void send(...sendArgs);
	};
	const chatAction = (key: string, quote: string, sendArgs: Parameters<typeof send>): MenuAction => ({
		label: addLabel(key),
		onSelect: () => addOne(key, quote, sendArgs),
		// Sending straight to the agent happens once; adding to the draft can be repeated.
		disabled: added.has(key) || (!addToChat && sent.has(key)),
	});
	// The session's diff only has this PR's files when its worktree is on the PR's branch.
	const viewInDiff = (path: string, line?: number, url?: string) => {
		if (url && !prCheckedOut(pr, session.branchState)) window.open(prThreadDiffUrl(url), "_blank", "noopener,noreferrer");
		else onViewInDiff?.({ path, line });
	};
	const linkActions = (url?: string): MenuAction[] => url ? [
		"separator",
		{ label: t("pr.comments.openOnProvider"), onSelect: () => window.open(url, "_blank", "noopener,noreferrer") },
		{ label: t("pr.comments.copyLink"), onSelect: () => void navigator.clipboard?.writeText(url).catch(() => {}) },
	] : [];

	const items: Item[] = [
		...(pr.conversation ?? []).map((comment): Item => ({ kind: "comment", key: `c:${comment.id}`, at: time(comment.createdAt), comment })),
		...(pr.review.reviews ?? []).filter((review) => review.body?.trim()).map((review): Item => ({ kind: "review", key: `r:${review.reviewUrl ?? review.reviewerId + review.submittedAt}`, at: time(review.submittedAt), review })),
		...(pr.threads ?? []).filter((thread) => thread.comments.length > 0).map((thread): Item => ({ kind: "thread", key: `t:${thread.id}`, at: time(thread.comments.at(-1)?.createdAt), thread: resolvedHere.has(thread.id) ? { ...thread, resolved: true } : thread })),
	// Newest on top, so what just arrived (or what you just posted) is the first card; a thread sorts by its latest reply.
	].sort((a, b) => b.at - a.at);
	// GitHub's conversation has no threads; a Quote reply opens by quoting what
	// it answers. Such a reply nests under that comment or review, as on a PR page
	// read in order, and the pair sorts by the reply.
	const quoteReplies = new Map<string, Comment[]>();
	const nested = new Set<string>();
	// Oldest first, so a reply to a reply files under the card its chain started
	// at (only top-level cards draw their replies), in the order they were written.
	const parentOf = new Map<string, Item>();
	for (const item of [...items].reverse()) {
		if (item.kind !== "comment") continue;
		const quote = leadingQuote(item.comment.body);
		if (quote.length < 8) continue;
		let parent = items.find((other) => other !== item && other.kind !== "thread" && other.at <= item.at
			&& squash(other.kind === "comment" ? other.comment.body : other.review.body ?? "").startsWith(quote.slice(0, 120)));
		if (!parent) continue;
		for (let root = parentOf.get(parent.key); root; root = parentOf.get(root.key)) parent = root;
		parentOf.set(item.key, parent);
		quoteReplies.set(parent.key, [...(quoteReplies.get(parent.key) ?? []), item.comment]);
		nested.add(item.key);
		parent.at = Math.max(parent.at, item.at);
	}
	items.sort((a, b) => b.at - a.at);
	const resolved = items.filter((item) => item.kind === "thread" && item.thread.resolved);
	const main = items.filter((item) => !nested.has(item.key) && !(item.kind === "thread" && item.thread.resolved));

	const myAvatar = login ? <PRAvatar name={login} size="lg" /> : undefined;
	// Each box shows its own post's state: the open reply's, or the new comment's.
	const postedFrom = (from?: string) => post.variables?.from === from;
	const postError = (from?: string) => (postedFrom(from) && post.error instanceof Error ? post.error.message : null);
	// An edit answers its own box's error; another box's stays.
	const clearPostError = (from?: string) => { if (post.error && postedFrom(from)) post.reset(); };
	const closeReply = () => { setReplying(null); if (post.variables?.from) post.reset(); };
	const openReply = (key: string) => { if (post.variables?.from) post.reset(); setReplying(key); };
	// The same simple box as a new comment, opened in place under the card it answers.
	const replyFor = (item: Item, threadId?: string, initial?: string) => canWrite && replying === item.key ? (
		<PRCommentComposer
			autoFocus
			busy={post.isPending && postedFrom(item.key)}
			error={postError(item.key)}
			initial={initial}
			onCancel={closeReply}
			onEdit={() => clearPostError(item.key)}
			onSubmit={(body, files) => postWithFiles({ body, threadId, files, from: item.key })}
			placeholder={t("pr.comments.replyPlaceholder")}
			roomy
			submitLabel={t("pr.comments.reply")}
			attachments={attachments}
		/>
	) : undefined;
	// Reply is offered on someone else's words, and hides while its box is open.
	const replyAction = (item: Item, offered: boolean): FootAction[] => offered && canWrite && replying !== item.key ? [{ label: t("pr.comments.reply"), icon: replyIcon, onClick: () => openReply(item.key) }] : [];
	const errorFor = (key: string) => actionError?.key === key ? <p className="m-0 text-2xs font-medium text-error" role="status">{actionError.message}</p> : null;
	const addLabel = (key: string) => added.has(key) ? t("pr.comments.addedToChat") : addToChat ? t("pr.comments.addToChat") : sent.has(key) ? t("pr.comments.sentToAgent") : t("pr.comments.sendToAgent");
	const prWhere = t("pr.comments.quoteOnPR", { number: pr.number });
	const replyIcon = <Reply aria-hidden="true" className="size-icon-sm" />;

	const quoteOf = (item: Item): string => {
		if (item.kind === "thread") {
			const location = item.thread.path ? `${item.thread.path}${item.thread.line ? `:${item.thread.line}` : ""}` : "";
			const where = location ? t("pr.comments.quoteOnFile", { location, number: pr.number }) : prWhere;
			return item.thread.comments.map((comment, index) => quoteForChat(comment.author, comment.body, index === 0 ? where : t("pr.comments.quoteReplied"), index === 0 ? comment.url : undefined)).join("\n>\n");
		}
		if (item.kind === "review") return quoteForChat(item.review.reviewerId, item.review.body ?? "", prWhere, item.review.reviewUrl);
		return quoteForChat(item.comment.author, item.comment.body, prWhere, item.comment.url);
	};

	// A thread's replies as they are; a quote reply without the quote and the @mention of `answering`.
	const replyList = (replies: { id: string; author: string; isBot: boolean; body: string; createdAt?: string | null }[], answering?: string) => replies.map((reply) => (
		<div className="flex min-w-0 flex-col gap-1 border-l-2 border-border-strong pl-2.5" data-testid="pr-comment-reply" key={reply.id}>
			<span className="flex min-w-0 items-center gap-1.5 text-control">
				<PRAvatar bot={reply.isBot} name={reply.author} size="sm" />
				<span className="truncate font-semibold text-foreground">{reply.author}</span>
				{reply.createdAt ? <span className="text-muted-foreground">{formatTimeCompact(reply.createdAt)}</span> : null}
			</span>
			<Body body={answering ? ownWords(reply.body, answering) || reply.body : reply.body} roomy />
		</div>
	));
	const card = (item: Item) => {
		if (item.kind === "thread") {
			const { thread } = item;
			const first = thread.comments[0];
			const location = thread.path ? `${thread.path}${thread.line ? `:${thread.line}` : ""}` : t("pr.comments.general");
			const allText = thread.comments.map((comment) => `@${comment.author}: ${comment.body}`).join("\n\n");
			return (
				<div data-kind="thread" key={item.key}>
					<Card
						at={first?.createdAt}
						author={first?.author ?? ""}
						body={first?.body ?? ""}
						bot={first?.isBot}
						// A thread with someone else in it takes a reply; one only you wrote waits for theirs.
						foot={replyAction(item, thread.comments.some((comment) => !mine(comment.author)))}
						label={location}
						location={location}
						muted={thread.resolved}
						menu={[
							...(canWrite && !thread.resolved ? [{ label: t("pr.comments.resolve"), onSelect: () => { setActionError(null); resolve.mutate(thread); }, disabled: resolve.isPending }] : []),
							...(!thread.resolved ? [chatAction(item.key, quoteOf(item), [item.key, first?.author ?? "", allText, first?.url, location])] : []),
							...(thread.path && onViewInDiff ? [{ label: t("pr.comments.viewInDiff"), onSelect: () => viewInDiff(thread.path ?? "", thread.line ?? undefined, first?.url) }] : []),
							...(!thread.resolved && addToChat ? [{ label: t("pr.comments.sendToAgentNow"), onSelect: () => void send(item.key, first?.author ?? "", allText, first?.url, location), disabled: sent.has(item.key) }] : []),
							...linkActions(first?.url),
						]}
						note={errorFor(item.key)}
						reply={replyFor(item, thread.id)}
						roomy
						sent={sent.has(item.key)}
						status={thread.resolved ? <span className="shrink-0 text-micro text-success">{t("pr.comments.resolvedTag")}</span> : null}
					>
						{replyList(thread.comments.slice(1))}
					</Card>
				</div>
			);
		}
		if (item.kind === "review") {
			const { review } = item;
			const verdict = review.verdict === "approved" || review.verdict === "changes_requested" ? review.verdict : "commented";
			return (
				<div data-kind="review" key={item.key}>
					<Card
						at={review.submittedAt}
						author={review.reviewerId}
						body={review.body ?? ""}
						bot={review.isBot}
						foot={replyAction(item, !mine(review.reviewerId))}
						label={review.reviewerId}
						menu={[
							chatAction(item.key, quoteOf(item), [item.key, review.reviewerId, review.body ?? "", review.reviewUrl]),
							...(addToChat ? [{ label: t("pr.comments.sendToAgentNow"), onSelect: () => void send(item.key, review.reviewerId, review.body ?? "", review.reviewUrl), disabled: sent.has(item.key) }] : []),
							...linkActions(review.reviewUrl),
						]}
						note={errorFor(item.key)}
						reply={replyFor(item, undefined, replyQuote(review.reviewerId, review.body ?? ""))}
						roomy
						sent={sent.has(item.key)}
						status={verdict === "commented" ? null : (
							<span className={cn("inline-flex shrink-0 items-center gap-0.5 text-micro font-medium", verdict === "approved" ? "text-success" : "text-status-in-review")}>
								{verdict === "approved" ? <CircleCheck aria-hidden="true" className="size-icon-2xs" /> : <CircleAlert aria-hidden="true" className="size-icon-2xs" />}
								{t(`pr.comments.verdict.${verdict}`)}
							</span>
						)}
					>
						{replyList(quoteReplies.get(item.key) ?? [], review.reviewerId)}
					</Card>
				</div>
			);
		}
		const { comment } = item;
		return (
			<div data-kind="comment" key={item.key}>
				<Card
					at={comment.createdAt}
					author={comment.author}
					body={comment.body}
					bot={comment.isBot}
					foot={replyAction(item, !mine(comment.author))}
					label={comment.author}
					menu={[
						chatAction(item.key, quoteOf(item), [item.key, comment.author, comment.body, comment.url]),
						...(addToChat ? [{ label: t("pr.comments.sendToAgentNow"), onSelect: () => void send(item.key, comment.author, comment.body, comment.url), disabled: sent.has(item.key) }] : []),
						...linkActions(comment.url),
					]}
					note={errorFor(item.key)}
					reply={replyFor(item, undefined, replyQuote(comment.author, comment.body))}
					roomy
					sent={sent.has(item.key)}
				>
					{replyList(quoteReplies.get(item.key) ?? [], comment.author)}
				</Card>
			</div>
		);
	};

	const newComment = (
		<PRCommentComposer
			avatar={myAvatar}
			busy={post.isPending && postedFrom(undefined)}
			error={postError(undefined)}
			onEdit={() => clearPostError(undefined)}
			onSubmit={(body, files) => postWithFiles({ body, files })}
			placeholder={t("pr.comments.leaveComment")}
			roomy
			submitLabel={t("pr.comments.comment")}
			attachments={attachments}
		/>
	);
	return (
		<div className="flex min-w-0 flex-col gap-3 text-sm" data-testid="pr-comments">
			{consent.dialog}
			{items.length === 0 ? (
				<p className="m-0 px-0.5 text-xs text-passive">{pr.detailsObservedAt ? t("pr.comments.none") : detailsFailed ? t("pr.description.unavailable") : t("pr.description.pending")}</p>
			) : null}
			{main.length > 0 ? <div className="flex min-w-0 flex-col gap-3" data-testid="pr-comments-main">{[...main].reverse().map(card)}</div> : null}
			<Fold count={resolved.length} defaultOpen={false} label={t("pr.comments.resolvedThreads")} testId="pr-comments-resolved">{resolved.map(card)}</Fold>
			{canWrite ? newComment : null}
		</div>
	);
}
