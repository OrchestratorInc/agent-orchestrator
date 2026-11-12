import { act, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "../ui/tooltip";
import { useIndependentSideChats } from "./IndependentSideChats";
import type { PendingSideClose } from "../../../shared/side-close";

const { get, post, put, remove, capture, confirmClose, pending, listeners, daemonListeners } = vi.hoisted(() => ({
	get: vi.fn(), post: vi.fn(), remove: vi.fn(), capture: vi.fn(), confirmClose: vi.fn(),
	put: vi.fn(), listeners: new Set<() => void>(), daemonListeners: new Set<(status: { state: string; pid: number; port: number }) => void>(),
	pending: [] as PendingSideClose[],
}));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, POST: post, DELETE: remove, PUT: put, PATCH: vi.fn() },
	apiErrorMessage: () => "Close failed", getApiBaseUrl: () => "", subscribeApiBaseUrl: () => () => {},
}));
vi.mock("../../lib/bridge", () => ({ aoBridge: { daemon: { getStatus: async () => ({ state: "ready", pid: 1, port: 3002 }), onStatus: (listener: (status: { state: string; pid: number; port: number }) => void) => { daemonListeners.add(listener); return () => daemonListeners.delete(listener); } }, sideChats: {
	capture, confirmClose, getPendingCloses: async (sessionId: string) => pending.filter(side => side.sessionId === sessionId), onPendingClosesChanged: (listener: () => void) => { listeners.add(listener); return () => listeners.delete(listener); },
} } }));

beforeEach(() => {
	vi.resetAllMocks(); pending.length = 0; listeners.clear(); daemonListeners.clear();
	put.mockResolvedValue({});
	HTMLElement.prototype.scrollTo = vi.fn();
	get.mockImplementation(async (path: string) => path.endsWith("/side-chats")
		? { data: { sides: [{ id: "side", sessionId: "session", state: "ready", label: "Side" }] } }
		: path.endsWith("/draft") ? { data: { contentJson: "" } }
		: { data: { snapshot: { side: { id: "side", state: "ready" }, messages: [], turns: [], activities: [] } } });
	capture.mockImplementation(async (id?: string, cancel?: boolean) => {
		if (!id) return;
		pending.length = 0;
		if (!cancel) pending.push({ id, sessionId: "session", label: "Side", phase: "unconfirmed" });
	});
	confirmClose.mockImplementation(async () => { pending.length = 0; return "closed"; });
});

afterEach(() => vi.useRealTimers());

function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (cause: Error) => void;
	const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
	return { promise, resolve, reject };
}

async function changed() {
	await act(async () => { for (const listener of listeners) listener(); });
}

function Panel() {
	const chat = useIndependentSideChats("session", [], [], async () => [], false);
	useEffect(() => chat.show("side"), []);
	return <TooltipProvider>{chat.panel}</TooltipProvider>;
}

async function deleteFromPanel() {
	await screen.findByRole("tab", { name: "Side" });
	await userEvent.click(screen.getByRole("button", { name: "Close Side" }));
	await userEvent.click(screen.getByRole("button", { name: "Delete" }));
}

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

it("keeps the dialog busy and the transcript present until shutdown succeeds", async () => {
	const deletion = deferred<object>(); remove.mockReturnValueOnce(deletion.promise);
	render(<Panel />); await screen.findByLabelText("Side chat question");
	await deleteFromPanel();
	expect(screen.getByRole("dialog")).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
	expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
	expect(screen.getByTitle("Side")).toHaveAttribute("aria-busy", "true");
	expect(screen.getByTitle("Side").querySelector(".animate-spin")).toBeInTheDocument();
	expect(screen.queryByText(/Closing side chat/)).not.toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Retry close" })).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Delete" }));
	expect(remove).toHaveBeenCalledOnce();
	await act(async () => deletion.resolve({}));
	await screen.findByText("No side chat opened.");
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	expect(screen.getByRole("button", { name: "New side chat" })).toBeEnabled();
	await waitFor(() => expect(screen.getByRole("button", { name: "New side chat" })).toHaveFocus());
});

it("dismisses a rejected delete into an interactive tab warning", async () => {
	remove.mockResolvedValueOnce({ error: {}, response: { status: 409 } });
	render(<Panel />); await screen.findByLabelText("Side chat question"); await deleteFromPanel();
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	expect(screen.getByRole("alert")).toHaveTextContent("You can keep using it");
	expect(screen.getByLabelText("Side chat question")).toBeInTheDocument();
	expect(screen.getByTitle("Side")).not.toHaveAttribute("aria-busy");
});

