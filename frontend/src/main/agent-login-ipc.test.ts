import { describe, expect, it, vi } from "vitest";
import { parseAgentLoginRequest, registerAgentLoginIpc, requestAgentLogin } from "./agent-login-ipc";

function daemon(response: Response) {
	return vi.fn(async (_url: string | URL | Request, _init?: RequestInit) => response.clone());
}

describe("requestAgentLogin", () => {
	it("starts a login on the local daemon with only the fixed operations route", async () => {
		const fetchImpl = daemon(new Response(JSON.stringify({ id: "login-1", status: "running" }), { status: 202 }));
		await expect(requestAgentLogin({ action: "start", agentId: "claude-code", method: "browser" }, 3002, fetchImpl))
			.resolves.toEqual({ status: 202, body: { id: "login-1", status: "running" } });
		const [url, init] = fetchImpl.mock.calls[0];
		expect(url).toBe("http://127.0.0.1:3002/api/v1/agents/claude-code/auth/operations");
		expect(init).toMatchObject({ method: "POST", body: JSON.stringify({ method: "browser" }) });
	});

	it("maps poll, cancel, and code actions to their operation routes", async () => {
		const fetchImpl = daemon(new Response("{}"));
		await requestAgentLogin({ action: "get", agentId: "codex", operationId: "op-1" }, 3002, fetchImpl);
		await requestAgentLogin({ action: "cancel", agentId: "codex", operationId: "op-1" }, 3002, fetchImpl);
		await requestAgentLogin({ action: "code", agentId: "claude-code", operationId: "op-1", code: "abc#state" }, 3002, fetchImpl);
		await requestAgentLogin({ action: "start", agentId: "codex", method: "api_key", secret: "test-secret" }, 3002, fetchImpl);
		await requestAgentLogin({ action: "start", agentId: "openhands", method: "env_LLM_API_KEY", secret: "test-key", model: "anthropic/claude-sonnet-4-5" }, 3002, fetchImpl);
		expect(fetchImpl.mock.calls.map(([url, init]) => [init?.method, url, init?.body])).toEqual([
			["GET", "http://127.0.0.1:3002/api/v1/agents/codex/auth/operations/op-1", undefined],
			["DELETE", "http://127.0.0.1:3002/api/v1/agents/codex/auth/operations/op-1", undefined],
			["POST", "http://127.0.0.1:3002/api/v1/agents/claude-code/auth/operations/op-1/code", JSON.stringify({ code: "abc#state" })],
			["POST", "http://127.0.0.1:3002/api/v1/agents/codex/auth/operations", JSON.stringify({ method: "api_key", secret: "test-secret" })],
			["POST", "http://127.0.0.1:3002/api/v1/agents/openhands/auth/operations", JSON.stringify({ method: "env_LLM_API_KEY", secret: "test-key", model: "anthropic/claude-sonnet-4-5" })],
		]);
	});

	it("passes daemon errors through and reports an unavailable daemon", async () => {
		const conflict = daemon(new Response(JSON.stringify({ code: "AGENT_AUTH_IN_PROGRESS" }), { status: 409 }));
		await expect(requestAgentLogin({ action: "start", agentId: "codex", method: "browser" }, 3002, conflict))
			.resolves.toEqual({ status: 409, body: { code: "AGENT_AUTH_IN_PROGRESS" } });
		const unused = daemon(new Response("{}"));
		await expect(requestAgentLogin({ action: "start", agentId: "codex", method: "browser" }, undefined, unused))
			.resolves.toMatchObject({ status: 503, body: { code: "DAEMON_UNAVAILABLE" } });
		expect(unused).not.toHaveBeenCalled();
		const offline = vi.fn(async () => { throw new TypeError("fetch failed"); });
		await expect(requestAgentLogin({ action: "get", agentId: "codex", operationId: "op-1" }, 3002, offline))
			.resolves.toMatchObject({ status: 503, body: { message: "AO daemon is not reachable." } });
	});
});

describe("parseAgentLoginRequest", () => {
	it("rejects anything outside the fixed sign-in actions", () => {
		expect(() => parseAgentLoginRequest(null)).toThrow("Invalid agent.");
		expect(() => parseAgentLoginRequest({ action: "start", agentId: "../projects", method: "browser" })).toThrow("Invalid agent.");
		expect(() => parseAgentLoginRequest({ action: "start", agentId: "codex", method: "browser/../x" })).toThrow("Invalid sign-in method.");
		expect(() => parseAgentLoginRequest({ action: "start", agentId: "openhands", method: "env_LLM_API_KEY", model: "bad\nmodel" })).toThrow("Invalid model.");
		expect(() => parseAgentLoginRequest({ action: "start", agentId: "openhands", method: "env_LLM_API_KEY", model: "x".repeat(257) })).toThrow("Invalid model.");
		expect(() => parseAgentLoginRequest({ action: "get", agentId: "codex", operationId: "a/b" })).toThrow("Invalid sign-in operation.");
		expect(() => parseAgentLoginRequest({ action: "code", agentId: "codex", operationId: "op-1", code: "x".repeat(4097) })).toThrow("Invalid sign-in code.");
		expect(() => parseAgentLoginRequest({ action: "delete-all", agentId: "codex", operationId: "op-1" })).toThrow("Invalid sign-in action.");
	});
});

describe("registerAgentLoginIpc", () => {
	it("serves only the trusted shell renderer", async () => {
		const registered = new Map<string, (event: unknown, ...args: unknown[]) => unknown>();
		const shell = {};
		const fetchImpl = daemon(new Response("{}"));
		registerAgentLoginIpc({
			ipcMain: { handle: vi.fn((channel: string, handler: (event: unknown, ...args: unknown[]) => unknown) => registered.set(channel, handler)) },
			daemonPort: () => 3002,
			trustedSender: (sender) => sender === shell,
			fetch: fetchImpl,
		});
		const handler = registered.get("agentLogin:request")!;
		expect(() => handler({ sender: {} }, { action: "get", agentId: "codex", operationId: "op-1" })).toThrow("Untrusted sign-in request.");
		await expect(handler({ sender: shell }, { action: "get", agentId: "codex", operationId: "op-1" })).resolves.toEqual({ status: 200, body: {} });
		expect(fetchImpl).toHaveBeenCalledTimes(1);
	});
});
