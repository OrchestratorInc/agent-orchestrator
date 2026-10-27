import { useUiStore } from "../stores/ui-store";

/**
 * Carries a search result's match into the session that result opens.
 *
 * The search view navigates with `?messageId=…&highlight=…`, but the Chat
 * timeline is mounted by the session shell rather than the route, so the target
 * is handed over through this small store instead of route props. It is keyed by
 * session so a stale target can never reveal a message in another session.
 *
 * A target is consumed exactly once: arriving at a session should scroll to the
 * match, but returning to that session later must not jump again.
 */

export type SessionSearchReveal = {
	/** Durable conversation message id the Chat timeline scrolls to. */
	messageId: string;
	/** The matched text inside that message, highlighted in place. */
	text: string;
	/**
	 * The search that produced the match. The opened session's find bar starts
	 * with it, so Enter/Shift+Enter step through the session's other matches.
	 */
	query?: string;
};

const pending = new Map<string, SessionSearchReveal>();
const listeners = new Map<string, Set<() => void>>();

function notify(sessionId: string): void {
	for (const listener of listeners.get(sessionId) ?? []) listener();
}

/** Records the match a freshly opened session should scroll to. */
export function requestSessionSearchReveal(sessionId: string, reveal: SessionSearchReveal): void {
	if (!sessionId || !reveal.messageId) return;
	pending.set(sessionId, reveal);
	notify(sessionId);
}

/**
 * Takes the pending match for a session, clearing it so a later mount does not
 * replay the jump. Returns undefined when there is nothing to reveal.
 */
export function takeSessionSearchReveal(sessionId: string): SessionSearchReveal | undefined {
	const reveal = pending.get(sessionId);
	if (!reveal) return undefined;
	pending.delete(sessionId);
	return reveal;
}

/** Peeks without consuming; used by a surface that mounts before it can scroll. */
export function peekSessionSearchReveal(sessionId: string): SessionSearchReveal | undefined {
	return pending.get(sessionId);
}

/**
 * Registers a listener for new targets on one session. The callback fires when
 * a target arrives after the surface mounted (the common case: the session is
 * already open and the user searches from the sidebar).
 */
export function subscribeSessionSearchReveal(sessionId: string, listener: () => void): () => void {
	let set = listeners.get(sessionId);
	if (!set) {
		set = new Set();
		listeners.set(sessionId, set);
	}
	set.add(listener);
	return () => {
		const current = listeners.get(sessionId);
		if (!current) return;
		current.delete(listener);
		if (current.size === 0) listeners.delete(sessionId);
	};
}

/** One search result's target: the session, and the match inside it. */
export type SessionSearchMatchTarget = {
	sessionId: string;
	messageId?: string;
	snippet: string;
	/** Matched spans in the snippet (UTF-16 offsets, as the daemon returns them). */
	highlights?: ReadonlyArray<{ start: number; end: number }>;
	query: string;
	/** Set when the match is in one of the session's reviewer chats. */
	reviewId?: string;
};

/**
 * The text to highlight in the opened message: the first matched word, as it
 * appears in the message. The whole snippet rarely matches the rendered message
 * exactly (it is cut with ellipses and has its whitespace collapsed), and the
 * timeline highlights an exact range, so a snippet only scrolls; the matched
 * word scrolls and marks the match.
 */
export function sessionSearchMatchText(
	snippet: string,
	highlights?: ReadonlyArray<{ start: number; end: number }>,
): string {
	const first = highlights?.find((span) => span.end > span.start && span.end <= snippet.length);
	if (first) return snippet.slice(first.start, first.end);
	return snippet.replace(/^…|…$/g, "");
}

/**
 * Hands a search result's match to the session it opens, before navigating.
 *
 * A match in the session's own transcript goes through this module, which also
 * carries the query so Enter/Shift+Enter can step through the session's other
 * matches. A match in a reviewer chat goes through the same focus request the
 * palette's chat search uses (useChatMessageFocus): it is the one that brings
 * the reviewer chat forward, and two channels for one job would drift.
 */
export function revealSessionSearchMatch(target: SessionSearchMatchTarget): void {
	if (!target.messageId) return;
	const text = sessionSearchMatchText(target.snippet, target.highlights);
	if (target.reviewId) {
		useUiStore.getState().requestChatMessageFocus(target.sessionId, target.messageId, text, target.reviewId);
		return;
	}
	requestSessionSearchReveal(target.sessionId, {
		messageId: target.messageId,
		text,
		query: target.query,
	});
}

/** Clears every pending target. Tests use it to isolate cases. */
export function resetSessionSearchReveals(): void {
	pending.clear();
}