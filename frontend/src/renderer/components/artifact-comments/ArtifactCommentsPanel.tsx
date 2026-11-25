import { useEffect, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ArrowRight, ArrowUp, Check, MoreHorizontal, RotateCcw, Send, X } from "@/components/app-icons";
import { formatTimeTerse } from "../../lib/format-time";
import { cn } from "../../lib/utils";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { RetryButton } from "../WorkspaceDiffView";
import {
	ARTIFACT_COMMENT_MAX_BODY_LENGTH,
	artifactCommentAnchorKind,
	type ArtifactComment,
	type ArtifactCommentAnchor,
	type ArtifactCommentAuthor,
	type ArtifactCommentGroups,
	type ArtifactCommentThread,
} from "../../../shared/artifact-comments";
import { useArtifactCommentAuthorLabel } from "./author-label";
import { artifactCommentAnchorText } from "./anchor-label";
import { SendThreadToAgentDialog, type ArtifactCommentsSendTarget } from "./SendThreadToAgentDialog";
import { isPendingId, type ArtifactCommentsState } from "./useArtifactComments";

type Viewer = { userId: string; isOwner: boolean };

function initials(name: string): string {
	const parts = name.trim().split(/\s+/).filter(Boolean);
	return ((parts[0]?.[0] ?? "") + (parts.length > 1 ? parts[parts.length - 1][0] : parts[0]?.[1] ?? "")).toUpperCase() || "?";
}

/** Same hues as ProjectIcon's letter tiles, solid, so each commenter keeps one colour. */
const AVATAR_COLORS = [
	"bg-orange-500",
	"bg-emerald-600",
	"bg-sky-600",
	"bg-violet-500",
	"bg-pink-500",
	"bg-teal-600",
	"bg-amber-600",
	"bg-indigo-500",
	"bg-lime-600",
	"bg-fuchsia-500",
] as const;

export function artifactCommentAvatarColor(seed: string): string {
	let hash = 0;
	for (let index = 0; index < seed.length; index++) hash = (hash * 31 + seed.charCodeAt(index)) | 0;
	return AVATAR_COLORS[Math.abs(hash) % AVATAR_COLORS.length];
}

function useAuthorName() {
	const { t } = useTranslation();
	const label = useArtifactCommentAuthorLabel();
	// Your own comment shows as You until it has your display name (an instant copy has none).
	return (comment: ArtifactComment, viewer: Viewer) =>
		comment.author?.id === viewer.userId && !comment.author.name.trim() ? t("artifactComments.you") : label(comment.author);
}

function Avatar({ author, name }: { author: ArtifactCommentAuthor | null; name: string }) {
	return (
		<span
			aria-hidden="true"
			className={cn("inline-flex size-[18px] shrink-0 items-center justify-center rounded-full text-[8px] font-semibold text-white", artifactCommentAvatarColor(author?.id ?? name))}
			data-testid="artifact-comment-avatar"
		>
			{initials(name)}
		</span>
	);
}

/** Avatar, name and "· 2h"; anything after it sits at the row's end. */
function AuthorRow({ children, comment, viewer }: { children?: ReactNode; comment: ArtifactComment; viewer: Viewer }) {
	const authorName = useAuthorName();
	const name = authorName(comment, viewer);
	return (
		<div className="flex min-h-5 items-center gap-1.5 text-xs">
			<Avatar author={comment.author} name={name} />
			<span className="min-w-0 truncate font-semibold text-foreground">{name}</span>
			<span className="shrink-0 text-muted-foreground tabular-nums">· {formatTimeTerse(comment.createdAt)}</span>
			<span className="flex-1" />
			{children}
		</div>
	);
}

/** A thread's comments as shown: deleted replies drop out; a deleted first comment leaves "Comment deleted". */
function visibleComments(thread: ArtifactCommentThread): ArtifactComment[] {
	return thread.comments.filter((comment, index) => index === 0 || !comment.deleted);
}

