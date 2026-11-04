// SSE subscription over the Electron-main cloud-cp proxy. EventSource/fetch
// cannot be used renderer-side (no CORS on the CP, and only main holds the
// bearer token), so the stream is opened by main and its chunks arrive over
// aoBridge.cloudCp stream events. This module reassembles those chunks with
// the shared SSE frame parser and delivers the same CloudCpClientEvent
// payloads — and the same resolve/onError contract — as the typed client's
// subscribeSessionEvents.

import type { CloudCpOpenStreamResult, CloudCpProxyRequestInit, CloudCpStreamEvent } from "../../../main/cloud-cp-proxy";
import { CLOUD_MALFORMED_RESPONSE } from "../../../shared/cloud-cp-errors";
import { aoBridge } from "../bridge";
import { noteCloudFailure } from "../cloud-error-report";
import { CloudCpError, cloudCpErrorFromFailure } from "./errors";
import { createSseFrameParser, type CloudCpSseFrame } from "./sse";
import type { CloudCpClientEvent, CloudCpNotificationEvent, CloudCpSessionLive } from "./types";

/** The slice of the preload cloudCp bridge this adapter needs. */
export interface CloudCpStreamBridge {
	openStream(init: CloudCpProxyRequestInit): Promise<CloudCpOpenStreamResult>;
	closeStream(streamId: string): void;
	onStreamEvent(streamId: string, listener: (event: CloudCpStreamEvent) => void): () => void;
}

export interface SubscribeNotificationEventsBridgedOptions {
	baseUrl: string;
	orgId: string;
	after?: number;
	onEvent: (event: CloudCpNotificationEvent) => void;
	onError?: (error: CloudCpError) => void;
	signal?: AbortSignal;
	bridge?: CloudCpStreamBridge;
}

export interface SubscribeSessionEventsBridgedOptions {
	/** Control-plane origin, e.g. "https://cloud.example.com". A trailing slash is tolerated. */
	baseUrl: string;
	orgId: string;
	sessionId: string;
	/** Resume strictly after this sequence; omit to replay from the beginning. */
	after?: number;
	/**
	 * Lists the caller as a viewer of the session while the stream is open,
	 * and adds its session.live frames, delivered to `onLive`.
	 */
	live?: boolean;
	/** Only these client event types (the control plane's types filter). */
	types?: readonly string[];
	/** Invoked once per parsed event, in stream order. */
	onEvent: (event: CloudCpClientEvent) => void;
	/** Invoked with each session.live frame of a live stream; each replaces the last. */
	onLive?: (live: CloudCpSessionLive) => void;
	/** Invoked once the stream is open (the control plane accepted it), before any frame. */
	onOpen?: () => void;
	/** Invoked on setup or mid-stream failure. Aborts via `signal` are silent. */
	onError?: (error: CloudCpError) => void;
	/** Aborting closes the stream and resolves the subscription promise. */
	signal?: AbortSignal;
	/** Bridge override for tests; defaults to the preload cloudCp bridge. */
	bridge?: CloudCpStreamBridge;
}

// Through a helper so TS does not narrow `signal.aborted` to `false` after an
// early-return check — the flag flips concurrently across awaits.
function isAborted(signal: AbortSignal | undefined): boolean {
	return signal?.aborted === true;
}

/** A rejected openStream: main refused the request itself, or the bridge is missing. */
function errorFromOpenFailure(error: unknown): CloudCpError {
	if (error instanceof CloudCpError) return error;
	const message = error instanceof Error ? error.message : String(error);
	return new CloudCpError(message, { status: 0, cause: error });
}

/**
 * Opens a stream through main. A control plane that refused it, or was not
 * reached, comes back as fields (status, code, requestId), never a message to
 * parse; the failure is noted for diagnostics before it is returned.
 */
async function openBridgedStream(bridge: CloudCpStreamBridge, init: CloudCpProxyRequestInit): Promise<{ streamId: string } | { error: CloudCpError }> {
	let result: CloudCpOpenStreamResult;
	try {
		result = await bridge.openStream(init);
	} catch (error) {
		return { error: errorFromOpenFailure(error) };
	}
	if ("error" in result) {
		const error = cloudCpErrorFromFailure(result.error);
		noteCloudFailure(error);
		return { error };
	}
	return result;
}

