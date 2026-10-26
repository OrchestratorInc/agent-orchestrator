import { useEffect } from "react";
import { isConfigured, machineIdentity, type ServerConfig } from "../config";
import { streamGlobalConversationEvents } from "./api";
import { HEAD_CURSOR } from "./eventCursor";
import { createConversationEventRegistry, type ConversationEvent } from "./sse";

const RECONNECT_MIN_MS = 1_000;
const RECONNECT_MAX_MS = 15_000;
const registry = createConversationEventRegistry();

export function subscribeConversationEvents(
	cfg: ServerConfig,
	sessionId: string,
	listener: (event: ConversationEvent) => void,
): () => void {
	return registry.subscribe(machineIdentity(cfg), sessionId, listener);
}

/**
 * Per machine: "open" once its stream has been accepted in the current start
 * (reconnects within the start replay their gap, so it stays open), "failed"
 * when the start's first attempt failed. Absent while no stream runs.
 */
export type ConversationStreamState = "open" | "failed";
const streamStates = new Map<string, ConversationStreamState>();
const streamWatchers = new Map<string, Set<(state: ConversationStreamState | undefined) => void>>();

function setStreamState(hostId: string, state: ConversationStreamState | undefined): void {
	if (streamStates.get(hostId) === state) return;
	if (state) streamStates.set(hostId, state);
	else streamStates.delete(hostId);
	for (const watcher of [...(streamWatchers.get(hostId) ?? [])]) watcher(state);
}

export function conversationStreamState(cfg: ServerConfig): ConversationStreamState | undefined {
	return streamStates.get(machineIdentity(cfg));
}

/** Calls `onChange` whenever this machine's stream state changes. */
export function watchConversationStream(
	cfg: ServerConfig,
	onChange: (state: ConversationStreamState | undefined) => void,
): () => void {
	const hostId = machineIdentity(cfg);
	const watchers = streamWatchers.get(hostId) ?? new Set();
	watchers.add(onChange);
	streamWatchers.set(hostId, watchers);
	return () => {
		watchers.delete(onChange);
		if (watchers.size === 0) streamWatchers.delete(hostId);
	};
}

/**
 * Own the daemon's global CDC stream for the lifetime of the configured app.
 *
 * Every start opens at head. A start follows a new config (launch, resume, a
 * different endpoint), and each of those makes the board and any open chat
 * load fresh over REST. Replaying from a stored cursor instead downloaded every
 * change on the machine since the phone left: megabytes after an hour in the
 * background, most of it for sessions nobody had open. An open chat waits for
 * this stream to open before it loads (see useConversation), so its load holds
 * everything up to the head and the stream everything after.
 * Reconnects within one start resume from the cursor, a short gap.
 */
export function useConversationEventTransport(cfg: ServerConfig | null): void {
	useEffect(() => {
		if (!cfg || !isConfigured(cfg)) return;
		let stopped = false;
		let controller: AbortController | undefined;
		const hostId = machineIdentity(cfg);
		const run = async () => {
			let cursor = HEAD_CURSOR;
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
								if (!stopped) setStreamState(hostId, "open");
							},
						},
					);
					delay = RECONNECT_MIN_MS;
				} catch {
					if (stopped || controller.signal.aborted) return;
					// A chat waiting for the stream loads without it, as it would on main.
					if (streamStates.get(hostId) !== "open") setStreamState(hostId, "failed");
				}
				await wait(delay);
				delay = Math.min(RECONNECT_MAX_MS, delay * 2);
			}
		};
		void run();
		return () => {
			stopped = true;
			setStreamState(hostId, undefined);
			controller?.abort();
		};
	}, [cfg]);
}

function wait(ms: number): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, ms));
}
