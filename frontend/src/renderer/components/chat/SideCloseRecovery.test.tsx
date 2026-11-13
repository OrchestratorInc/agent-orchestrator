import { act, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "../ui/tooltip";
import { useIndependentSideChats } from "./IndependentSideChats";
import { SIDE_CLOSE_OPERATION_TIMEOUT_MS, type SideCloseEntry, type SideCloseRequest, type SideCloseState } from "../../../shared/side-close";

const { get, post, put, patch, remove, capture, requestClose, getState, listeners, daemonListeners } = vi.hoisted(() => ({
 get: vi.fn(), post: vi.fn(), put: vi.fn(), patch: vi.fn(), remove: vi.fn(), capture: vi.fn(), requestClose: vi.fn(), getState: vi.fn(),
 listeners: new Set<(state: SideCloseState) => void>(), daemonListeners: new Set<(status: { state: string; pid: number; port: number }) => void>(),
}));
vi.mock("../../lib/api-client", () => ({ apiClient: { GET: get, POST: post, DELETE: remove, PUT: put, PATCH: patch }, apiErrorMessage: () => "Operation failed", getApiBaseUrl: () => "", subscribeApiBaseUrl: () => () => {} }));
vi.mock("../../lib/bridge", () => ({ aoBridge: {
 daemon: { getStatus: async () => ({ state: "ready", pid: 1, port: 3002 }), onStatus: (listener: (status: { state: string; pid: number; port: number }) => void) => { daemonListeners.add(listener); return () => daemonListeners.delete(listener); } },
 sideChats: { protocolVersion: 1, mode: "desktop", capture, requestClose, getCloseState: getState, onCloseStateChanged: (listener: (state: SideCloseState) => void) => { listeners.add(listener); return () => listeners.delete(listener); } },
} }));

const row = (id = "side", sessionId = "session") => ({ id, sessionId, state: "ready", label: id === "side" ? "Side" : id, mainConversationId: "main" });
let state: SideCloseState;
let rows: ReturnType<typeof row>[];
function summary(sides: SideCloseEntry[] = [], revision = 0, recovery: SideCloseState["recovery"] = "ready", epoch = "test-daemon"): SideCloseState { return { protocolVersion: 1, sessionId: "session", epoch, revision, recovery, sides }; }
function entry(status: SideCloseEntry["status"], version = 1, id = "side", operationId?: string): SideCloseEntry { return { id, sessionId: "session", label: id === "side" ? "Side" : id, status, version, operationId }; }
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (error: Error) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
async function publish(next: SideCloseState) { state = next; await act(async () => { for (const listener of listeners) listener({ ...next, sessionId: null }); }); }
function snapshot(id = "side", sideState = "ready", queued = false) { return { side: { ...row(id), state: sideState }, messages: [], turns: queued ? [{ id: `${id}-queued`, state: "queued", text: `${id} queued text`, createdAt: "2026-01-01T00:00:00Z" }] : [], activities: [] }; }

beforeEach(() => {
 vi.resetAllMocks(); listeners.clear(); daemonListeners.clear(); state = summary(); rows = [row()];
 HTMLElement.prototype.scrollTo = vi.fn();
 getState.mockImplementation(async (sessionId: string) => ({ ...state, sessionId, sides: state.sides.filter(side => side.sessionId === sessionId) }));
 get.mockImplementation(async (path: string, options?: { params?: { path?: { sideId?: string } } }) => path.endsWith("/side-chats") ? { data: { sides: rows } } : path.endsWith("/draft") ? { data: { contentJson: "" } } : { data: { snapshot: snapshot(options?.params?.path?.sideId) } });
 put.mockResolvedValue({}); post.mockResolvedValue({}); patch.mockResolvedValue({}); capture.mockResolvedValue(undefined);
 requestClose.mockImplementation(async (request: SideCloseRequest) => { state = summary([entry("closed", 2, request.sideId, request.requestId)], state.revision + 2); return state; });
});
afterEach(() => vi.useRealTimers());
function Panel({ showId }: { showId?: string } = {}) { const chat = useIndependentSideChats("session", [], [], async () => [], false); useEffect(() => chat.show(showId), []); return <TooltipProvider>{chat.panel}<output data-testid="active-error">{chat.error}</output></TooltipProvider>; }
async function setup() { const hook = renderHook(() => useIndependentSideChats("session", [], [], async () => [], false)); await waitFor(() => expect(hook.result.current.activeId).toBe("side")); await waitFor(() => expect(getState).toHaveBeenCalled()); return hook; }
async function deleteFromPanel(name = "Side") { await userEvent.click(screen.getByRole("button", { name: `Close ${name}` })); await userEvent.click(screen.getByRole("button", { name: "Delete" })); }

it("keeps a confirmed shutdown removed across remount after a lost IPC reply", async () => {
 const hook = await setup();
 requestClose.mockImplementationOnce(async (request: SideCloseRequest) => { state = summary([entry("closed", 2, "side", request.requestId)], 2); throw new Error("IPC reply lost after main accepted shutdown"); });
 await act(async () => { await expect(hook.result.current.close("side")).rejects.toThrow(); });
 await waitFor(() => expect(hook.result.current.sides).toEqual([]));
 hook.unmount(); rows = [];
 const restored = renderHook(() => useIndependentSideChats("session", [], [], async () => [], false));
 await waitFor(() => expect(getState.mock.calls.length).toBeGreaterThan(1));
 expect(restored.result.current.sides).toEqual([]); expect(remove).not.toHaveBeenCalled();
});

it.each(["error", "pending"])("selects a restored pending side without an empty state when the list is %s", async failure => {
 state = summary([entry("unconfirmed")], 1);
 get.mockImplementation((path: string) => path.endsWith("/side-chats") ? failure === "pending" ? new Promise(() => {}) : { error: {} } : {});
 render(<Panel />); await screen.findByRole("tab", { name: "Side" });
 expect(screen.queryByText("No side chat opened.")).not.toBeInTheDocument();
 expect(screen.getByRole("tab", { name: "Side" })).toHaveAttribute("aria-selected", "true");
 expect(screen.getByText(/temporarily read-only/)).toBeInTheDocument();
 expect(screen.queryByLabelText("Side chat question")).not.toBeInTheDocument();
});

it.each(["failed", "pending"])("keeps recovered existing rows readonly while initial close-state hydration is %s", async failure => {
 if (failure === "failed") getState.mockRejectedValue(new Error("IPC unavailable")); else getState.mockReturnValue(new Promise(() => {}));
 const hook = await setup(); await act(async () => hook.result.current.send("side", "blocked"));
 expect(post).not.toHaveBeenCalled(); expect(remove).not.toHaveBeenCalled();
});

it.each(["unconfirmed", "cleanup_pending"])("does not restore interaction for an authoritative %s result", async status => {
 const hook = await setup(); requestClose.mockImplementationOnce(async (request: SideCloseRequest) => state = summary([entry(status as SideCloseEntry["status"], 2, "side", request.requestId)], 2));
 await act(async () => { await expect(hook.result.current.close("side")).rejects.toThrow(); });
 await act(async () => hook.result.current.send("side", "blocked"));
 expect(post).not.toHaveBeenCalled(); expect(hook.result.current.sides).toHaveLength(1);
});

it("restores interaction only for main's rejected-open classification", async () => {
 const hook = await setup(); requestClose.mockImplementationOnce(async (request: SideCloseRequest) => state = summary([entry("rejected_open", 2, "side", request.requestId)], 2));
 await act(async () => { await expect(hook.result.current.close("side")).rejects.toThrow(); });
 await act(async () => hook.result.current.send("side", "usable again")); expect(post).toHaveBeenCalledOnce();
 expect(remove).not.toHaveBeenCalled();
});

it.each(["recover", "cancel"])("ignores a late %s failure after confirmed removal", async operation => {
 const normal = get.getMockImplementation()!;
 get.mockImplementation((path: string, ...args: unknown[]) => path.endsWith("/{sideId}") ? { data: { snapshot: snapshot("side", operation === "recover" ? "failed" : "ready", operation === "cancel") } } : normal(path, ...args));
 const pending = deferred<object>(); post.mockReturnValueOnce(pending.promise);
 render(<Panel />); await userEvent.click(await screen.findByRole("button", { name: operation === "recover" ? "Retry connection" : "Delete queued message" }));
 await deleteFromPanel(); await screen.findByText("No side chat opened.");
 await act(async () => pending.resolve({ error: { code: "CHAT_SIDE_CLOSED" } }));
 expect(screen.getByTestId("active-error")).toBeEmptyDOMElement();
});

it("clears the deleted side's queued editor while preserving the neighbor", async () => {
 rows = [row(), row("Other")]; const normal = get.getMockImplementation()!;
 get.mockImplementation((path: string, options: { params?: { path?: { sideId?: string } } }) => path.endsWith("/{sideId}") ? { data: { snapshot: snapshot(options.params?.path?.sideId, "ready", true) } } : normal(path, options));
 render(<Panel showId="side" />); await userEvent.click(await screen.findByRole("button", { name: "Edit queued message" }));
 expect(screen.getByLabelText("Edit queued side question")).toHaveValue("side queued text");
 await deleteFromPanel(); await waitFor(() => expect(screen.getByRole("tab", { name: "Other" })).toHaveAttribute("aria-selected", "true"));
 expect(screen.queryByLabelText("Edit queued side question")).not.toBeInTheDocument();
});

it("dispatches the authorized main close before a view can disable and recovers its eventual proof", async () => {
 const reply = deferred<SideCloseState>(); requestClose.mockReturnValueOnce(reply.promise);
 const hook = renderHook(({ enabled }) => useIndependentSideChats("session", [], [], async () => [], false, enabled), { initialProps: { enabled: true } });
 await waitFor(() => expect(hook.result.current.activeId).toBe("side"));
 let closing!: Promise<void>; act(() => { closing = hook.result.current.close("side"); });
 await waitFor(() => expect(requestClose).toHaveBeenCalledOnce()); hook.rerender({ enabled: false });
 state = summary([entry("closed", 2)], 2); await act(async () => { reply.resolve(state); await closing; });
 hook.rerender({ enabled: true }); await waitFor(() => expect(getState.mock.calls.length).toBeGreaterThan(1));
 expect(hook.result.current.sides).toEqual([]); expect(remove).not.toHaveBeenCalled();
});

it("keeps duplicate confirmation busy with one main operation and an accessible status", async () => {
 const reply = deferred<SideCloseState>(); requestClose.mockReturnValueOnce(reply.promise);
 render(<Panel />); await screen.findByLabelText("Side chat question"); await deleteFromPanel();
 expect(screen.getByRole("dialog")).toHaveAttribute("aria-busy", "true");
 expect(screen.getByRole("status", { name: "" })).toHaveTextContent("Deleting side chat");
 expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
 expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
 fireEvent.click(screen.getByRole("button", { name: "Delete" })); expect(requestClose).toHaveBeenCalledOnce();
 await act(async () => reply.resolve(summary([entry("closed", 2)], 2)));
 await screen.findByText("No side chat opened."); await waitFor(() => expect(screen.getByRole("button", { name: "New side chat" })).toHaveFocus());
 expect(screen.queryByRole("button", { name: "Retry close" })).not.toBeInTheDocument();
});

it.each(["response", "network", "pending"])("applies shutdown proof independently of a %s list outage", async failure => {
 const hook = await setup(); const normal = get.getMockImplementation()!;
 get.mockImplementation((path: string, ...args: unknown[]) => path.endsWith("/side-chats") ? failure === "network" ? Promise.reject(new Error("Network lost")) : failure === "pending" ? new Promise(() => {}) : { error: {} } : normal(path, ...args));
 await publish(summary([entry("closed", 2)], 2));
 expect(hook.result.current.sides).toEqual([]); expect(hook.result.current.activeId).toBeUndefined();
 if (failure !== "pending") expect(hook.result.current.error).toBe("Operation failed");
});

it("keeps cached ready knowledge through a transient status read failure", async () => {
 const hook = await setup(); getState.mockRejectedValue(new Error("Transient IPC read failure"));
 vi.useFakeTimers(); await act(async () => vi.advanceTimersByTimeAsync(2001));
 await act(async () => hook.result.current.send("side", "still open")); expect(post).toHaveBeenCalledOnce();
});

it("allows a claimed restoring side to request close without enabling other mutations", async () => {
 state = summary([], 1, "restoring"); const hook = await setup();
 await act(async () => hook.result.current.send("side", "blocked")); expect(post).not.toHaveBeenCalled();
 await act(async () => hook.result.current.close("side")); expect(requestClose).toHaveBeenCalledOnce();
});

it("preserves a newly created selected tab and draft while another operation finishes", async () => {
 const hook = await setup(); const reply = deferred<SideCloseState>(); requestClose.mockReturnValueOnce(reply.promise);
 let closing!: Promise<void>; act(() => { closing = hook.result.current.close("side"); });
 await waitFor(() => expect(requestClose).toHaveBeenCalled());
 post.mockResolvedValueOnce({ data: { side: row("New") } }); await act(async () => hook.result.current.create(undefined, true));
 act(() => hook.result.current.setQuestionDraft("New", "Keep this draft"));
 await act(async () => { reply.resolve(summary([entry("closed", 2)], 2)); await closing; });
 expect(hook.result.current.activeId).toBe("New"); expect(hook.result.current.sides.map(side => side.id)).toEqual(["New"]);
 await act(async () => hook.result.current.send("New", "Keep this draft")); expect(post).toHaveBeenLastCalledWith(expect.stringContaining("/messages"), expect.objectContaining({ body: expect.objectContaining({ text: "Keep this draft" }) }));
});

it.each(["middle", "last"])("selects the correct neighbor after removing active %s", async target => {
 rows = [row("first"), row("middle"), row("last")]; const hook = renderHook(() => useIndependentSideChats("session", [], [], async () => [], false));
 await waitFor(() => expect(hook.result.current.activeId).toBe("last")); act(() => hook.result.current.show(target));
 await act(async () => hook.result.current.close(target)); expect(hook.result.current.activeId).toBe(target === "middle" ? "last" : "middle");
});

it("ignores old list results issued before a close mutation", async () => {
 const hook = await setup(); const oldList = deferred<object>(); const normal = get.getMockImplementation()!;
 get.mockImplementationOnce(() => oldList.promise);
 vi.useFakeTimers(); await act(async () => vi.advanceTimersByTimeAsync(2001));
 await act(async () => hook.result.current.close("side"));
 await act(async () => oldList.resolve({ data: { sides: [row()] } })); expect(hook.result.current.sides).toEqual([]);
 get.mockImplementation(normal);
});

it.each(["session", "daemon"])("fences old close replies after a %s replacement", async replacement => {
 const hook = renderHook(({ id }) => useIndependentSideChats(id, [], [], async () => [], false), { initialProps: { id: "session" } });
 await waitFor(() => expect(hook.result.current.activeId).toBe("side")); const reply = deferred<SideCloseState>(); requestClose.mockReturnValueOnce(reply.promise);
 let closing!: Promise<void>; act(() => { closing = hook.result.current.close("side"); }); await waitFor(() => expect(requestClose).toHaveBeenCalled());
 if (replacement === "session") { rows = [row("Other", "replacement")]; hook.rerender({ id: "replacement" }); await waitFor(() => expect(hook.result.current.activeId).toBe("Other")); }
 else await act(async () => { for (const listener of daemonListeners) listener({ state: "ready", pid: 2, port: 3002 }); });
 await act(async () => { reply.resolve(summary([entry("closed", 2)], 2)); await closing; });
 expect(hook.result.current.sides).toHaveLength(1);
});

function history() {
 const normal = get.getMockImplementation()!;
 get.mockImplementation((path: string, options: { params?: { path?: { sideId?: string }; query?: { before?: string } } }) => {
  if (!path.endsWith("/{sideId}")) return normal(path, options);
  const id = options.params?.path?.sideId ?? "side"; const older = Boolean(options.params?.query?.before); const turn = older ? "old" : "latest";
  const createdAt = older ? "2026-01-01T00:00:00Z" : "2026-01-02T00:00:00Z";
  return { data: { snapshot: { ...snapshot(id), hasMore: !older, turns: [{ id: turn, state: "completed", createdAt }], messages: [{ id: turn, turnId: turn, role: "assistant", text: older ? "Loaded older history" : "Latest history", sequence: older ? 1 : 2, revision: 1, createdAt }] } } };
 });
}

it.each(["inactive", "rejected"])("preserves loaded older history during %s close", async mode => {
 rows = [row(), row("Other")]; history();
 requestClose.mockImplementationOnce(async (request: SideCloseRequest) => summary([entry(mode === "rejected" ? "rejected_open" : "closed", 2, request.sideId, request.requestId)], 2));
 render(<Panel showId="side" />); await screen.findByText("Latest history"); await userEvent.click(screen.getByRole("button", { name: "Load earlier messages" })); await screen.findByText("Loaded older history");
 await deleteFromPanel(mode === "inactive" ? "Other" : "Side"); await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
 expect(screen.getByText("Loaded older history")).toBeInTheDocument();
});

it("ignores late pagination for the closing target and allows it again after explicit open recovery", async () => {
 history(); const normal = get.getMockImplementation()!; const older = deferred<object>();
 get.mockImplementation((path: string, options: { params?: { query?: { before?: string } } }) => options?.params?.query?.before ? older.promise : normal(path, options));
 const reply = deferred<SideCloseState>(); requestClose.mockReturnValueOnce(reply.promise);
 render(<Panel />); await screen.findByText("Latest history"); await userEvent.click(screen.getByRole("button", { name: "Load earlier messages" })); await deleteFromPanel();
 await act(async () => older.resolve({ data: { snapshot: { ...snapshot(), turns: [], messages: [{ id: "late", role: "assistant", text: "Late page" }] } } }));
 expect(screen.queryByText("Late page")).not.toBeInTheDocument();
 await act(async () => reply.resolve(summary([entry("rejected_open", 2)], 2))); await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
 expect(screen.getByRole("button", { name: "Load earlier messages" })).toBeEnabled();
});

it("ignores a late mutation even after closing and rejected-open phases restore permission", async () => {
 const normal = get.getMockImplementation()!;
 get.mockImplementation((path: string, ...args: unknown[]) => path.endsWith("/{sideId}") ? { data: { snapshot: snapshot("side", "failed") } } : normal(path, ...args));
 const pending = deferred<object>(); post.mockReturnValueOnce(pending.promise);
 render(<Panel />); await userEvent.click(await screen.findByRole("button", { name: "Retry connection" }));
 await publish(summary([entry("closing", 1)], 1)); await publish(summary([entry("rejected_open", 2)], 2));
 await act(async () => pending.resolve({ error: {} })); expect(screen.getByTestId("active-error")).toBeEmptyDOMElement();
});

it("does not let an older same-side completion dismiss a later busy dialog", async () => {
 const first = deferred<SideCloseState>(); const second = deferred<SideCloseState>();
 requestClose.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
 render(<Panel />); await screen.findByLabelText("Side chat question"); vi.useFakeTimers();
 fireEvent.click(screen.getByRole("button", { name: "Close Side" })); fireEvent.click(screen.getByRole("button", { name: "Delete" }));
 await act(async () => vi.advanceTimersByTimeAsync(SIDE_CLOSE_OPERATION_TIMEOUT_MS + 5001)); vi.useRealTimers();
 await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
 await deleteFromPanel(); expect(requestClose).toHaveBeenCalledTimes(2);
 await publish(summary([entry("closing", 3)], 3));
 await act(async () => first.resolve(summary([entry("rejected_open", 2)], 2)));
 expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
 await act(async () => second.resolve(summary([entry("closed", 4)], 4))); await screen.findByText("No side chat opened.");
});

it("allows a verified rejected-open side during restore, then revokes its proof on replacement", async () => {
 state = summary([entry("rejected_open", 2)], 2, "restoring"); const hook = await setup();
 await act(async () => hook.result.current.send("side", "verified open")); expect(post).toHaveBeenCalledOnce();
 state = summary([entry("unconfirmed", 3)], 3, "restoring", "replacement-daemon");
 await act(async () => { for (const listener of daemonListeners) listener({ state: "ready", pid: 2, port: 3002 }); });
 await waitFor(() => expect(getState.mock.calls.length).toBeGreaterThan(1));
 await act(async () => hook.result.current.send("side", "blocked on replacement")); expect(post).toHaveBeenCalledOnce();
});

it("keeps mutation errors owned by their side when another side reads successfully", async () => {
 rows = [row(), row("Other")]; const normal = get.getMockImplementation()!;
 get.mockImplementation((path: string, options: { params?: { path?: { sideId?: string } } }) => path.endsWith("/{sideId}") ? { data: { snapshot: snapshot(options.params?.path?.sideId, "failed") } } : normal(path, options));
 post.mockResolvedValueOnce({ error: {} }); render(<Panel showId="side" />);
 await userEvent.click(await screen.findByRole("button", { name: "Retry connection" })); expect(screen.getByTestId("active-error")).toHaveTextContent("Operation failed");
 await userEvent.click(screen.getByRole("tab", { name: "Other" })); expect(screen.getByTestId("active-error")).toBeEmptyDOMElement();
 await userEvent.click(screen.getByRole("tab", { name: "Side" })); await screen.findByRole("button", { name: "Retry connection" });
 expect(screen.getByTestId("active-error")).toHaveTextContent("Operation failed");
});

it("reports a rejected late destination send to its caller without changing the closed side", async () => {
 const hook = await setup(); const pending = deferred<object>(); post.mockReturnValueOnce(pending.promise);
 let sending!: Promise<void>; act(() => { sending = hook.result.current.send("side", "handoff", [], [], true); });
 const failure = expect(sending).rejects.toEqual({ code: "CHAT_SIDE_CLOSED" });
 await waitFor(() => expect(post).toHaveBeenCalledOnce()); await publish(summary([entry("closed", 2)], 2));
 await act(async () => { pending.resolve({ error: { code: "CHAT_SIDE_CLOSED" } }); await failure; });
 expect(hook.result.current.sides).toEqual([]); expect(hook.result.current.error).toBeUndefined();
});

it("fences a stale replaced draft failure after closure", async () => {
 const hook = await setup(); const saved = deferred<object>(); put.mockReturnValueOnce(saved.promise);
 let replacing!: Promise<void>; act(() => { replacing = hook.result.current.replaceDraft("side", { version: 1, text: "replacement", attachments: [], references: [] }); });
 await waitFor(() => expect(put).toHaveBeenCalled()); await publish(summary([entry("closed", 2)], 2));
 await act(async () => { saved.resolve({ error: new Error("Late save failure") }); await expect(replacing).resolves.toBeUndefined(); });
 expect(hook.result.current.sides).toEqual([]); expect(hook.result.current.error).toBeUndefined();
});

it("keeps an accepted close busy during restore until main publishes a terminal outcome", async () => {
 state = summary([], 1, "restoring"); const reply = deferred<SideCloseState>(); requestClose.mockReturnValueOnce(reply.promise);
 const hook = await setup(); let finished = false; let closing!: Promise<void>;
 act(() => { closing = hook.result.current.close("side").finally(() => { finished = true; }); });
 await waitFor(() => expect(requestClose).toHaveBeenCalledOnce());
 await publish(summary([entry("closing", 1)], 2, "restoring")); expect(finished).toBe(false);
 await act(async () => reply.resolve(summary([entry("closing", 1)], 2, "restoring"))); expect(finished).toBe(false);
 await publish(summary([entry("closed", 2)], 3, "restoring")); await act(async () => closing);
 expect(finished).toBe(true); expect(hook.result.current.sides).toEqual([]);
});

it.each(["failed", "pending"])("does not describe an ordinary %s bootstrap as a shutdown failure", async failure => {
 if (failure === "failed") getState.mockRejectedValue(new Error("IPC unavailable")); else getState.mockReturnValue(new Promise(() => {}));
 render(<Panel />); await screen.findByRole("tab", { name: "Side" });
 expect(screen.queryByText(/shutdown could not be confirmed/i)).not.toBeInTheDocument();
 expect(screen.queryByText(/temporarily read-only/i)).not.toBeInTheDocument();
 expect(screen.queryByLabelText("Side chat question")).not.toBeInTheDocument();
});

it("keeps confirmation busy and prevents duplicate dispatch while initial hydration waits", async () => {
 const hydration = deferred<SideCloseState>(); getState.mockReturnValue(hydration.promise);
 render(<Panel />); await screen.findByRole("tab", { name: "Side" }); await deleteFromPanel();
 expect(screen.getByRole("dialog")).toHaveAttribute("aria-busy", "true");
 fireEvent.click(screen.getByRole("button", { name: "Delete" })); expect(requestClose).not.toHaveBeenCalled();
 await act(async () => hydration.resolve(summary())); await screen.findByText("No side chat opened.");
 expect(requestClose).toHaveBeenCalledOnce();
});

it("does not dispatch again when pending hydration proves the requested side already closed", async () => {
 const hydration = deferred<SideCloseState>(); getState.mockReturnValue(hydration.promise);
 render(<Panel />); await screen.findByRole("tab", { name: "Side" }); await deleteFromPanel();
 await act(async () => hydration.resolve(summary([entry("closed", 2)], 2))); await screen.findByText("No side chat opened.");
 expect(requestClose).not.toHaveBeenCalled(); await publish(summary([entry("closed", 2)], 3));
 expect(screen.queryByRole("tab", { name: "Side" })).not.toBeInTheDocument();
});

it.each(["failed", "pending"])("updates a recovered placeholder title when authority learns it during a %s list outage", async failure => {
 state = summary([{ ...entry("unconfirmed"), label: "" }], 1);
 get.mockImplementation((path: string) => path.endsWith("/side-chats") ? failure === "pending" ? new Promise(() => {}) : { error: {} } : {});
 const hook = renderHook(() => useIndependentSideChats("session", [], [], async () => [], false));
 await waitFor(() => expect(hook.result.current.activeId).toBe("side")); expect(hook.result.current.sides[0].label).toBe("");
 await publish(summary([{ ...entry("unconfirmed", 1), label: "Recovered title" }], 2));
 expect(hook.result.current.sides[0].label).toBe("Recovered title"); expect(hook.result.current.activeId).toBe("side");
 await publish(summary([{ ...entry("unconfirmed", 1), label: "" }], 3));
 expect(hook.result.current.sides[0].label).toBe("Recovered title");
});

it("does not replace an HTTP row title with an older recovery label", async () => {
 const hook = await setup(); rows = [{ ...row(), label: "Renamed in chat list" }];
 await publish(summary([{ ...entry("unconfirmed", 1), label: "Old recovery title" }], 1));
 await waitFor(() => expect(hook.result.current.sides[0].label).toBe("Renamed in chat list"));
 await publish(summary([{ ...entry("unconfirmed", 2), label: "Another recovery title" }], 2));
 expect(hook.result.current.sides[0].label).toBe("Renamed in chat list"); expect(hook.result.current.activeId).toBe("side");
});

it.each(["failed", "pending"])("shows one side-owned warning when Delete cannot hydrate %s status", async failure => {
 rows = [row(), row("Other")];
 if (failure === "failed") getState.mockRejectedValue(new Error("IPC unavailable")); else getState.mockReturnValue(new Promise(() => {}));
 render(<Panel showId="side" />); await screen.findByRole("tab", { name: "Side" });
 if (failure === "pending") {
  vi.useFakeTimers(); fireEvent.click(screen.getByRole("button", { name: "Close Side" })); fireEvent.click(screen.getByRole("button", { name: "Delete" }));
  await act(async () => vi.advanceTimersByTimeAsync(3001)); vi.useRealTimers();
 } else await deleteFromPanel();
 await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
 const warning = screen.getByRole("alert"); expect(warning).toHaveTextContent("Side chat is unavailable"); expect(screen.queryByText(/Could not confirm shutdown/)).not.toBeInTheDocument();
 expect(screen.queryByLabelText("Side chat question")).not.toBeInTheDocument(); expect(requestClose).not.toHaveBeenCalled(); expect(remove).not.toHaveBeenCalled();
 await userEvent.click(screen.getByRole("tab", { name: "Other" })); expect(screen.queryByRole("alert")).not.toBeInTheDocument();
 await userEvent.click(screen.getByRole("tab", { name: "Side" })); expect(screen.getByRole("alert")).toHaveTextContent("Side chat is unavailable");
 getState.mockResolvedValue(summary());
 await screen.findByLabelText("Side chat question", {}, { timeout: 3500 }); expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("clears only the preflight warning when fresh hydration restores a usable side", async () => {
 const hook = await setup(); post.mockResolvedValueOnce({ error: {} });
 await act(async () => hook.result.current.send("side", "rejected question")); expect(hook.result.current.error).toBe("Operation failed");
 getState.mockRejectedValue(new Error("IPC unavailable")); vi.useFakeTimers();
 await act(async () => { for (const listener of daemonListeners) listener({ state: "ready", pid: 2, port: 3002 }); });
 await act(async () => { await expect(hook.result.current.close("side")).rejects.toThrow(); });
 expect(hook.result.current.error).toBe("Side chat is unavailable"); expect(requestClose).not.toHaveBeenCalled();
 getState.mockResolvedValue(summary([], 1, "ready", "replacement-daemon")); await act(async () => vi.advanceTimersByTimeAsync(2001));
 expect(hook.result.current.error).toBe("Operation failed");
});
