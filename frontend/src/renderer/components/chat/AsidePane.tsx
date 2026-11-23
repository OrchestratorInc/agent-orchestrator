import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ArrowUp, AtSign, CircleCheck, MoreHorizontal } from "@/components/app-icons";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCachedQueryData } from "../../hooks/useCloudScriptRuns";
import {
	asideIdOf,
	asideRemovalOf,
	asideReplyEditOf,
	asideReplyOf,
	asideStateChangeOf,
	cloudAsideKey,
	cloudAsidesKey,
	loadAsideThread,
	mergeAsideReplies,
	openAside,
	cloudUnreadThreadsKey,
	invalidateCommentThreads,
	patchCachedAside,
	patchCommentThreadRows,
	replaceCachedAside,
	sentMessageSequence,
	threadTimeline,
	type CommentDraft,
} from "../../lib/cloud-asides";
import { CHAT_EVENTS_CACHE_MS } from "../../lib/cloud-chat-history";
import { cloudMeQueryKey } from "../../lib/cloud-offering";
import type {
	CloudCpAside,
	CloudCpAsideHistoryEntry,
	CloudCpAsideListResponse,
	CloudCpAsideReply,
	CloudCpAsideThread,
	CloudCpClientEvent,
	CloudCpEventMention,
	CloudCpMeResponse,
	CloudCpMentionCandidates,
	CloudCpNotificationListResponse,
} from "../../lib/cloud-cp/types";
import { CloudCpError } from "../../lib/cloud-cp/errors";
import { isPathQuery, MAX_MENTIONS, mentionRefusal, mentionToken, parseEventMentions, type ComposerMention } from "../../lib/cloud-mentions";
import { ASIDE_REPLY, asideReplyMetadata, cloudInboxQueryKey, MENTIONED, mentionMetadata } from "../../lib/cloud-notifications";
import { formatTimeCompact } from "../../lib/format-time";
import type { MessageKey } from "../../i18n/messages";
import { cn } from "../../lib/utils";
import { PersonAvatar } from "../teams/TeamVisuals";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { CloseButton } from "../ui/close-button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { messageExcerpt, NO_MENTION_TINT } from "./AsideMarker";
import { ComposerEditor, type ComposerEditorHandle, type ComposerEditorSnapshot, type ComposerTrigger } from "./ComposerEditor";
import { ComposerSuggestMenu } from "./ComposerSuggestMenu";
import { MAX_SUGGESTIONS, moveHighlight } from "./composerSuggest";
import { MentionedText, MentionTintContext } from "./MentionChip";
import { mentionSuggestions } from "./mentionSuggestions";

/** One entry in the pane: the tagged message that started a thread, or a comment. */
export interface AsideEntry {
	id: string;
	text: string;
	mentions: readonly CloudCpEventMention[];
	authorName?: string;
	createdAt: string;
	/** Still on its way to the control plane. */
	pending?: boolean;
	/** Its author edited it. */
	edited?: boolean;
	/** The reader wrote it, so it can be edited or deleted. */
	own?: boolean;
	/** Its session event, which orders it among the thread's state changes. */
	sequence?: number;
}

/** Who the reply box's @ menu offers: people who can open the session, and the workspace's teams. */
export interface AsideMentionSource {
	onQuery: (query: string | undefined) => void;
	candidates?: CloudCpMentionCandidates;
	workspaceName?: string;
	selfUserId?: string;
}

/** What a comment thread is about, at the top of the pane. */
export interface CommentAnchorView {
	/** The quoted passage; otherwise the thread is about the whole message. */
	quote?: string;
	/** "On the agent's answer", "On Mohak's message": for a thread on a whole message. */
	label?: string;
	/** The message's first line, under the label. */
	excerpt?: string;
	/** The message is no longer shown (a rollback): the quote stays. */
	gone?: boolean;
	/** Scrolls the conversation to the message. */
	onJump?: () => void;
	/** Names the jump ("Jump to message", or "Open in session" outside the session). */
	jumpLabel?: string;
}

/** A thread in the list of one message's threads. */
export interface CommentThreadCard {
	asideId: string;
	quote?: string;
	startedBy?: string;
	replyCount: number;
	lastAt?: string;
	unread: boolean;
}

/** How a resolved thread reads in the pane, collapsed to one status line. */
export interface ResolvedThreadView {
	/** Who resolved it (or sent it to the agent, which resolves it). */
	byName: string;
	at?: string;
	/** "Also send to agent" resolved it. */
	sent: boolean;
	/** Jumps to the message the send became. */
	onJumpToSent?: () => void;
	/** Names the jump ("Jump to the sent message", or "Open the sent message" outside the session). */
	jumpLabel?: string;
}

/**
 * The comment pane beside the conversation, like the comment thread of a
 * shared document: its header (the session's title, Resolve or Reopen, a
 * menu with Mute thread and Mark unread, close), what the thread is about,
 * the comments with its state changes among them ("Mohak resolved this"),
 * and a reply box with @ mentions and, for people who can prompt the agent,
 * "Also send to agent". A resolved thread collapses to one status line, with
 * its comments behind "Show the N comments", and its send button reads
 * "Reply and reopen". Comments are between people; nothing here reaches the
 * agent unless that box is ticked.
 */
