import * as Clipboard from "expo-clipboard";
import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { Alert, Pressable, StyleSheet, Text, View } from "react-native";
import { reviewCommentIsOpen, type ReviewComment, type ReviewReply } from "../../shared/review/review-comments";
import { reviewCommentsWorkerMessage, reviewCommentWorkerMessage } from "../../shared/review/review-messages";
import { AccountAvatar } from "./AccountAvatar";
import { AgentLogo } from "./AgentLogo";
import type { ReviewRun } from "./api";
import { AppSymbol } from "./AppSymbol";
import { GitHubComment, WorkerSessionsProvider } from "./chat/AutomationMessage";
import { RECONNECTING_ACTION_COPY, userFacingError } from "./connectionError";
import { rowDividerWidth } from "./divider";
import { haptics } from "./haptics";
import { ItemActionsMenu } from "./item-actions-menu";
import type { ItemAction } from "./item-actions-menu.types";
import { openGitHub } from "./openGitHub";
import { usePressFeedback } from "./pressFeedback";
import { ReviewActionButton } from "./review-action-button";
import { ReviewCommentFilter } from "./review-comment-filter";
import type { ReviewCommentView } from "./review-comment-filter.types";
import type { ReviewBackend } from "./reviewBackend";
import {
	blockOnGitLab,
	commentAge,
	commentAvatarUrl,
	commentCanResolve,
	commentCanSend,
	commentIsSent,
	commentLocation,
	commentPreview,
	commentReplyOffered,
	commentStartsExpanded,
	unsentOpenComments,
	verdictLabels,
	verdictTone,
	type MobileReviewBlock,
} from "./reviewCommentView";
import { markReviewCommentsSent, useSentReviewComments } from "./reviewSent";
import { rerequestKey } from "./reviewView";
import { Spinner } from "./Spinner";
import { symbols } from "./symbols";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { fontScaleCap, iconSize, radius, space, touchTarget, type } from "./tokens";
import { Dot, ListSectionHeader } from "./ui";

/** What the page's reply dock answers. */
export type ReviewReplyTarget = { comment: ReviewComment; block: MobileReviewBlock };

/**
 * The PR's review comments as the desktop's Reviews tab lists them: the
 * Orchestrator reviewer's passes and inline comments beside the provider's
 * reviews, threads and top-level comments, Open first and Resolved behind a
 * switch. A row is two lines (who and when, then where and the first words)
 * and opens in place to the whole text, its replies and Send to agent.
 */
