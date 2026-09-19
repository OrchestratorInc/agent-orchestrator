import type { ClientEvent, CloudClient } from "@aoagents/cloud-client";
import type { ConversationItem, ConversationMessage } from "../chat/types";

/**
 * Cloud transcript events as mobile's conversation items.
 *
 * Assistant deltas are fragments of one reply, so consecutive deltas coalesce
 * into a single message that stays `streaming` until its turn ends (whether
 * completed, interrupted, or aborted). A user message, or any of those turn
 * endings, closes whatever assistant message is open so a later delta starts
 * a fresh bubble rather than reopening a settled one.
 */
export function toConversationItems(events: ClientEvent[]): ConversationItem[] {
	const items: ConversationItem[] = [];
	let open: ConversationMessage | undefined;

	for (const event of events) {
		switch (event.type) {
			case "chat.user_message":
				// A user message can interleave before a turn-ending event ever
				// arrives; settle whatever assistant bubble was open rather than
				// dropping the only reference to it while it is still streaming.
				if (open) open.streaming = false;
				open = undefined;
				items.push({
					kind: "message",
					id: `${event.sessionId}:${event.sequence}`,
					sequence: event.sequence,
					revision: 0,
					role: "user",
					origin: "human",
					text: event.payload.text,
					streaming: false,
					createdAt: event.createdAt,
				});
				break;
			case "chat.assistant_delta":
				if (open) {
					open.text += event.payload.text;
				} else {
					open = {
						kind: "message",
						id: `${event.sessionId}:${event.sequence}`,
						sequence: event.sequence,
						revision: 0,
						role: "assistant",
						origin: "provider",
						text: event.payload.text,
						streaming: true,
						createdAt: event.createdAt,
					};
					items.push(open);
				}
				break;
			case "chat.turn_completed":
			case "chat.turn_interrupted":
			case "chat.turn_aborted":
				if (open) open.streaming = false;
				open = undefined;
				break;
			case "chat.turn_started":
			case "chat.interrupt_requested":
				// No rendering of their own; the controller/turn state they imply
				// is not tracked from a bare transcript replay.
				break;
		}
	}
	return items;
}

/**
 * Pages a cloud transcript to its end, since a single `replayEvents` call is
 * server-limited (`eventPageLimit`, currently 100) and reports `hasMore` when
 * there is more. A conversation snapshot must show the *live* tail, not
 * whatever the first page happened to contain, so this keeps requesting with
 * the returned `nextAfter` until the server says there is no more.
 *
 * Guarded against a page that claims `hasMore: true` without advancing the
 * cursor: that would otherwise re-request the same page forever, hanging the
 * app on a server bug rather than just returning what was fetched so far.
 */
export async function fetchConversationReplay(
	client: CloudClient,
	orgId: string,
	sessionId: string,
): Promise<{ events: ClientEvent[]; latestSequence: number }> {
	const events: ClientEvent[] = [];
	let cursor = 0;
	for (;;) {
		const page = await client.replayEvents(orgId, sessionId, { after: cursor });
		events.push(...page.events);
		if (!page.hasMore || page.nextAfter <= cursor) return { events, latestSequence: page.nextAfter };
		cursor = page.nextAfter;
	}
}

export type PollOptions = {
	client: CloudClient;
	orgId: string;
	sessionId: string;
	/** Cursor to resume from; only events after this sequence are requested. */
	after: number;
	onEvents(events: ClientEvent[], cursor: number): void;
	signal: AbortSignal;
	intervalMs?: number;
};

/**
 * Polls the cloud transcript from a cursor until aborted.
 *
 * Polling rather than SSE is deliberate: React Native has no EventSource and
 * expo/fetch streaming hits a JNI global-reference ceiling that aborts
 * long-lived responses. The control plane's chat-events replay is
 * cursor-based, so this loses nothing but latency, and mobile already polls
 * rather than streams on the tunnel path.
 *
 * A transient request failure must not kill the loop -- the phone's network
 * drops constantly, and the next tick simply retries from the same cursor.
 * An aborted signal stops the loop promptly and never leaves a pending timer
 * behind, so callers can `AbortController#abort()` and be done.
 */
export async function pollCloudEvents(options: PollOptions): Promise<void> {
	const interval = options.intervalMs ?? 2000;
	let cursor = options.after;
	while (!options.signal.aborted) {
		// Set only when a page reports more is waiting *and* the cursor actually
		// moved forward (a backlog page capped at the server's eventPageLimit).
		// Catching up must not idle for a full interval between pages -- at 100
		// events/page and a 2s interval, a 14k-event backlog would otherwise
		// trickle in for minutes before reaching the live tail. But hasMore
		// alone is not enough: if the server ever reports hasMore without the
		// cursor advancing, treating that as progress spins this loop against a
		// live server at round-trip rate, ignoring intervalMs entirely. A
		// non-advancing cursor is "caught up" for pacing purposes even though
		// the server claims otherwise.
		let advanced = false;
		const previousCursor = cursor;
		try {
			const page = await options.client.replayEvents(options.orgId, options.sessionId, {
				after: cursor,
				signal: options.signal,
			});
			// `nextAfter` is the highest sequence returned, or the supplied cursor
			// when the page is empty -- so this always advances monotonically and
			// never regresses on an empty page.
			cursor = page.nextAfter;
			advanced = page.hasMore && page.nextAfter > previousCursor;
			if (page.events.length > 0) {
				options.onEvents(page.events, cursor);
			}
		} catch {
			if (options.signal.aborted) return;
			// Transient failure: fall through to the wait below and retry.
		}
		if (options.signal.aborted) return;
		if (advanced) continue;
		await new Promise<void>((resolve) => {
			const timer = setTimeout(resolve, interval);
			options.signal.addEventListener(
				"abort",
				() => {
					clearTimeout(timer);
					resolve();
				},
				{ once: true },
			);
		});
	}
}