function repliesOf(thread: ArtifactCommentThread): number {
	return Math.max(0, visibleComments(thread).length - 1);
}

/** A new comment being written on one element. Kept by the panel, so a refresh or a failed post never loses it. */
export type NewCommentDraft = { text: string; failed: boolean; sending: boolean; clientId: string };

/**
 * The new-comment box: one bordered box holding the text, the post error and
 * Cancel / Comment. A failed post keeps the text with Retry, under the same
 * client id, so a retry never saves the comment twice.
 */
function NewCommentBox({ draft, onCancel, onChange, onSubmit }: {
	draft: NewCommentDraft;
	onCancel: () => void;
	onChange: (text: string) => void;
	onSubmit: () => void;
}) {
	const { t } = useTranslation();
	return (
		<div className="flex flex-col gap-1.5 rounded-md border border-logo-accent bg-background p-2" data-testid="artifact-comments-new">
			<textarea
				aria-label={t("artifactComments.newPlaceholder")}
				autoFocus
				className="board-scrollbar max-h-40 min-h-10 w-full resize-none border-0 bg-transparent text-sm leading-5 text-foreground outline-none placeholder:text-muted-foreground"
				maxLength={ARTIFACT_COMMENT_MAX_BODY_LENGTH}
				onChange={(event) => onChange(event.target.value)}
				onKeyDown={(event) => {
					if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
						event.preventDefault();
						onSubmit();
					} else if (event.key === "Escape") {
						event.stopPropagation();
						onCancel();
					}
				}}
				placeholder={t("artifactComments.newPlaceholder")}
				rows={2}
				value={draft.text}
			/>
			{draft.failed ? (
				<p className="flex items-center gap-1 text-caption text-destructive" role="alert">
					{t("artifactComments.postFailed")}
					<Button className="text-destructive" onClick={onSubmit} size="none" type="button" variant="link">{t("artifactComments.retry")}</Button>
				</p>
			) : null}
			<div className="flex justify-end gap-1">
				<Button onClick={onCancel} size="xs" type="button" variant="quiet">{t("artifactComments.cancel")}</Button>
				<Button disabled={!draft.text.trim() || draft.sending} onClick={onSubmit} size="xs" type="button" variant="primary">{t("artifactComments.comment")}</Button>
			</div>
		</div>
	);
}

/**
 * The chat-style reply box, opened by Reply: Enter sends, Shift+Enter adds a
 * line, and the accent arrow sends. It closes once the reply is posted, or on
 * Esc while empty; a failed post keeps it open with the text and Retry.
 */
function ReplyBox({ onClose, onSubmit }: { onClose: () => void; onSubmit: (body: string, clientId: string) => Promise<unknown> }) {
	const { t } = useTranslation();
	const [text, setText] = useState("");
	const [failed, setFailed] = useState(false);
	const [sending, setSending] = useState(false);
	const clientId = useRef(crypto.randomUUID());
	const submit = async () => {
		const body = text.trim();
		if (!body || sending) return;
		setSending(true);
		setFailed(false);
		setText("");
		try {
			await onSubmit(body, clientId.current);
			clientId.current = crypto.randomUUID();
			onClose();
		} catch {
			setText(body);
			setFailed(true);
		} finally {
			setSending(false);
		}
	};
	const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
		if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
			event.preventDefault();
			void submit();
		} else if (event.key === "Escape" && !text.trim()) {
			// Closes the box, not the popover.
			event.preventDefault();
			onClose();
		}
	};
	return (
		<div className="flex flex-col gap-1">
			<div className="flex items-end gap-1 rounded-md border border-border bg-background py-1 pr-1 pl-2.5 focus-within:border-logo-accent">
				<textarea
					aria-label={t("artifactComments.replyPlaceholder")}
					autoFocus
					className="board-scrollbar max-h-32 min-h-6 w-full resize-none border-0 bg-transparent py-0.5 text-sm leading-5 text-foreground outline-none placeholder:text-muted-foreground"
					maxLength={ARTIFACT_COMMENT_MAX_BODY_LENGTH}
					onChange={(event) => {
						// Edited after a failed post: a new id, so a retry can't come back as the first text.
						if (failed) clientId.current = crypto.randomUUID();
						setText(event.target.value);
					}}
					onKeyDown={onKeyDown}
					placeholder={t("artifactComments.replyPlaceholder")}
					rows={1}
					value={text}
				/>
				<Button aria-label={t("artifactComments.sendReply")} disabled={!text.trim() || sending} onClick={() => void submit()} size="icon-xs" type="button" variant="primary">
					<ArrowUp aria-hidden="true" className="size-3.5" />
				</Button>
			</div>
			{failed ? (
				<p className="flex items-center gap-1 text-caption text-destructive" role="alert">
					{t("artifactComments.postFailed")}
					<Button className="text-destructive" onClick={() => void submit()} size="none" type="button" variant="link">{t("artifactComments.retry")}</Button>
				</p>
			) : null}
		</div>
	);
}

