import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { setApiBaseUrl } from "../../lib/api-client";
import { ChatImageSourceProvider } from "./chat-image-source";
import { MCPAppFrame } from "./MCPAppFrame";

const snapshot = { server: "analytics", tool: "chart", arguments: { year: 2026 }, result: { content: [] }, ready: true, live: true, policy: "sandbox allow-scripts allow-forms; default-src 'none'; script-src 'unsafe-inline'" };
let fetchMock: ReturnType<typeof vi.fn>;
const dialogMethods = new Map(["show", "showModal", "close"].map(name => [name, Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, name)]));
beforeEach(() => {
	setApiBaseUrl("http://127.0.0.1:3001");
	fetchMock = vi.fn(async (request: Request) => new Response(request.url.endsWith("/document") ? "<h1>Remote app</h1>" : JSON.stringify(snapshot), { headers: { "Content-Type": request.url.endsWith("/document") ? "text/html" : "application/json" } }));
	vi.stubGlobal("fetch", fetchMock);
	for (const name of dialogMethods.keys()) Object.defineProperty(HTMLDialogElement.prototype, name, { configurable: true, value: function(this: HTMLDialogElement) { this.open = name !== "close"; } });
});
afterEach(() => {
	vi.restoreAllMocks(); vi.unstubAllGlobals(); setApiBaseUrl(null);
	for (const [name, descriptor] of dialogMethods) { if (descriptor) Object.defineProperty(HTMLDialogElement.prototype, name, descriptor); else Reflect.deleteProperty(HTMLDialogElement.prototype, name); }
});

function post(frame: HTMLIFrameElement, id: number | undefined, method: string, params: unknown = {}, source: MessageEventSource | null = frame.contentWindow) {
	act(() => window.dispatchEvent(new MessageEvent("message", { source, origin: "null", data: { jsonrpc: "2.0", id, method, params } })));
}

it("preserves the sandboxed frame and its URL across expansion", async () => {
	render(<MCPAppFrame conversationId="c" activityId="a" completed />);
	const frame = await screen.findByTitle("analytics interactive app") as HTMLIFrameElement;
	expect(frame.getAttribute("sandbox")).toBe("allow-scripts allow-forms");
	const src = frame.src;
	fireEvent.click(screen.getByRole("button", { name: "Expand app" }));
	expect(screen.getByTitle("analytics interactive app")).toBe(frame);
	expect(frame.src).toBe(src);
	fireEvent.click(screen.getByRole("button", { name: "Collapse app" }));
	expect(screen.getByTitle("analytics interactive app")).toBe(frame);
});

it("keeps a remote proxy capability out of the app document and frame URL", async () => {
	render(<ChatImageSourceProvider sessionId="s" remoteHost assetBaseUrl="http://127.0.0.1:4000/secret-proxy-token"><MCPAppFrame conversationId="c" activityId="a" completed /></ChatImageSourceProvider>);
	const frame = await screen.findByTitle("analytics interactive app") as HTMLIFrameElement;
	expect(frame.getAttribute("src")).toBeNull();
	expect(frame.srcdoc).toContain("Content-Security-Policy");
	expect(frame.srcdoc).toContain("Remote app");
	expect(frame.outerHTML).not.toContain("secret-proxy-token");
	expect(fetchMock.mock.calls.some(([request]) => request.url.includes("secret-proxy-token"))).toBe(true);
});

it("accepts only its own frame messages and asks before forwarding an approved action", async () => {
	render(<MCPAppFrame conversationId="c" activityId="a" completed />);
	const frame = await screen.findByTitle("analytics interactive app") as HTMLIFrameElement;
	const reply = vi.spyOn(frame.contentWindow!, "postMessage");
	post(frame, 1, "ui/initialize");
	await waitFor(() => expect(reply).toHaveBeenCalledWith(expect.objectContaining({ id: 1 }), "*"));
	post(frame, undefined, "ui/notifications/initialized");
	fetchMock.mockClear();
	fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ approvalToken: "once", approvalTitle: "Update chart" }), { headers: { "Content-Type": "application/json" } }));
	fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ result: { content: [] } }), { headers: { "Content-Type": "application/json" } }));
	post(frame, 2, "tools/call", { name: "write" }, window);
	expect(fetchMock).not.toHaveBeenCalled();
	post(frame, 2, "tools/call", { name: "write" });
	await screen.findByRole("alertdialog", { name: "Update chart" });
	expect(fetchMock).toHaveBeenCalledTimes(1);
	fireEvent.click(screen.getByRole("button", { name: "Allow once" }));
	await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
	const approved = await fetchMock.mock.calls[1][0].clone().json();
	expect(approved).toEqual({ method: "tools/call", params: { name: "write", arguments: {} }, approvalToken: "once" });
});