export function AsidePaneView({
	label,
	sessionTitle,
	anchor,
	message,
	replies,
	history,
	resolved,
	muted,
	onMute,
	onMarkUnread,
	onResolve,
	onReopen,
	onClose,
	onReply,
	onEdit,
	onDelete,
	replyDisabled,
	canSendToAgent = false,
	mentionSource,
	focusKey,
	firstComment = false,
	initialText,
}: {
	/** The pane's accessible name ("Comment", "New comment"); its header shows only the session's title. */
	label: string;
	sessionTitle: string;
	anchor?: CommentAnchorView;
	/** The tagged message that started the thread; a comment thread has none. */
	message?: AsideEntry;
	replies: readonly AsideEntry[];
	/** Its resolves, reopens and sends, drawn as system rows among the comments. */
	history?: readonly CloudCpAsideHistoryEntry[];
	/** Set while the thread is resolved: the pane collapses to its status line. */
	resolved?: ResolvedThreadView;
	muted: boolean;
	onMute?: (muted: boolean) => void;
	/** "Mark unread": the thread reads as unread again for this person. */
	onMarkUnread?: () => void;
	onResolve?: () => void;
	onReopen?: () => void;
	onClose: () => void;
	/** Sends a comment; rejects with the reason, in the reader's words, to keep the draft. */
	onReply: (text: string, mentions: ComposerMention[], sendToAgent: boolean) => Promise<void>;
	onEdit?: (id: string, text: string) => Promise<void>;
	onDelete?: (id: string) => Promise<void>;
	/** Why nothing can be written (the session ended); undefined while it can. */
	replyDisabled?: string;
	/** The reader can prompt the agent, so the reply box offers "Also send to agent". */
	canSendToAgent?: boolean;
	mentionSource?: AsideMentionSource;
	/** A new value puts the caret in the reply box. */
	focusKey?: number;
	/** The box writes a new thread's first comment. */
	firstComment?: boolean;
	/** Text the reply box starts with when focusKey puts the caret there. */
	initialText?: string;
}) {
	const { t } = useTranslation();
	const list = useRef<HTMLDivElement>(null);
	// A resolved thread opens collapsed; reopening it shows everything again.
	const [expanded, setExpanded] = useState(false);
	const rows = useMemo(() => threadTimeline(replies, history), [history, replies]);
	// The newest comment stays in view as comments arrive.
	const newest = replies.at(-1)?.id;
	useEffect(() => {
		const node = list.current;
		if (node) node.scrollTop = node.scrollHeight;
	}, [newest, message?.id, expanded]);
	const commentRows = rows.map((row) => row.kind === "comment"
		? <AsideEntryRow entry={row.reply} key={row.reply.id} onDelete={onDelete} onEdit={onEdit} />
		: <AsideHistoryRow entry={row.entry} key={`history:${row.entry.sequence}:${row.entry.kind}`} />);
	const menu = onMute || onMarkUnread;
	return (
		<aside
			aria-label={label}
			className="flex h-full min-h-0 w-[min(340px,45%)] shrink-0 flex-col border-l border-border bg-background"
			data-testid="aside-pane"
			data-comment-state={resolved ? "resolved" : "open"}
		>
			<header className="flex h-9 shrink-0 items-center gap-1.5 border-b border-border pr-1.5 pl-3.5">
				<span className="min-w-0 flex-1 truncate text-control font-medium text-foreground" data-comment-pane-title="" title={sessionTitle}>{sessionTitle}</span>
				{onResolve ? (
					<Button className="h-6 gap-1 px-2 text-xs" onClick={onResolve} size="sm" type="button" variant="quiet" data-comment-resolve="">
						<CircleCheck aria-hidden="true" className="size-3.5" />
						{t("comment.resolve")}
					</Button>
				) : null}
				{onReopen ? (
					<Button className="h-6 px-2 text-xs" onClick={onReopen} size="sm" type="button" variant="secondary" data-comment-reopen="">
						{t("comment.reopen")}
					</Button>
				) : null}
				{menu ? (
					<DropdownMenu>
						<DropdownMenuTrigger asChild>
							<Button aria-label={t("comment.more")} size="icon-sm" type="button" variant="quiet">
								<MoreHorizontal aria-hidden="true" className="size-icon-base" />
							</Button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" className="min-w-40">
							{onMarkUnread ? <DropdownMenuItem onSelect={onMarkUnread}>{t("comment.markUnread")}</DropdownMenuItem> : null}
							{onMute ? <DropdownMenuItem onSelect={() => onMute(!muted)}>{muted ? t("comment.unmute") : t("comment.mute")}</DropdownMenuItem> : null}
						</DropdownMenuContent>
					</DropdownMenu>
				) : null}
				<CloseButton aria-label={t("comment.close")} onClick={onClose} size="icon-sm" />
			</header>
			<MentionTintContext.Provider value={NO_MENTION_TINT}>
				<div className="min-h-0 flex-1 overflow-y-auto px-3.5 pt-3.5 pb-2" ref={list}>
					<div className="flex flex-col gap-3.5">
						{anchor ? <CommentAnchorBlock anchor={anchor} /> : null}
						{message ? <AsideEntryRow entry={message} /> : null}
						{resolved ? (
							<>
								<div className="flex flex-col gap-1" data-comment-resolved="">
									<p className="flex items-start gap-1.5 text-caption text-muted-foreground">
										<CircleCheck aria-hidden="true" className="mt-px size-3.5 shrink-0" />
										<span>
											{resolved.sent
												? t("comment.sentResolved", { name: resolved.byName, time: resolved.at ? formatTimeCompact(resolved.at) : "" })
												: t("comment.resolved", { name: resolved.byName, time: resolved.at ? formatTimeCompact(resolved.at) : "" })}
										</span>
									</p>
									{resolved.sent && resolved.onJumpToSent ? (
										<Button className="w-fit pl-5 text-caption text-status-working" data-comment-jump-sent="" onClick={resolved.onJumpToSent} size="xs" type="button" variant="link">
											{resolved.jumpLabel ?? t("comment.jumpToSent")}
										</Button>
									) : null}
								</div>
								{replies.length > 0 ? (
									<Button
										aria-expanded={expanded}
										className="w-fit text-caption text-muted-foreground"
										data-comment-show-all=""
										onClick={() => setExpanded((value) => !value)}
										size="xs"
										type="button"
										variant="link"
									>
										{expanded ? t("comment.hideComments") : t("comment.showComments", { count: replies.length })}
									</Button>
								) : null}
								{expanded ? commentRows : null}
							</>
						) : commentRows}
					</div>
				</div>
			</MentionTintContext.Provider>
			{replyDisabled ? (
				// Read-only (the session ended): no reply box, only why.
				<p className="shrink-0 border-t border-border px-3.5 py-3 text-caption text-muted-foreground" data-comment-read-only="">{replyDisabled}</p>
			) : (
				<AsideReplyBox
					canSendToAgent={canSendToAgent && !resolved}
					firstComment={firstComment}
					focusKey={focusKey}
					initialText={initialText}
					mentionSource={mentionSource}
					onSend={onReply}
					reopens={Boolean(resolved)}
				/>
			)}
		</aside>
	);
}

/** A state change among the comments: "Mohak resolved this", "Pritom sent this thread to the agent". */
function AsideHistoryRow({ entry }: { entry: CloudCpAsideHistoryEntry }) {
	const { t } = useTranslation();
	const name = entry.actor.name || t("comment.someone");
	// A send is one entry: it also resolved the thread (no separate resolved entry).
	const text = entry.kind === "resolved" ? t("comment.history.resolved", { name })
		: entry.kind === "reopened" ? t("comment.history.reopened", { name })
			: t("comment.history.sentResolved", { name });
	return (
		<p className="pl-8.5 text-caption text-muted-foreground" data-aside-history={entry.kind}>
			{text}
			{" · "}
			<time className="tabular-nums" dateTime={entry.at}>{formatTimeCompact(entry.at)}</time>
		</p>
	);
}

/** The passage (or the message) a thread is about. */
function CommentAnchorBlock({ anchor }: { anchor: CommentAnchorView }) {
	const { t } = useTranslation();
	return (
		<div className="flex flex-col gap-1 rounded-r-lg border-l-2 border-[var(--comment-quote-edge)] bg-raised/60 px-2.5 py-2" data-comment-anchor-block="">
			{anchor.gone ? <span className="text-caption text-muted-foreground">{t("comment.gone")}</span> : null}
			{anchor.quote ? (
				<p className="text-control leading-[1.5] text-foreground/90">“{anchor.quote}”</p>
			) : (
				<>
					{anchor.label ? <span className="text-caption text-muted-foreground">{anchor.label}</span> : null}
					{anchor.excerpt ? <p className="line-clamp-2 text-control leading-[1.5] text-foreground/90">{anchor.excerpt}</p> : null}
				</>
			)}
			{anchor.onJump && !anchor.gone ? (
				<Button className="w-fit text-caption text-status-working" data-comment-jump="" onClick={anchor.onJump} size="xs" type="button" variant="link">{anchor.jumpLabel ?? t("comment.jump")}</Button>
			) : null}
		</div>
	);
}

