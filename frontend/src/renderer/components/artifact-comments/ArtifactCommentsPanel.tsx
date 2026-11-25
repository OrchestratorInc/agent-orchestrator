import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ArrowRight, ArrowUp, Check, ChevronDown, ChevronRight, MoreHorizontal, RotateCcw, Send, X } from "@/components/app-icons";
import { formatTimeTerse } from "../../lib/format-time";
import { cn } from "../../lib/utils";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { RetryButton } from "../WorkspaceDiffView";
import {
	ARTIFACT_COMMENT_MAX_BODY_LENGTH,
	type ArtifactComment,
	type ArtifactCommentAnchor,
	type ArtifactCommentAuthor,
	type ArtifactCommentGroup,
	type ArtifactCommentGroups,
	type BridgeAnchorPosition,
	type ArtifactCommentThread,
} from "../../../shared/artifact-comments";
import { useArtifactCommentAuthorLabel } from "./author-label";
import { SendThreadToAgentDialog, type ArtifactCommentsSendTarget } from "./SendThreadToAgentDialog";
import { isPendingId, type ArtifactCommentsState } from "./useArtifactComments";
import type { CommentBridgeStatus } from "./useCommentBridge";

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

/** The chat-style reply box: Enter sends, Shift+Enter adds a line, and the accent arrow sends. */
function ReplyBox({ onSubmit }: { onSubmit: (body: string, clientId: string) => Promise<unknown> }) {
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
		}
	};
	return (
		<div className="flex flex-col gap-1">
			<div className="flex items-end gap-1 rounded-md border border-border bg-background py-1 pr-1 pl-2.5 focus-within:border-logo-accent">
				<textarea
					aria-label={t("artifactComments.replyPlaceholder")}
					className="board-scrollbar max-h-32 min-h-6 w-full resize-none border-0 bg-transparent py-0.5 text-sm leading-5 text-foreground outline-none placeholder:text-muted-foreground"
					maxLength={ARTIFACT_COMMENT_MAX_BODY_LENGTH}
					onChange={(event) => setText(event.target.value)}
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
function CommentItem({ aside, comment, comments, reply, viewer }: {
	aside?: ReactNode;
	comment: ArtifactComment;
	comments: ArtifactCommentsState;
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
	const canEdit = own && !comment.deleted && !pending;
	// The ⋯ menu: Edit and Delete on your own comments; the owner may delete any.
	const canDelete = (own || viewer.isOwner) && !comment.deleted && !pending;
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

/** A thread folded to its first comment, cut to two lines, and its reply count. */
function ThreadSummary({ thread, viewer }: { thread: ArtifactCommentThread; viewer: Viewer }) {
	const { t } = useTranslation();
	const first = thread.comments[0];
	if (!first) return null;
	const replies = repliesOf(thread);
	return (
		<span className="flex flex-col gap-1">
			<AuthorRow comment={first} viewer={viewer} />
			<span className={cn("line-clamp-2 whitespace-pre-wrap break-words text-sm leading-5", first.deleted ? "italic text-muted-foreground" : "text-foreground")}>
				{first.deleted ? t("artifactComments.deleted") : first.body}
			</span>
			<span className="text-caption text-muted-foreground">
				{replies > 0 ? t("artifactComments.replies", { count: replies }) : t("artifactComments.noReplies")}
			</span>
		</span>
	);
}

/**
 * A folded thread in an expanded group: avatar, name, time, its first comment
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

function ThreadView({ comments, sendTarget, thread, viewer, version }: {
	comments: ArtifactCommentsState;
	sendTarget?: ArtifactCommentsSendTarget;
	thread: ArtifactCommentThread;
	viewer: Viewer;
	version: number;
}) {
	const { t, i18n } = useTranslation();
	const authorLabel = useArtifactCommentAuthorLabel();
	const [sendOpen, setSendOpen] = useState(false);
	const [statusError, setStatusError] = useState(false);
	const pending = isPendingId(thread.id);
	const resolved = thread.status === "resolved";
	// Send to agent: the app's owner only, and only when the session that made the file is here.
	const canSend = viewer.isOwner && Boolean(sendTarget) && !pending && !resolved;
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
				<CommentItem aside={index === 0 ? aside : undefined} comment={comment} comments={comments} key={comment.id} reply={index > 0} viewer={viewer} />
			))}
			{!resolved && !pending ? (
				<ReplyBox onSubmit={(body, clientId) => comments.reply(thread.id, body, clientId)} />
			) : null}
			{!pending ? (
				<div className="flex items-center gap-1">
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
 * One element's card. Collapsed: the newest thread's first comment, cut to two
 * lines, then "+N more" when the element has several threads, else its reply
 * count; never a thread count. Expanded: an accent border around the content.
 */
function GroupCard({ children, expanded, group, onToggle, viewer }: {
	children?: ReactNode;
	expanded: boolean;
	group: ArtifactCommentGroup;
	onToggle: () => void;
	viewer: Viewer;
}) {
	const { t } = useTranslation();
	const thread = group.threads[0];
	const first = thread?.comments[0];
	const replies = thread ? repliesOf(thread) : 0;
	if (expanded) {
		return (
			<section className="rounded-lg border border-logo-accent bg-card p-2.5" data-expanded="true" data-group-path={group.path}>
				{children}
			</section>
		);
	}
	return (
		<section className="rounded-lg border border-border bg-card" data-group-path={group.path}>
			<button
				aria-expanded={false}
				className="flex w-full flex-col gap-1 rounded-lg p-2.5 text-left outline-none hover:bg-interactive-hover focus-visible:ring-1 focus-visible:ring-ring"
				onClick={onToggle}
				type="button"
			>
				{first ? (
					<>
						<AuthorRow comment={first} viewer={viewer} />
						<span className={cn("line-clamp-2 whitespace-pre-wrap break-words text-sm leading-5", first.deleted ? "italic text-muted-foreground" : "text-foreground")}>
							{first.deleted ? t("artifactComments.deleted") : first.body}
						</span>
						{group.threads.length > 1 ? (
							<span className="text-caption text-logo-accent-text">{t("artifactComments.moreThreads", { n: group.threads.length - 1 })}</span>
						) : (
							<span className="text-caption text-muted-foreground">
								{replies > 0 ? t("artifactComments.replies", { count: replies }) : t("artifactComments.noReplies")}
							</span>
						)}
					</>
				) : null}
			</button>
		</section>
	);
}

export type ArtifactCommentsPanelProps = {
	bridgeStatus: CommentBridgeStatus;
	comments: ArtifactCommentsState;
	expandedPath: string | null;
	groups: ArtifactCommentGroups;
	onClose: () => void;
	onExpand: (path: string | null) => void;
	onCancelPick: () => void;
	picked: ArtifactCommentAnchor | null;
	positions: readonly BridgeAnchorPosition[];
	/** The session that made the file, when the app knows it: offers Send to agent to the owner. */
	sendTarget?: ArtifactCommentsSendTarget;
	/** The surface: the workspace floats it over the artifact as a popover. */
	className?: string;
};

/**
 * The comments panel beside the published artifact: one card per commented
 * element in document order, the expanded element's threads, the new comment
 * box for a picked element, then not-found and resolved threads.
 */
export function ArtifactCommentsPanel({
	bridgeStatus,
	className,
	comments,
	expandedPath,
	groups,
	onCancelPick,
	onClose,
	onExpand,
	picked,
	positions,
	sendTarget,
}: ArtifactCommentsPanelProps) {
	const { t } = useTranslation();
	const snapshot = comments.snapshot;
	const viewer: Viewer = snapshot?.you ?? { userId: "", isOwner: false };
	const version = snapshot?.version ?? 0;
	const [showResolved, setShowResolved] = useState(false);
	const [shownResolvedPath, setShownResolvedPath] = useState<string | null>(null);
	const resolvedByPath = useMemo(() => new Map(groups.resolved.map((group) => [group.path, group])), [groups.resolved]);
	// One thread open per expanded group: the newest, until another is clicked.
	// Back to the newest when the group changes or a new thread is posted on it.
	const [openThreadId, setOpenThreadId] = useState<string | null>(null);
	useEffect(() => setOpenThreadId(null), [expandedPath]);
	// New comments being written, by element: they outlive the box, which moves
	// when the posted copy turns a new element's box into its group.
	const [drafts, setDrafts] = useState<Record<string, NewCommentDraft>>({});
	const draftFor = (path: string): NewCommentDraft => drafts[path] ?? { text: "", failed: false, sending: false, clientId: "" };
	const updateDraft = (path: string, patch: Partial<NewCommentDraft>) => setDrafts((current) => {
		const base = current[path] ?? { text: "", failed: false, sending: false, clientId: crypto.randomUUID() };
		return { ...current, [path]: { ...base, ...patch } };
	});
	const dropDraft = (path: string) => setDrafts((current) => {
		const { [path]: _dropped, ...rest } = current;
		return rest;
	});
	const cancelDraft = (path: string) => {
		dropDraft(path);
		onCancelPick();
	};
	const postDraft = async (anchor: ArtifactCommentAnchor, expandAfter: boolean) => {
		const draft = drafts[anchor.path];
		const text = draft?.text.trim();
		if (!draft || !text || draft.sending) return;
		updateDraft(anchor.path, { sending: true, failed: false });
		try {
			await comments.createThread(anchor, text, draft.clientId);
			dropDraft(anchor.path);
			// The posted thread is the group's newest, so it opens.
			setOpenThreadId(null);
			onCancelPick();
			if (expandAfter) onExpand(anchor.path);
		} catch {
			updateDraft(anchor.path, { sending: false, failed: true });
		}
	};
	const newCommentBox = (anchor: ArtifactCommentAnchor, expandAfter: boolean) => (
		<NewCommentBox
			draft={draftFor(anchor.path)}
			onCancel={() => cancelDraft(anchor.path)}
			onChange={(text) => updateDraft(anchor.path, { text })}
			onSubmit={() => void postDraft(anchor, expandAfter)}
		/>
	);
	// A picked element with no open threads gets a new group at its place in the page.
	const pickedIsNew = picked !== null && !groups.open.some((group) => group.path === picked.path);
	const entries = useMemo(() => {
		const list: ({ kind: "group"; group: ArtifactCommentGroup } | { kind: "new"; anchor: ArtifactCommentAnchor })[] = groups.open.map((group) => ({ kind: "group", group }));
		if (pickedIsNew && picked) {
			const top = positions.find((position) => position.path === picked.path)?.top;
			const index = top === undefined ? 0 : groups.open.findIndex((group) => {
				const other = positions.find((position) => position.path === group.path)?.top;
				return other !== undefined && other > top;
			});
			list.splice(index < 0 ? list.length : index, 0, { kind: "new", anchor: picked });
		}
		return list;
	}, [groups.open, picked, pickedIsNew, positions]);
	// If a refresh removes the expanded group (its last thread resolved), collapse quietly.
	useEffect(() => {
		if (expandedPath && !pickedIsNew && !groups.open.some((group) => group.path === expandedPath) && !resolvedByPath.has(expandedPath)) onExpand(null);
	}, [expandedPath, groups.open, onExpand, pickedIsNew, resolvedByPath]);

	const header = (
		<div className="flex h-10 shrink-0 items-center gap-1 border-b border-border pr-1.5 pl-3">
			<h2 className="min-w-0 flex-1 truncate text-sm font-semibold text-foreground">
				{t("artifactComments.title")}
				{snapshot ? (
					<span className="font-normal text-muted-foreground">
						{" · "}{t("artifactComments.versionLabel", { version })}{comments.openCount > 0 ? ` · ${t("artifactComments.openCount", { n: comments.openCount })}` : ""}
					</span>
				) : null}
			</h2>
			<Button aria-label={t("artifactComments.close")} onClick={onClose} size="icon-sm" type="button" variant="quiet">
				<X aria-hidden="true" className="size-icon-sm" />
			</Button>
		</div>
	);

	const threadViews = (threads: ArtifactCommentThread[]) => (
		<div className="flex flex-col divide-y divide-border">
			{threads.map((thread) => (
				<ThreadView comments={comments} key={thread.id} sendTarget={sendTarget} thread={thread} version={version} viewer={viewer} />
			))}
		</div>
	);

	let body: ReactNode;
	if (!snapshot) {
		body = comments.error ? (
			<div className="flex flex-1 flex-col items-center justify-center gap-2 p-6 text-center text-xs text-muted-foreground" role="alert">
				{t("artifactComments.loadError")}
				<RetryButton onClick={() => void comments.refetch()} />
			</div>
		) : null;
	} else if (entries.length === 0 && groups.resolved.length === 0 && groups.unanchored.length === 0) {
		body = (
			<div className="flex flex-1 flex-col items-center justify-center gap-2 p-6 text-center text-xs text-muted-foreground">
				{bridgeStatus === "unsupported" ? <p role="status">{t("artifactComments.unsupported")}</p> : null}
				<p data-testid="artifact-comments-empty">
					{t("artifactComments.empty")}
					{bridgeStatus !== "unsupported" ? <><br />{t("artifactComments.emptyHint")}</> : null}
				</p>
			</div>
		);
	} else {
		body = (
			<div className="flex flex-col gap-2 p-2.5">
				{bridgeStatus === "unsupported" ? <p className="px-0.5 text-caption text-muted-foreground" role="status">{t("artifactComments.unsupported")}</p> : null}
				{entries.map((entry) => {
					if (entry.kind === "new") {
						return (
							<section className="rounded-lg border border-logo-accent bg-card p-2.5" data-group-path={entry.anchor.path} key={`new:${entry.anchor.path}`}>
								{newCommentBox(entry.anchor, true)}
							</section>
						);
					}
					const { group } = entry;
					const expanded = expandedPath === group.path;
					const resolvedHere = resolvedByPath.get(group.path);
					const composing = picked?.path === group.path;
					return (
						<GroupCard expanded={expanded} group={group} key={group.path} onToggle={() => onExpand(group.path)} viewer={viewer}>
							{composing ? (
								// Writing on an element that has threads: the box on top, what's been said below it.
								<div className="flex flex-col gap-2.5">
									{newCommentBox(picked, false)}
									<div className="flex flex-col divide-y divide-border">
										{group.threads.map((thread) => (
											<div className="py-2.5 first:pt-0 last:pb-0" key={thread.id}><ThreadSummary thread={thread} viewer={viewer} /></div>
										))}
									</div>
								</div>
							) : (
								<>
									<div className="flex flex-col divide-y divide-border">
										{group.threads.map((thread, index) => {
											const chosen = openThreadId !== null && group.threads.some((other) => other.id === openThreadId);
											const open = chosen ? thread.id === openThreadId : index === 0;
											return open ? (
												<ThreadView
													comments={comments}
													key={thread.id}
													sendTarget={sendTarget}
													thread={thread}
													version={version}
													viewer={viewer}
												/>
											) : (
												<div className="flex flex-col py-1 first:pt-0 last:pb-0" key={thread.id}>
													<ThreadLine onExpand={() => setOpenThreadId(thread.id)} thread={thread} viewer={viewer} />
												</div>
											);
										})}
									</div>
									{resolvedHere ? (
										<div className="mt-2.5 flex flex-col gap-2 border-t border-border pt-2.5">
											<button
												className="text-left text-caption text-logo-accent-text hover:underline"
												onClick={() => setShownResolvedPath(shownResolvedPath === group.path ? null : group.path)}
												type="button"
											>
												{t("artifactComments.resolvedOnElement", { n: resolvedHere.threads.length })} · {shownResolvedPath === group.path ? t("artifactComments.hide") : t("artifactComments.show")}
											</button>
											{shownResolvedPath === group.path ? threadViews(resolvedHere.threads) : null}
										</div>
									) : null}
								</>
							)}
						</GroupCard>
					);
				})}
				{groups.unanchored.length > 0 ? (
					<div className="flex flex-col gap-2">
						<p className="px-0.5 text-caption text-muted-foreground">{t("artifactComments.unanchored")}</p>
						{groups.unanchored.map((group) => (
							<section className="rounded-lg border border-border bg-card p-2.5" data-unanchored-path={group.path} key={group.path}>{threadViews(group.threads)}</section>
						))}
					</div>
				) : null}
				{groups.resolved.length > 0 ? (
					<div className="flex flex-col gap-2">
						<button
							aria-expanded={showResolved}
							className="flex items-center gap-1 rounded-md px-0.5 py-1 text-left text-xs text-muted-foreground hover:text-foreground"
							onClick={() => setShowResolved((current) => !current)}
							type="button"
						>
							<span className="flex-1">{t("artifactComments.resolvedSection", { n: groups.resolvedCount })}</span>
							{showResolved ? <ChevronDown aria-hidden="true" className="size-3.5" /> : <ChevronRight aria-hidden="true" className="size-3.5" />}
						</button>
						{showResolved ? groups.resolved.map((group) => (
							<section className="rounded-lg border border-border bg-card p-2.5" data-resolved-path={group.path} key={group.path}>
								{threadViews(group.threads)}
							</section>
						)) : null}
					</div>
				) : null}
			</div>
		);
	}

	return (
		<aside
			aria-label={t("artifactComments.title")}
			className={cn("flex w-[340px] shrink-0 flex-col", className ?? "h-full border-l border-border bg-background")}
			onKeyDown={(event) => {
				if (event.key === "Escape" && !event.defaultPrevented && !(event.target instanceof HTMLTextAreaElement)) onClose();
			}}
		>
			{header}
			<div className="board-scrollbar flex min-h-0 flex-1 flex-col overflow-y-auto">{body}</div>
		</aside>
	);
}