/** A stream main ended with an error event: the connection to the control plane dropped. */
function errorFromStreamEvent(event: Extract<CloudCpStreamEvent, { type: "error" }>): CloudCpError {
	const error = cloudCpErrorFromFailure({ status: event.status ?? 0, message: event.message, code: event.code, requestId: event.requestId, cause: event.cause });
	// A live stream dropping (sleep, a network change) is routine and reconnects;
	// it is kept for diagnostics but is not a Sentry issue.
	noteCloudFailure(error, { report: false });
	return error;
}

function malformedFrame(message: string): CloudCpError {
	const error = new CloudCpError(message, { status: 200, code: CLOUD_MALFORMED_RESPONSE });
	noteCloudFailure(error);
	return error;
}

/**
 * Subscribe to a session's live SSE stream through the main-process proxy.
 * Resolves when the stream ends (server close or abort); failures are
 * reported through `onError`, never as a rejection, so fire-and-forget
 * callers cannot leak unhandled rejections.
 */
export async function subscribeSessionEventsBridged(options: SubscribeSessionEventsBridgedOptions): Promise<void> {
	const { baseUrl, orgId, sessionId, after, live, types, onEvent, onLive, onOpen, onError, signal } = options;
	const bridge = options.bridge ?? aoBridge.cloudCp;
	if (isAborted(signal)) return;

	const fail = (error: CloudCpError): void => {
		if (isAborted(signal)) return;
		onError?.(error);
	};

	const params = [
		...(after === undefined ? [] : [`after=${encodeURIComponent(String(after))}`]),
		...(live ? ["live=1"] : []),
		...(types?.length ? [`types=${types.map(encodeURIComponent).join(",")}`] : []),
	];
	const query = params.length ? `?${params.join("&")}` : "";
	const path = `/api/cloud/v1/orgs/${encodeURIComponent(orgId)}/sessions/${encodeURIComponent(sessionId)}/events${query}`;

	const opened = await openBridgedStream(bridge, {
		baseUrl: baseUrl.replace(/\/+$/, ""),
		path,
		method: "GET",
		headers: { Accept: "text/event-stream" },
	});
	if ("error" in opened) {
		fail(opened.error);
		return;
	}
	const { streamId } = opened;

	// Aborted while the stream was being opened: main already holds it open,
	// so close it instead of leaking a slot from the per-window stream cap.
	if (isAborted(signal)) {
		bridge.closeStream(streamId);
		return;
	}

	const parser = createSseFrameParser();
	await new Promise<void>((resolve) => {
		let settled = false;
		let removeAbortListener: () => void = () => undefined;
		let unsubscribe: () => void = () => undefined;

		// Subscribing synchronously in the same microtask as the openStream
		// resolution matters: main defers the first chunk to a later macrotask,
		// so this listener is guaranteed to be attached before any event lands.
		unsubscribe = bridge.onStreamEvent(streamId, (event) => {
			if (settled) return;
			if (event.type === "chunk") {
				for (const frame of parser.push(event.data)) deliver(frame);
				return;
			}
			if (event.type === "end") {
				for (const frame of parser.flush()) deliver(frame);
				finish();
				return;
			}
			fail(errorFromStreamEvent(event));
			finish();
		});
		onOpen?.();

		function finish(): void {
			if (settled) return;
			settled = true;
			unsubscribe();
			removeAbortListener();
			resolve();
		}

		function deliver(frame: CloudCpSseFrame): void {
			if (frame.event === "session.live") {
				try {
					const state = JSON.parse(frame.data) as CloudCpSessionLive;
					if (state?.type === "session.live") onLive?.(state);
				} catch {
					fail(malformedFrame("The live stream sent a frame with malformed JSON."));
				}
				return;
			}
			let event: CloudCpClientEvent;
			try {
				event = JSON.parse(frame.data) as CloudCpClientEvent;
			} catch {
				fail(malformedFrame("The event stream sent a frame with malformed JSON."));
				return;
			}
			onEvent(event);
		}

		if (signal !== undefined) {
			const onAbort = (): void => {
				bridge.closeStream(streamId);
				finish();
			};
			signal.addEventListener("abort", onAbort, { once: true });
			removeAbortListener = () => {
				signal.removeEventListener("abort", onAbort);
			};
			// Raced with the subscription setup above.
			if (isAborted(signal)) onAbort();
		}
	});
}

/** The org-level durable notification stream, kept on the Electron proxy so
 * renderer code never sees the cloud bearer credential. */