function AsideEntryRow({ entry, onEdit, onDelete }: { entry: AsideEntry; onEdit?: (id: string, text: string) => Promise<void>; onDelete?: (id: string) => Promise<void> }) {
	const { t } = useTranslation();
	const [editing, setEditing] = useState(false);
	const [draft, setDraft] = useState(entry.text);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string>();
	const canChange = entry.own && !entry.pending && (onEdit || onDelete);
	const save = async () => {
		const text = draft.trim();
		if (!onEdit || !text || busy) return;
		setBusy(true);
		setError(undefined);
		try {
			await onEdit(entry.id, text);
			setEditing(false);
		} catch (reason) {
			setError(reason instanceof Error && reason.message ? reason.message : t("comment.editFailed"));
		} finally {
			setBusy(false);
		}
	};
	return (
		<div className={cn("group/entry flex items-start gap-2.5", entry.pending && "opacity-60")} data-aside-entry={entry.id}>
			<PersonAvatar name={entry.authorName || "?"} />
			<div className="min-w-0 flex-1">
				<div className="flex items-baseline gap-2">
					<span className="min-w-0 truncate text-control font-medium text-foreground">{entry.authorName}</span>
					<time className="shrink-0 text-caption tabular-nums text-muted-foreground" dateTime={entry.createdAt}>
						{formatTimeCompact(entry.createdAt)}
					</time>
					{entry.edited ? <span className="text-caption text-muted-foreground">{t("comment.edited")}</span> : null}
					<span className="flex-1" />
					{canChange && !editing ? (
						<DropdownMenu>
							<DropdownMenuTrigger asChild>
								<Button aria-label={t("comment.ownMore")} className="-my-1 opacity-0 focus-visible:opacity-100 group-hover/entry:opacity-100 data-[state=open]:opacity-100" size="icon-xs" type="button" variant="quiet">
									<MoreHorizontal aria-hidden="true" className="size-icon-sm" />
								</Button>
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end" className="min-w-32">
								{onEdit ? <DropdownMenuItem onSelect={() => { setDraft(entry.text); setEditing(true); }}>{t("comment.edit")}</DropdownMenuItem> : null}
								{onDelete ? (
									<DropdownMenuItem
										onSelect={() => {
											void onDelete(entry.id).catch((reason: unknown) => setError(reason instanceof Error && reason.message ? reason.message : t("comment.deleteFailed")));
										}}
									>
										{t("comment.delete")}
									</DropdownMenuItem>
								) : null}
							</DropdownMenuContent>
						</DropdownMenu>
					) : null}
				</div>
				{editing ? (
					<div className="mt-1 flex flex-col gap-1.5">
						<textarea
							aria-label={t("comment.edit")}
							autoFocus
							className="min-h-14 w-full resize-none rounded-md border border-border bg-raised px-2 py-1.5 text-control leading-[1.5] text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
							disabled={busy}
							onChange={(event) => setDraft(event.target.value)}
							onKeyDown={(event) => {
								if (event.key === "Escape") {
									event.preventDefault();
									setEditing(false);
								} else if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
									event.preventDefault();
									void save();
								}
							}}
							value={draft}
						/>
						<div className="flex justify-end gap-1">
							<Button disabled={busy} onClick={() => setEditing(false)} size="sm" type="button" variant="quiet">{t("comment.cancel")}</Button>
							<Button disabled={busy || !draft.trim()} onClick={() => void save()} size="sm" type="button" variant="primary">{t("comment.save")}</Button>
						</div>
					</div>
				) : (
					<p className="mt-0.5 whitespace-pre-wrap break-words text-control leading-[1.55] text-foreground">
						<MentionedText mentions={entry.mentions} text={entry.text} />
					</p>
				)}
				{error ? <p className="mt-1 text-xs text-error" role="alert">{error}</p> : null}
			</div>
		</div>
	);
}

/**
 * The reply box: a short composer whose @ opens the people and teams menu,
 * with "Also send to agent" under the text for people who can prompt it, as
 * Slack puts "Also send as direct message". Enter sends.
 */
