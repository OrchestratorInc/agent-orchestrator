import { act, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "../ui/tooltip";
import { useIndependentSideChats } from "./IndependentSideChats";
import type { PendingSideClose, SideCloseState } from "../../../shared/side-close";
import { SideCloseRecovery } from "../../../main/side-close";

const { get, post, put, remove, capture, confirmClose, getState, pending, closedIds, listeners, daemonListeners } = vi.hoisted(() => ({
	get: vi.fn(), post: vi.fn(), remove: vi.fn(), capture: vi.fn(), confirmClose: vi.fn(),
	getState: vi.fn(),
	put: vi.fn(), listeners: new Set<() => void>(), daemonListeners: new Set<(status: { state: string; pid: number; port: number }) => void>(),
	pending: [] as PendingSideClose[], closedIds: [] as string[],
}));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, POST: post, DELETE: remove, PUT: put, PATCH: vi.fn() },
	apiErrorMessage: () => "Close failed", getApiBaseUrl: () => "", subscribeApiBaseUrl: () => () => {},
}));
vi.mock("../../lib/bridge", () => ({ aoBridge: { daemon: { getStatus: async () => ({ state: "ready", pid: 1, port: 3002 }), onStatus: (listener: (status: { state: string; pid: number; port: number }) => void) => { daemonListeners.add(listener); return () => daemonListeners.delete(listener); } }, sideChats: {
	capture, confirmClose, getCloseState: getState, onPendingClosesChanged: (listener: () => void) => { listeners.add(listener); return () => listeners.delete(listener); },
} } }));

