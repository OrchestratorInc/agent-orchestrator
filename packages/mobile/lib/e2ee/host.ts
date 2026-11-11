import type { ServerConfig } from "../config";
import { normalizeServerHost } from "../endpoints";
import { decodeHostKey, E2EEError } from "./handshake";
import { secureFetch, type SecureResponse, type SecureTarget } from "./http";
import { SecureWebSocket } from "./socket";

/** What callers read from a machine's answer, encrypted or not. */
export type HostResponse = Pick<Response, "ok" | "status" | "statusText" | "headers" | "text" | "arrayBuffer"> & {
	json(): Promise<unknown>;
	body: SecureResponse["body"] | ReadableStream<Uint8Array> | null;
};

/** Where to open the encrypted channel to the machine `cfg` points at, or null when its pairing has no key. */
export function secureTarget(cfg: ServerConfig): SecureTarget | null {
	if (!cfg.hostKey) return null;
	const hostKey = decodeHostKey(cfg.hostKey);
	// Never fall back to plain HTTP for a machine whose key was pinned.
	if (!hostKey) throw new E2EEError("This machine's saved key is unreadable. Pair it again.");
	const host = normalizeServerHost(cfg.host);
	return { url: `${cfg.secure ? "wss" : "ws"}://${host}:${cfg.httpPort}/api/v1/e2ee`, hostKey, authority: `${host}:${cfg.httpPort}` };
}

/** fetch for a path on the machine, encrypted to its key when the pairing has one. */
export function hostFetch(cfg: ServerConfig, path: string, init: RequestInit = {}): Promise<HostResponse> {
	const target = secureTarget(cfg);
	if (!target) return fetch(`${cfg.secure ? "https" : "http"}://${normalizeServerHost(cfg.host)}:${cfg.httpPort}${path}`, init);
	if (init.body !== undefined && init.body !== null && typeof init.body !== "string") {
		return Promise.reject(new E2EEError("Only text request bodies are sent encrypted."));
	}
	return secureFetch(target, {
		method: init.method ?? "GET",
		path,
		headers: (init.headers ?? {}) as Record<string, string>,
		body: init.body ?? undefined,
	}, init.signal ?? undefined);
}

type NativeSocket = new (url: string, protocols?: string | string[], options?: { headers?: Record<string, string> }) => WebSocket;

/** A WebSocket to `url` on the machine, inside the encrypted channel when the pairing has a key. */
export function hostSocket(cfg: ServerConfig, url: string, headers: Record<string, string>): WebSocket {
	const target = secureTarget(cfg);
	if (!target) return new (WebSocket as unknown as NativeSocket)(url, undefined, { headers });
	const { pathname, search } = new URL(url);
	return new SecureWebSocket(target, `${pathname}${search}`, headers) as unknown as WebSocket;
}

export function hostPath(url: string): string {
	const { pathname, search } = new URL(url);
	return `${pathname}${search}`;
}

export function bytesToDataUri(bytes: Uint8Array, mimeType: string): string {
	let binary = "";
	for (let at = 0; at < bytes.length; at += 0x8000) binary += String.fromCharCode(...bytes.subarray(at, at + 0x8000));
	return `data:${mimeType.split(";")[0].trim() || "application/octet-stream"};base64,${btoa(binary)}`;
}
