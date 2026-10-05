import type { ClientEvent, CloudClient } from "@aoagents/cloud-client";
import type { ConversationActivity, ConversationItem, ConversationMessage, ConversationTurn } from "../chat/types";

function payloadField(event: ClientEvent, field: string): unknown {
	const payload = event.payload as Record<string, unknown> | undefined;
	return payload?.[field];
}

function eventTurnId(event: ClientEvent): string | undefined {
	const value = payloadField(event, "turnId");
	return typeof value === "string" && value ? value : undefined;
}

/** Rebuild durable turn history from the same Cloud events shown by desktop. */
export function toConversationTurns(events: ClientEvent[]): ConversationTurn[] {
	const turns = new Map<string, ConversationTurn>();
	for (const event of events) {
		const id = eventTurnId(event);
		if (!id) continue;
		let turn = turns.get(id);
		if (!turn) {
			turn = { id, state: "queued", requestedAt: event.createdAt };
			turns.set(id, turn);
		}
		if (event.type === "chat.turn_started" || (event.type === "chat.assistant_delta" && turn.state === "queued")) {
			turn.state = "running";
			turn.startedAt = event.createdAt;
		} else if (event.type === "chat.turn_completed" || event.type === "chat.turn_interrupted" || event.type === "chat.turn_aborted") {
			turn.state = event.type === "chat.turn_completed" ? "completed" : event.type === "chat.turn_interrupted" ? "interrupted" : "failed";
			turn.completedAt = event.createdAt;
			const error = payloadField(event, "error");
			if (typeof error === "string") turn.errorMessage = error;
		}
	}
	const ordered = [...turns.values()];
	// Cloud may durably accept the first message before the worker claims its
	// turn. Show it as the active exchange rather than hiding it behind the
	// timeline's queued-turn filter while provisioning catches up.
	if (!ordered.some((turn) => turn.state === "running")) {
		const next = ordered.find((turn) => turn.state === "queued");
		if (next) next.state = "running";
	}
	return ordered;
}

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
	const open = new Map<string, ConversationMessage>();
	const approvals = new Map<string, ConversationActivity>();

	for (const event of events) {
		switch (event.type as string) {
			case "chat.user_message":
				// A user message can interleave before a turn-ending event ever
				// arrives; settle whatever assistant bubble was open rather than
				// dropping the only reference to it while it is still streaming.
				for (const message of open.values()) message.streaming = false;
				open.clear();
				const userText = payloadField(event, "text");
				if (typeof userText !== "string" || !userText) break;
				items.push({
					kind: "message",
					id: `${event.sessionId}:${event.sequence}`,
					sequence: event.sequence,
					revision: 0,
					turnId: eventTurnId(event),
					role: "user",
					origin: "human",
					text: userText,
					streaming: false,
					createdAt: event.createdAt,
				});
				break;
			case "chat.assistant_delta":
				const assistantText = payloadField(event, "text");
				if (typeof assistantText !== "string" || !assistantText) break;
				const turnId = eventTurnId(event);
				const key = turnId ?? "without-turn";
				const previous = open.get(key);
				if (previous) {
					previous.text += assistantText;
				} else {
					const message: ConversationMessage = {
						kind: "message",
						id: `${event.sessionId}:${event.sequence}`,
						sequence: event.sequence,
						revision: 0,
						turnId,
						role: "assistant",
						origin: "provider",
						text: assistantText,
						streaming: true,
						createdAt: event.createdAt,
					};
					open.set(key, message);
					items.push(message);
				}
				break;
			case "chat.turn_completed":
			case "chat.turn_interrupted":
			case "chat.turn_aborted":
				const ended = eventTurnId(event);
				if (ended) {
					const message = open.get(ended);
					if (message) message.streaming = false;
					open.delete(ended);
				} else {
					for (const message of open.values()) message.streaming = false;
					open.clear();
				}
				for (const activity of approvals.values()) {
					if (activity.status === "pending" && (!ended || activity.turnId === ended)) activity.status = "cancelled";
				}
				if (event.type === "chat.turn_aborted") {
					const error = payloadField(event, "error");
					if (typeof error === "string" && error) items.push({
						kind: "activity", id: `cloud-error-${event.sequence}`, turnId: ended,
						sequence: event.sequence, revision: 1, activityKind: "error", status: "failed",
						summary: error, detail: { error }, createdAt: event.createdAt,
					});
				}
				break;
			case "chat.approval_requested": {
				const requestId = payloadField(event, "requestId");
				if (typeof requestId !== "string" || !requestId) break;
				const rawDecisions = payloadField(event, "decisions");
				const decisions = Array.isArray(rawDecisions) ? rawDecisions.filter((item): item is { id: string; label: string } =>
					Boolean(item && typeof item === "object" && typeof item.id === "string" && typeof item.label === "string")) : [];
				const summary = payloadField(event, "summary");
				const activity: ConversationActivity = {
					kind: "activity", id: `cloud-approval-${requestId}`, turnId: eventTurnId(event),
					sequence: event.sequence, revision: 1, activityKind: "approval", status: "pending",
					summary: typeof summary === "string" ? summary : "Permission required",
					requestId, decisions, createdAt: event.createdAt,
				};
				approvals.set(requestId, activity);
				items.push(activity);
				break;
			}
			case "chat.approval_decided": {
				const requestId = payloadField(event, "requestId");
				if (typeof requestId === "string") {
					const activity = approvals.get(requestId);
					if (activity) { activity.status = "completed"; activity.revision += 1; }
				}
				break;
			}
			case "chat.turn_started":
			case "chat.interrupt_requested":
				// These change turn state, not the timeline's visible items.
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
