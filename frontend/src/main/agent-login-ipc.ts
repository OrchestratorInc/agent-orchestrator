import type { IpcMain, WebContents } from "electron";

// Inline sign-in must not wait behind the renderer's HTTP/1.1 socket pool.
// Chromium allows six connections per origin, and the daemon's long-lived event
// streams plus slow readiness probes can hold all of them for many seconds.
// Node's fetch in the main process has its own pool, so these fixed
// auth-operation routes reach the local daemon immediately.

export type AgentLoginRequest =
	| { action: "start"; agentId: string; method: string; secret?: string; model?: string; device?: boolean; provider?: string }
	| { action: "get" | "cancel"; agentId: string; operationId: string }
	| { action: "code"; agentId: string; operationId: string; code: string };

export type AgentLoginResponse = { status: number; body: unknown };

export type AgentLoginIpcDeps = {
	ipcMain: Pick<IpcMain, "handle">;
	daemonPort: () => number | undefined;
	trustedSender: (sender: WebContents) => boolean;
	fetch?: typeof fetch;
};

const AGENT_ID = /^[a-z0-9][a-z0-9_-]{0,63}$/;
const OPERATION_ID = /^[A-Za-z0-9-]{1,64}$/;
const METHOD_ID = /^[a-z][A-Za-z0-9_]{0,63}$/;
const MODEL_ID = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$/;
const CATALOG_PROVIDER_ID = /^[a-z0-9][a-z0-9._-]{0,63}$/;
const MAX_SECRET_LENGTH = 16 * 1024;
const MAX_CODE_LENGTH = 4096;
const REQUEST_TIMEOUT_MS = 60_000;

function text(value: unknown, max: number): value is string {
	return typeof value === "string" && value.length <= max;
}

export function parseAgentLoginRequest(input: unknown): AgentLoginRequest {
	const value = (typeof input === "object" && input !== null ? input : {}) as Record<string, unknown>;
	if (typeof value.agentId !== "string" || !AGENT_ID.test(value.agentId)) throw new Error("Invalid agent.");
	if (value.action === "start") {
		if (typeof value.method !== "string" || !METHOD_ID.test(value.method)) throw new Error("Invalid sign-in method.");
		if (value.secret !== undefined && !text(value.secret, MAX_SECRET_LENGTH)) throw new Error("Invalid credential.");
		if (value.model !== undefined && (typeof value.model !== "string" || !MODEL_ID.test(value.model))) throw new Error("Invalid model.");
		if (value.device !== undefined && typeof value.device !== "boolean") throw new Error("Invalid device setting.");
		if (value.provider !== undefined && (typeof value.provider !== "string" || !CATALOG_PROVIDER_ID.test(value.provider))) throw new Error("Invalid provider.");
		return { action: "start", agentId: value.agentId, method: value.method, ...(value.secret ? { secret: value.secret } : {}), ...(value.model ? { model: value.model } : {}), ...(value.device ? { device: true } : {}), ...(value.provider ? { provider: value.provider } : {}) };
	}
	if (typeof value.operationId !== "string" || !OPERATION_ID.test(value.operationId)) throw new Error("Invalid sign-in operation.");
	if (value.action === "get" || value.action === "cancel") return { action: value.action, agentId: value.agentId, operationId: value.operationId };
	if (value.action === "code") {
		if (!text(value.code, MAX_CODE_LENGTH)) throw new Error("Invalid sign-in code.");
		return { action: "code", agentId: value.agentId, operationId: value.operationId, code: value.code };
	}
	throw new Error("Invalid sign-in action.");
}

export async function requestAgentLogin(input: unknown, port: number | undefined, fetchImpl: typeof fetch = fetch): Promise<AgentLoginResponse> {
	const request = parseAgentLoginRequest(input);
	if (!port) return { status: 503, body: { code: "DAEMON_UNAVAILABLE", message: "AO daemon is not ready." } };
	const operations = `http://127.0.0.1:${port}/api/v1/agents/${encodeURIComponent(request.agentId)}/auth/operations`;
	let url = operations;
	let method = "POST";
	let body: unknown;
	if (request.action === "start") {
		body = { method: request.method, ...(request.secret ? { secret: request.secret } : {}), ...(request.model ? { model: request.model } : {}), ...(request.device ? { device: true } : {}), ...(request.provider ? { provider: request.provider } : {}) };
	} else {
		url = `${operations}/${encodeURIComponent(request.operationId)}`;
		if (request.action === "get") method = "GET";
		else if (request.action === "cancel") method = "DELETE";
		else if (request.action === "code") {
			url += "/code";
			body = { code: request.code };
		}
	}
	let response: Response;
	try {
		// Electron net.fetch adds browser metadata without Origin, which the daemon rejects.
		response = await fetchImpl(url, {
			method,
			headers: body === undefined ? undefined : { "Content-Type": "application/json" },
			body: body === undefined ? undefined : JSON.stringify(body),
			signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
		});
	} catch {
		return { status: 503, body: { code: "DAEMON_UNAVAILABLE", message: "AO daemon is not reachable." } };
	}
	const raw = await response.text().catch(() => "");
	let parsed: unknown;
	try {
		parsed = raw ? JSON.parse(raw) : undefined;
	} catch {
		parsed = undefined;
	}
	return { status: response.status, body: parsed };
}

export function registerAgentLoginIpc(deps: AgentLoginIpcDeps): void {
	deps.ipcMain.handle("agentLogin:request", (event, input: unknown) => {
		if (!deps.trustedSender(event.sender)) throw new Error("Untrusted sign-in request.");
		return requestAgentLogin(input, deps.daemonPort(), deps.fetch);
	});
}