function AsideReplyBox({
	disabled,
	focusKey,
	mentionSource,
	onSend,
	canSendToAgent,
	firstComment,
	initialText,
	reopens = false,
}: {
	disabled?: string;
	focusKey?: number;
	initialText?: string;
	mentionSource?: AsideMentionSource;
	onSend: (text: string, mentions: ComposerMention[], sendToAgent: boolean) => Promise<void>;
	canSendToAgent: boolean;
	firstComment: boolean;
	/** The thread is resolved: a reply reopens it, and the send button says so. */
	reopens?: boolean;
}) {
	const { t } = useTranslation();
	const editor = useRef<ComposerEditorHandle>(null);
	const menuId = useId();
	const checkboxId = useId();
	const [trigger, setTrigger] = useState<ComposerTrigger>();
	const [dismissedKey, setDismissedKey] = useState<string>();
	const [highlighted, setHighlighted] = useState(0);
	const [hasText, setHasText] = useState(false);
	const [sending, setSending] = useState(false);
	const [sendToAgent, setSendToAgent] = useState(false);
	const [error, setError] = useState<string>();
	const toAgent = canSendToAgent && sendToAgent;

	const query = trigger && trigger.kind === "file" && trigger.key !== dismissedKey && !isPathQuery(trigger.query) ? trigger.query : undefined;
	const onQuery = mentionSource?.onQuery;
	useEffect(() => {
		onQuery?.(query);
	}, [onQuery, query]);
	const suggestions = useMemo(() => query === undefined || !mentionSource ? [] : mentionSuggestions(mentionSource.candidates, query, mentionSource.selfUserId, {
		inSession: t("mention.section.inSession"),
		teams: t("mention.section.teams"),
		workspace: mentionSource.workspaceName ? t("mention.section.workspace", { name: mentionSource.workspaceName }) : t("mention.section.workspaceUnnamed"),
		hereNow: t("mention.hereNow"),
	}).slice(0, MAX_SUGGESTIONS), [mentionSource, query, t]);
	const menuOpen = suggestions.length > 0;
	const active = Math.min(highlighted, Math.max(0, suggestions.length - 1));

	// initialText is read when focusKey changes, not on every render: it seeds a
	// new comment once and never overwrites what the person typed since.
	const seed = useRef(initialText);
	seed.current = initialText;
	useEffect(() => {
		if (focusKey === undefined || disabled) return;
		// Focus first: the editor applies setText after this, so the caret it
		// puts at the end of the seeded text is the one the next key types at.
		editor.current?.focus();
		if (seed.current) editor.current?.setText(seed.current, "end", []);
	}, [disabled, focusKey]);

	const onChange = useCallback((snapshot: ComposerEditorSnapshot) => {
		setHasText(snapshot.hasText);
		setTrigger((current) => {
			if (current?.key !== snapshot.trigger?.key) setHighlighted(0);
			return snapshot.trigger;
		});
	}, []);

	const pick = useCallback((value: string) => {
		const mention = suggestions.find((suggestion) => suggestion.value === value)?.mention;
		if (!trigger || !mention) return;
		editor.current?.insertToken(trigger, mention);
		editor.current?.focus();
	}, [suggestions, trigger]);

	const submit = useCallback(async () => {
		const snapshot = editor.current?.getSnapshot();
		if (!snapshot || sending || disabled) return;
		const text = snapshot.text.trim();
		// With "Also send to agent" the thread can go as it is, with no new text.
		if (text === "" && !toAgent) return;
		const mentions = snapshot.mentions ?? [];
		if (mentions.length > MAX_MENTIONS) {
			setError(t("mention.tooMany", { max: MAX_MENTIONS }));
			return;
		}
		setError(undefined);
		setSending(true);
		editor.current?.clear();
		try {
			await onSend(text, mentions, toAgent);
			setSendToAgent(false);
		} catch (reason) {
			// Nothing was sent: the draft comes back, with its chips.
			editor.current?.setText(snapshot.text, "end", mentions);
			setError(reason instanceof Error && reason.message ? reason.message : t("comment.replyFailed"));
		} finally {
			setSending(false);
		}
	}, [disabled, onSend, sending, t, toAgent]);

	const onComplete = useCallback((_snapshot: ComposerEditorSnapshot) => (menuOpen ? suggestions[active]?.mention : undefined), [active, menuOpen, suggestions]);
	const onEnter = useCallback((_snapshot: ComposerEditorSnapshot, event: globalThis.KeyboardEvent) => {
		if (event.shiftKey) return false;
		void submit();
		return true;
	}, [submit]);
	const onKeyDown = useCallback((event: KeyboardEvent<HTMLDivElement>) => {
		if (!menuOpen) return;
		if (event.key === "ArrowDown" || event.key === "ArrowUp") {
			event.preventDefault();
			setHighlighted((current) => moveHighlight(Math.min(current, suggestions.length - 1), event.key === "ArrowDown" ? 1 : -1, suggestions.length));
		} else if (event.key === "Escape" && trigger) {
			event.preventDefault();
			event.stopPropagation();
			setDismissedKey(trigger.key);
		}
	}, [menuOpen, suggestions.length, trigger]);

	return (
		<div className="shrink-0 px-3 pb-3">
			<div className="relative rounded-[12px] bg-raised px-1.5 pt-1 pb-1.5" data-aside-reply-box="">
				{menuOpen ? (
					<ComposerSuggestMenu highlighted={active} id={menuId} items={suggestions} kind="file" onPick={pick} />
				) : null}
				<ComposerEditor
					activeIndex={active}
					compact
					disabled={Boolean(disabled)}
					enterSends
					label={firstComment ? t("comment.firstLabel") : t("comment.replyLabel")}
					menuId={menuId}
					menuOpen={menuOpen}
					onChange={onChange}
					onComplete={onComplete}
					onCompositionChange={() => undefined}
					onEnter={onEnter}
					onKeyDown={onKeyDown}
					onPaste={() => undefined}
					placeholder={disabled ?? (firstComment ? t("comment.firstPlaceholder") : t("comment.replyPlaceholder"))}
					ref={editor}
				/>
				{canSendToAgent && !disabled && !firstComment ? (
					<label className="flex w-fit cursor-pointer items-center gap-2 px-1.5 pt-1 pb-1 text-xs text-foreground/85" htmlFor={checkboxId} data-comment-send-to-agent="">
						<Checkbox
							checked={sendToAgent}
							id={checkboxId}
							onCheckedChange={(value) => {
								setSendToAgent(value === true);
								// Back to the text, so Enter sends as it did before the tick.
								editor.current?.focus();
							}}
						/>
						{t("comment.alsoSendToAgent")}
					</label>
				) : null}
				<div className="mt-0.5 flex items-center gap-1">
					{mentionSource && !disabled ? (
						<Button
							aria-label={t("comment.mentionSomeone")}
							onClick={() => {
								editor.current?.focus();
								editor.current?.insertText("@");
							}}
							size="icon-xs"
							type="button"
							variant="quiet"
						>
							<AtSign aria-hidden="true" className="size-icon-sm" />
						</Button>
					) : null}
					<span className="flex-1" />
					{reopens ? (
						// Replying reopens a resolved thread, so the button says it will.
						<Button
							data-comment-reply-reopen=""
							disabled={Boolean(disabled) || !hasText || sending}
							onClick={() => void submit()}
							shape="pill"
							size="xs"
							type="button"
							variant="primary"
						>
							{t("comment.replyAndReopen")}
						</Button>
					) : (
						<Button
							aria-label={toAgent ? t("comment.sendWithAgent") : t("comment.send")}
							disabled={Boolean(disabled) || (!hasText && !toAgent) || sending}
							onClick={() => void submit()}
							shape="pill"
							size="icon-xs"
							type="button"
							variant="primary"
						>
							<ArrowUp aria-hidden="true" className="size-icon-sm" />
						</Button>
					)}
				</div>
			</div>
			{error ? <p className="mt-1.5 px-1 text-xs text-error" role="alert">{error}</p> : null}
		</div>
	);
}

/** The list of one message's open threads: its comment icon when it has several. */
export function CommentThreadListView({
	sessionTitle,
	threads,
	onOpen,
	onClose,
}: {
	sessionTitle: string;
	threads: readonly CommentThreadCard[];
	onOpen: (asideId: string) => void;
	onClose: () => void;
}) {
	const { t } = useTranslation();
	return (
		<aside
			aria-label={t("comment.threads")}
			className="flex h-full min-h-0 w-[min(340px,45%)] shrink-0 flex-col border-l border-border bg-background"
			data-testid="aside-pane"
		>
			<header className="flex h-9 shrink-0 items-center gap-2 border-b border-border pr-1.5 pl-3.5">
				<span className="shrink-0 text-control font-medium text-foreground">{t("comment.threads")}</span>
				<span className="min-w-0 flex-1 truncate text-xs text-muted-foreground" title={sessionTitle}>{sessionTitle}</span>
				<CloseButton aria-label={t("comment.close")} onClick={onClose} size="icon-sm" />
			</header>
			<div className="min-h-0 flex-1 overflow-y-auto p-2">
				{threads.map((thread) => (
					<button
						className="flex w-full flex-col gap-1 rounded-lg px-2.5 py-2 text-left hover:bg-raised"
						data-comment-card={thread.asideId}
						key={thread.asideId}
						onClick={() => onOpen(thread.asideId)}
						type="button"
					>
						{thread.quote ? (
							<span className="line-clamp-2 border-l-2 border-[var(--comment-quote-edge)] pl-2 text-control text-foreground/90">“{thread.quote}”</span>
						) : (
							<span className="text-control text-foreground/90">{t("comment.wholeMessage")}</span>
						)}
						<span className="flex items-center gap-1.5 text-caption text-muted-foreground">
							{thread.startedBy ? <span className="truncate">{thread.startedBy}</span> : null}
							<span className={cn("tabular-nums", thread.unread && "text-status-working")}>{t("comment.count", { count: thread.replyCount })}</span>
							{thread.lastAt ? <span className="tabular-nums">· {formatTimeCompact(thread.lastAt)}</span> : null}
						</span>
					</button>
				))}
			</div>
		</aside>
	);
}

