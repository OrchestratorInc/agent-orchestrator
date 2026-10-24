import { CloudApiError, type CloudClient } from "@aoagents/cloud-client";
import type { BrowserLiveCommand } from "../browserLive";
import type { BrowserLiveHandlers, BrowserLiveTransport, PreviewSource } from "../preview/source";
import { cloudBrowserCommand, cloudBrowserHello, decodeCloudBrowserFrame, initialCloudBrowserState, projectCloudBrowserMessage, validBrowserId } from "./browserProtocol";

type BrowserClient = Pick<CloudClient, "getSession" | "getAgentBrowserStats" | "getAgentBrowserSocketRequest">;
type NativeSocketConstructor = new (url: string, protocols?: string | string[], options?: { headers: Record<string, string> }) => WebSocket;

/** The client owns fresh bearer acquisition. Credentials never enter renderer state or URLs. */
export function createCloudPreviewSource(input: {
	client: BrowserClient;
	orgId: string;
	scopeKey: string;
	isCurrent(): boolean;
	WebSocketImpl?: typeof WebSocket;
}): PreviewSource {
	const streams = new Set<BrowserLiveTransport>();
	const retryAt = new Map<string, number>();
	const current = () => input.isCurrent();
	return {
		kind: "cloud", scopeKey: input.scopeKey, isCurrent: current,
		dispose: () => { for (const stream of streams) stream.close(); streams.clear(); },
		createLive: (sessionId, handlers) => {
			const stream = createStream(input, sessionId, handlers, current, retryAt, () => streams.delete(stream));
			streams.add(stream);
			return stream;
		},
	};
}