/** One comment: its author row (with the ⋯ menu and anything the thread adds), text, and "edited". */
function CommentItem({ aside, comment, comments, readOnly = false, reply, viewer }: {
	aside?: ReactNode;
	comment: ArtifactComment;
	comments: ArtifactCommentsState;
	readOnly?: boolean;
	reply: boolean;
	viewer: Viewer;
}) {
	const { t } = useTranslation();
	const [editing, setEditing] = useState(false);
	const [draft, setDraft] = useState(comment.body);
	const [confirmDelete, setConfirmDelete] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [busy, setBusy] = useState(false);
	const own = comment.author?.id === viewer.userId;
	const pending = isPendingId(comment.id);
	const canEdit = own && !comment.deleted && !pending && !readOnly;
	// The ⋯ menu: Edit and Delete on your own comments; the owner may delete any.
	const canDelete = (own || viewer.isOwner) && !comment.deleted && !pending && !readOnly;
	const run = async (action: () => Promise<unknown>, done: () => void) => {
		setBusy(true);
		setError(null);
		try {
			await action();
			done();
		} catch {
			setError(t("artifactComments.actionFailed"));
		} finally {
			setBusy(false);
		}
	};
	if (comment.deleted) {
		return (
			<p className={cn("text-xs italic text-muted-foreground", reply && "ml-2.5 border-l-2 border-border pl-2.5")} data-comment-id={comment.id}>
				{t("artifactComments.deleted")}
			</p>
		);
	}
	return (
		<div className={cn("flex flex-col gap-1", reply && "ml-2.5 border-l-2 border-border pl-2.5", pending && "opacity-70")} data-comment-id={comment.id}>
			<AuthorRow comment={comment} viewer={viewer}>
				{aside}
				{canEdit || canDelete ? (
					<DropdownMenu>
						<DropdownMenuTrigger asChild>
							<Button aria-label={t("artifactComments.commentActions")} className="-my-1" size="icon-xs" type="button" variant="quiet">
								<MoreHorizontal aria-hidden="true" className="size-3.5" />
							</Button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end">
							{canEdit ? <DropdownMenuItem onSelect={() => { setDraft(comment.body); setEditing(true); }}>{t("artifactComments.edit")}</DropdownMenuItem> : null}
							{canDelete ? <DropdownMenuItem onSelect={() => setConfirmDelete(true)} variant="destructive">{t("artifactComments.delete")}</DropdownMenuItem> : null}
						</DropdownMenuContent>
					</DropdownMenu>
				) : null}
			</AuthorRow>
			{editing ? (
				<div className="flex flex-col gap-1.5 rounded-md border border-logo-accent bg-background p-2">
					<textarea
						aria-label={t("artifactComments.edit")}
						autoFocus
						className="board-scrollbar min-h-10 w-full resize-none border-0 bg-transparent text-sm leading-5 text-foreground outline-none"
						maxLength={ARTIFACT_COMMENT_MAX_BODY_LENGTH}
						onChange={(event) => setDraft(event.target.value)}
						value={draft}
					/>
					<div className="flex justify-end gap-1">
						<Button disabled={busy} onClick={() => setEditing(false)} size="xs" type="button" variant="quiet">{t("artifactComments.cancel")}</Button>
						<Button disabled={busy || !draft.trim()} onClick={() => void run(() => comments.edit(comment.id, draft.trim()), () => setEditing(false))} size="xs" type="button" variant="primary">{t("artifactComments.save")}</Button>
					</div>
				</div>
			) : (
				<p className="whitespace-pre-wrap break-words text-sm leading-5 text-foreground">{comment.body}</p>
			)}
			{comment.editedAt && !editing ? <p className="text-caption text-muted-foreground">{t("artifactComments.edited")}</p> : null}
			{error && !confirmDelete ? <p className="text-caption text-destructive" role="alert">{error}</p> : null}
			<ConfirmDialog
				busy={busy}
				confirmLabel={t("artifactComments.delete")}
				description={t("artifactComments.deleteDescription")}
				destructive
				error={confirmDelete ? error : null}
				onConfirm={() => void run(() => comments.remove(comment.id), () => setConfirmDelete(false))}
				onOpenChange={(open) => { setConfirmDelete(open); if (!open) setError(null); }}
				open={confirmDelete}
				title={t("artifactComments.deleteTitle")}
			/>
		</div>
	);
}

