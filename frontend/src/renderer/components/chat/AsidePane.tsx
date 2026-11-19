import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ArrowUp, AtSign, CircleCheck, MoreHorizontal } from "@/components/app-icons";
import { useCloudCp } from "../../hooks/useCloudCp";
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
	patchCachedAside,
	type CommentDraft,
} from "../../lib/cloud-asides";
import { CHAT_EVENTS_CACHE_MS } from "../../lib/cloud-chat-history";
import type {
	CloudCpAside,
	CloudCpAsideListResponse,
	CloudCpAsideReply,
	CloudCpAsideThread,
	CloudCpClientEvent,
	CloudCpEventMention,
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

/**
 * The comment pane beside the conversation, like the comment thread of a
 * shared document: its header ("Comment" and the session's title, Resolve or
 * Reopen, Mute thread, close), what the thread is about, the comments, and a
 * reply box with @ mentions and, for people who can prompt the agent, "Also
 * send to agent". Comments are between people; nothing here reaches the
 * agent unless that box is ticked.
 */
export function AsidePaneView({
	title,
	sessionTitle,
	anchor,
	message,
	replies,
	resolvedNote,
	muted,
	onMute,
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
}: {
	title: string;
	sessionTitle: string;
	anchor?: CommentAnchorView;
	/** The tagged message that started the thread; a comment thread has none. */
	message?: AsideEntry;
	replies: readonly AsideEntry[];
	/** "Resolved by Mohak · now", shown under the comments while resolved. */
	resolvedNote?: string;
	muted: boolean;
	onMute?: (muted: boolean) => void;
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
}) {
	const { t } = useTranslation();
	const list = useRef<HTMLDivElement>(null);
	// The newest comment stays in view as comments arrive.
	const newest = replies.at(-1)?.id;
	useEffect(() => {
		const node = list.current;
		if (node) node.scrollTop = node.scrollHeight;
	}, [newest, message?.id]);
	return (
		<aside
			aria-label={title}
			className="flex h-full min-h-0 w-[min(340px,45%)] shrink-0 flex-col border-l border-border bg-background"
			data-testid="aside-pane"
		>
			<header className="flex h-9 shrink-0 items-center gap-1.5 border-b border-border pr-1.5 pl-3.5">
				<span className="shrink-0 text-control font-medium text-foreground">{title}</span>
				<span className="min-w-0 flex-1 truncate text-xs text-muted-foreground" title={sessionTitle}>{sessionTitle}</span>
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
				{onMute ? (
					<DropdownMenu>
						<DropdownMenuTrigger asChild>
							<Button aria-label={t("comment.more")} size="icon-sm" type="button" variant="quiet">
								<MoreHorizontal aria-hidden="true" className="size-icon-base" />
							</Button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" className="min-w-40">
							<DropdownMenuItem onSelect={() => onMute(!muted)}>{muted ? t("comment.unmute") : t("comment.mute")}</DropdownMenuItem>
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
						{replies.map((reply) => <AsideEntryRow entry={reply} key={reply.id} onDelete={onDelete} onEdit={onEdit} />)}
						{resolvedNote ? (
							<div className="flex items-center gap-2 text-caption text-muted-foreground" data-comment-resolved="">
								<span aria-hidden="true" className="h-px flex-1 bg-border" />
								<span>{resolvedNote}</span>
								<span aria-hidden="true" className="h-px flex-1 bg-border" />
							</div>
						) : null}
					</div>
				</div>
			</MentionTintContext.Provider>
			<AsideReplyBox
				canSendToAgent={canSendToAgent && !resolvedNote}
				disabled={replyDisabled}
				firstComment={firstComment}
				focusKey={focusKey}
				mentionSource={mentionSource}
				onSend={onReply}
			/>
		</aside>
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
				<button className="w-fit text-caption text-link hover:underline" onClick={anchor.onJump} type="button">{t("comment.jump")}</button>
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
}: {
	disabled?: string;
	focusKey?: number;
	mentionSource?: AsideMentionSource;
	onSend: (text: string, mentions: ComposerMention[], sendToAgent: boolean) => Promise<void>;
	canSendToAgent: boolean;
	firstComment: boolean;
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

	useEffect(() => {
		if (focusKey !== undefined && !disabled) editor.current?.focus();
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
						<Checkbox checked={sendToAgent} id={checkboxId} onCheckedChange={(value) => setSendToAgent(value === true)} />
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
							<span className={cn("tabular-nums", thread.unread && "text-accent")}>{t("comment.count", { count: thread.replyCount })}</span>
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
	onClose: () => void;
}) {
	const { t } = useTranslation();
	const { client, ready, baseUrl, userId } = useCloudCp();
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
	const listed = useQuery<CloudCpAsideListResponse>({ queryKey: cloudAsidesKey(orgId, sessionId), enabled: false }).data?.items.find((item) => item.asideId === asideId);
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
		const gone = Boolean(eventsLoaded && !shown && !thread.data?.message && aside.anchor.kind !== "agent_message");
		const onJump = () => jumpToCommentAnchor(aside.anchor.sequence);
		if (aside.quote) return { quote: aside.quote, gone, onJump };
		const text = thread.data?.message.text ?? shown?.text ?? "";
		return { label: anchorLabel(t as unknown as AnchorLabelT, aside.anchor.kind, thread.data?.message.authorName ?? shown?.authorName), excerpt: messageExcerpt(text), gone, onJump };
	}, [aside, comment, events, eventsLoaded, t, thread.data?.message]);

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
		void client.markSessionAsideRead(orgId, sessionId, asideId)
			.then((next) => patchCachedAside(queryClient, orgId, sessionId, asideId, next))
			.catch(() => {
				// Asked again when the next comment arrives or the pane opens again.
				markedKey.current = "";
			});
	}, [asideId, attentive, baseUrl, client, newestFromOthers, orgId, queryClient, ready, sessionId, userId]);

	const muted = aside?.muted ?? false;
	const onMute = useCallback((next: boolean) => {
		patchCachedAside(queryClient, orgId, sessionId, asideId, { muted: next });
		void client.muteSessionAside(orgId, sessionId, asideId, next)
			.then((changed) => patchCachedAside(queryClient, orgId, sessionId, asideId, changed))
			.catch(() => patchCachedAside(queryClient, orgId, sessionId, asideId, { muted: !next }));
	}, [asideId, client, orgId, queryClient, sessionId]);

	const resolved = Boolean(aside?.resolvedAt);
	const setResolved = useCallback((next: boolean) => {
		const before = aside ? { resolvedAt: aside.resolvedAt, resolvedBy: aside.resolvedBy } : undefined;
		patchCachedAside(queryClient, orgId, sessionId, asideId, next ? { resolvedAt: new Date().toISOString() } : { resolvedAt: undefined, resolvedBy: undefined });
		const request = next ? client.resolveSessionAside(orgId, sessionId, asideId) : client.reopenSessionAside(orgId, sessionId, asideId);
		void request
			.then((changed) => patchCachedAside(queryClient, orgId, sessionId, asideId, changed))
			.catch(() => { if (before) patchCachedAside(queryClient, orgId, sessionId, asideId, before); });
	}, [aside, asideId, client, orgId, queryClient, sessionId]);

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
				aside: { ...current.aside, ...(reply ? { replyCount: current.aside.replyCount + 1, lastReplyAt: reply.createdAt } : {}), unread: false, ...(result.aside ?? {}) },
				replies: reply ? mergeAsideReplies(current.replies, [reply]) : current.replies,
			});
			patchCachedAside(queryClient, orgId, sessionId, asideId, { unread: false, ...(reply ? { lastReplyAt: reply.createdAt } : {}), ...(result.aside ?? {}) });
			// The comment resolved this person's mention and reply rows for the thread.
			void queryClient.invalidateQueries({ queryKey: cloudInboxQueryKey(baseUrl, userId) });
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

	const resolvedNote = resolved && aside
		? t(aside.sentToAgentAt && aside.sentToAgentAt === aside.resolvedAt ? "comment.resolvedSent" : "comment.resolved", {
			name: aside.resolvedBy?.name ?? t("comment.someone"),
			time: formatTimeCompact(aside.resolvedAt),
		})
		: undefined;

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
			onReopen={resolved && !ended ? () => setResolved(false) : undefined}
			onReply={onReply}
			onResolve={!resolved && !ended && aside ? () => setResolved(true) : undefined}
			replies={replies}
			replyDisabled={ended ? t("comment.ended") : undefined}
			resolvedNote={resolvedNote}
			sessionTitle={sessionTitle}
			title={t("comment.title")}
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
			title={t("comment.newTitle")}
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