function createStream(input: { client: BrowserClient; orgId: string; WebSocketImpl?: typeof WebSocket }, sessionId: string, handlers: BrowserLiveHandlers, sourceCurrent: () => boolean, retryAt: Map<string, number>, onClose: () => void): BrowserLiveTransport {
	let closed = false;
	let connecting = false;
	let socket: WebSocket | null = null;
	let state = initialCloudBrowserState();
	let sequence = 0;
	const controller = new AbortController();
	const current = () => !closed && sourceCurrent() && !controller.signal.aborted;
	const close = () => {
		closed = true; controller.abort();
		const prior = socket; socket = null; prior?.close(); onClose();
	};
	const check = () => {
		if (current()) return true;
		close(); return false;
	};
	const status = (message: string) => { if (check()) handlers.onStatus("error", message); };
	const send = (value: Record<string, unknown>) => { if (check() && socket?.readyState === 1) socket.send(JSON.stringify(value)); };
	const options = { signal: controller.signal };
	async function message(data: unknown, owner: WebSocket) {
		if (!check() || socket !== owner) return;
		try {
			if (typeof data === "string") {
				const projected = projectCloudBrowserMessage(state, data);
				if (!check() || socket !== owner) return;
				state = projected.state; handlers.onState(state);
				if (projected.notice) handlers.onStatus("open", projected.notice);
				return;
			}
			const buffer = data instanceof ArrayBuffer ? data : typeof Blob !== "undefined" && data instanceof Blob ? await data.arrayBuffer() : null;
			if (!check() || socket !== owner || !buffer) return;
			const frame = decodeCloudBrowserFrame(buffer);
			handlers.onFrame(frame);
			// Ack only after a frame is accepted, and only on the same live lease.
			if (check() && socket === owner) send({ t: "ack", seq: Number(frame.sequence) });
		} catch {
			if (check() && socket === owner) {
				status("The Cloud browser sent an unsupported frame. Check again to reconnect.");
				close();
			}
		}
	}
	return {
		connect: async () => {
			if (connecting || !check()) return;
			connecting = true;
			if (!validBrowserId(input.orgId) || !validBrowserId(sessionId)) { status("This preview doesn't identify a Cloud session."); return; }
			handlers.onStatus("connecting");
			const wait = Math.ceil(((retryAt.get(sessionId) ?? 0) - Date.now()) / 1000);
			if (wait > 0) { status(`The Cloud browser is unavailable. Check again after ${wait} seconds.`); return; }
			try {
				const { session } = await input.client.getSession(input.orgId, sessionId, options);
				if (!check()) return;
				if (session.desiredState !== "running" || !session.runtimeConnected || session.isTerminated || session.runtimeState === "terminated") {
					status("The Cloud session must be running and connected. Return to the session to resume or reconnect it."); return;
				}
				const stats = await input.client.getAgentBrowserStats(input.orgId, sessionId, options);
				if (!check()) return;
				if (typeof stats !== "object" || stats === null || Array.isArray(stats)) throw new Error("Invalid Cloud browser stats");
				if (stats.running === false) { status("Ask the agent to open a browser page, then check again."); return; }
				const request = await input.client.getAgentBrowserSocketRequest(input.orgId, sessionId, options);
				if (!check()) return;
				// React Native's URL hostname/userinfo getters only understand HTTP.
				// The authenticated client supplies this URL from its configured base;
				// reject credentials, queries and a mismatched session path here too.
				const url = /^(wss?):\/\/(\[[\da-f:.]+\]|[\da-z.-]+)(?::([0-9]{1,5}))?\/[^?#\\\s]*$/i.exec(request.url);
				const tail = `/api/cloud/v1/orgs/${encodeURIComponent(input.orgId)}/sessions/${encodeURIComponent(sessionId)}/agent-browser/ws/cdp`;
				if (!url || !request.url.endsWith(tail) || (url[3] && Number(url[3]) > 65535) || (url[1] !== "wss" && !["localhost", "127.0.0.1", "[::1]", "10.0.2.2"].includes(url[2]))) throw new Error("Invalid Cloud browser transport");
				const Socket = (input.WebSocketImpl ?? WebSocket) as unknown as NativeSocketConstructor;
				const owner = new Socket(request.url, undefined, { headers: request.headers });
				if (!check()) { owner.close(); return; }
				socket = owner; owner.binaryType = "arraybuffer";
				owner.onopen = () => { if (!check() || socket !== owner) return; send(cloudBrowserHello()); handlers.onStatus("open"); };
				owner.onmessage = (event) => { void message(event.data, owner); };
				owner.onerror = () => { if (!check() || socket !== owner) return; status("Couldn't connect to the Cloud browser. Check the session's connection, then try again."); close(); };
				owner.onclose = () => { if (!check() || socket !== owner) return; status("The Cloud browser disconnected. Check the session, then try again."); close(); };
			} catch (cause) {
				if (!check()) return;
				if (cause instanceof CloudApiError) {
					if (cause.code === "AGENT_BROWSER_UNAVAILABLE" && cause.retryAfterSeconds !== undefined) retryAt.set(sessionId, Date.now() + Math.max(0, Math.min(3600, cause.retryAfterSeconds)) * 1000);
					if (cause.status === 401) status("Sign in to Orchestrator Cloud again to view this browser.");
					else if (cause.status === 403) status("Your Cloud account doesn't have access to control this browser.");
					else if (cause.status === 404) status("This Cloud deployment doesn't support this browser viewer yet.");
					else if (cause.code === "AGENT_BROWSER_UNAVAILABLE") status(`This Cloud worker's browser is unavailable. It may be disconnected or use an image without browser support.${cause.retryAfterSeconds ? ` Check again after ${cause.retryAfterSeconds} seconds.` : ""}`);
					else status("The Cloud browser is unavailable. Return to the session to check its status.");
				} else status("Couldn't connect to the Cloud browser. Check your connection and try again.");
			}
		},
		send: (command: BrowserLiveCommand) => { if (!check()) return; const mapped = cloudBrowserCommand(command, state, ++sequence); if (mapped) send(mapped); },
		close,
	};
}