/**
 * A folded thread in an element's popover: avatar, name, time, its first comment
 * cut to one line, and its reply count. Click to open it.
 */
function ThreadLine({ onExpand, thread, viewer }: { onExpand: () => void; thread: ArtifactCommentThread; viewer: Viewer }) {
	const { t } = useTranslation();
	const authorName = useAuthorName();
	const first = thread.comments[0];
	if (!first) return null;
	const name = authorName(first, viewer);
	const replies = repliesOf(thread);
	return (
		<button
			aria-expanded={false}
			className="-mx-1 flex min-w-0 flex-col gap-0.5 rounded-md px-1 py-1.5 text-left text-xs outline-none hover:bg-interactive-hover focus-visible:ring-1 focus-visible:ring-ring"
			data-thread-id={thread.id}
			data-thread-folded="true"
			onClick={onExpand}
			type="button"
		>
			<span className="flex min-w-0 items-center gap-1.5">
				<Avatar author={first.author} name={name} />
				<span className="max-w-[40%] shrink-0 truncate font-semibold text-foreground">{name}</span>
				<span className="shrink-0 text-muted-foreground tabular-nums">· {formatTimeTerse(first.createdAt)}</span>
				<span className={cn("min-w-0 flex-1 truncate text-sm", first.deleted ? "italic text-muted-foreground" : "text-foreground")}>
					{first.deleted ? t("artifactComments.deleted") : first.body}
				</span>
			</span>
			<span className="pl-6 text-caption text-muted-foreground">
				{replies > 0 ? t("artifactComments.replies", { count: replies }) : t("artifactComments.noReplies")}
			</span>
		</button>
	);
}

