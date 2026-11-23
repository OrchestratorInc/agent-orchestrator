import { takeSseFrames } from "../chat/sse";
import type { CloudClient } from "./client";
import { CloudError } from "./errors";
import type { CloudClientEvent } from "./types";

// A cloud session's chat as its events are committed, over the session's
// event stream (GET …/sessions/{id}/events), as the desktop reads it
// (frontend/src/renderer/lib/cloud-cp/session-events-hub.ts): replies stream
// in as they are written instead of a second at a time. The chat-events poll
// stays as the safety net for a stream a carrier proxy cuts or never opens.

/** How long the stream gathers events before the chat redraws: one frame's worth. */
export const STREAM_FLUSH_MS = 32;
const RECONNECT_MIN_MS = 1_000;
const RECONNECT_MAX_MS = 15_000;
/** A stream that stayed open this long was healthy even if the chat was idle. */
const HEALTHY_OPEN_MS = 10_000;

/**
 * One frame of the stream as a client event; undefined for a keep-alive, a
 * comment, or a `session.live` frame (live viewer state, which carries no
 * sequence). The server names each client event's type in `event:`.
 */
export function parseCloudSseFrame(frame: string): CloudClientEvent | undefined {
	let name = "";
	const data: string[] = [];
	for (const raw of frame.split("\n")) {
		const line = raw.endsWith("\r") ? raw.slice(0, -1) : raw;
		if (!line || line.startsWith(":")) continue;
		const colon = line.indexOf(":");
		const field = colon < 0 ? line : line.slice(0, colon);
		let value = colon < 0 ? "" : line.slice(colon + 1);
		if (value.startsWith(" ")) value = value.slice(1);
		if (field === "event") name = value;
		else if (field === "data") data.push(value);
	}
	if (name === "session.live" || !data.length) return undefined;
	try {
		const parsed = JSON.parse(data.join("\n")) as Partial<CloudClientEvent> | null;
		if (!parsed || typeof parsed !== "object" || typeof parsed.sequence !== "number" || typeof parsed.type !== "string") return undefined;
		return parsed as CloudClientEvent;
	} catch {
		return undefined;
	}
}

/** The next wait before reconnecting: soon after a healthy stream, backing off after one refused or cut at once. */
export function reconnectDelay(previous: number, healthy: boolean): number {
	return healthy ? RECONNECT_MIN_MS : Math.min(Math.max(previous, RECONNECT_MIN_MS / 2) * 2, RECONNECT_MAX_MS);
}

/** The stream route is missing (an older control plane) or the session is gone: the poll alone carries on. */
function permanent(error: unknown): boolean {
	return error instanceof CloudError && (error.status === 404 || error.status === 410);
}

export type CloudEventStreamOptions = {
	client: Pick<CloudClient, "openEventStream">;
	orgId: string;
	sessionId: string;
	/** Where a (re)connection starts: the newest event the chat holds. */
	resumeAfter(): number;
	/** Events in stream order, a burst at a time. They may repeat ones the poll already added. */
	onEvents(events: CloudClientEvent[]): void;
	/** The stream opened (true) or dropped (false). */
	onOpenChange(open: boolean): void;
};

/**
 * Follows the session's event stream until stopped, reconnecting with backoff
 * after the newest event the chat holds. Returns the stop.
 */
export function followCloudSessionEvents(options: CloudEventStreamOptions): () => void {
	let stopped = false;
	let controller: AbortController | undefined;
	let retry: ReturnType<typeof setTimeout> | undefined;
	let delay = RECONNECT_MIN_MS;

	const run = async () => {
		if (stopped) return;
		controller = new AbortController();
		const signal = controller.signal;
		const openedAt = Date.now();
		let received = false;
		let opened = false;
		let pending: CloudClientEvent[] = [];
		let flush: ReturnType<typeof setTimeout> | undefined;
		const deliver = () => {
			flush = undefined;
			const events = pending;
			pending = [];
			if (events.length && !stopped) options.onEvents(events);
		};
		try {
			const response = await options.client.openEventStream(options.orgId, options.sessionId, { after: options.resumeAfter(), signal });
			if (!response.body) throw new Error("This stream has no body.");
			opened = true;
			options.onOpenChange(true);
			const reader = response.body.getReader();
			const decoder = new TextDecoder();
			let buffer = "";
			try {
				while (!signal.aborted) {
					const { done, value } = await reader.read();
					if (done) break;
					buffer += decoder.decode(value, { stream: true });
					const split = takeSseFrames(buffer);
					buffer = split.remainder;
					for (const frame of split.frames) {
						const event = parseCloudSseFrame(frame);
						if (!event || event.sessionId !== options.sessionId) continue;
						received = true;
						pending.push(event);
					}
					if (pending.length) flush ??= setTimeout(deliver, STREAM_FLUSH_MS);
				}
			} finally {
				try { await reader.cancel(); } catch { /* aborting the request may close it first */ }
			}
		} catch (error) {
			if (stopped || signal.aborted) return;
			if (permanent(error)) {
				if (opened) options.onOpenChange(false);
				stopped = true;
				return;
			}
		} finally {
			if (flush !== undefined) { clearTimeout(flush); deliver(); }
		}
		if (stopped) return;
		if (opened) options.onOpenChange(false);
		delay = reconnectDelay(delay, received || Date.now() - openedAt >= HEALTHY_OPEN_MS);
		retry = setTimeout(() => void run(), delay);
	};

	void run();
	return () => {
		stopped = true;
		if (retry !== undefined) clearTimeout(retry);
		controller?.abort();
	};
}