/** Why a comment was refused, as a message key; nothing was sent. */
function replyRefusalKey(error: unknown): MessageKey {
	const reason = mentionRefusal(error);
	if (reason) {
		return reason === "too_many" ? "mention.refused.tooMany"
			: reason === "no_access" ? "mention.refused.noAccess"
				: reason === "outside_session" ? "mention.refused.outsideSession"
					: "mention.refused.unknown";
	}
	if (error instanceof CloudCpError && error.status === 403) return "comment.sendToAgentForbidden";
	return error instanceof CloudCpError && error.status === 409 ? "comment.ended" : "comment.replyFailed";
}

/** What a notification said about the thread, shown until it loads (no cache after a restart). */
export interface AsideFallback {
	kind: "mention" | "aside_reply";
	body: string;
	authorName?: string;
	createdAt: string;
}

const EMPTY_EVENTS: CloudCpClientEvent[] = [];

/** Scrolls the conversation to a message's anchor and flashes it. */
export function jumpToCommentAnchor(sequence: number): void {
	const node = document.querySelector<HTMLElement>(`[data-comment-anchor="${sequence}"]`);
	if (!node) return;
	node.scrollIntoView({ block: "center", behavior: "smooth" });
	node.dataset.commentFlash = "";
	window.setTimeout(() => { delete node.dataset.commentFlash; }, 1200);
}

/** Translates one of the anchor labels. */
type AnchorLabelT = (key: "comment.onAgentAnswer" | "comment.onSteer" | "comment.onMessage" | "comment.onAMessage", values?: { name: string }) => string;

/** "On the agent's answer", "On Mohak's message", "On a steer". */
function anchorLabel(t: AnchorLabelT, kind: CommentDraft["anchor"]["kind"] | undefined, authorName?: string): string {
	if (kind === "agent_message") return t("comment.onAgentAnswer");
	if (kind === "steer") return t("comment.onSteer");
	return authorName ? t("comment.onMessage", { name: authorName }) : t("comment.onAMessage");
}

/** The conversation's message for a sequence, from the events the chat holds. */
function anchorEventText(events: readonly CloudCpClientEvent[], sequence: number): { text: string; authorName?: string } | undefined {
	const event = events.find((candidate) => candidate.sequence === sequence);
	if (!event) return undefined;
	const payload = (event.payload ?? {}) as Record<string, unknown>;
	const text = typeof payload.displayText === "string" && payload.displayText !== "" ? payload.displayText : typeof payload.text === "string" ? payload.text : "";
	return { text, ...(typeof payload.authorName === "string" ? { authorName: payload.authorName } : {}) };
}

/**
 * The comment pane open beside a shared Cloud session's conversation, on one
 * thread. It draws from what is already here (the thread's message from the
 * conversation, the comments a notification read into the cache, and
 * comments on the session's stream), reads the thread again behind that, and
 * marks it read while the person can see it. Viewers comment too: a comment
 * needs no composer lease. Only people who can prompt the agent get "Also
 * send to agent".
 */