function ThreadView({ comments, readOnly = false, sendTarget, thread, viewer, version }: {
	comments: ArtifactCommentsState;
	/** Only its comments: no reply, resolve, send or ⋯ menu. */
	readOnly?: boolean;
	sendTarget?: ArtifactCommentsSendTarget;
	thread: ArtifactCommentThread;
	viewer: Viewer;
	version: number;
}) {
	const { t, i18n } = useTranslation();
	const authorLabel = useArtifactCommentAuthorLabel();
	const [sendOpen, setSendOpen] = useState(false);
	const [statusError, setStatusError] = useState(false);
	const [replying, setReplying] = useState(false);
	const pending = isPendingId(thread.id);
	const resolved = thread.status === "resolved";
	// Send to agent: the app's owner only, and only when the session that made the file is here.
	const canSend = viewer.isOwner && Boolean(sendTarget) && !pending && !resolved && !readOnly;
	const setStatus = (status: "open" | "resolved") => {
		setStatusError(false);
		comments.setStatus(thread.id, status).catch(() => setStatusError(true));
	};
	const sentTime = thread.sentToAgentAt
		? new Intl.DateTimeFormat(i18n.resolvedLanguage ?? "en", { hour: "2-digit", minute: "2-digit" }).format(new Date(thread.sentToAgentAt))
		: null;
	// On the first comment's row: who resolved it, or when it went to the agent.
	const aside = resolved ? (
		<span className="min-w-0 shrink-[3] truncate rounded-sm border border-border px-1.5 text-caption text-muted-foreground">
			{t("artifactComments.resolvedBy", { name: authorLabel(thread.resolvedBy) })}
		</span>
	) : sentTime ? (
		<span className="flex shrink-0 items-center gap-1 text-caption text-muted-foreground" data-testid="artifact-comments-sent">
			<ArrowRight aria-hidden="true" className="size-3 shrink-0" />
			<span className="truncate">{t("artifactComments.sentAt", { time: sentTime })}</span>
		</span>
	) : null;
	return (
		<div className="flex flex-col gap-2 py-2.5 first:pt-0 last:pb-0" data-thread-id={thread.id}>
			{visibleComments(thread).map((comment, index) => (
				<CommentItem aside={index === 0 ? aside : undefined} comment={comment} comments={comments} key={comment.id} readOnly={readOnly} reply={index > 0} viewer={viewer} />
			))}
			{replying && !resolved && !pending && !readOnly ? (
				<ReplyBox onClose={() => setReplying(false)} onSubmit={(body, clientId) => comments.reply(thread.id, body, clientId)} />
			) : null}
			{!pending && !readOnly ? (
				<div className="flex items-center gap-1">
					{!resolved && !replying ? (
						<Button onClick={() => setReplying(true)} size="xs" type="button" variant="secondary">{t("artifactComments.reply")}</Button>
					) : null}
					{resolved ? (
						<Button onClick={() => setStatus("open")} size="xs" type="button" variant="secondary">
							<RotateCcw aria-hidden="true" className="size-3" />{t("artifactComments.reopen")}
						</Button>
					) : (
						<Button onClick={() => setStatus("resolved")} size="xs" type="button" variant="secondary">
							<Check aria-hidden="true" className="size-3" />{t("artifactComments.resolve")}
						</Button>
					)}
					{canSend && sendTarget ? (
						<Button onClick={() => setSendOpen(true)} size="xs" type="button" variant="quiet">
							<Send aria-hidden="true" className="size-3.5" />
							{thread.sentToAgentAt ? t("artifactComments.sendAgain") : t("artifactComments.sendToAgent")}
						</Button>
					) : null}
				</div>
			) : null}
			{statusError ? <p className="text-caption text-destructive" role="alert">{t("artifactComments.actionFailed")}</p> : null}
			{canSend && sendTarget ? (
				<SendThreadToAgentDialog markSent={comments.markSent} onOpenChange={setSendOpen} open={sendOpen} target={sendTarget} thread={thread} version={version} />
			) : null}
		</div>
	);
}

/**
 * The popover's natural height, as it changes: its frame (header, borders)
 * plus all of its content, so placement can size it without a cap.
 */