export async function subscribeNotificationEventsBridged(options: SubscribeNotificationEventsBridgedOptions): Promise<void> {
	const { baseUrl, orgId, after, onEvent, onError, signal } = options;
	const bridge = options.bridge ?? aoBridge.cloudCp;
	if (isAborted(signal)) return;
	const fail = (error: CloudCpError): void => { if (!isAborted(signal)) onError?.(error); };
	const query = after === undefined ? "" : `?after=${encodeURIComponent(String(after))}`;
	const opened = await openBridgedStream(bridge, { baseUrl: baseUrl.replace(/\/+$/, ""), path: `/api/cloud/v1/orgs/${encodeURIComponent(orgId)}/notification-events${query}`, method: "GET", headers: { Accept: "text/event-stream" } });
	if ("error" in opened) { fail(opened.error); return; }
	const { streamId } = opened;
	if (isAborted(signal)) { bridge.closeStream(streamId); return; }
	const parser = createSseFrameParser();
	await new Promise<void>((resolve) => {
		let settled = false;
		let unsubscribe: () => void = () => undefined;
		const finish = () => { if (settled) return; settled = true; unsubscribe(); signal?.removeEventListener("abort", abort); resolve(); };
		const abort = () => { bridge.closeStream(streamId); finish(); };
		function deliver(data: string): void {
			try { onEvent(JSON.parse(data) as CloudCpNotificationEvent); } catch { fail(malformedFrame("The notification stream sent a frame with malformed JSON.")); }
		}
		unsubscribe = bridge.onStreamEvent(streamId, (event) => {
			if (settled) return;
			if (event.type === "chunk") { for (const frame of parser.push(event.data)) deliver(frame.data); return; }
			if (event.type === "end") { for (const frame of parser.flush()) deliver(frame.data); finish(); return; }
			fail(errorFromStreamEvent(event)); finish();
		});
		if (signal !== undefined) signal.addEventListener("abort", abort, { once: true });
	});
}

export interface SubscribeSessionLiveBridgedOptions {
	baseUrl: string;
	orgId: string;
	sessionId: string;
	/** Invoked with each session.live frame; each replaces the last. */
	onLive: (live: CloudCpSessionLive) => void;
	onError?: (error: CloudCpError) => void;
	signal?: AbortSignal;
	bridge?: CloudCpStreamBridge;
}

/**
 * Subscribe to a shared session's live state: who has it open and who holds
 * the composer. Opening the stream counts the caller as a viewer until it
 * closes. The types filter names no event type, so the stream carries only
 * session.live frames. Resolves when the stream ends; failures go to onError.
 */
export async function subscribeSessionLiveBridged(options: SubscribeSessionLiveBridgedOptions): Promise<void> {
	const { baseUrl, orgId, sessionId, onLive, onError, signal } = options;
	const bridge = options.bridge ?? aoBridge.cloudCp;
	if (isAborted(signal)) return;
	const fail = (error: CloudCpError): void => { if (!isAborted(signal)) onError?.(error); };
	const path = `/api/cloud/v1/orgs/${encodeURIComponent(orgId)}/sessions/${encodeURIComponent(sessionId)}/events?live=1&types=session.live`;
	const opened = await openBridgedStream(bridge, { baseUrl: baseUrl.replace(/\/+$/, ""), path, method: "GET", headers: { Accept: "text/event-stream" } });
	if ("error" in opened) { fail(opened.error); return; }
	const { streamId } = opened;
	if (isAborted(signal)) { bridge.closeStream(streamId); return; }
	const parser = createSseFrameParser();
	await new Promise<void>((resolve) => {
		let settled = false;
		let unsubscribe: () => void = () => undefined;
		const finish = () => { if (settled) return; settled = true; unsubscribe(); signal?.removeEventListener("abort", abort); resolve(); };
		const abort = () => { bridge.closeStream(streamId); finish(); };
		function deliver(frame: CloudCpSseFrame): void {
			if (frame.event !== "session.live") return;
			try {
				const live = JSON.parse(frame.data) as CloudCpSessionLive;
				if (live?.type === "session.live") onLive(live);
			} catch {
				fail(malformedFrame("The live stream sent a frame with malformed JSON."));
			}
		}
		unsubscribe = bridge.onStreamEvent(streamId, (event) => {
			if (settled) return;
			if (event.type === "chunk") { for (const frame of parser.push(event.data)) deliver(frame); return; }
			if (event.type === "end") { for (const frame of parser.flush()) deliver(frame); finish(); return; }
			fail(errorFromStreamEvent(event)); finish();
		});
		if (signal !== undefined) signal.addEventListener("abort", abort, { once: true });
	});
}