export function ReviewCommentsSection({
	block,
	backend,
	sessionId,
	sentScope,
	runs,
	onChanged,
	onReply,
	onViewInFile,
}: {
	block: MobileReviewBlock;
	/**
	 * The machine's live connection, or null while the app re-races its
	 * endpoints (after an app switch). The section stays on screen through that;
	 * only its requests wait for the connection.
	 */
	backend: ReviewBackend | null;
	sessionId: string;
	/** Where this session's hand-sent comments are remembered. */
	sentScope: string;
	/** The session's review runs, for Cloud's stored-review delivery. */
	runs: readonly ReviewRun[];
	/** Reload the PR and its reviews after a change on the provider. */
	onChanged: () => Promise<void>;
	onReply?: (target: ReviewReplyTarget) => void;
	onViewInFile?: (file: string, line?: number) => void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [view, setView] = useState<ReviewCommentView>("open");
	// A row the person opened or folded keeps that; otherwise it starts as commentStartsExpanded says.
	const [expandedHere, setExpandedHere] = useState<ReadonlyMap<string, boolean>>(() => new Map());
	const sent = useSentReviewComments(sentScope);
	// On their way to the worker, from a row or Send all: a ref, so two taps before the next render see each other.
	const sending = useRef<Set<string>>(new Set());
	const [, rerender] = useReducer((count: number) => count + 1, 0);
	const [sendingAll, setSendingAll] = useState(false);
	const [errors, setErrors] = useState<ReadonlyMap<string, string>>(() => new Map());
	const [sectionError, setSectionError] = useState("");
	const [resolving, setResolving] = useState<string>();
	// Resolved here before the provider's next read says so.
	const [resolvedHere, setResolvedHere] = useState<ReadonlySet<string>>(() => new Set());
	// Asked to review again from here: not offered again, so a second tap cannot send another request.
	const [asked, setAsked] = useState<ReadonlySet<string>>(() => new Set());
	const providerUrl = block.provider?.url ?? block.prUrl;
	// What this machine can do (resolve, reply, ask again), from its last connection,
	// so a reconnect does not take actions out of the menus. Never sent a request.
	const knownBackend = useRef(backend);
	if (backend) knownBackend.current = backend;
	const can = backend ?? knownBackend.current;
	// The connection as of now, not as of the render that made a handler: a
	// Resolve confirmation can stay open across a reconnect.
	const liveBackend = useRef(backend);
	liveBackend.current = backend;
	const live = (): ReviewBackend => {
		if (!liveBackend.current) throw new Error(RECONNECTING_ACTION_COPY);
		return liveBackend.current;
	};

	// The SCM observer reads a PR's threads only for the open PRs it follows; ask once for the rest.
	const [details, setDetails] = useState<"idle" | "loading" | "failed">("idle");
	const [detailsAttempt, retryDetails] = useReducer((count: number) => count + 1, 0);
	const askedDetails = useRef(new Set<string>());
	const loadDetails = backend?.loadDetails;
	// Keyed on the PR, not its summary object: every quiet reload (the page polls
	// while a review runs) hands over a new object for the same PR, and a re-run
	// for that must not throw away the read already out.
	const detailsUrl = block.provider?.url;
	const detailsNumber = block.provider?.number;
	const detailsRead = Boolean(block.provider?.detailsObservedAt);
	// The PR on screen, and whether the section still is: the only reasons to drop a read's answer.
	const shownDetailsUrl = useRef(detailsUrl);
	shownDetailsUrl.current = detailsUrl;
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => { mounted.current = false; };
	}, []);
	useEffect(() => {
		if (!detailsUrl || detailsNumber === undefined || detailsRead || !loadDetails || askedDetails.current.has(detailsUrl)) return;
		askedDetails.current.add(detailsUrl);
		const stillShown = () => mounted.current && shownDetailsUrl.current === detailsUrl;
		setDetails("loading");
		void loadDetails({ number: detailsNumber, url: detailsUrl }).then(
			async () => { if (stillShown()) { setDetails("idle"); await onChanged(); } },
			() => { if (stillShown()) setDetails("failed"); },
		);
	}, [detailsAttempt, detailsNumber, detailsRead, detailsUrl, loadDetails, onChanged]);

	// Back on the machine: a "still reconnecting" note is no longer true. Other failures stay.
	const connected = Boolean(backend);
	useEffect(() => {
		if (!connected) return;
		setSectionError((current) => current === RECONNECTING_ACTION_COPY ? "" : current);
		setErrors((current) => [...current.values()].includes(RECONNECTING_ACTION_COPY)
			? new Map([...current].filter(([, message]) => message !== RECONNECTING_ACTION_COPY))
			: current);
	}, [connected]);
	const setError = useCallback((key: string, message?: string) => setErrors((current) => {
		if (!message && !current.has(key)) return current;
		const next = new Map(current);
		if (message) next.set(key, message);
		else next.delete(key);
		return next;
	}), []);
	const hold = (keys: readonly string[], held: boolean) => {
		for (const key of keys) {
			if (held) sending.current.add(key);
			else sending.current.delete(key);
		}
		rerender();
	};

	const comments = block.comments.map((comment) => resolvedHere.has(comment.key) ? { ...comment, resolved: true } : comment);
	const openComments = comments.filter(reviewCommentIsOpen);
	const resolvedComments = comments.filter((comment) => !reviewCommentIsOpen(comment));
	const shown = view === "open" ? openComments : resolvedComments;
	const unsent = unsentOpenComments(openComments, sent, sending.current);
	const isExpanded = (comment: ReviewComment) => expandedHere.get(comment.key) ?? (view === "open" && commentStartsExpanded(comment, sent));
	const setExpanded = (comment: ReviewComment, expanded: boolean) => setExpandedHere((current) => new Map(current).set(comment.key, expanded));

	// Cloud delivers an Orchestrator review it stored through its own queue; everything else is a message to the worker.
	const deliver = async (comment: ReviewComment) => {
		const connection = live();
		const run = connection.cloud && comment.source === "orchestrator" && comment.kind === "summary" ? runs.find((item) => item.id === comment.runId) : undefined;
		if (run) await connection.sendRun(sessionId, run);
		else await connection.sendToWorker(sessionId, reviewCommentWorkerMessage(comment, block));
	};
	const sendOne = async (comment: ReviewComment) => {
		if (sending.current.has(comment.sentKey) || commentIsSent(comment, sent)) return;
		hold([comment.sentKey], true);
		setError(comment.key);
		try {
			await deliver(comment);
			// Stays open, so "Sent to agent" shows where it was pressed.
			setExpanded(comment, true);
			markReviewCommentsSent(sentScope, [comment.sentKey]);
			haptics.success();
		} catch (cause) {
			setError(comment.key, userFacingError(cause, "Could not send this comment to the agent."));
		} finally {
			hold([comment.sentKey], false);
		}
	};
	const sendAll = async () => {
		const batch = unsent;
		if (!batch.length || sendingAll) return;
		const keys = batch.map((comment) => comment.sentKey);
		hold(keys, true);
		setSendingAll(true);
		setSectionError("");
		try {
			const connection = live();
			if (connection.cloud) {
				// One delivery each, marked as it lands: a failure partway leaves only the rest to send again.
				for (const comment of batch) {
					await deliver(comment);
					markReviewCommentsSent(sentScope, [comment.sentKey]);
				}
			} else {
				await connection.sendToWorker(sessionId, reviewCommentsWorkerMessage(block, batch));
				markReviewCommentsSent(sentScope, keys);
			}
			haptics.success();
		} catch (cause) {
			setSectionError(userFacingError(cause, "Could not send the open comments to the agent."));
		} finally {
			hold(keys, false);
			setSendingAll(false);
		}
	};
	const resolve = (comment: ReviewComment) => {
		const url = comment.url;
		if (!can?.resolveComment || !url || resolving) return;
		Alert.alert("Resolve comment?", "Marks the thread resolved on GitHub.", [
			{ text: "Cancel", style: "cancel" },
			{ text: "Resolve", onPress: () => void (async () => {
				setResolving(comment.key);
				setError(comment.key);
				try {
					const resolveComment = live().resolveComment;
					if (!resolveComment) return;
					await resolveComment(sessionId, providerUrl, url);
					setResolvedHere((current) => new Set(current).add(comment.key));
					setExpanded(comment, false);
					haptics.success();
					await onChanged();
				} catch (cause) {
					setError(comment.key, userFacingError(cause, "Could not resolve this comment."));
				} finally {
					setResolving(undefined);
				}
			})() },
		]);
	};
	const askAgain = async (comment: ReviewComment, reviewerId: string) => {
		if (!can?.rerequest) return;
		setError(comment.key);
		try {
			const rerequest = live().rerequest;
			if (!rerequest) return;
			await rerequest(sessionId, providerUrl, reviewerId);
			setAsked((current) => new Set(current).add(rerequestKey(reviewerId)));
			haptics.success();
			await onChanged();
		} catch (cause) {
			setError(comment.key, userFacingError(cause, "Could not request another review."));
		}
	};

	const detailsRow = details === "failed" ? (
		<View accessibilityRole="alert" style={styles.inlineNote}>
			<Text style={[styles.noteText, styles.flex]}>Could not load this pull request's threads.</Text>
			<ReviewActionButton label="Retry" icon={symbols.refresh} tone="outlined" onPress={() => { askedDetails.current.clear(); setDetails("idle"); retryDetails(); }} />
		</View>
	) : null;

	// GitHubComment opens the links in a comment as the chat does, which reads the board's sessions from this provider.
	return (
		<WorkerSessionsProvider>
		<View>
			<ListSectionHeader label="Comments" count={comments.length ? openComments.length : undefined} />
			{comments.length === 0 ? (
				<>
					{details === "loading" ? <View style={styles.loading}><Spinner accessibilityLabel="Loading comments" /></View> : null}
					{details !== "loading" ? <Text style={styles.note}>No comments yet.</Text> : null}
					{detailsRow}
				</>
			) : (
				<>
					<ReviewCommentFilter view={view} counts={{ open: openComments.length, resolved: resolvedComments.length }} onChange={(next) => { setView(next); setExpandedHere(new Map()); }} />
					{detailsRow}
					{view === "open" && unsent.length > 0 ? (
						<View style={styles.sendAll}>
							<ReviewActionButton label={unsent.length === 1 ? "Send 1 open to agent" : `Send ${unsent.length} open to agent`} accessibilityLabel="Send all open comments to the agent" icon={symbols.send} tone="filled" busy={sendingAll} disabled={sendingAll} onPress={() => void sendAll()} />
						</View>
					) : null}
					{sectionError ? <Text accessibilityRole="alert" style={[styles.note, styles.error]}>{sectionError}</Text> : null}
					{shown.length ? shown.map((comment) => {
						const expanded = isExpanded(comment);
						const reviewerId = comment.rerequestReviewerId;
						return (
							<ReviewCommentRow
								key={comment.key}
								comment={comment}
								block={block}
								expanded={expanded}
								onToggle={() => setExpanded(comment, !expanded)}
								sent={commentIsSent(comment, sent)}
								sending={sending.current.has(comment.sentKey)}
								resolving={resolving === comment.key}
								error={errors.get(comment.key)}
								onSend={commentCanSend(comment) ? () => void sendOne(comment) : undefined}
								onResolve={can?.resolveComment && commentCanResolve(comment) ? () => resolve(comment) : undefined}
								onReply={onReply && can?.reply && commentReplyOffered(comment, block) ? () => onReply({ comment, block }) : undefined}
								onAskAgain={can?.rerequest && reviewerId && !asked.has(rerequestKey(reviewerId)) ? () => void askAgain(comment, reviewerId) : undefined}
								onViewInFile={onViewInFile && comment.file ? () => onViewInFile(comment.file!, comment.line) : undefined}
								t={t}
							/>
						);
					}) : <Text style={styles.note}>{view === "open" ? "No open comments." : "Nothing resolved yet."}</Text>}
				</>
			)}
		</View>
		</WorkerSessionsProvider>
	);
}