function useNaturalHeight(onNaturalHeight: ((height: number) => void) | undefined) {
	const asideRef = useRef<HTMLElement>(null);
	const scrollRef = useRef<HTMLDivElement>(null);
	const contentRef = useRef<HTMLDivElement>(null);
	const onNaturalHeightRef = useRef(onNaturalHeight);
	onNaturalHeightRef.current = onNaturalHeight;
	useEffect(() => {
		const aside = asideRef.current;
		const scroll = scrollRef.current;
		const content = contentRef.current;
		if (!aside || !scroll || !content || typeof ResizeObserver === "undefined") return;
		const report = () => onNaturalHeightRef.current?.(aside.offsetHeight - scroll.clientHeight + content.scrollHeight);
		const observer = new ResizeObserver(report);
		observer.observe(aside);
		observer.observe(content);
		report();
		return () => observer.disconnect();
	}, []);
	return { asideRef, contentRef, scrollRef };
}

export type ArtifactCommentsPanelProps = {
	comments: ArtifactCommentsState;
	groups: ArtifactCommentGroups;
	/**
	 * The element the popover is for. Clicked on the page, it brings its anchor
	 * and the popover is a new comment on it; from its count, no anchor, and the
	 * popover is its threads.
	 */
	element: { path: string; anchor: ArtifactCommentAnchor | null };
	/** Closes the popover; comment mode stays on. */
	onClose: () => void;
	/** A new comment posted: show the element's threads, the new one open. */
	onPosted: () => void;
	/** The session that made the file, when the app knows it: offers Send to agent to the owner. */
	sendTarget?: ArtifactCommentsSendTarget;
	className?: string;
	/** Where the popover sits and how tall it may grow (see placeCommentsPopover). */
	style?: CSSProperties;
	/** The height the popover would take with nothing capping it, as it changes. */
	onNaturalHeight?: (height: number) => void;
};

/**
 * One element's popover, beside it. From its count: its open threads, newest
 * first with one open at a time, and its resolved threads behind "N resolved
 * on this element · Show". From a click on the element: a box for a new
 * comment on it, and nothing else.
 */
