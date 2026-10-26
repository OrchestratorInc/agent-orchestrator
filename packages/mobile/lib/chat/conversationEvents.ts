import { useEffect } from "react";
import { forgetAcceptedCredential } from "../acceptedCredentials";
import { isConfigured, machineIdentity, type ServerConfig } from "../config";
import { streamGlobalConversationEvents } from "./api";
import { HEAD_CURSOR } from "./eventCursor";
import { createConversationEventRegistry, type ConversationEvent } from "./sse";

const RECONNECT_MIN_MS = 1_000;
const RECONNECT_MAX_MS = 15_000;
const registry = createConversationEventRegistry();

/**
 * How recently a heartbeat must have arrived for the stream to count as
 * delivering. The daemon sends one every 10 s; an intermediary that buffers
 * small writes (a Cloudflare quick tunnel) holds them back, so they stop
 * arriving on time exactly when events would too.
 */
export const STREAM_HEALTHY_WITHIN_MS = 15_000;
const lastHeartbeatAt = new Map<string, number>();

export function subscribeConversationEvents(
	cfg: ServerConfig,
	sessionId: string,
	listener: (event: ConversationEvent) => void,
): () => void {
	return registry.subscribe(machineIdentity(cfg), sessionId, listener);
}

/** Whether this machine's event stream is delivering frames promptly right now. */
export function conversationStreamHealthy(cfg: ServerConfig, now = Date.now()): boolean {
	const at = lastHeartbeatAt.get(machineIdentity(cfg));
	return at !== undefined && now - at < STREAM_HEALTHY_WITHIN_MS;
}

/**
 * Own the daemon's global CDC stream for the lifetime of the configured app.
 *
 * Every start opens at head. A start follows a new config — launch, resume,
 * a different endpoint — and each of those makes the board and any open chat
 * load fresh over REST, which is all the replayed events were ever used for.
 * Replaying from a stored cursor instead downloaded every change on the
 * machine since the phone left: megabytes after an hour of background, most
 * of it for sessions nobody had open. Listeners are told to refresh once the
 * stream is open, which covers changes between their load and the head.
 * Reconnects within one start still resume from the cursor, a short gap.
 */
export function useConversationEventTransport(cfg: ServerConfig | null): void {
	useEffect(() => {
		if (!cfg || !isConfigured(cfg)) return;
		let stopped = false;
		let controller: AbortController | undefined;
		const hostId = machineIdentity(cfg);
		const run = async () => {
			let cursor = HEAD_CURSOR;
			let opened = false;
			let delay = RECONNECT_MIN_MS;
			while (!stopped) {
				controller = new AbortController();
				try {
					cursor = await streamGlobalConversationEvents(
						cfg,
						cursor,
						controller.signal,
						(event) => {
							cursor = Math.max(cursor, event.seq);
							registry.publish(hostId, event);
						},
						(resetCursor) => {
							cursor = resetCursor;
						},
						{
							// Only the chat screen currently on top subscribes, so most frames
							// have no reader. Those only need to move the cursor.
							wantsPayload: () => registry.hasListeners(hostId),
							onCursorAdvance: (seq) => {
								cursor = Math.max(cursor, seq);
							},
							onOpen: () => {
								if (opened) return;
								opened = true;
								registry.resync(hostId);
							},
							onHeartbeat: () => {
								if (!stopped) lastHeartbeatAt.set(hostId, Date.now());
							},
						},
					);
					delay = RECONNECT_MIN_MS;
				} catch {
					if (stopped || controller.signal.aborted) return;
				}
				// The stream ended or failed, so the daemon may have restarted.
				lastHeartbeatAt.delete(hostId);
				forgetAcceptedCredential(cfg);
				await wait(delay);
				delay = Math.min(RECONNECT_MAX_MS, delay * 2);
			}
		};
		void run();
		return () => {
			stopped = true;
			lastHeartbeatAt.delete(hostId);
			forgetAcceptedCredential(cfg);
			controller?.abort();
		};
	}, [cfg]);
}

function wait(ms: number): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, ms));
}
