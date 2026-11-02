import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { useIndependentSideChats } from "./IndependentSideChats";
import type { PendingSideClose } from "../../../shared/side-close";

const { get, post, remove, capture, confirmClose, pending } = vi.hoisted(() => ({
	get: vi.fn(), post: vi.fn(), remove: vi.fn(), capture: vi.fn(), confirmClose: vi.fn(),
	pending: [] as PendingSideClose[],
}));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, POST: post, DELETE: remove, PUT: vi.fn(), PATCH: vi.fn() },
	apiErrorMessage: () => "Close failed", getApiBaseUrl: () => "", subscribeApiBaseUrl: () => () => {},
}));
vi.mock("../../lib/bridge", () => ({ aoBridge: { sideChats: {
	capture, confirmClose, getPendingCloses: async () => [...pending], onPendingClosesChanged: () => () => {},
} } }));

beforeEach(() => {
	vi.clearAllMocks(); pending.length = 0;
	HTMLElement.prototype.scrollTo = vi.fn();
	get.mockImplementation(async (path: string) => path.endsWith("/side-chats")
		? { data: { sides: [{ id: "side", sessionId: "session", state: "ready", label: "Side" }] } }
		: path.endsWith("/draft") ? { data: { contentJson: "" } }
		: { data: { snapshot: { side: { id: "side", state: "ready" }, messages: [], turns: [], activities: [] } } });
	capture.mockImplementation(async (id?: string, cancel?: boolean) => {
		if (!id) return;
		pending.length = 0;
		if (!cancel) pending.push({ id, sessionId: "session", label: "Side" });
	});
	confirmClose.mockImplementation(async () => { pending.length = 0; });
});

async function setup() {
	const hook = renderHook(() => useIndependentSideChats("session", [], [], async () => [], false));
	await waitFor(() => expect(hook.result.current.activeId).toBe("side"));
	return hook;
}

it.each(["transport", "server", "timeout"])("retains an uncertain %s close through remount and blocks submission", async (mode) => {
	const hook = await setup();
	if (mode === "transport") remove.mockRejectedValueOnce(new Error("Lost reply"));
	else remove.mockResolvedValueOnce({ error: {}, response: { status: mode === "timeout" ? 408 : 500 } });
	await act(async () => { await expect(hook.result.current.close("side")).rejects.toThrow(); });
	expect(capture).not.toHaveBeenCalledWith("side", true);
	hook.unmount();
	const restored = await setup();
	await act(async () => { await restored.result.current.send("side", "must not send"); });
	expect(post).not.toHaveBeenCalled();
	remove.mockResolvedValueOnce({});
	await act(async () => { await restored.result.current.close("side"); });
	expect(confirmClose).toHaveBeenCalledWith("side");
});

it("reconciles an already-closed response without cancelling intent", async () => {
	const hook = await setup();
	remove.mockResolvedValueOnce({ error: {}, response: { status: 404 } });
	await act(async () => { await hook.result.current.close("side"); });
	expect(confirmClose).toHaveBeenCalledWith("side", true);
	expect(capture).not.toHaveBeenCalledWith("side", true);
});

it("restores the open record after a definitive rejection", async () => {
	const hook = await setup();
	remove.mockResolvedValueOnce({ error: {}, response: { status: 409 } });
	await act(async () => { await expect(hook.result.current.close("side")).rejects.toThrow(); });
	expect(capture).toHaveBeenCalledWith("side", true);
	expect(pending).toEqual([]);
});