export function CloudAsidePane({
	orgId,
	sessionId,
	sessionTitle,
	asideId,
	events: loadedEvents,
	ended,
	attentive,
	focusKey,
	workspaceName,
	fallback,
	canPrompt = false,
	anchorShown,
	onOpenInSession,
	onClose,
}: {
	orgId: string;
	sessionId: string;
	sessionTitle: string;
	asideId: string;
	/** The session's events the chat holds, its live comments included; undefined until they load. */
	events?: readonly CloudCpClientEvent[];
	/** The session ended or was deleted: no more comments. */
	ended: boolean;
	/** The person can see it now (the window is visible and focused). */
	attentive: boolean;
	focusKey?: number;
	workspaceName?: string;
	fallback?: AsideFallback;
	/** The reader can prompt the agent (not a viewer): "Also send to agent" shows. */
	canPrompt?: boolean;
	/** Whether the thread's message is on screen; false when it is gone (withdrawn, rolled back). */
	anchorShown?: (aside: Pick<CloudCpAside, "asideId" | "anchor">) => boolean | undefined;
	/**
	 * Outside the session (the threads view): opens the session at a message,
	 * with this thread open beside it. Without it, a jump scrolls the
	 * conversation on screen.
	 */
	onOpenInSession?: (sequence: number) => void;
	onClose: () => void;
}) {
	const { t } = useTranslation();
	const { client, ready, baseUrl, userId } = useCloudCp();
	// Jumps to a message: in the conversation on screen, or in its session from the threads view.
	const jumpTo = useCallback((sequence: number) => (onOpenInSession ? onOpenInSession(sequence) : jumpToCommentAnchor(sequence)), [onOpenInSession]);
	const jumpLabel = onOpenInSession ? t("comment.openInSession") : undefined;
	const queryClient = useQueryClient();
	const eventsLoaded = loadedEvents !== undefined;
	const events = loadedEvents ?? EMPTY_EVENTS;
	const threadKey = cloudAsideKey(orgId, sessionId, asideId);
	const thread = useQuery<CloudCpAsideThread>({
		queryKey: threadKey,
		enabled: ready,
		gcTime: CHAT_EVENTS_CACHE_MS,
		queryFn: ({ signal }) => loadAsideThread(client, orgId, sessionId, asideId, signal),
	});
	// The thread as the list knows it, for a pane opened before the thread loads.
	const listed = useCachedQueryData<CloudCpAsideListResponse>(cloudAsidesKey(orgId, sessionId), true)?.items.find((item) => item.asideId === asideId);
	const aside: CloudCpAside | undefined = thread.data?.aside ?? listed;
	const comment = aside?.origin === "comment";

	// A resolve or reopen on the stream (by anyone) reads the thread again.
	const newestState = useMemo(() => {
		let newest = 0;
		for (const event of events) {
			const change = asideStateChangeOf(event);
			if (change?.asideId === asideId) newest = Math.max(newest, event.sequence);
		}
		return newest;
	}, [asideId, events]);
	const seenState = useRef(newestState);
	useEffect(() => {
		if (newestState === seenState.current) return;
		seenState.current = newestState;
		void queryClient.invalidateQueries({ queryKey: threadKey });
	}, [newestState, queryClient, threadKey]);

	// The tagged message (a thread its tags started): from the conversation
	// when it is loaded, else from the thread, else from the notification.
	const message = useMemo<AsideEntry | undefined>(() => {
		if (comment) return undefined;
		const event = events.find((candidate) => asideIdOf(candidate) === asideId);
		if (event) {
			const payload = (event.payload ?? {}) as Record<string, unknown>;
			return {
				id: `message:${event.sequence}`,
				text: typeof payload.displayText === "string" && payload.displayText !== "" ? payload.displayText : typeof payload.text === "string" ? payload.text : "",
				mentions: parseEventMentions(payload.mentions),
				authorName: typeof payload.authorName === "string" ? payload.authorName : undefined,
				createdAt: event.createdAt,
			};
		}
		const stored = thread.data?.message;
		if (stored) return { id: `message:${stored.sequence}`, text: stored.text, mentions: stored.mentions, authorName: stored.authorName, createdAt: stored.createdAt };
		if (fallback?.kind === "mention" && !aside) return { id: "message:fallback", text: fallback.body, mentions: [], authorName: fallback.authorName, createdAt: fallback.createdAt };
		return undefined;
	}, [aside, asideId, comment, events, fallback, thread.data?.message]);

	// What a comment thread is about: its quote, or the message it's on.
	const anchor = useMemo<CommentAnchorView | undefined>(() => {
		if (!comment || !aside) return undefined;
		const shown = anchorEventText(events, aside.anchor.sequence);
		const gone = anchorShown?.(aside) === false;
		const onJump = () => jumpTo(aside.anchor.sequence);
		if (aside.quote) return { quote: aside.quote, gone, onJump, jumpLabel };
		const text = thread.data?.message.text ?? shown?.text ?? "";
		return { label: anchorLabel(t as unknown as AnchorLabelT, aside.anchor.kind, thread.data?.message.authorName ?? shown?.authorName), excerpt: messageExcerpt(text), gone, onJump, jumpLabel };
	}, [anchorShown, aside, comment, events, jumpLabel, jumpTo, t, thread.data?.message]);

	const [sent, setSent] = useState<CloudCpAsideReply[]>([]);
	const [pending, setPending] = useState<AsideEntry[]>([]);
	const [removed, setRemoved] = useState<Set<string>>(() => new Set());
	const live = useMemo(
		() => events.flatMap((event) => {
			const reply = asideReplyOf(event);
			return reply && reply.asideId === asideId ? [reply] : [];
		}),
		[asideId, events],
	);
	// Edits and deletions of this thread's comments on the stream.
	const liveChanges = useMemo(() => {
		const edits = new Map<string, { text: string; mentions: CloudCpEventMention[]; editedAt: string }>();
		const deleted = new Set<string>();
		for (const event of events) {
			const edit = asideReplyEditOf(event);
			if (edit?.asideId === asideId) edits.set(edit.replyId, { text: edit.text, mentions: edit.mentions, editedAt: edit.editedAt });
			const removal = asideRemovalOf(event);
			if (removal?.asideId === asideId && removal.replyId) deleted.add(removal.replyId);
		}
		return { edits, deleted };
	}, [asideId, events]);
	const replies = useMemo<AsideEntry[]>(() => {
		const stored = mergeAsideReplies(thread.data?.replies ?? [], live, sent.filter((reply) => reply.asideId === asideId));
		const shown: AsideEntry[] = stored
			.filter((reply) => !liveChanges.deleted.has(reply.replyId) && !removed.has(reply.replyId))
			.map((reply) => {
				const edit = liveChanges.edits.get(reply.replyId);
				return {
					id: reply.replyId,
					text: edit?.text ?? reply.text,
					mentions: edit?.mentions ?? reply.mentions,
					authorName: reply.authorName,
					createdAt: reply.createdAt,
					edited: Boolean(edit || reply.editedAt),
					own: Boolean(userId && reply.authorUserId === userId),
					...(reply.sequence !== undefined ? { sequence: reply.sequence } : {}),
				};
			});
		// With nothing loaded yet, the comment the notification named stands in.
		if (shown.length === 0 && !thread.data && fallback?.kind === "aside_reply") {
			shown.push({ id: "reply:fallback", text: fallback.body, mentions: [], authorName: fallback.authorName, createdAt: fallback.createdAt });
		}
		return [...shown, ...pending];
	}, [asideId, fallback, live, liveChanges, pending, removed, sent, thread.data, userId]);

	// Read while the person can see it: on opening, and as others' comments
	// arrive on the stream after that (not the ones already there when the
	// conversation loaded).
	const loadedAt = useRef<number | undefined>(undefined);
	if (loadedAt.current === undefined && eventsLoaded) loadedAt.current = events.at(-1)?.sequence ?? 0;
	const newestFromOthers = useMemo(() => {
		const after = loadedAt.current ?? Number.POSITIVE_INFINITY;
		for (let index = events.length - 1; index >= 0; index -= 1) {
			const event = events[index]!;
			if (event.sequence <= after) break;
			const reply = asideReplyOf(event);
			if (reply && reply.asideId === asideId && reply.authorUserId !== userId) return reply.replyId;
		}
		return "";
	}, [asideId, events, userId]);
	const markedKey = useRef("");
	useEffect(() => {
		if (!ready || !attentive) return;
		const key = `${asideId}:${newestFromOthers}`;
		if (markedKey.current === key) return;
		markedKey.current = key;
		patchCachedAside(queryClient, orgId, sessionId, asideId, { unread: false });
		// Its reply and mention notifications read at once too, so no badge shows.
		queryClient.setQueryData<CloudCpNotificationListResponse>(cloudInboxQueryKey(baseUrl, userId), (page) => {
			if (!page) return page;
			let read = 0;
			const items = page.items.map((row) => {
				const rowAside = row.type === ASIDE_REPLY ? asideReplyMetadata(row)?.asideId : row.type === MENTIONED ? mentionMetadata(row)?.asideId : undefined;
				if (row.status !== "unread" || rowAside !== asideId) return row;
				read += 1;
				return { ...row, status: "read" as const };
			});
			return read === 0 ? page : { ...page, items, unreadCount: Math.max(0, page.unreadCount - read) };
		});
		patchCommentThreadRows(queryClient, baseUrl, userId, asideId, { unread: false });
		void client.markSessionAsideRead(orgId, sessionId, asideId)
			.then((next) => {
				replaceCachedAside(queryClient, orgId, sessionId, asideId, next);
				void queryClient.invalidateQueries({ queryKey: cloudUnreadThreadsKey(baseUrl, userId) });
			})
			.catch(() => {
				// Asked again when the next comment arrives or the pane opens again.
				markedKey.current = "";
			});
	}, [asideId, attentive, baseUrl, client, newestFromOthers, orgId, queryClient, ready, sessionId, userId]);

	// "Mark unread": unread again for this person until they open it next.
	const onMarkUnread = useCallback(() => {
		patchCachedAside(queryClient, orgId, sessionId, asideId, { unread: true });
		patchCommentThreadRows(queryClient, baseUrl, userId, asideId, { unread: true });
		void client.markSessionAsideUnread(orgId, sessionId, asideId)
			.then((next) => {
				replaceCachedAside(queryClient, orgId, sessionId, asideId, next);
				patchCommentThreadRows(queryClient, baseUrl, userId, asideId, { unread: next.unread });
			})
			.catch(() => {
				patchCachedAside(queryClient, orgId, sessionId, asideId, { unread: false });
				patchCommentThreadRows(queryClient, baseUrl, userId, asideId, { unread: false });
			})
			.finally(() => void queryClient.invalidateQueries({ queryKey: cloudUnreadThreadsKey(baseUrl, userId) }));
	}, [asideId, baseUrl, client, orgId, queryClient, sessionId, userId]);

	const muted = aside?.muted ?? false;
	const onMute = useCallback((next: boolean) => {
		patchCachedAside(queryClient, orgId, sessionId, asideId, { muted: next });
		void client.muteSessionAside(orgId, sessionId, asideId, next)
			.then((changed) => replaceCachedAside(queryClient, orgId, sessionId, asideId, changed))
			.catch(() => patchCachedAside(queryClient, orgId, sessionId, asideId, { muted: !next }));
	}, [asideId, client, orgId, queryClient, sessionId]);

	const resolved = Boolean(aside?.resolvedAt);
	// The person's own name, for "Resolved by {Name}" before the server answers (else it read "Someone").
	const myName = useCachedQueryData<CloudCpMeResponse>([...cloudMeQueryKey, baseUrl, userId], true)?.user.displayName;
	const setResolved = useCallback((next: boolean) => {
		const before = aside ? { resolvedAt: aside.resolvedAt, resolvedBy: aside.resolvedBy } : undefined;
		patchCachedAside(queryClient, orgId, sessionId, asideId, next
			? { resolvedAt: new Date().toISOString(), ...(myName && userId ? { resolvedBy: { userId, name: myName } } : {}) }
			: { resolvedAt: undefined, resolvedBy: undefined });
		const request = next ? client.resolveSessionAside(orgId, sessionId, asideId) : client.reopenSessionAside(orgId, sessionId, asideId);
		void request
			// The aside it answers carries its new history entry ("resolved this").
			.then((changed) => replaceCachedAside(queryClient, orgId, sessionId, asideId, changed))
			.catch(() => { if (before) patchCachedAside(queryClient, orgId, sessionId, asideId, before); })
			// It moves between the threads view's Open and Resolved.
			.finally(() => invalidateCommentThreads(queryClient, baseUrl, userId));
	}, [aside, asideId, baseUrl, client, myName, orgId, queryClient, sessionId, userId]);

	const onReply = useCallback(async (text: string, mentions: ComposerMention[], sendToAgent: boolean) => {
		const clientMessageId = crypto.randomUUID();
		const shown: AsideEntry | undefined = text ? {
			id: `pending:${clientMessageId}`, text, mentions: mentions.map(({ kind, id, name }) => ({ kind, id, name })),
			createdAt: new Date().toISOString(), pending: true,
		} : undefined;
		if (shown) setPending((current) => [...current, shown]);
		try {
			const result = await client.replyToSessionAside(orgId, sessionId, asideId, {
				text,
				...(mentions.length ? { mentions: mentions.map(mentionToken) } : {}),
				clientMessageId,
				...(sendToAgent ? { sendToAgent: true } : {}),
			});
			const reply = result.reply;
			if (reply) setSent((current) => [...current, reply]);
			queryClient.setQueryData<CloudCpAsideThread>(threadKey, (current) => current && {
				...current,
				// The server's aside is whole (a reply may have reopened the thread, or resolved it by sending).
				aside: result.aside ?? { ...current.aside, ...(reply ? { replyCount: current.aside.replyCount + 1, lastReplyAt: reply.createdAt } : {}), unread: false },
				replies: reply ? mergeAsideReplies(current.replies, [reply]) : current.replies,
			});
			if (result.aside) replaceCachedAside(queryClient, orgId, sessionId, asideId, result.aside);
			else patchCachedAside(queryClient, orgId, sessionId, asideId, { unread: false, ...(reply ? { lastReplyAt: reply.createdAt } : {}) });
			// The comment resolved this person's mention and reply rows for the thread.
			void queryClient.invalidateQueries({ queryKey: cloudInboxQueryKey(baseUrl, userId) });
			// A reply can reopen the thread, or (sent to the agent) resolve it; the aside
			// the server answers carries its history, and the threads view reads again.
			invalidateCommentThreads(queryClient, baseUrl, userId);
		} catch (error) {
			throw new Error(t(replyRefusalKey(error)));
		} finally {
			if (shown) setPending((current) => current.filter((entry) => entry.id !== shown.id));
		}
	}, [asideId, baseUrl, client, orgId, queryClient, sessionId, t, threadKey, userId]);

	const onEdit = useCallback(async (replyId: string, text: string) => {
		const original = replies.find((entry) => entry.id === replyId);
		// Mentions still written in the text stay; the server checks them again.
		const kept = (original?.mentions ?? []).filter((mention) => text.includes(`@${mention.name}`));
		try {
			const { reply } = await client.editSessionAsideReply(orgId, sessionId, asideId, replyId, {
				text,
				...(kept.length ? { mentions: kept.map((mention) => mentionToken({ kind: mention.kind, id: mention.id, name: mention.name } as ComposerMention)) } : {}),
			});
			setSent((current) => [...current.filter((entry) => entry.replyId !== replyId), reply]);
			queryClient.setQueryData<CloudCpAsideThread>(threadKey, (current) => current && { ...current, replies: mergeAsideReplies(current.replies, [reply]) });
		} catch (error) {
			throw new Error(t(error instanceof CloudCpError && error.status === 409 ? "comment.ended" : "comment.editFailed"));
		}
	}, [asideId, client, orgId, queryClient, replies, sessionId, t, threadKey]);

	const onDelete = useCallback(async (replyId: string) => {
		const first = comment && replies[0]?.id === replyId;
		try {
			await client.deleteSessionAsideReply(orgId, sessionId, asideId, replyId);
		} catch (error) {
			throw new Error(t(error instanceof CloudCpError && error.status === 409 ? "comment.ended" : "comment.deleteFailed"));
		}
		setRemoved((current) => new Set(current).add(replyId));
		if (first) {
			// The first comment of a comment thread takes the thread with it.
			queryClient.setQueryData<CloudCpAsideListResponse>(cloudAsidesKey(orgId, sessionId), (list) => list && { ...list, items: list.items.filter((item) => item.asideId !== asideId) });
			onClose();
		}
	}, [asideId, client, comment, onClose, orgId, queryClient, replies, sessionId, t]);

	// The @ menu: viewers comment too, so they get it as well.
	const [mentionQuery, setMentionQuery] = useState<string>();
	const mentionSource = useMentionSource(orgId, sessionId, ended, mentionQuery, setMentionQuery, workspaceName);

	// A resolved thread collapses to who resolved it (or sent it to the agent) and when.
	const resolvedView = useMemo<ResolvedThreadView | undefined>(() => {
		if (!resolved || !aside) return undefined;
		const lastChange = aside.history?.at(-1);
		const sent = lastChange ? lastChange.kind === "sent_to_agent" : Boolean(aside.sentToAgentAt && aside.sentToAgentAt === aside.resolvedAt);
		const sentSequence = sent ? sentMessageSequence(aside) : undefined;
		return {
			byName: aside.resolvedBy?.name ?? (sent ? aside.sentToAgentBy?.name : undefined) ?? lastChange?.actor.name ?? t("comment.someone"),
			...(aside.resolvedAt ? { at: aside.resolvedAt } : {}),
			sent,
			...(sentSequence !== undefined ? { onJumpToSent: () => jumpTo(sentSequence) } : {}),
			...(onOpenInSession ? { jumpLabel: t("comment.openSentInSession") } : {}),
		};
	}, [aside, jumpTo, onOpenInSession, resolved, t]);

	return (
		<AsidePaneView
			anchor={anchor}
			canSendToAgent={canPrompt && !ended}
			focusKey={focusKey}
			mentionSource={mentionSource}
			message={message}
			muted={muted}
			onClose={onClose}
			onDelete={ended ? undefined : onDelete}
			onEdit={ended ? undefined : onEdit}
			onMute={aside ? onMute : undefined}
			onMarkUnread={aside ? onMarkUnread : undefined}
			onReopen={resolved && !ended ? () => setResolved(false) : undefined}
			onReply={onReply}
			onResolve={!resolved && !ended && aside ? () => setResolved(true) : undefined}
			history={aside?.history}
			replies={replies}
			replyDisabled={ended ? t("comment.ended") : undefined}
			resolved={resolvedView}
			sessionTitle={sessionTitle}
			label={t("comment.title")}
		/>
	);
}

