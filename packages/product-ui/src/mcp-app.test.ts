import { describe, expect, it, vi } from "vitest";
import { createMCPAppHost, mcpAppAllowAttribute, mcpAppDownloads, mcpAppSandboxDocument, mcpAppToolResult, mcpAppWebURL, type MCPAppHostContext } from "./mcp-app";

function fixture() {
	const post = vi.fn();
	const action = vi.fn().mockResolvedValue({ result: {} });
	const confirm = vi.fn().mockResolvedValue(true);
	const context: MCPAppHostContext = { theme: "dark", styles: { variables: {} }, displayMode: "inline", availableDisplayModes: ["inline", "fullscreen"], containerDimensions: { width: 600, maxHeight: 2000 }, platform: "web" };
	const options = { post, action, confirm, context: () => context, openLink: vi.fn().mockResolvedValue(undefined), download: vi.fn().mockResolvedValue(undefined), displayMode: vi.fn().mockResolvedValue("fullscreen" as const), height: vi.fn(), ready: vi.fn(), close: vi.fn() };
	const host = createMCPAppHost(options);
	const receive = (id: number | undefined, method: string, params: unknown = {}) => host.receive({ jsonrpc: "2.0", id, method, params });
	const initialize = async () => {
		receive(1, "ui/initialize", { protocolVersion: "2026-01-26" });
		await vi.waitFor(() => expect(post).toHaveBeenCalledWith(expect.objectContaining({ id: 1, result: expect.objectContaining({ protocolVersion: "2026-01-26" }) })));
		receive(undefined, "ui/notifications/initialized");
	};
	return { host, receive, initialize, ...options };
}

describe("MCP app host", () => {
	it("waits for handshake, delivers input once and accepts a late tool result", async () => {
		const f = fixture();
		const call = { server: "s", tool: "t", arguments: { a: 1 }, live: true, ready: true };
		f.host.setCall(call);
		expect(f.post).not.toHaveBeenCalled();
		await f.initialize();
		f.host.setCall({ ...call, result: { content: [], structuredContent: null, _meta: null } });
		f.host.setCall({ ...call, result: { content: [] } });
		expect(f.post.mock.calls.filter(([x]) => x.method === "ui/notifications/tool-input")).toHaveLength(1);
		expect(f.post.mock.calls.filter(([x]) => x.method === "ui/notifications/tool-result")).toEqual([[{ jsonrpc: "2.0", method: "ui/notifications/tool-result", params: { content: [] } }]]);
	});
	it("requires host consent before sending a single-use approval token", async () => {
		const f = fixture(); await f.initialize();
		f.action.mockResolvedValueOnce({ approvalToken: "token", approvalTitle: "Write record" }).mockResolvedValueOnce({ result: { content: [] } });
		f.receive(2, "tools/call", { name: "write", arguments: { value: 3 } });
		await vi.waitFor(() => expect(f.action).toHaveBeenCalledTimes(2));
		expect(f.confirm).toHaveBeenCalledWith("Write record", expect.stringContaining('"value": 3'));
		expect(f.action.mock.calls[1][0]).toEqual({ method: "tools/call", params: { name: "write", arguments: { value: 3 } }, approvalToken: "token" });
	});
	it("declines writes without forwarding the approval and deduplicates pending ids", async () => {
		const f = fixture(); await f.initialize();
		f.action.mockResolvedValue({ approvalToken: "token" }); f.confirm.mockResolvedValue(false);
		f.receive(2, "tools/call", { name: "write" }); f.receive(2, "tools/call", { name: "write" });
		await vi.waitFor(() => expect(f.post).toHaveBeenCalledWith(expect.objectContaining({ id: 2, error: expect.objectContaining({ message: "Action declined." }) })));
		expect(f.action).toHaveBeenCalledTimes(1);
	});
	it("rejects pre-handshake actions and unsafe links", async () => {
		const f = fixture(); f.receive(2, "tools/call", { name: "write" });
		await vi.waitFor(() => expect(f.post).toHaveBeenCalledWith(expect.objectContaining({ id: 2, error: expect.anything() })));
		expect(f.action).not.toHaveBeenCalled(); await f.initialize();
		f.receive(3, "ui/open-link", { url: "javascript:alert(1)" });
		await vi.waitFor(() => expect(f.post).toHaveBeenCalledWith(expect.objectContaining({ id: 3, error: expect.anything() })));
		expect(f.openLink).not.toHaveBeenCalled(); expect(f.confirm).not.toHaveBeenCalled();
	});
	it("resolves linked downloads through the server only after consent", async () => {
		const f = fixture(); await f.initialize();
		f.action.mockResolvedValue({ result: { contents: [{ uri: "file:///report.csv", text: "a,b", mimeType: "text/csv" }] } });
		f.receive(2, "ui/download-file", { contents: [{ type: "resource_link", uri: "file:///report.csv", name: "report.csv" }] });
		await vi.waitFor(() => expect(f.download).toHaveBeenCalledWith([{ name: "report.csv", text: "a,b", mimeType: "text/csv" }]));
		expect(f.action).toHaveBeenCalledWith({ method: "resources/read", params: { uri: "file:///report.csv" } });
		expect(f.openLink).not.toHaveBeenCalled();
		f.confirm.mockResolvedValue(false); f.action.mockClear();
		f.receive(3, "ui/download-file", { contents: [{ type: "resource_link", uri: "file:///private.csv" }] });
		await vi.waitFor(() => expect(f.post).toHaveBeenCalledWith({ jsonrpc: "2.0", id: 3, result: { isError: true } }));
		expect(f.action).not.toHaveBeenCalled();
	});
	it("clamps size, updates theme and tears down without accepting late actions", async () => {
		const f = fixture(); await f.initialize();
		f.receive(undefined, "ui/notifications/size-changed", { height: 1e9 }); expect(f.height).toHaveBeenCalledWith(2000);
		f.host.updateContext(); expect(f.post).toHaveBeenCalledWith(expect.objectContaining({ method: "ui/notifications/host-context-changed" }));
		const done = f.host.teardown();
		f.host.receive({ jsonrpc: "2.0", id: "ao-teardown", result: {} }); await done;
		f.receive(3, "tools/call", { name: "write" }); expect(f.action).not.toHaveBeenCalled();
	});
});

it("preserves standards mode and delegates only declared known device permissions", () => {
	expect(mcpAppSandboxDocument('<!DOCTYPE html><html>app</html>', "sandbox allow-scripts; default-src 'none'")).toBe('<!DOCTYPE html><meta http-equiv="Content-Security-Policy" content=" default-src \'none\'"><html>app</html>');
	expect(mcpAppAllowAttribute({ camera: {}, microphone: null, clipboardWrite: {}, geolocation: [], unknown: {} })).toBe("camera; clipboard-write");
	expect(mcpAppAllowAttribute(undefined)).toBe("");
});

it("validates file downloads and normalizes Codex optional result fields", () => {
	expect(mcpAppDownloads([{ type: "resource", resource: { uri: "file:///report.txt", name: "../report.txt", text: "hello" } }])[0]).toEqual({ name: "__report.txt", mimeType: "application/octet-stream", text: "hello" });
	expect(() => mcpAppDownloads([{ type: "resource_link", uri: "" }])).toThrow();
	expect(() => mcpAppDownloads([{ type: "resource", resource: { blob: "bad!" } }])).toThrow();
	expect(mcpAppToolResult({ content: [], _meta: null, structuredContent: { a: 1 } })).toEqual({ content: [], structuredContent: { a: 1 } });
	expect(mcpAppWebURL("https://user:password@example.com")).toBe(false);
});