beforeEach(() => {
	vi.resetAllMocks(); pending.length = 0; closedIds.length = 0; listeners.clear(); daemonListeners.clear();
	getState.mockImplementation(async (sessionId: string) => ({ pending: pending.filter(side => side.sessionId === sessionId), closedIds: [...closedIds] }));
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

function Panel({ stage = async () => [] }: { stage?: () => Promise<string[]> } = {}) {
	const chat = useIndependentSideChats("session", [], [], stage, false);
	useEffect(() => chat.show("side"), []);
	return <TooltipProvider>{chat.panel}<output data-testid="active-error">{chat.error}</output></TooltipProvider>;
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

function trackRecovery() {
	const recovery = new SideCloseRecovery();
	const record = { side: { id: "side", sessionId: "session", state: "ready", label: "Side" }, providerHostId: "host", providerForkId: "fork", harness: "codex" };
	const snapshot = (sides: unknown[]) => ({ appRunId: "run", sides });
	recovery.remember(snapshot([record]));
	getState.mockImplementation(async (sessionId: string) => recovery.summary(sessionId));
	capture.mockImplementation(async (id?: string) => { if (id) recovery.begin(id); });
	return { recovery, record, snapshot };
}

function paginatedHistory() {
	const rows = ["side", "other"].map(id => ({ id, sessionId: "session", state: "ready", label: id === "side" ? "Side" : "Other" }));
	const page = (id: string, older: boolean) => {
		const turn = older ? "old" : "latest";
		const createdAt = older ? "2026-01-01T00:00:00Z" : "2026-01-02T00:00:00Z";
		return { data: { snapshot: {
			side: rows.find(row => row.id === id), hasMore: !older, activities: [],
			turns: [{ id: turn, state: "completed", createdAt }],
			messages: [{ id: turn, turnId: turn, role: "assistant", text: older ? "Loaded older history" : "Latest history", sequence: older ? 1 : 2, revision: 1, createdAt }],
		} } };
	};
	get.mockImplementation(async (path: string, options?: { params?: { path?: { sideId?: string }; query?: { before?: string } } }) => {
		if (path.endsWith("/side-chats")) return { data: { sides: rows } };
		if (path.endsWith("/draft")) return { data: { contentJson: "" } };
		return page(options?.params?.path?.sideId ?? "side", Boolean(options?.params?.query?.before));
	});
	return page;
}

it.each(["inactive", "rejected"])("preserves loaded history during an %s close", async mode => {
	paginatedHistory();
	const deletion = deferred<object>(); remove.mockReturnValueOnce(deletion.promise);
	render(<Panel />); await screen.findByText("Latest history");
	await userEvent.click(screen.getByRole("button", { name: "Load earlier messages" }));
	await screen.findByText("Loaded older history");
	await userEvent.click(screen.getByRole("button", { name: mode === "inactive" ? "Close Other" : "Close Side" }));
	await userEvent.click(screen.getByRole("button", { name: "Delete" }));
	expect(screen.getByText("Loaded older history")).toBeInTheDocument();
	await act(async () => deletion.resolve(mode === "rejected" ? { error: {}, response: { status: 409 } } : {}));
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	expect(screen.getByText("Loaded older history")).toBeInTheDocument();
	expect(screen.getByRole("tab", { name: "Side" })).toHaveAttribute("aria-selected", "true");
	if (mode === "rejected") expect(screen.getByLabelText("Side chat question")).toBeInTheDocument();
});

it.each(["rejected", "closed"])("ignores late older messages after closing starts (%s) and releases pagination", async outcome => {
	const page = paginatedHistory(); const normal = get.getMockImplementation()!;
	const older = deferred<object>(); const deletion = deferred<object>();
	get.mockImplementation((path: string, options: { params?: { query?: { before?: string } } }) => options?.params?.query?.before ? older.promise : normal(path, options));
	remove.mockReturnValueOnce(deletion.promise);
	render(<Panel />); await screen.findByText("Latest history");
	await userEvent.click(screen.getByRole("button", { name: "Load earlier messages" }));
	await deleteFromPanel();
	await act(async () => older.resolve(page("side", true)));
	expect(screen.queryByText("Loaded older history")).not.toBeInTheDocument();
	await act(async () => deletion.resolve(outcome === "rejected" ? { error: {}, response: { status: 409 } } : {}));
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	expect(screen.queryByText("Loaded older history")).not.toBeInTheDocument();
	if (outcome === "rejected") {
		expect(screen.getByRole("button", { name: "Load earlier messages" })).toBeEnabled();
		await userEvent.click(screen.getByRole("button", { name: "Load earlier messages" }));
		await screen.findByText("Loaded older history");
	}
});

it("allows an active pagination request to finish while an inactive chat closes", async () => {
	const page = paginatedHistory(); const normal = get.getMockImplementation()!;
	const older = deferred<object>(); const deletion = deferred<object>();
	get.mockImplementation((path: string, options: { params?: { query?: { before?: string } } }) => options?.params?.query?.before ? older.promise : normal(path, options));
	remove.mockReturnValueOnce(deletion.promise);
	render(<Panel />); await screen.findByText("Latest history");
	await userEvent.click(screen.getByRole("button", { name: "Load earlier messages" }));
	await userEvent.click(screen.getByRole("button", { name: "Close Other" }));
	await userEvent.click(screen.getByRole("button", { name: "Delete" }));
	await act(async () => older.resolve(page("side", true)));
	await screen.findByText("Loaded older history");
	await act(async () => deletion.resolve({}));
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	expect(screen.getByText("Loaded older history")).toBeInTheDocument();
});

it.each(["response", "network", "pending"])("applies shutdown proof independently of a %s list failure", async failure => {
	const { recovery, record, snapshot } = trackRecovery();
	const hook = await setup(); remove.mockResolvedValueOnce({}); confirmClose.mockResolvedValueOnce("unconfirmed");
	await act(async () => { await expect(hook.result.current.close("side")).rejects.toThrow(); });
	recovery.remember(snapshot([{ ...record, cleanupOnly: true, hostStopped: true }]));
	const normal = get.getMockImplementation()!;
	get.mockImplementation((path: string, ...args: unknown[]) => {
		if (!path.endsWith("/side-chats")) return normal(path, ...args);
		if (failure === "network") return Promise.reject(new Error("Network lost"));
		return failure === "pending" ? new Promise(() => {}) : { error: { code: "TEMPORARILY_UNAVAILABLE" } };
	});
	await changed();
	expect(hook.result.current.sides).toEqual([]);
	expect(hook.result.current.activeId).toBeUndefined();
	if (failure !== "pending") expect(hook.result.current.error).toBe("Close failed");
});

it("keeps unknown close locks when close-state lookup fails but the list succeeds", async () => {
	const hook = await setup(); remove.mockRejectedValueOnce(new Error("Lost reply"));
	await act(async () => { await expect(hook.result.current.close("side")).rejects.toThrow(); });
	getState.mockRejectedValue(new Error("IPC unavailable"));
	const normal = get.getMockImplementation()!;
	get.mockImplementation((path: string, ...args: unknown[]) => path.endsWith("/side-chats") ? { data: { sides: [] } } : normal(path, ...args));
	await changed();
	expect(hook.result.current.activeId).toBe("side");
	await act(async () => hook.result.current.send("side", "blocked"));
	expect(post).not.toHaveBeenCalled();
});

it("ignores an older close-state response after newer shutdown proof and selection", async () => {
	const hook = await setup(); const oldState = deferred<SideCloseState>();
	getState.mockReturnValueOnce(oldState.promise); await changed();
	closedIds.push("side");
	get.mockResolvedValueOnce({ data: { sides: [{ id: "new", sessionId: "session", label: "New" }] } });
	await changed();
	expect(hook.result.current.activeId).toBe("new");
	await act(async () => oldState.resolve({ pending: [{ id: "side", sessionId: "session", label: "Side", phase: "unconfirmed" }], closedIds: [] }));
	expect(hook.result.current.sides.map(side => side.id)).toEqual(["new"]);
	expect(hook.result.current.activeId).toBe("new");
});

it("does not apply an older pending-close summary after a newer summary", async () => {
	const hook = await setup(); const oldState = deferred<SideCloseState>();
	getState.mockReturnValueOnce(oldState.promise); await changed();
	await changed();
	await act(async () => oldState.resolve({ pending: [{ id: "side", sessionId: "session", label: "Side", phase: "unconfirmed" }], closedIds: [] }));
	post.mockResolvedValueOnce({});
	await act(async () => hook.result.current.send("side", "still usable"));
	expect(post).toHaveBeenCalledOnce();
});

it("ignores close-state evidence from a replaced daemon attachment", async () => {
	const hook = await setup();
	await act(async () => { for (const listener of daemonListeners) listener({ state: "ready", pid: 1, port: 3002 }); });
	const oldState = deferred<SideCloseState>(); getState.mockReturnValueOnce(oldState.promise); await changed();
	await act(async () => { for (const listener of daemonListeners) listener({ state: "ready", pid: 2, port: 3002 }); });
	await act(async () => oldState.resolve({ pending: [], closedIds: ["side"] }));
	expect(hook.result.current.activeId).toBe("side");
});

it.each(["cancelled", "pending", "unavailable", "closed"])("reconciles a rejected delete when cancellation bookkeeping fails (%s)", async state => {
	const normal = capture.getMockImplementation()!;
	capture.mockImplementation(async (id?: string, cancel?: boolean) => {
		if (!cancel) return normal(id, cancel);
		if (state === "cancelled" || state === "closed") pending.length = 0;
		if (state === "closed") closedIds.push(id!);
		if (state === "unavailable") getState.mockRejectedValue(new Error("IPC unavailable"));
		throw new Error("Cancellation snapshot failed");
	});
	remove.mockResolvedValueOnce({ error: {}, response: { status: 409 } });
	render(<Panel />); await screen.findByLabelText("Side chat question"); await deleteFromPanel();
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	if (state === "closed") {
		await screen.findByText("No side chat opened.");
	} else if (state === "cancelled") {
		expect(screen.getByRole("alert")).toHaveTextContent("You can keep using it");
		expect(screen.getByLabelText("Side chat question")).toBeInTheDocument();
	} else {
		expect(screen.getByRole("alert")).toHaveTextContent("temporarily read-only");
		expect(screen.queryByLabelText("Side chat question")).not.toBeInTheDocument();
	}
});

it.each(["host_stopped", "cleanup_gone"])("removes an unconfirmed tab after %s without observing cleanup pending", async completion => {
	const { recovery, record, snapshot } = trackRecovery();
	confirmClose.mockResolvedValueOnce("unconfirmed"); remove.mockResolvedValueOnce({});
	const hook = await setup();
	await act(async () => { await expect(hook.result.current.close("side")).rejects.toThrow(); });
	await act(async () => hook.result.current.send("side", "must remain read-only"));
	expect(post).not.toHaveBeenCalled();
	recovery.remember(snapshot([{ ...record, cleanupOnly: true, hostStopped: true }]));
	if (completion === "cleanup_gone") recovery.remember(snapshot([]));
	// The list still contains an old open row; authoritative shutdown wins.
	await changed();
	expect(hook.result.current.sides).toEqual([]);
	expect(hook.result.current.activeId).toBeUndefined();
	await changed(); expect(hook.result.current.sides).toEqual([]);
	hook.unmount();
	const restored = renderHook(() => useIndependentSideChats("session", [], [], async () => [], false));
	await waitFor(() => expect(getState).toHaveBeenLastCalledWith("session"));
	await changed();
	expect(restored.result.current.sides).toEqual([]);
});

it("preserves a newly selected tab when direct shutdown confirmation removes another side", async () => {
	const { recovery, record, snapshot } = trackRecovery();
	confirmClose.mockResolvedValueOnce("unconfirmed"); remove.mockResolvedValueOnce({});
	const hook = await setup();
	await act(async () => { await expect(hook.result.current.close("side")).rejects.toThrow(); });
	post.mockResolvedValueOnce({ data: { side: { id: "new", sessionId: "session", state: "ready", label: "New" } } });
	await act(async () => hook.result.current.create(undefined, true));
	act(() => hook.result.current.setQuestionDraft("new", "Keep this draft"));
	recovery.remember(snapshot([{ ...record, cleanupOnly: true, hostStopped: true }]));
	await changed();
	expect(hook.result.current.activeId).toBe("new");
	expect(hook.result.current.sides.map(side => side.id)).toEqual(["new"]);
	post.mockResolvedValueOnce({});
	await act(async () => hook.result.current.send("new", "Keep this draft"));
	expect(post).toHaveBeenLastCalledWith(expect.stringContaining("/messages"), expect.objectContaining({ body: expect.objectContaining({ text: "Keep this draft" }) }));
	expect(hook.result.current.error).toBeUndefined();
});

it("uses authoritative shutdown proof returned after a failed DELETE", async () => {
	const { recovery, record, snapshot } = trackRecovery();
	const hook = await setup();
	remove.mockImplementationOnce(async () => {
		recovery.remember(snapshot([{ ...record, cleanupOnly: true, hostStopped: true }]));
		return { error: {}, response: { status: 500 } };
	});
	await act(async () => hook.result.current.close("side"));
	expect(hook.result.current.sides).toEqual([]);
});

it("keeps an unknown close read-only when a list and pending summary are merely empty", async () => {
	const hook = await setup(); capture.mockRejectedValueOnce(new Error("Capture unavailable"));
	await act(async () => { await expect(hook.result.current.close("side")).rejects.toThrow(); });
	get.mockResolvedValue({ data: { sides: [] } });
	await changed();
	expect(hook.result.current.activeId).toBe("side");
	expect(hook.result.current.sides.map(side => side.id)).toEqual(["side"]);
	await act(async () => hook.result.current.send("side", "blocked"));
	expect(post).not.toHaveBeenCalled();
});

it("ignores an old close-state reply after switching sessions", async () => {
	const hook = renderHook(({ id }) => useIndependentSideChats(id, [], [], async () => [], false), { initialProps: { id: "session" } });
	await waitFor(() => expect(hook.result.current.activeId).toBe("side"));
	const oldState = deferred<SideCloseState>(); getState.mockReturnValueOnce(oldState.promise);
	await changed();
	get.mockResolvedValue({ data: { sides: [{ id: "other", sessionId: "replacement", label: "Other" }] } });
	hook.rerender({ id: "replacement" });
	await waitFor(() => expect(hook.result.current.activeId).toBe("other"));
	await act(async () => oldState.resolve({ pending: [], closedIds: ["side"] }));
	expect(hook.result.current.activeId).toBe("other");
	expect(hook.result.current.sides.map(side => side.id)).toEqual(["other"]);
});

it("dismisses a completed delete without letting its late reply unlock a newer dialog", async () => {
	const oldDelete = deferred<object>(); const newDelete = deferred<object>();
	remove.mockReturnValueOnce(oldDelete.promise).mockReturnValueOnce(newDelete.promise);
	render(<Panel />); await screen.findByLabelText("Side chat question"); await deleteFromPanel();
	closedIds.push("side"); pending.length = 0; await changed();
	await screen.findByText("No side chat opened.");
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	post.mockResolvedValueOnce({ data: { side: { id: "new", sessionId: "session", state: "ready", label: "New" } } });
	await userEvent.click(screen.getByRole("button", { name: "New side chat" }));
	await screen.findByRole("tab", { name: "New" });
	await userEvent.click(screen.getByRole("button", { name: "Close New" }));
	await userEvent.click(screen.getByRole("button", { name: "Delete" }));
	await act(async () => oldDelete.resolve({ error: {}, response: { status: 500 } }));
	expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
	expect(screen.getByTitle("New")).toHaveAttribute("aria-busy", "true");
	await act(async () => newDelete.resolve({}));
	await screen.findByText("No side chat opened.");
	expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

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
	get.mockResolvedValue({ data: { sides: [] } }); pending.length = 0; closedIds.push("side");
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

it("retains the transcript read-only when successful deletion returns an unconfirmed shutdown", async () => {
	const normal = get.getMockImplementation()!;
	get.mockImplementation((path: string, ...args: unknown[]) => path.endsWith("/{sideId}") ? { data: { snapshot: {
		side: { id: "side", state: "ready" }, activities: [],
		turns: [{ id: "turn", state: "completed", createdAt: "2026-01-01T00:00:00Z" }],
		messages: [{ id: "answer", turnId: "turn", role: "assistant", text: "Retained answer", revision: 1, sequence: 1, createdAt: "2026-01-01T00:00:00Z" }],
	} } } : normal(path, ...args));
	remove.mockResolvedValueOnce({}); confirmClose.mockResolvedValueOnce("unconfirmed");
	render(<Panel />); await screen.findByText("Retained answer"); await deleteFromPanel();
	await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
	expect(screen.getByRole("tab", { name: "Side" })).not.toHaveAttribute("aria-busy");
	expect(screen.getByRole("alert")).toHaveTextContent("temporarily read-only");
	expect(screen.getByText("Retained answer")).toBeInTheDocument();
	expect(screen.queryByLabelText("Side chat question")).not.toBeInTheDocument();
	expect(screen.queryByText("No side chat opened.")).not.toBeInTheDocument();
	expect(capture).not.toHaveBeenCalledWith("side", true);
	expect(pending).toHaveLength(1);
});

it("preserves another selected tab while an unconfirmed deletion awaits cleanup", async () => {
	const hook = await setup(); const confirmation = deferred<string>();
	remove.mockResolvedValueOnce({}); confirmClose.mockReturnValueOnce(confirmation.promise);
	let closing!: Promise<void>;
	act(() => { closing = hook.result.current.close("side").catch(() => undefined); });
	await waitFor(() => expect(confirmClose).toHaveBeenCalledWith("side"));
	post.mockResolvedValueOnce({ data: { side: { id: "new", sessionId: "session", state: "ready", label: "New" } } });
	await act(async () => hook.result.current.create(undefined, true));
	await act(async () => { confirmation.resolve("unconfirmed"); await closing; });
	expect(hook.result.current.activeId).toBe("new");
	expect(hook.result.current.sides.map(side => side.id)).toEqual(expect.arrayContaining(["side", "new"]));
	post.mockClear(); await act(async () => hook.result.current.send("side", "blocked"));
	expect(post).not.toHaveBeenCalled();
	const normal = get.getMockImplementation()!;
	get.mockImplementation((path: string, ...args: unknown[]) => path.endsWith("/side-chats")
		? { data: { sides: [{ id: "new", sessionId: "session", state: "ready", label: "New" }] } }
		: normal(path, ...args));
	pending[0].phase = "cleanup_pending"; await changed();
	pending.length = 0; closedIds.push("side"); await changed();
	expect(hook.result.current.activeId).toBe("new");
	expect(hook.result.current.sides.map(side => side.id)).toEqual(["new"]);
});

it("removes a confirmed shutdown even if confirmation IPC fails", async () => {
	const hook = await setup(); remove.mockResolvedValueOnce({});
	confirmClose.mockRejectedValueOnce(new Error("IPC lost"));
	await act(async () => hook.result.current.close("side"));
	expect(hook.result.current.sides).toEqual([]);
	expect(hook.result.current.activeId).toBeUndefined();
	expect(capture).not.toHaveBeenCalledWith("side", true);
	// Bookkeeping failure must retain Electron's cleanup ownership without
	// allowing a subsequent refresh to resurrect a confirmed shutdown.
	expect(pending).toHaveLength(1);
	await changed();
	expect(hook.result.current.sides).toEqual([]);
});

it("keeps an already-closed response read-only when confirmation is unavailable", async () => {
	const hook = await setup();
	remove.mockResolvedValueOnce({ error: {}, response: { status: 404 } });
	confirmClose.mockRejectedValueOnce(new Error("Desktop close confirmation is unavailable."));
	await act(async () => { await expect(hook.result.current.close("side")).rejects.toThrow(); });
	expect(hook.result.current.activeId).toBe("side");
	expect(capture).not.toHaveBeenCalledWith("side", true);
	await act(async () => hook.result.current.send("side", "blocked"));
	expect(post).not.toHaveBeenCalled();
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

it("applies a slow list response even while a newer poll is pending", async () => {
	const hook = await setup();
	const first = deferred<object>(); const second = deferred<object>();
	get.mockImplementationOnce(() => first.promise); await changed();
	get.mockImplementationOnce(() => second.promise); await changed();
	await act(async () => first.resolve({ data: { sides: [{ id: "new", sessionId: "session", label: "New" }] } }));
	expect(hook.result.current.activeId).toBe("new");
	await act(async () => second.resolve({ data: { sides: [{ id: "latest", sessionId: "session", label: "Latest" }] } }));
	expect(hook.result.current.activeId).toBe("latest");
});

it("clears a recovered list error without clearing a create failure", async () => {
	const hook = await setup();
	get.mockResolvedValueOnce({ error: {} }); await changed();
	expect(hook.result.current.error).toBe("Close failed");
	await changed(); expect(hook.result.current.error).toBeUndefined();
	post.mockRejectedValueOnce(new Error("Create failed"));
	await act(async () => { await expect(hook.result.current.create(undefined, true)).rejects.toThrow(); });
	await changed(); expect(hook.result.current.error).toBe("Close failed");
});

it("keeps a failed capture locked while pending-close reconciliation is delayed", async () => {
	const hook = await setup(); const reconciliation = deferred<SideCloseState>();
	capture.mockRejectedValueOnce(new Error("Capture failed"));
	getState.mockClear(); getState.mockReturnValueOnce(reconciliation.promise);
	let closing!: Promise<void>;
	act(() => { closing = hook.result.current.close("side").catch(() => undefined); });
	await waitFor(() => expect(getState).toHaveBeenCalledOnce());
	await changed();
	await act(async () => hook.result.current.send("side", "must stay blocked"));
	expect(post).not.toHaveBeenCalled();
	await act(async () => { reconciliation.resolve({ pending: [], closedIds: [] }); await closing; });
	expect(remove).not.toHaveBeenCalled();
});

it("does not display a late attachment failure on another selected side", async () => {
	const normal = get.getMockImplementation()!;
	get.mockImplementation((path: string, ...args: unknown[]) => path.endsWith("/side-chats") ? { data: { sides: [
		{ id: "side", sessionId: "session", state: "ready", label: "Side" },
		{ id: "other", sessionId: "session", state: "ready", label: "Other" },
	] } } : normal(path, ...args));
	const staged = deferred<string[]>(); const stage = vi.fn(() => staged.promise);
	const view = render(<Panel stage={stage} />);
	await screen.findByLabelText("Side chat question");
	fireEvent.change(view.container.querySelector('input[type="file"]')!, { target: { files: [new File(["content"], "note.txt", { type: "text/plain" })] } });
	await waitFor(() => expect(stage).toHaveBeenCalledOnce());
	await userEvent.click(screen.getByRole("tab", { name: "Other" }));
	await act(async () => staged.reject(new Error("Staging failed")));
	expect(screen.getByTestId("active-error")).toBeEmptyDOMElement();
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
