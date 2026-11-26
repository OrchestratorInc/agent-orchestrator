import { createContext, useCallback, useContext, useSyncExternalStore } from "react";
import type { ConversationMessage } from "./types";

/**
 * The newest text of each reply still streaming, kept apart from the
 * conversation's state.
 *
 * While an agent writes, nearly every read of the conversation differs from the
 * last only in that text. Set as the conversation, each one rendered the whole
 * screen (header, menus, composer, every row's plumbing) a few times a second,
 * for one message's words. A read like that is published here instead and only
 * the reply listening for it renders; anything else still goes through state.
 */
export type LiveReply = { text: string; revision: number };

export type LiveReplies = {
	get(messageId: string): LiveReply | undefined;
	publish(replies: ReadonlyArray<Pick<ConversationMessage, "id" | "text" | "revision">>): void;
	subscribe(messageId: string, listener: () => void): () => void;
	clear(): void;
};

export function createLiveReplies(): LiveReplies {
	const replies = new Map<string, LiveReply>();
	const listeners = new Map<string, Set<() => void>>();
	return {
		get: (messageId) => replies.get(messageId),
		publish(next) {
			const changed: string[] = [];
			for (const { id, text, revision } of next) {
				const known = replies.get(id);
				if (known && known.revision >= revision) continue;
				replies.set(id, { text, revision });
				changed.push(id);
			}
			for (const id of changed) for (const listener of listeners.get(id) ?? []) listener();
		},
		subscribe(messageId, listener) {
			const set = listeners.get(messageId) ?? new Set();
			set.add(listener);
			listeners.set(messageId, set);
			return () => {
				set.delete(listener);
				if (!set.size) listeners.delete(messageId);
			};
		},
		clear() {
			replies.clear();
		},
	};
}

export const LiveRepliesContext = createContext<LiveReplies | null>(null);

/** A reply's text: the newest of what the conversation holds and what was published for it since. */
export function useLiveReplyText(message: Pick<ConversationMessage, "id" | "text" | "revision">): string {
	const replies = useContext(LiveRepliesContext);
	const { id } = message;
	const subscribe = useCallback((listener: () => void) => replies?.subscribe(id, listener) ?? (() => {}), [id, replies]);
	const get = useCallback(() => replies?.get(id), [id, replies]);
	const live = useSyncExternalStore(subscribe, get, get);
	return live && live.revision > message.revision ? live.text : message.text;
}