/** The reply box's @ menu: people who can open the session, and the workspace's teams. */
function useMentionSource(
	orgId: string,
	sessionId: string,
	ended: boolean,
	mentionQuery: string | undefined,
	setMentionQuery: (query: string | undefined) => void,
	workspaceName: string | undefined,
): AsideMentionSource | undefined {
	const { client, ready, baseUrl, userId } = useCloudCp();
	const [settledQuery, setSettledQuery] = useState<string>();
	useEffect(() => {
		const timer = window.setTimeout(() => setSettledQuery(mentionQuery), mentionQuery ? 150 : 0);
		return () => window.clearTimeout(timer);
	}, [mentionQuery]);
	const candidates = useQuery<CloudCpMentionCandidates>({
		queryKey: ["cloud-mention-candidates", baseUrl, orgId, sessionId, (settledQuery ?? "").trim()],
		enabled: ready && !ended && settledQuery !== undefined,
		staleTime: 30_000,
		retry: false,
		placeholderData: (previous) => previous,
		queryFn: ({ signal }) => client.listMentionCandidates(orgId, sessionId, { q: (settledQuery ?? "").trim(), limit: 20 }, { signal }),
	});
	return useMemo<AsideMentionSource | undefined>(() => ended ? undefined : {
		onQuery: setMentionQuery,
		candidates: candidates.data,
		workspaceName,
		selfUserId: userId || undefined,
	}, [candidates.data, ended, setMentionQuery, userId, workspaceName]);
}