function ReviewCommentRow({
	comment,
	block,
	expanded,
	onToggle,
	sent,
	sending,
	resolving,
	error,
	onSend,
	onResolve,
	onReply,
	onAskAgain,
	onViewInFile,
	t,
}: {
	comment: ReviewComment;
	block: MobileReviewBlock;
	expanded: boolean;
	onToggle: () => void;
	sent: boolean;
	sending: boolean;
	resolving: boolean;
	error?: string;
	onSend?: () => void;
	onResolve?: () => void;
	onReply?: () => void;
	onAskAgain?: () => void;
	onViewInFile?: () => void;
	t: Theme;
}) {
	const styles = useThemedStyles(makeStyles);
	const rowFeedback = usePressFeedback("row");
	const orchestrator = comment.source === "orchestrator";
	const name = orchestrator ? "Orchestrator" : comment.author;
	const gitLab = blockOnGitLab(block);
	const verdict = comment.kind === "summary" && comment.verdict ? comment.verdict : undefined;
	const tone = verdict ? verdictTone(verdict) : "muted";
	const verdictColor = tone === "green" ? t.green : tone === "amber" ? t.amber : t.textTertiary;
	const location = commentLocation(comment, !expanded);
	const age = commentAge(comment.at);
	const url = comment.url;
	const actions: ItemAction[] = [
		...(onSend && !sent ? [{ id: "send", label: "Send to agent", icon: "send" as const, onPress: onSend }] : []),
		...(onReply ? [{ id: "reply", label: "Reply", icon: "reply" as const, onPress: onReply }] : []),
		...(onResolve ? [{ id: "resolve", label: "Resolve", icon: "resolve" as const, onPress: onResolve }] : []),
		...(onViewInFile ? [{ id: "file", label: "View in file", icon: "file" as const, onPress: onViewInFile }] : []),
		...(onAskAgain ? [{ id: "ask", label: "Ask to review again", icon: "review" as const, onPress: onAskAgain }] : []),
		...(url ? [
			{ id: "open", label: gitLab ? "Open on GitLab" : "Open on GitHub", icon: "openExternal" as const, onPress: () => void openGitHub(url) },
			{ id: "copy", label: "Copy link", icon: "copy" as const, onPress: () => { haptics.success(); void Clipboard.setStringAsync(url); } },
		] : []),
	];
	const describe = [name, verdict ? verdictLabels[verdict] : location, comment.resolved ? "resolved" : comment.earlier ? "earlier" : "", sent ? "sent to agent" : ""].filter(Boolean).join(", ");
	return (
		<View style={styles.row} testID="review-comment">
			<Pressable
				accessibilityRole="button"
				accessibilityState={{ expanded }}
				accessibilityLabel={describe}
				accessibilityHint={expanded ? "Folds the comment" : "Opens the whole comment"}
				android_ripple={rowFeedback.android_ripple}
				onPress={onToggle}
				style={({ pressed }) => [styles.rowHead, pressed && rowFeedback.pressed]}
			>
				<View style={styles.who}>
					{orchestrator
						? <AgentLogo harness={comment.harness || "reviewer"} size={20} />
						: <AccountAvatar account={{ name: comment.author.replace(/\[bot\]$/i, ""), email: comment.author, avatarUrl: commentAvatarUrl(comment.author, block) }} size={20} />}
					<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={styles.name}>{name}</Text>
					{!orchestrator && comment.isBot ? <Text style={styles.tag}>bot</Text> : null}
					{comment.resolved ? <Text style={[styles.tag, { color: t.green }]}>resolved</Text> : null}
					{comment.earlier ? <Text style={styles.tag}>earlier</Text> : null}
					{comment.requestedByAgent ? <Text numberOfLines={1} style={[styles.tag, styles.shrink]}>requested by agent</Text> : null}
					<View style={styles.flex} />
					{sent && !expanded ? <Text style={styles.tag}>Sent to agent</Text> : null}
					{age ? <Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.age}>{age}</Text> : null}
				</View>
				<View style={styles.where}>
					{verdict ? (
						<View style={styles.verdict}>
							<Dot color={verdictColor} size={6} />
							<Text style={[styles.verdictText, { color: verdictColor }]}>{verdictLabels[verdict]}</Text>
						</View>
					) : location ? (
						<View style={styles.chip}>
							<AppSymbol name="file" size={iconSize.xs} color={t.textSecondary} />
							<Text numberOfLines={1} style={styles.chipText}>{location}</Text>
						</View>
					) : null}
					{!expanded ? <Text numberOfLines={1} style={styles.preview}>{commentPreview(comment)}</Text> : null}
				</View>
			</Pressable>
			{/* Laid over the header's end, so a tap on ⋯ never also folds the row. */}
			{actions.length ? (
				<View style={styles.menuSlot} pointerEvents="box-none">
					<ItemActionsMenu accessibilityLabel={`Actions for ${name}'s comment`} actions={actions} loading={sending || resolving} />
				</View>
			) : null}
			{expanded ? (
				<View style={styles.body}>
					<GitHubComment body={comment.body || "No comment text."} />
					{comment.replies.map((reply) => <ReplyView key={reply.id} reply={reply} block={block} />)}
					{onSend || onReply ? (
						<View style={styles.actions}>
							{onSend ? sent ? (
								<View accessibilityRole="text" style={styles.sentNote}>
									<AppSymbol name="checkmark" size={iconSize.sm} color={t.green} />
									<Text style={styles.sentText}>Sent to agent</Text>
								</View>
							) : <ReviewActionButton label="Send to agent" icon={symbols.send} tone="filled" busy={sending} disabled={sending} onPress={onSend} /> : null}
							{onReply ? <ReviewActionButton label="Reply" icon={REPLY} tone="outlined" onPress={onReply} /> : null}
						</View>
					) : null}
				</View>
			) : null}
			{error ? <Text accessibilityRole="alert" style={[styles.rowError]}>{error}</Text> : null}
		</View>
	);
}