export function ArtifactCommentsPanel({
	className,
	comments,
	element,
	groups,
	onClose,
	onNaturalHeight,
	onPosted,
	sendTarget,
	style,
}: ArtifactCommentsPanelProps) {
	const { t } = useTranslation();
	const snapshot = comments.snapshot;
	const viewer: Viewer = snapshot?.you ?? { userId: "", isOwner: false };
	const version = snapshot?.version ?? 0;
	const openGroup = groups.open.find((group) => group.path === element.path) ?? groups.unanchored.find((group) => group.path === element.path);
	const resolvedGroup = groups.resolved.find((group) => group.path === element.path);
	const threads = openGroup?.threads ?? [];
	const resolvedThreads = resolvedGroup?.threads ?? [];
	const anchor = element.anchor;
	// Lines or a video's moment: where, in words. Not an image's point or
	// region: its pin on the picture shows where.
	const whereAnchor = anchor ?? openGroup?.anchor ?? resolvedGroup?.anchor;
	const where = whereAnchor && artifactCommentAnchorKind(whereAnchor) === "image" ? null : artifactCommentAnchorText(t, whereAnchor);
	// One thread open: the newest, until another is clicked; back to the newest
	// for another element or once a new thread is posted here.
	const [openThreadId, setOpenThreadId] = useState<string | null>(null);
	const [showResolved, setShowResolved] = useState(false);
	useEffect(() => {
		setOpenThreadId(null);
		setShowResolved(false);
	}, [element.path]);
	// The new comment's text, by element, so a failed post or a refresh never loses it.
	const [drafts, setDrafts] = useState<Record<string, NewCommentDraft>>({});
	const draft = drafts[element.path] ?? { text: "", failed: false, sending: false, clientId: "" };
	const updateDraft = (path: string, patch: Partial<NewCommentDraft>) => setDrafts((current) => {
		const base = current[path] ?? { text: "", failed: false, sending: false, clientId: crypto.randomUUID() };
		return { ...current, [path]: { ...base, ...patch } };
	});
	const dropDraft = (path: string) => setDrafts((current) => {
		const { [path]: _dropped, ...rest } = current;
		return rest;
	});
	const post = async () => {
		const text = draft.text.trim();
		if (!anchor || !text || draft.sending) return;
		const { path } = element;
		updateDraft(path, { sending: true, failed: false });
		try {
			await comments.createThread(anchor, text, draft.clientId);
			dropDraft(path);
			setOpenThreadId(null);
			onPosted();
		} catch {
			updateDraft(path, { sending: false, failed: true });
		}
	};
	const cancel = () => {
		dropDraft(element.path);
		onClose();
	};

	const { asideRef, contentRef, scrollRef } = useNaturalHeight(onNaturalHeight);

	const threadView = (thread: ArtifactCommentThread) => (
		<ThreadView comments={comments} key={thread.id} sendTarget={sendTarget} thread={thread} version={version} viewer={viewer} />
	);

	return (
		<aside
			aria-label={t("artifactComments.title")}
			className={cn("flex w-[340px] flex-col", className)}
			onKeyDown={(event) => {
				if (event.key === "Escape" && !event.defaultPrevented && !(event.target instanceof HTMLTextAreaElement)) {
					// Handled here: full screen's own Esc leaves it alone.
					event.preventDefault();
					onClose();
				}
			}}
			ref={asideRef}
			style={style}
		>
			<div className="flex h-10 shrink-0 items-center gap-1 border-b border-border pr-1.5 pl-3">
				<h2 className="min-w-0 flex-1 truncate text-sm font-semibold text-foreground">
					{t("artifactComments.title")}
					{!anchor && threads.length > 0 ? <span className="font-normal text-muted-foreground">{" · "}{t("artifactComments.openCount", { n: threads.length })}</span> : null}
				</h2>
				<Button aria-label={t("artifactComments.close")} onClick={onClose} size="icon-sm" type="button" variant="quiet">
					<X aria-hidden="true" className="size-icon-sm" />
				</Button>
			</div>
			<div className="board-scrollbar min-h-0 flex-1 overflow-y-auto" ref={scrollRef}>
				<div className="flex flex-col gap-2.5 p-2.5" ref={contentRef}>
					{where ? <p className="text-caption font-medium text-muted-foreground tabular-nums" data-testid="artifact-comments-where">{where}</p> : null}
					{!snapshot && comments.error ? (
						<div className="flex flex-col items-center gap-2 p-4 text-center text-xs text-muted-foreground" role="alert">
							{t("artifactComments.loadError")}
							<RetryButton onClick={() => void comments.refetch()} />
						</div>
					) : null}
					{!anchor && threads.length > 0 ? (
						<div className="flex flex-col divide-y divide-border">
							{threads.map((thread, index) => {
								const chosen = openThreadId !== null && threads.some((other) => other.id === openThreadId);
								return (chosen ? thread.id === openThreadId : index === 0) ? threadView(thread) : (
									<div className="flex flex-col py-1 first:pt-0 last:pb-0" key={thread.id}>
										<ThreadLine onExpand={() => setOpenThreadId(thread.id)} thread={thread} viewer={viewer} />
									</div>
								);
							})}
						</div>
					) : null}
					{!anchor && resolvedThreads.length > 0 ? (
						<div className={cn("flex flex-col gap-2", threads.length > 0 && "border-t border-border pt-2.5")}>
							<button
								aria-expanded={showResolved}
								className="text-left text-caption text-logo-accent-text hover:underline"
								onClick={() => setShowResolved((current) => !current)}
								type="button"
							>
								{t("artifactComments.resolvedOnElement", { n: resolvedThreads.length })} · {showResolved ? t("artifactComments.hide") : t("artifactComments.show")}
							</button>
							{showResolved ? <div className="flex flex-col divide-y divide-border">{resolvedThreads.map(threadView)}</div> : null}
						</div>
					) : null}
					{snapshot && anchor ? (
						<NewCommentBox
							draft={draft}
							onCancel={cancel}
							onChange={(text) => updateDraft(element.path, draft.failed ? { text, clientId: crypto.randomUUID() } : { text })}
							onSubmit={() => void post()}
						/>
					) : null}
				</div>
			</div>
		</aside>
	);
}