it("retains a partial-failure tab read-only and removes it when background cleanup finishes", async () => {
	remove.mockImplementationOnce(async () => {
		pending[0].phase = "cleanup_pending";
		return { error: {}, response: { status: 500 } };
	});
	render(<Panel />); await screen.findByLabelText("Side chat question"); await deleteFromPanel();
	await screen.findByText(/its agent is still stopping/);
	expect(screen.queryByLabelText("Side chat question")).not.toBeInTheDocument();
	expect(screen.getByRole("tab", { name: "Side" })).not.toHaveAttribute("aria-busy");
	get.mockResolvedValue({ data: { sides: [] } }); pending.length = 0;
	await changed();
	await screen.findByText("No side chat opened.");
	expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it.each(["cleanup_pending", "unconfirmed"])("does not turn an already-closed %s response into success", async outcome => {
	const hook = await setup(); remove.mockResolvedValueOnce({ error: {}, response: { status: 404 } });
	confirmClose.mockResolvedValueOnce(outcome);
	await act(async () => { await expect(hook.result.current.close("side")).rejects.toThrow(); });
	expect(hook.result.current.activeId).toBe("side");
	await act(async () => hook.result.current.send("side", "blocked"));
	expect(post).not.toHaveBeenCalled();
});

it("removes a confirmed shutdown even if confirmation IPC fails", async () => {
	const hook = await setup(); remove.mockResolvedValueOnce({});
	confirmClose.mockRejectedValueOnce(new Error("IPC lost"));
	await act(async () => hook.result.current.close("side"));
	expect(hook.result.current.sides).toEqual([]);
	expect(capture).not.toHaveBeenCalledWith("side", true);
});

it("preserves a tab created and selected while an older deletion finishes", async () => {
	const hook = await setup(); const deletion = deferred<object>(); remove.mockReturnValueOnce(deletion.promise);
	let closing!: Promise<void>; act(() => { closing = hook.result.current.close("side"); });
	await waitFor(() => expect(remove).toHaveBeenCalled());
	post.mockResolvedValueOnce({ data: { side: { id: "new", sessionId: "session", state: "ready", label: "New" } } });
	await act(async () => { await hook.result.current.create(undefined, true); });
	await act(async () => { deletion.resolve({}); await closing; });
	expect(hook.result.current.activeId).toBe("new");
	expect(hook.result.current.sides.map(side => side.id)).toEqual(["new"]);
	await changed();
	expect(hook.result.current.activeId).toBe("new");
});

it("ignores list results issued before a close mutation", async () => {
	const hook = await setup(); const oldList = deferred<object>();
	get.mockImplementationOnce(() => oldList.promise); await changed();
	remove.mockResolvedValueOnce({}); await act(async () => hook.result.current.close("side"));
	await act(async () => oldList.resolve({ data: { sides: [{ id: "side", sessionId: "session" }] } }));
	expect(hook.result.current.sides).toEqual([]);
});

it("ignores an older list response after a newer response selected another side", async () => {
	const hook = await setup(); const oldList = deferred<object>();
	get.mockImplementationOnce(() => oldList.promise); await changed();
	get.mockResolvedValueOnce({ data: { sides: [{ id: "new", sessionId: "session", label: "New" }] } });
	await changed();
	await waitFor(() => expect(hook.result.current.activeId).toBe("new"));
	await act(async () => oldList.resolve({ data: { sides: [{ id: "side", sessionId: "session" }] } }));
	expect(hook.result.current.activeId).toBe("new");
});

it("does not write an old close completion into a replacement session", async () => {
	const hook = renderHook(({ id }) => useIndependentSideChats(id, [], [], async () => [], false), { initialProps: { id: "session" } });
	await waitFor(() => expect(hook.result.current.activeId).toBe("side"));
	const deletion = deferred<object>(); remove.mockReturnValueOnce(deletion.promise);
	let closing!: Promise<void>; act(() => { closing = hook.result.current.close("side"); });
	await waitFor(() => expect(remove).toHaveBeenCalled());
	get.mockResolvedValue({ data: { sides: [{ id: "other", sessionId: "replacement", label: "Other" }] } });
	hook.rerender({ id: "replacement" });
	await waitFor(() => expect(hook.result.current.activeId).toBe("other"));
	await act(async () => { deletion.resolve({}); await closing; });
	expect(hook.result.current.activeId).toBe("other");
	expect(confirmClose).not.toHaveBeenCalled();
});

it("fences a late close reply after a same-port daemon replacement", async () => {
	const hook = await setup();
	await act(async () => { for (const listener of daemonListeners) listener({ state: "ready", pid: 1, port: 3002 }); });
	const deletion = deferred<object>(); remove.mockReturnValueOnce(deletion.promise);
	let closing!: Promise<void>; act(() => { closing = hook.result.current.close("side"); });
	await waitFor(() => expect(remove).toHaveBeenCalled());
	await act(async () => { for (const listener of daemonListeners) listener({ state: "ready", pid: 2, port: 3002 }); });
	await act(async () => { deletion.resolve({}); await closing; });
	expect(hook.result.current.activeId).toBe("side");
	expect(confirmClose).not.toHaveBeenCalled();
});

it("ignores a snapshot error that arrives after the final tab is deleted", async () => {
	const hook = await setup(); const snapshot = deferred<object>();
	const normal = get.getMockImplementation()!;
	get.mockImplementation((path: string, ...args: unknown[]) => path.endsWith("/{sideId}") ? snapshot.promise : normal(path, ...args));
	vi.useFakeTimers(); await act(async () => vi.advanceTimersByTimeAsync(501));
	remove.mockResolvedValueOnce({});
	await act(async () => hook.result.current.close("side"));
	await act(async () => snapshot.resolve({ error: { code: "CHAT_SIDE_CLOSED", message: "Side chat is closed" } }));
	expect(hook.result.current.sides).toEqual([]);
	expect(hook.result.current.error).toBeUndefined();
});

it("ignores a draft-save error that arrives after shutdown without saving again", async () => {
	const hook = await setup(); const save = deferred<object>();
	put.mockReturnValueOnce(save.promise);
	vi.useFakeTimers(); act(() => hook.result.current.setQuestionDraft("side", "draft"));
	await act(async () => vi.advanceTimersByTimeAsync(351));
	expect(put).toHaveBeenCalledOnce();
	remove.mockResolvedValueOnce({}); await act(async () => hook.result.current.close("side"));
	await act(async () => save.resolve({ error: { code: "CHAT_SIDE_CLOSED" } }));
	await act(async () => vi.advanceTimersByTimeAsync(1000));
	expect(hook.result.current.error).toBeUndefined();
	expect(put).toHaveBeenCalledOnce();
});

it("ignores a late send error without resurrecting the deleted draft", async () => {
	const hook = await setup(); const sent = deferred<object>(); post.mockReturnValueOnce(sent.promise);
	let sending!: Promise<void>; act(() => { sending = hook.result.current.send("side", "question"); });
	await waitFor(() => expect(post).toHaveBeenCalledOnce());
	remove.mockResolvedValueOnce({}); await act(async () => hook.result.current.close("side"));
	const savedCount = put.mock.calls.length;
	await act(async () => { sent.resolve({ error: { code: "CHAT_SIDE_CLOSED" }, response: { status: 404 } }); await sending; });
	expect(hook.result.current.error).toBeUndefined();
	expect(hook.result.current.sides).toEqual([]);
	expect(put).toHaveBeenCalledTimes(savedCount);
});

it("retains an uncached inactive failed-close tab without an opening or empty-chat invitation", async () => {
	const normal = get.getMockImplementation()!;
	get.mockImplementation((path: string, ...args: unknown[]) => path.endsWith("/side-chats") ? { data: { sides: [
		{ id: "side", sessionId: "session", state: "ready", label: "Side" },
		{ id: "other", sessionId: "session", state: "ready", label: "Other" },
	] } } : normal(path, ...args));
	remove.mockRejectedValueOnce(new Error("Lost reply"));
	render(<Panel />); await screen.findByLabelText("Side chat question");
	await userEvent.click(screen.getByRole("button", { name: "Close Other" }));
	await userEvent.click(screen.getByRole("button", { name: "Delete" }));
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	expect(screen.getByRole("tab", { name: "Side" })).toHaveAttribute("aria-selected", "true");
	expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	await userEvent.click(screen.getByRole("tab", { name: "Other" }));
	expect(screen.getByRole("alert")).toHaveTextContent("temporarily read-only");
	expect(screen.queryByText("Opening side chat…")).not.toBeInTheDocument();
	expect(screen.queryByText("Ask a side question")).not.toBeInTheDocument();
	expect(screen.queryByLabelText("Side chat question")).not.toBeInTheDocument();
});

it.each(["middle", "last"])("selects the correct neighbor when deleting the %s active tab", async target => {
	const normal = get.getMockImplementation()!;
	const rows = ["first", "middle", "last"].map(id => ({ id, sessionId: "session", label: id, state: "ready" }));
	get.mockImplementation((path: string, ...args: unknown[]) => path.endsWith("/side-chats") ? { data: { sides: rows } } : normal(path, ...args));
	const hook = renderHook(() => useIndependentSideChats("session", [], [], async () => [], false));
	await waitFor(() => expect(hook.result.current.activeId).toBe("last"));
	act(() => hook.result.current.show(target)); remove.mockResolvedValueOnce({});
	await act(async () => hook.result.current.close(target));
	expect(hook.result.current.activeId).toBe(target === "middle" ? "last" : "middle");
});
