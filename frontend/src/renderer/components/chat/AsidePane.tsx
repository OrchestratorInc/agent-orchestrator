import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ArrowUp, AtSign, MoreHorizontal } from "@/components/app-icons";
import { useCloudCp } from "../../hooks/useCloudCp";
import {
	asideIdOf,
	asideReplyOf,
	cloudAsideKey,
	loadAsideThread,
	mergeAsideReplies,
	patchCachedAside,
} from "../../lib/cloud-asides";
import { CHAT_EVENTS_CACHE_MS } from "../../lib/cloud-chat-history";
import type {
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
import { CloseButton } from "../ui/close-button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { NO_MENTION_TINT } from "./AsideMarker";
import { ComposerEditor, type ComposerEditorHandle, type ComposerEditorSnapshot, type ComposerTrigger } from "./ComposerEditor";
import { ComposerSuggestMenu } from "./ComposerSuggestMenu";
import { MAX_SUGGESTIONS, moveHighlight } from "./composerSuggest";
import { MentionedText, MentionTintContext } from "./MentionChip";
import { mentionSuggestions } from "./mentionSuggestions";

/** One entry in the pane: the message that started the aside, or a reply. */
export interface AsideEntry {
	id: string;
	text: string;
	mentions: readonly CloudCpEventMention[];
	authorName?: string;
	createdAt: string;
	/** Still on its way to the control plane. */
	pending?: boolean;
}

/** Who the reply box's @ menu offers: people who can open the session, and the workspace's teams. */
export interface AsideMentionSource {
	onQuery: (query: string | undefined) => void;
	candidates?: CloudCpMentionCandidates;
	workspaceName?: string;
	selfUserId?: string;
}

/**
 * The aside beside the conversation, like a thread in a chat app: its header
 * ("Aside" and the session's title, Mute aside, close), the message that
 * started it, an "N replies" divider and the replies, then a reply box with
 * @ mentions. Replies are between people; nothing here reaches the agent.
 */
export function AsidePaneView({
	sessionTitle,
	message,
	replies,
	muted,
	onMute,
	onClose,
	onReply,
	replyDisabled,
	mentionSource,
	focusKey,
}: {
	sessionTitle: string;
	message?: AsideEntry;
	replies: readonly AsideEntry[];
	muted: boolean;
	onMute?: (muted: boolean) => void;
	onClose: () => void;
	/** Sends a reply; rejects with the reason, in the reader's words, to keep the draft. */
	onReply: (text: string, mentions: ComposerMention[]) => Promise<void>;
	/** Why no reply can be written (the session ended); undefined while one can. */
	replyDisabled?: string;
	mentionSource?: AsideMentionSource;
	/** A new value puts the caret in the reply box. */
	focusKey?: number;
}) {
	const { t } = useTranslation();
	const list = useRef<HTMLDivElement>(null);
	// The newest reply stays in view as replies arrive.
	const newest = replies.at(-1)?.id;
	useEffect(() => {
		const node = list.current;
		if (node) node.scrollTop = node.scrollHeight;
	}, [newest, message?.id]);
	return (
		<aside
			aria-label={t("aside.title")}
			className="flex h-full min-h-0 w-[min(340px,45%)] shrink-0 flex-col border-l border-border bg-background"
			data-testid="aside-pane"
		>
			<header className="flex h-9 shrink-0 items-center gap-2 border-b border-border pr-1.5 pl-3.5">
				<span className="shrink-0 text-control font-medium text-foreground">{t("aside.title")}</span>
				<span className="min-w-0 flex-1 truncate text-xs text-muted-foreground" title={sessionTitle}>{sessionTitle}</span>
				{onMute ? (
					<DropdownMenu>
						<DropdownMenuTrigger asChild>
							<Button aria-label={t("aside.more")} size="icon-sm" type="button" variant="quiet">
								<MoreHorizontal aria-hidden="true" className="size-icon-base" />
							</Button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" className="min-w-40">
							<DropdownMenuItem onSelect={() => onMute(!muted)}>{muted ? t("aside.unmute") : t("aside.mute")}</DropdownMenuItem>
						</DropdownMenuContent>
					</DropdownMenu>
				) : null}
				<CloseButton aria-label={t("aside.close")} onClick={onClose} size="icon-sm" />
			</header>
			<MentionTintContext.Provider value={NO_MENTION_TINT}>
				<div className="min-h-0 flex-1 overflow-y-auto px-3.5 pt-3.5 pb-2" ref={list}>
					<div className="flex flex-col gap-3.5">
						{message ? <AsideEntryRow entry={message} /> : null}
						{replies.length > 0 ? (
							<div className="flex items-center gap-2 text-caption text-muted-foreground" data-aside-divider="">
								<span aria-hidden="true" className="h-px flex-1 bg-border" />
								<span className="tabular-nums">{t("aside.replies", { count: replies.length })}</span>
								<span aria-hidden="true" className="h-px flex-1 bg-border" />
							</div>
						) : null}
						{replies.map((reply) => <AsideEntryRow entry={reply} key={reply.id} />)}
					</div>
				</div>
			</MentionTintContext.Provider>
			<AsideReplyBox disabled={replyDisabled} focusKey={focusKey} mentionSource={mentionSource} onSend={onReply} />
		</aside>
	);
}

function AsideEntryRow({ entry }: { entry: AsideEntry }) {
	return (
		<div className={cn("flex items-start gap-2.5", entry.pending && "opacity-60")} data-aside-entry={entry.id}>
			<PersonAvatar name={entry.authorName || "?"} />
			<div className="min-w-0 flex-1">
				<div className="flex items-baseline gap-2">
					<span className="min-w-0 truncate text-control font-medium text-foreground">{entry.authorName}</span>
					<time className="shrink-0 text-caption tabular-nums text-muted-foreground" dateTime={entry.createdAt}>
						{formatTimeCompact(entry.createdAt)}
					</time>
				</div>
				<p className="mt-0.5 whitespace-pre-wrap break-words text-control leading-[1.55] text-foreground">
					<MentionedText mentions={entry.mentions} text={entry.text} />
				</p>
			</div>
		</div>
	);
}

/** The reply box: a short composer whose @ opens the people and teams menu. Enter sends. */
function AsideReplyBox({
	disabled,
	focusKey,
	mentionSource,
	onSend,
}: {
	disabled?: string;
	focusKey?: number;
	mentionSource?: AsideMentionSource;
	onSend: (text: string, mentions: ComposerMention[]) => Promise<void>;
}) {
	const { t } = useTranslation();
	const editor = useRef<ComposerEditorHandle>(null);
	const menuId = useId();
	const [trigger, setTrigger] = useState<ComposerTrigger>();
	const [dismissedKey, setDismissedKey] = useState<string>();
	const [highlighted, setHighlighted] = useState(0);
	const [hasText, setHasText] = useState(false);
	const [sending, setSending] = useState(false);
	const [error, setError] = useState<string>();

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
		if (text === "") return;
		const mentions = snapshot.mentions ?? [];
		if (mentions.length > MAX_MENTIONS) {
			setError(t("mention.tooMany", { max: MAX_MENTIONS }));
			return;
		}
		setError(undefined);
		setSending(true);
		editor.current?.clear();
		try {
			await onSend(text, mentions);
		} catch (reason) {
			// Nothing was sent: the draft comes back, with its chips.
			editor.current?.setText(snapshot.text, "end", mentions);
			setError(reason instanceof Error && reason.message ? reason.message : t("aside.replyFailed"));
		} finally {
			setSending(false);
		}
	}, [disabled, onSend, sending, t]);

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
					label={t("aside.replyLabel")}
					menuId={menuId}
					menuOpen={menuOpen}
					onChange={onChange}
					onComplete={onComplete}
					onCompositionChange={() => undefined}
					onEnter={onEnter}
					onKeyDown={onKeyDown}
					onPaste={() => undefined}
					placeholder={disabled ?? t("aside.replyPlaceholder")}
					ref={editor}
				/>
				<div className="mt-0.5 flex items-center gap-1">
					{mentionSource && !disabled ? (
						<Button
							aria-label={t("aside.mentionSomeone")}
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
						aria-label={t("aside.send")}
						disabled={Boolean(disabled) || !hasText || sending}
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

/** Why a reply was refused, as a message key; nothing was sent. */
function replyRefusalKey(error: unknown): MessageKey {
	const reason = mentionRefusal(error);
	if (reason) {
		return reason === "too_many" ? "mention.refused.tooMany"
			: reason === "no_access" ? "mention.refused.noAccess"
				: reason === "outside_session" ? "mention.refused.outsideSession"
					: "mention.refused.unknown";
	}
	return error instanceof CloudCpError && error.status === 409 ? "aside.replyEnded" : "aside.replyFailed";
}

/** What a notification said about the aside, shown until the aside itself loads (no cache after a restart). */
export interface AsideFallback {
	kind: "mention" | "aside_reply";
	body: string;
	authorName?: string;
	createdAt: string;
}

const EMPTY_EVENTS: CloudCpClientEvent[] = [];

/**
 * The aside open beside a shared Cloud session's conversation. It draws from
 * what is already here (the message from the conversation, the replies the
 * aside's arrival read into the cache, and replies on the session's stream),
 * reads the aside again behind that, and marks it read while the person can
 * see it. Viewers reply too: a reply needs no composer lease.
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
	onClose,
}: {
	orgId: string;
	sessionId: string;
	sessionTitle: string;
	asideId: string;
	/** The session's events the chat holds, its live replies included; undefined until they load. */
	events?: readonly CloudCpClientEvent[];
	/** The session ended or was deleted: no more replies. */
	ended: boolean;
	/** The person can see it now (the window is visible and focused). */
	attentive: boolean;
	focusKey?: number;
	workspaceName?: string;
	fallback?: AsideFallback;
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

	// The message: from the conversation when it is loaded (it draws at once),
	// else from the aside, else from the notification that opened it.
	const message = useMemo<AsideEntry | undefined>(() => {
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
		if (fallback?.kind === "mention") return { id: "message:fallback", text: fallback.body, mentions: [], authorName: fallback.authorName, createdAt: fallback.createdAt };
		return undefined;
	}, [asideId, events, fallback, thread.data?.message]);

	const [sent, setSent] = useState<CloudCpAsideReply[]>([]);
	const [pending, setPending] = useState<AsideEntry[]>([]);
	const live = useMemo(
		() => events.flatMap((event) => {
			const reply = asideReplyOf(event);
			return reply && reply.asideId === asideId ? [reply] : [];
		}),
		[asideId, events],
	);
	const replies = useMemo<AsideEntry[]>(() => {
		const stored = mergeAsideReplies(thread.data?.replies ?? [], live, sent.filter((reply) => reply.asideId === asideId));
		const shown: AsideEntry[] = stored.map((reply) => ({
			id: reply.replyId, text: reply.text, mentions: reply.mentions, authorName: reply.authorName, createdAt: reply.createdAt,
		}));
		// With nothing loaded yet, the reply the notification named stands in.
		if (shown.length === 0 && !thread.data && fallback?.kind === "aside_reply") {
			shown.push({ id: "reply:fallback", text: fallback.body, mentions: [], authorName: fallback.authorName, createdAt: fallback.createdAt });
		}
		return [...shown, ...pending];
	}, [asideId, fallback, live, pending, sent, thread.data]);

	// Read while the person can see it: on opening, and as others' replies
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
			.then((aside) => patchCachedAside(queryClient, orgId, sessionId, asideId, aside))
			.catch(() => {
				// Asked again when the next reply arrives or the pane opens again.
				markedKey.current = "";
			});
	}, [asideId, attentive, baseUrl, client, newestFromOthers, orgId, queryClient, ready, sessionId, userId]);

	const muted = thread.data?.aside.muted ?? false;
	const onMute = useCallback((next: boolean) => {
		patchCachedAside(queryClient, orgId, sessionId, asideId, { muted: next });
		void client.muteSessionAside(orgId, sessionId, asideId, next)
			.then((aside) => patchCachedAside(queryClient, orgId, sessionId, asideId, aside))
			.catch(() => patchCachedAside(queryClient, orgId, sessionId, asideId, { muted: !next }));
	}, [asideId, client, orgId, queryClient, sessionId]);

	const onReply = useCallback(async (text: string, mentions: ComposerMention[]) => {
		const clientMessageId = crypto.randomUUID();
		const shown: AsideEntry = {
			id: `pending:${clientMessageId}`, text, mentions: mentions.map(({ kind, id, name }) => ({ kind, id, name })),
			createdAt: new Date().toISOString(), pending: true,
		};
		setPending((current) => [...current, shown]);
		try {
			const { reply } = await client.replyToSessionAside(orgId, sessionId, asideId, {
				text,
				...(mentions.length ? { mentions: mentions.map(mentionToken) } : {}),
				clientMessageId,
			});
			setSent((current) => [...current, reply]);
			queryClient.setQueryData<CloudCpAsideThread>(threadKey, (current) => current && {
				...current,
				aside: { ...current.aside, replyCount: current.aside.replyCount + 1, unread: false, lastReplyAt: reply.createdAt },
				replies: mergeAsideReplies(current.replies, [reply]),
			});
			patchCachedAside(queryClient, orgId, sessionId, asideId, { unread: false, lastReplyAt: reply.createdAt });
			// The reply resolved this person's mention and reply rows for the aside.
			void queryClient.invalidateQueries({ queryKey: cloudInboxQueryKey(baseUrl, userId) });
		} catch (error) {
			throw new Error(t(replyRefusalKey(error)));
		} finally {
			setPending((current) => current.filter((entry) => entry.id !== shown.id));
		}
	}, [asideId, baseUrl, client, orgId, queryClient, sessionId, t, threadKey, userId]);

	// The reply box's @ menu: viewers write replies too, so they get it as well.
	const [mentionQuery, setMentionQuery] = useState<string>();
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
	const mentionSource = useMemo<AsideMentionSource | undefined>(() => ended ? undefined : {
		onQuery: setMentionQuery,
		candidates: candidates.data,
		workspaceName,
		selfUserId: userId || undefined,
	}, [candidates.data, ended, userId, workspaceName]);

	return (
		<AsidePaneView
			focusKey={focusKey}
			mentionSource={mentionSource}
			message={message}
			muted={muted}
			onClose={onClose}
			onMute={thread.data ? onMute : undefined}
			onReply={onReply}
			replies={replies}
			replyDisabled={ended ? t("aside.replyEnded") : undefined}
			sessionTitle={sessionTitle}
		/>
	);
}
