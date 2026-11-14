import type { CloudCpProxyRequestInit, CloudCpStreamEvent } from "../../../main/cloud-cp-proxy";
import { aoBridge } from "../bridge";
import { CloudCpAuthError, CloudCpError } from "./errors";
import { createSseFrameParser, type CloudCpSseFrame } from "./sse";
import type { CloudCpClientEvent, CloudCpNotificationEvent, CloudCpSessionLive } from "./types";

export interface CloudCpStreamBridge {
	openStream(init: CloudCpProxyRequestInit): Promise<{ streamId: string }>;
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
	baseUrl: string;
	orgId: string;
	sessionId: string;
	after?: number;
	live?: boolean;
	types?: readonly string[];
	onEvent: (event: CloudCpClientEvent) => void;
	onLive?: (live: CloudCpSessionLive) => void;
	onOpen?: () => void;
	onError?: (error: CloudCpError) => void;
	signal?: AbortSignal;
	bridge?: CloudCpStreamBridge;
}

const STREAM_ERROR_MARKER = /CLOUD_CP_STREAM_ERROR (\d+) ([\s\S]*)/;

function isAborted(signal: AbortSignal | undefined): boolean {
	return signal?.aborted === true;
}

function errorFromOpenFailure(error: unknown): CloudCpError {
	if (error instanceof CloudCpError) return error;
	const message = error instanceof Error ? error.message : String(error);
	const match = STREAM_ERROR_MARKER.exec(message);
	if (match !== null) {
		const status = Number(match[1]);
		const detail = match[2].trim() || `Cloud control plane stream failed with status ${status}.`;
		return status === 401
			? new CloudCpAuthError(detail, { status, cause: error })
			: new CloudCpError(detail, { status, cause: error });
	}
	return new CloudCpError(message, { status: 0, cause: error });
}

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

	let streamId: string;
	try {
		({ streamId } = await bridge.openStream({
			baseUrl: baseUrl.replace(/\/+$/, ""),
			path,
			method: "GET",
			headers: { Accept: "text/event-stream" },
		}));
	} catch (error) {
		fail(errorFromOpenFailure(error));
		return;
	}

	if (isAborted(signal)) {
		bridge.closeStream(streamId);
		return;
	}

	const parser = createSseFrameParser();
	await new Promise<void>((resolve) => {
		let settled = false;
		let removeAbortListener: () => void = () => undefined;
		let unsubscribe: () => void = () => undefined;

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
			fail(new CloudCpError(event.message, { status: 0 }));
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
					fail(new CloudCpError("The live stream sent a frame with malformed JSON.", { status: 200 }));
				}
				return;
			}
			let event: CloudCpClientEvent;
			try {
				event = JSON.parse(frame.data) as CloudCpClientEvent;
			} catch {
				fail(new CloudCpError("The event stream sent a frame with malformed JSON.", { status: 200 }));
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
			if (isAborted(signal)) onAbort();
		}
	});
}

export async function subscribeNotificationEventsBridged(options: SubscribeNotificationEventsBridgedOptions): Promise<void> {
	const { baseUrl, orgId, after, onEvent, onError, signal } = options;
	const bridge = options.bridge ?? aoBridge.cloudCp;
	if (isAborted(signal)) return;
	const fail = (error: CloudCpError): void => { if (!isAborted(signal)) onError?.(error); };
	const query = after === undefined ? "" : `?after=${encodeURIComponent(String(after))}`;
	let streamId: string;
	try {
		({ streamId } = await bridge.openStream({ baseUrl: baseUrl.replace(/\/+$/, ""), path: `/api/cloud/v1/orgs/${encodeURIComponent(orgId)}/notification-events${query}`, method: "GET", headers: { Accept: "text/event-stream" } }));
	} catch (error) { fail(errorFromOpenFailure(error)); return; }
	if (isAborted(signal)) { bridge.closeStream(streamId); return; }
	const parser = createSseFrameParser();
	await new Promise<void>((resolve) => {
		let settled = false;
		let unsubscribe: () => void = () => undefined;
		const finish = () => { if (settled) return; settled = true; unsubscribe(); signal?.removeEventListener("abort", abort); resolve(); };
		const abort = () => { bridge.closeStream(streamId); finish(); };
		function deliver(data: string): void {
			try { onEvent(JSON.parse(data) as CloudCpNotificationEvent); } catch { fail(new CloudCpError("The notification stream sent a frame with malformed JSON.", { status: 200 })); }
		}
		unsubscribe = bridge.onStreamEvent(streamId, (event) => {
			if (settled) return;
			if (event.type === "chunk") { for (const frame of parser.push(event.data)) deliver(frame.data); return; }
			if (event.type === "end") { for (const frame of parser.flush()) deliver(frame.data); finish(); return; }
			fail(new CloudCpError(event.message, { status: 0 })); finish();
		});
		if (signal !== undefined) signal.addEventListener("abort", abort, { once: true });
	});
}

export interface SubscribeSessionLiveBridgedOptions {
	baseUrl: string;
	orgId: string;
	sessionId: string;
	onLive: (live: CloudCpSessionLive) => void;
	onError?: (error: CloudCpError) => void;
	signal?: AbortSignal;
	bridge?: CloudCpStreamBridge;
}

export async function subscribeSessionLiveBridged(options: SubscribeSessionLiveBridgedOptions): Promise<void> {
	const { baseUrl, orgId, sessionId, onLive, onError, signal } = options;
	const bridge = options.bridge ?? aoBridge.cloudCp;
	if (isAborted(signal)) return;
	const fail = (error: CloudCpError): void => { if (!isAborted(signal)) onError?.(error); };
	const path = `/api/cloud/v1/orgs/${encodeURIComponent(orgId)}/sessions/${encodeURIComponent(sessionId)}/events?live=1&types=session.live`;
	let streamId: string;
	try {
		({ streamId } = await bridge.openStream({ baseUrl: baseUrl.replace(/\/+$/, ""), path, method: "GET", headers: { Accept: "text/event-stream" } }));
	} catch (error) { fail(errorFromOpenFailure(error)); return; }
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
				fail(new CloudCpError("The live stream sent a frame with malformed JSON.", { status: 200 }));
			}
		}
		unsubscribe = bridge.onStreamEvent(streamId, (event) => {
			if (settled) return;
			if (event.type === "chunk") { for (const frame of parser.push(event.data)) deliver(frame); return; }
			if (event.type === "end") { for (const frame of parser.flush()) deliver(frame); finish(); return; }
			fail(new CloudCpError(event.message, { status: 0 })); finish();
		});
		if (signal !== undefined) signal.addEventListener("abort", abort, { once: true });
	});
}
