import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ArrowUp, Check, ChevronDown, ChevronRight, MoreHorizontal, RotateCcw, Send, X } from "@/components/app-icons";
import { formatRelativeTime } from "../../lib/usage-format";
import { cn } from "../../lib/utils";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { RetryButton } from "../WorkspaceDiffView";
import {
	ARTIFACT_COMMENT_MAX_BODY_LENGTH,
	type ArtifactComment,
	type ArtifactCommentAnchor,
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

function useAuthorName() {
	const { t } = useTranslation();
	const label = useArtifactCommentAuthorLabel();
	// Your own comment shows as You until it has your display name (an instant copy has none).
	return (comment: ArtifactComment, viewer: Viewer) =>
		comment.author?.id === viewer.userId && !comment.author.name.trim() ? t("artifactComments.you") : label(comment.author);
}

function useRelative() {
	const { i18n } = useTranslation();
	return (iso: string) => formatRelativeTime(iso, new Date(), i18n.resolvedLanguage ?? "en");
}

function Avatar({ name }: { name: string }) {
	return (
		<span aria-hidden="true" className="inline-flex size-[18px] shrink-0 items-center justify-center rounded-full bg-muted text-[9px] font-medium text-muted-foreground">
			{initials(name)}
		</span>
	);
}

/**
 * A comment or reply box. Enter sends and Shift+Enter adds a line. A failed
 * send keeps the text with Retry; the same client id is reused so a retry
 * never saves the comment twice.
 */
function CommentComposer({ autoFocus, onCancel, onSubmit, placeholder, variant }: {
	autoFocus?: boolean;
	onCancel?: () => void;
	onSubmit: (body: string, clientId: string) => Promise<unknown>;
	placeholder: string;
	variant: "new" | "reply";
}) {
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
		} else if (event.key === "Escape" && onCancel) {
			event.stopPropagation();
			onCancel();
		}
	};
	return (
		<div className="flex flex-col gap-1.5">
			<div className="flex items-end gap-1 rounded-md bg-input/50 px-2 py-1">
				<textarea
					aria-label={placeholder}
					autoFocus={autoFocus}
					className="board-scrollbar max-h-32 min-h-7 w-full resize-none border-0 bg-transparent py-1 text-sm leading-5 text-foreground outline-none placeholder:text-muted-foreground"
					maxLength={ARTIFACT_COMMENT_MAX_BODY_LENGTH}
					onChange={(event) => setText(event.target.value)}
					onKeyDown={onKeyDown}
					placeholder={placeholder}
					rows={variant === "new" ? 2 : 1}
					value={text}
				/>
				{variant === "reply" ? (
					<Button aria-label={t("artifactComments.sendReply")} disabled={!text.trim() || sending} onClick={() => void submit()} shape="pill" size="icon-xs" type="button" variant="primary">
						<ArrowUp aria-hidden="true" className="size-3.5" />
					</Button>
				) : null}
			</div>
			{failed ? (
				<p className="flex items-center gap-1 text-caption text-destructive" role="alert">
					{t("artifactComments.postFailed")}
					<Button className="h-auto p-0 text-caption" onClick={() => void submit()} size="sm" type="button" variant="link">{t("artifactComments.retry")}</Button>
				</p>
			) : null}
			{variant === "new" ? (
				<div className="flex justify-end gap-1.5">
					{onCancel ? <Button onClick={onCancel} size="sm" type="button" variant="secondary">{t("artifactComments.cancel")}</Button> : null}
					<Button disabled={!text.trim() || sending} onClick={() => void submit()} size="sm" type="button" variant="primary">{t("artifactComments.comment")}</Button>
				</div>
			) : null}
		</div>
	);
}