/**
 * Comments on a page the comments bridge can't run in (its own policy blocks
 * the script, say): nothing on it can be pointed at, so every thread is
 * listed, read-only, under the text it was left on, with a notice saying why.
 */
export function ArtifactCommentsReadOnlyList({ className, comments, groups, onClose, onNaturalHeight, style }: {
	comments: ArtifactCommentsState;
	groups: ArtifactCommentGroups;
	/** Leaves comment mode: there is nothing else to do here. */
	onClose: () => void;
	className?: string;
	style?: CSSProperties;
	onNaturalHeight?: (height: number) => void;
}) {
	const { t } = useTranslation();
	const snapshot = comments.snapshot;
	const viewer: Viewer = snapshot?.you ?? { userId: "", isOwner: false };
	const version = snapshot?.version ?? 0;
	const [showResolved, setShowResolved] = useState(false);
	const { asideRef, contentRef, scrollRef } = useNaturalHeight(onNaturalHeight);
	const open = [...groups.open, ...groups.unanchored];
	const resolvedCount = groups.resolved.reduce((n, group) => n + group.threads.length, 0);
	const section = (group: (typeof open)[number]) => (
		<section className="flex flex-col gap-1.5" data-testid="artifact-comments-readonly-group" key={group.path}>
			<p className="truncate border-l-2 border-border pl-2 text-caption text-muted-foreground">{group.anchor.excerpt}</p>
			<div className="flex flex-col divide-y divide-border">
				{group.threads.map((thread) => (
					<ThreadView comments={comments} key={thread.id} readOnly thread={thread} version={version} viewer={viewer} />
				))}
			</div>
		</section>
	);
	return (
		<aside
			aria-label={t("artifactComments.title")}
			className={cn("flex w-[340px] flex-col", className)}
			onKeyDown={(event) => {
				if (event.key === "Escape" && !event.defaultPrevented) {
					event.preventDefault();
					onClose();
				}
			}}
			ref={asideRef}
			style={style}
		>
			<div className="flex h-10 shrink-0 items-center gap-1 border-b border-border pr-1.5 pl-3">
				<h2 className="min-w-0 flex-1 truncate text-sm font-semibold text-foreground">{t("artifactComments.title")}</h2>
				<Button aria-label={t("artifactComments.close")} onClick={onClose} size="icon-sm" type="button" variant="quiet">
					<X aria-hidden="true" className="size-icon-sm" />
				</Button>
			</div>
			<div className="board-scrollbar min-h-0 flex-1 overflow-y-auto" ref={scrollRef}>
				<div className="flex flex-col gap-3 p-2.5" ref={contentRef}>
					<p className="text-caption text-muted-foreground" role="status">{t("artifactComments.unsupported")}</p>
					{!snapshot && comments.error ? (
						<div className="flex flex-col items-center gap-2 p-4 text-center text-xs text-muted-foreground" role="alert">
							{t("artifactComments.loadError")}
							<RetryButton onClick={() => void comments.refetch()} />
						</div>
					) : null}
					{open.map(section)}
					{resolvedCount > 0 ? (
						<div className={cn("flex flex-col gap-3", open.length > 0 && "border-t border-border pt-2.5")}>
							<button
								aria-expanded={showResolved}
								className="text-left text-caption text-logo-accent-text hover:underline"
								onClick={() => setShowResolved((current) => !current)}
								type="button"
							>
								{t("artifactComments.resolvedSection", { n: resolvedCount })} · {showResolved ? t("artifactComments.hide") : t("artifactComments.show")}
							</button>
							{showResolved ? groups.resolved.map(section) : null}
						</div>
					) : null}
				</div>
			</div>
		</aside>
	);
}
