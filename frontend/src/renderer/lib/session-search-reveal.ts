import { useUiStore } from "../stores/ui-store";

export type SessionSearchReveal = {
	messageId: string;
	text: string;
	find?: boolean;
	activitySequence?: number;
};

const pending = new Map<string, SessionSearchReveal>();
const listeners = new Map<string, Set<() => void>>();

function notify(sessionId: string): void {
	for (const listener of listeners.get(sessionId) ?? []) listener();
}

export function requestSessionSearchReveal(sessionId: string, reveal: SessionSearchReveal): void {
	if (!sessionId || !reveal.messageId) return;
	pending.set(sessionId, reveal);
	notify(sessionId);
}

export function takeSessionSearchReveal(sessionId: string): SessionSearchReveal | undefined {
	const reveal = pending.get(sessionId);
	if (!reveal) return undefined;
	pending.delete(sessionId);
	return reveal;
}

export function peekSessionSearchReveal(sessionId: string): SessionSearchReveal | undefined {
	return pending.get(sessionId);
}

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

export type SessionSearchMatchTarget = {
	sessionId: string;
	messageId?: string;
	snippet: string;
	highlights?: ReadonlyArray<{ start: number; end: number }>;
	matchKind?: string;
	reviewId?: string;
	seq?: number;
};

export function sessionSearchMatchText(
	snippet: string,
	highlights?: ReadonlyArray<{ start: number; end: number }>,
): string {
	const first = highlights?.find((span) => span.end > span.start && span.end <= snippet.length);
	if (first) return snippet.slice(first.start, first.end);
	return snippet.replace(/^…|…$/g, "");
}

const ACTIVITY_MATCH_KINDS = new Set(["tool", "command", "path"]);

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
		find: target.matchKind === "user" || target.matchKind === "assistant",
		...(target.seq && ACTIVITY_MATCH_KINDS.has(target.matchKind ?? "") ? { activitySequence: target.seq } : {}),
	});
}

export function resetSessionSearchReveals(): void {
	pending.clear();
}