function CommentItem({ comment, comments, reply, viewer }: {
	comment: ArtifactComment;
	comments: ArtifactCommentsState;
	reply: boolean;
	viewer: Viewer;
}) {
	const { t } = useTranslation();
	const authorName = useAuthorName();
	const relative = useRelative();
	const [editing, setEditing] = useState(false);
	const [draft, setDraft] = useState(comment.body);
	const [confirmDelete, setConfirmDelete] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [busy, setBusy] = useState(false);
	const own = comment.author?.id === viewer.userId;
	const pending = isPendingId(comment.id);
	const canEdit = own && !comment.deleted && !pending;
	const canDelete = (own || viewer.isOwner) && !comment.deleted && !pending;
	const name = authorName(comment, viewer);
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
		return <p className={cn("text-xs italic text-muted-foreground", reply && "ml-2.5 border-l-2 border-border pl-2")}>{t("artifactComments.deleted")}</p>;
	}
	return (
		<div className={cn("flex flex-col gap-1", reply && "ml-2.5 border-l-2 border-border pl-2", pending && "opacity-70")} data-comment-id={comment.id}>
			<div className="flex items-center gap-1.5 text-xs">
				<Avatar name={name} />
				<span className="min-w-0 truncate font-medium text-foreground">{name}</span>
				<span className="shrink-0 text-muted-foreground">· {relative(comment.createdAt)}</span>
				{comment.editedAt ? <span className="shrink-0 text-muted-foreground">· {t("artifactComments.edited")}</span> : null}
				<span className="flex-1" />
				{canEdit || canDelete ? (
					<DropdownMenu>
						<DropdownMenuTrigger asChild>
							<Button aria-label={t("artifactComments.commentActions")} size="icon-xs" type="button" variant="quiet">
								<MoreHorizontal aria-hidden="true" className="size-3.5" />
							</Button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end">
							{canEdit ? <DropdownMenuItem onSelect={() => { setDraft(comment.body); setEditing(true); }}>{t("artifactComments.edit")}</DropdownMenuItem> : null}
							{canDelete ? <DropdownMenuItem onSelect={() => setConfirmDelete(true)}>{t("artifactComments.delete")}</DropdownMenuItem> : null}
						</DropdownMenuContent>
					</DropdownMenu>
				) : null}
			</div>
			{editing ? (
				<div className="flex flex-col gap-1.5">
					<textarea
						aria-label={t("artifactComments.edit")}
						autoFocus
						className="board-scrollbar min-h-12 w-full resize-none rounded-md bg-input/50 px-2 py-1 text-sm text-foreground outline-none"
						maxLength={ARTIFACT_COMMENT_MAX_BODY_LENGTH}
						onChange={(event) => setDraft(event.target.value)}
						value={draft}
					/>
					<div className="flex justify-end gap-1.5">
						<Button disabled={busy} onClick={() => setEditing(false)} size="sm" type="button" variant="secondary">{t("artifactComments.cancel")}</Button>
						<Button disabled={busy || !draft.trim()} onClick={() => void run(() => comments.edit(comment.id, draft.trim()), () => setEditing(false))} size="sm" type="button" variant="primary">{t("artifactComments.save")}</Button>
					</div>
				</div>
			) : (
				<p className="whitespace-pre-wrap break-words text-sm text-foreground">{comment.body}</p>
			)}
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
	const canSend = viewer.isOwner && Boolean(sendTarget) && !pending;
	const setStatus = (status: "open" | "resolved") => {
		setStatusError(false);
		comments.setStatus(thread.id, status).catch(() => setStatusError(true));
	};
	const sentTime = thread.sentToAgentAt
		? new Intl.DateTimeFormat(i18n.resolvedLanguage ?? "en", { hour: "2-digit", minute: "2-digit" }).format(new Date(thread.sentToAgentAt))
		: null;
	return (
		<div className="flex flex-col gap-2 py-2 first:pt-0 last:pb-0" data-thread-id={thread.id}>
			{resolved ? (
				<p className="text-caption text-muted-foreground">{t("artifactComments.resolvedBy", { name: authorLabel(thread.resolvedBy) })}</p>
			) : null}
			{sentTime ? <p className="text-caption text-muted-foreground">{t("artifactComments.sentAt", { time: sentTime })}</p> : null}
			{thread.comments.map((comment, index) => (
				<CommentItem comment={comment} comments={comments} key={comment.id} reply={index > 0} viewer={viewer} />
			))}
			{!resolved && !pending ? (
				<CommentComposer
					onSubmit={(body, clientId) => comments.reply(thread.id, body, clientId)}
					placeholder={t("artifactComments.replyPlaceholder")}
					variant="reply"
				/>
			) : null}
			{!pending ? (
				<div className="flex items-center gap-1">
					{resolved ? (
						<Button onClick={() => setStatus("open")} size="sm" type="button" variant="secondary">
							<RotateCcw aria-hidden="true" className="size-3.5" />{t("artifactComments.reopen")}
						</Button>
					) : (
						<Button onClick={() => setStatus("resolved")} size="sm" type="button" variant="secondary">
							<Check aria-hidden="true" className="size-3.5" />{t("artifactComments.resolve")}
						</Button>
					)}
					{canSend && sendTarget ? (
						<Button className="text-muted-foreground" onClick={() => setSendOpen(true)} size="sm" type="button" variant="quiet">
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

/** One element's card: collapsed to its first comment, or expanded to all its threads. */
function GroupCard({ children, expanded, group, onToggle, viewer }: {
	children?: ReactNode;
	expanded: boolean;
	group: ArtifactCommentGroup;
	onToggle: () => void;
	viewer: Viewer;
}) {
	const { t } = useTranslation();
	const authorName = useAuthorName();
	const relative = useRelative();
	const first = group.threads[0]?.comments[0];
	const replies = Math.max(0, (group.threads[0]?.comments.length ?? 1) - 1);
	const name = first ? authorName(first, viewer) : "";
	return (
		<section
			aria-label={first ? `${name}: ${first.body.slice(0, 80)}` : undefined}
			className={cn("rounded-lg border bg-card p-2.5", expanded ? "border-primary" : "border-border")}
			data-group-path={group.path}
		>
			<button
				aria-expanded={expanded}
				className="flex w-full flex-col gap-1 text-left outline-none focus-visible:ring-1 focus-visible:ring-ring"
				onClick={onToggle}
				type="button"
			>
				{expanded ? null : first ? (
					<>
						<span className="flex items-center gap-1.5 text-xs">
							<Avatar name={name} />
							<span className="min-w-0 truncate font-medium text-foreground">{name}</span>
							<span className="shrink-0 text-muted-foreground">· {relative(first.createdAt)}</span>
						</span>
						<span className="line-clamp-2 whitespace-pre-wrap break-words text-sm text-foreground">
							{first.deleted ? t("artifactComments.deleted") : first.body}
						</span>
						<span className="text-caption text-muted-foreground">
							{group.threads.length > 1
								? t("artifactComments.moreThreads", { n: group.threads.length - 1 })
								: replies > 0 ? t("artifactComments.replies", { count: replies }) : t("artifactComments.noReplies")}
						</span>
					</>
				) : null}
			</button>
			{expanded ? <div className="flex flex-col divide-y divide-border">{children}</div> : null}
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
};

/**
 * The comments panel beside the published artifact: one card per commented
 * element in document order, the expanded element's threads, the new comment
 * box for a picked element, then resolved and not-found threads.
 */
export function ArtifactCommentsPanel({
	bridgeStatus,
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
		<div className="flex h-9 shrink-0 items-center gap-1 border-b border-border px-3">
			<h2 className="min-w-0 flex-1 truncate text-sm font-medium text-foreground">
				{t("artifactComments.title")}
				{snapshot ? <span className="text-muted-foreground"> · {t("artifactComments.versionLabel", { version })}{comments.openCount > 0 ? ` · ${t("artifactComments.openCount", { n: comments.openCount })}` : ""}</span> : null}
			</h2>
			<Button aria-label={t("artifactComments.close")} onClick={onClose} size="icon-sm" type="button" variant="quiet">
				<X aria-hidden="true" className="size-icon-sm" />
			</Button>
		</div>
	);

	let body: ReactNode;
	if (!snapshot) {
		body = comments.error ? (
			<div className="flex flex-col items-center gap-2 p-6 text-center text-xs text-muted-foreground" role="alert">
				{t("artifactComments.loadError")}
				<RetryButton onClick={() => void comments.refetch()} />
			</div>
		) : null;
	} else {
		const threadViews = (threads: ArtifactCommentThread[]) => threads.map((thread) => (
			<ThreadView comments={comments} key={thread.id} sendTarget={sendTarget} thread={thread} version={version} viewer={viewer} />
		));
		body = (
			<div className="flex flex-col gap-2 p-3">
				{bridgeStatus === "unsupported" ? <p className="text-caption text-muted-foreground" role="status">{t("artifactComments.unsupported")}</p> : null}
				{entries.length === 0 && groups.resolved.length === 0 && groups.unanchored.length === 0 ? (
					<p className="py-6 text-center text-xs text-muted-foreground">
						{t("artifactComments.empty")}
						{bridgeStatus !== "unsupported" ? <><br />{t("artifactComments.emptyHint")}</> : null}
					</p>
				) : null}
				{entries.map((entry) => {
					if (entry.kind === "new") {
						return (
							<section className="rounded-lg border border-primary bg-card p-2.5" data-group-path={entry.anchor.path} key={`new:${entry.anchor.path}`}>
								<CommentComposer
									autoFocus
									key={entry.anchor.path}
									onCancel={onCancelPick}
									onSubmit={async (body, clientId) => {
										await comments.createThread(entry.anchor, body, clientId);
										onCancelPick();
										onExpand(entry.anchor.path);
									}}
									placeholder={t("artifactComments.newPlaceholder")}
									variant="new"
								/>
							</section>
						);
					}
					const { group } = entry;
					const expanded = expandedPath === group.path;
					const resolvedHere = resolvedByPath.get(group.path);
					return (
						<GroupCard expanded={expanded} group={group} key={group.path} onToggle={() => onExpand(expanded ? null : group.path)} viewer={viewer}>
							{picked?.path === group.path ? (
								<div className="py-2 first:pt-0">
									<CommentComposer
										autoFocus
										key={group.path}
										onCancel={onCancelPick}
										onSubmit={async (body, clientId) => { await comments.createThread(picked, body, clientId); onCancelPick(); }}
										placeholder={t("artifactComments.newPlaceholder")}
										variant="new"
									/>
								</div>
							) : null}
							{threadViews(group.threads)}
							{resolvedHere ? (
								<div className="flex flex-col gap-2 py-2 last:pb-0">
									<button
										className="text-left text-caption text-muted-foreground hover:text-foreground"
										onClick={() => setShownResolvedPath(shownResolvedPath === group.path ? null : group.path)}
										type="button"
									>
										{t("artifactComments.resolvedOnElement", { n: resolvedHere.threads.length })} · {shownResolvedPath === group.path ? t("artifactComments.hide") : t("artifactComments.show")}
									</button>
									{shownResolvedPath === group.path ? threadViews(resolvedHere.threads) : null}
								</div>
							) : null}
						</GroupCard>
					);
				})}
				{groups.resolved.length > 0 ? (
					<div className="flex flex-col gap-2">
						<button
							aria-expanded={showResolved}
							className="flex items-center gap-1 text-left text-xs text-muted-foreground hover:text-foreground"
							onClick={() => setShowResolved((current) => !current)}
							type="button"
						>
							{t("artifactComments.resolvedSection", { n: groups.resolvedCount })}
							{showResolved ? <ChevronDown aria-hidden="true" className="size-3.5" /> : <ChevronRight aria-hidden="true" className="size-3.5" />}
						</button>
						{showResolved ? groups.resolved.map((group) => (
							<section className="flex flex-col divide-y divide-border rounded-lg border border-border bg-card p-2.5" data-resolved-path={group.path} key={group.path}>
								{threadViews(group.threads)}
							</section>
						)) : null}
					</div>
				) : null}
				{groups.unanchored.length > 0 ? (
					<div className="flex flex-col gap-2">
						<p className="text-xs text-muted-foreground">{t("artifactComments.unanchored")}</p>
						<section className="flex flex-col divide-y divide-border rounded-lg border border-border bg-card p-2.5">{threadViews(groups.unanchored.flatMap((group) => group.threads))}</section>
					</div>
				) : null}
			</div>
		);
	}

	return (
		<aside
			aria-label={t("artifactComments.title")}
			className="flex h-full w-[340px] shrink-0 flex-col border-l border-border bg-background"
			onKeyDown={(event) => {
				if (event.key === "Escape" && !event.defaultPrevented && !(event.target instanceof HTMLTextAreaElement)) onClose();
			}}
		>
			{header}
			<div className="board-scrollbar min-h-0 flex-1 overflow-y-auto">{body}</div>
		</aside>
	);
}