/**
 * A new comment, before it exists: "Comment" on a message (or a selected
 * passage) opened the pane. Its first comment creates the thread, which the
 * pane then shows; closing it first leaves nothing behind.
 */
export function CloudCommentDraftPane({
	orgId,
	sessionId,
	sessionTitle,
	draft,
	focusKey,
	workspaceName,
	onClose,
}: {
	orgId: string;
	sessionId: string;
	sessionTitle: string;
	draft: CommentDraft;
	focusKey?: number;
	workspaceName?: string;
	onClose: () => void;
}) {
	const { t } = useTranslation();
	const { client } = useCloudCp();
	const queryClient = useQueryClient();
	const [mentionQuery, setMentionQuery] = useState<string>();
	const mentionSource = useMentionSource(orgId, sessionId, false, mentionQuery, setMentionQuery, workspaceName);
	const anchor = useMemo<CommentAnchorView>(() => draft.quote
		? { quote: draft.quote, onJump: () => jumpToCommentAnchor(draft.anchor.sequence) }
		: { label: anchorLabel(t as unknown as AnchorLabelT, draft.anchor.kind, draft.authorName), excerpt: draft.excerpt, onJump: () => jumpToCommentAnchor(draft.anchor.sequence) },
	[draft, t]);
	const onReply = useCallback(async (text: string, mentions: ComposerMention[]) => {
		try {
			const { aside, reply } = await client.createSessionAside(orgId, sessionId, {
				anchor: { sequence: draft.anchor.sequence },
				...(draft.quote ? { quote: draft.quote } : {}),
				text,
				...(mentions.length ? { mentions: mentions.map(mentionToken) } : {}),
				clientMessageId: crypto.randomUUID(),
			});
			queryClient.setQueryData<CloudCpAsideListResponse>(cloudAsidesKey(orgId, sessionId), (list) => ({
				items: [...(list?.items ?? []).filter((item) => item.asideId !== aside.asideId), aside],
			}));
			queryClient.setQueryData<CloudCpAsideThread>(cloudAsideKey(orgId, sessionId, aside.asideId), {
				aside,
				message: { kind: draft.anchor.kind, sequence: draft.anchor.sequence, text: draft.excerpt, mentions: [], ...(draft.authorName ? { authorName: draft.authorName } : {}), createdAt: reply.createdAt },
				replies: [reply],
				hasMore: false,
			});
			openAside(sessionId, aside.asideId, { focusReply: false });
		} catch (error) {
			throw new Error(t(createRefusalKey(error)));
		}
	}, [client, draft, orgId, queryClient, sessionId, t]);
	return (
		<AsidePaneView
			anchor={anchor}
			firstComment
			focusKey={focusKey}
			mentionSource={mentionSource}
			muted={false}
			onClose={onClose}
			onReply={onReply}
			replies={[]}
			sessionTitle={sessionTitle}
			label={t("comment.newTitle")}
			initialText={draft.initialText}
		/>
	);
}

/** Why a new comment was refused, as a message key; nothing was created. */
function createRefusalKey(error: unknown): MessageKey {
	if (mentionRefusal(error)) return replyRefusalKey(error);
	if (error instanceof CloudCpError && error.status === 409) return "comment.ended";
	if (error instanceof CloudCpError && error.status === 422) return "comment.anchorGone";
	return "comment.createFailed";
}