function ReplyView({ reply, block }: { reply: ReviewReply; block: MobileReviewBlock }) {
	const styles = useThemedStyles(makeStyles);
	const age = commentAge(reply.createdAt);
	return (
		<View style={styles.reply}>
			<View style={styles.who}>
				<AccountAvatar account={{ name: reply.author.replace(/\[bot\]$/i, ""), email: reply.author, avatarUrl: commentAvatarUrl(reply.author, block) }} size={16} />
				<Text numberOfLines={1} style={styles.name}>{reply.author}</Text>
				{reply.isBot ? <Text style={styles.tag}>bot</Text> : null}
				<View style={styles.flex} />
				{age ? <Text style={styles.age}>{age}</Text> : null}
			</View>
			<GitHubComment body={reply.body} />
		</View>
	);
}

const REPLY = { ios: "arrowshape.turn.up.left", android: "add_comment" } as const;

const MENU_RESERVE = touchTarget;

const makeStyles = (t: Theme) => StyleSheet.create({
	flex: { flex: 1 },
	shrink: { flexShrink: 1 },
	row: { borderBottomWidth: rowDividerWidth, borderBottomColor: t.borderSubtle, backgroundColor: t.bgBase },
	rowHead: { paddingLeft: space.lg, paddingRight: space.lg + MENU_RESERVE - space.xs, paddingVertical: space.sm, gap: space.xxs, minHeight: touchTarget },
	menuSlot: { position: "absolute", right: space.xs, top: space.xxs, height: touchTarget, justifyContent: "center" },
	who: { flexDirection: "row", alignItems: "center", gap: space.xs, minWidth: 0 },
	name: { fontFamily: "DMSans_600SemiBold", fontWeight: "600", flexShrink: 1, color: t.textPrimary, fontSize: type.subheadline.fontSize, lineHeight: type.subheadline.lineHeight },
	tag: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight },
	age: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontVariant: ["tabular-nums"] },
	where: { flexDirection: "row", alignItems: "center", gap: space.xs, minWidth: 0, paddingLeft: 20 + space.xs },
	verdict: { flexDirection: "row", alignItems: "center", gap: space.xxs },
	verdictText: { fontFamily: "DMSans_500Medium", fontWeight: "500", fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	chip: { flexDirection: "row", alignItems: "center", gap: space.xxs, maxWidth: "60%", paddingHorizontal: space.xs, paddingVertical: 1, borderRadius: radius.xs, backgroundColor: t.bgSubtle },
	chipText: { flexShrink: 1, fontFamily: t.fontMono, color: t.textSecondary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight },
	preview: { flex: 1, fontFamily: "DMSans_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	body: { paddingLeft: space.lg + 20 + space.xs, paddingRight: space.lg, paddingBottom: space.md, gap: space.sm },
	reply: { gap: space.xxs, paddingLeft: space.sm, borderLeftWidth: 2, borderLeftColor: t.borderDefault },
	actions: { flexDirection: "row", flexWrap: "wrap", alignItems: "center", gap: space.sm, paddingTop: space.xxs },
	sentNote: { flexDirection: "row", alignItems: "center", gap: space.xxs, minHeight: 32 },
	sentText: { fontFamily: "DMSans_500Medium", fontWeight: "500", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	sendAll: { flexDirection: "row", paddingHorizontal: space.lg, paddingBottom: space.sm },
	loading: { paddingVertical: space.lg, alignItems: "center" },
	note: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, paddingHorizontal: space.lg, paddingVertical: space.sm },
	noteText: { fontFamily: "DMSans_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	inlineNote: { flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.lg, paddingVertical: space.xs },
	error: { color: t.red },
	rowError: { fontFamily: "DMSans_400Regular", color: t.red, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, paddingLeft: space.lg + 20 + space.xs, paddingRight: space.lg, paddingBottom: space.sm },
});
