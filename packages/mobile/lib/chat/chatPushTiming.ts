import { useState } from "react";
import { useAfterPush } from "../useAfterPush";
import { usePushSettled } from "../usePushSettled";
import type { ConversationSnapshot } from "./types";

/**
 * Rows (`snapshot.items`) above which a cached transcript waits for the push to
 * settle instead of mounting a frame after it starts.
 *
 * A chat's live page holds at most `CHAT_INITIAL_PAGE_SIZE` (50, in api.ts)
 * rows, and that is all a cache holds until history is paged in, 200 rows a
 * page. The `useAfterPush` timings (page committed ~40 ms after the tap,
 * transcript mounted ~80 ms) were taken on ordinary chat opens, so a cache of
 * about that size is the measured case. Twice the live page leaves room for a
 * short conversation's whole history, and any cache holding a full history
 * page (250 rows or more) waits for the slide.
 * A plain number, not imported: api.ts would pull the network client into this
 * hook.
 */
export const LARGE_CACHED_TRANSCRIPT_ROWS = 100;

type Opened = "uncached" | "small" | "large";

/**
 * When the chat page may draw its conversation, judged by what the page opened
 * with, so a snapshot arriving or growing later never moves the rule.
 *
 * - Uncached: the page shows "Loading conversation…" and a snapshot that
 *   arrives mid-slide is held (`holdArrivingSnapshot`) until the slide settles
 *   (`usePushSettled`).
 * - Cached and small: the timeline mounts one frame after the push starts
 *   (`useAfterPush`), so the slide starts at once and the rows are ready long
 *   before it ends.
 * - Cached and large (over `LARGE_CACHED_TRANSCRIPT_ROWS`): the header and
 *   composer draw at once, and the timeline waits for the slide to settle, since
 *   mounting that many rows mid-slide stalls the UI thread and the slide jumps
 *   to its end.
 */
export function useChatPushTiming(snapshot: ConversationSnapshot | undefined): {
	holdArrivingSnapshot: boolean;
	timelineMounted: boolean;
} {
	const pushSettled = usePushSettled();
	const pushed = useAfterPush();
	const [opened] = useState<Opened>(() => {
		if (!snapshot) return "uncached";
		return snapshot.items.length > LARGE_CACHED_TRANSCRIPT_ROWS ? "large" : "small";
	});
	return {
		holdArrivingSnapshot: opened === "uncached" && !pushSettled && Boolean(snapshot),
		timelineMounted: opened === "large" ? pushSettled : pushed,
	};
}
