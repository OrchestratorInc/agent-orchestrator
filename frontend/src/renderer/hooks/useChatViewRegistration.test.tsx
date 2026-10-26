import { StrictMode, type ReactNode } from "react";
import type { components } from "../../api/schema";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ GET: get, POST: post }) }));
vi.mock("../lib/session-management-telemetry", () => ({ recordDirectWorkerInteraction: vi.fn() }));
import { conversationQueryKey, conversationQueryOptions, toSnapshot, useConversation, useConversationCommands } from "./useConversation";
import { useChatViewRegistration } from "./useChatViewRegistration";
import { patchWorkspaceSession, workspaceQueryKey } from "./useWorkspaceQuery";

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => { resolve = done; });
	return { promise, resolve };
}
function wire(sessionId: string, controller: components["schemas"]["ConversationSnapshotResponse"]["controller"] = "stopped"): components["schemas"]["ConversationSnapshotResponse"] {
	return { conversationId: `conv-${sessionId}`, sessionId, harness: "codex", mode: "chat", controller,
		latestSequence: 1, oldestSequence: 1, hasMoreBefore: false, messages: [], activities: [], turns: [],
		settings: {}, continueTurnId: "", branchedFromEarlierMessage: false, nativeForkAvailableAfterSequence: 0 };
}
const clients: QueryClient[] = [];
function setup(strict = false) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	clients.push(client);
	client.setQueryData(workspaceQueryKey, [{ id: "project", name: "Fixture", path: "/fixture", kind: "single_repo", sessions: [] }]);
	const wrapper = ({ children }: { children: ReactNode }) => (
		<QueryClientProvider client={client}>{strict ? <StrictMode>{children}</StrictMode> : children}</QueryClientProvider>
	);
	return { client, wrapper };
}
function useOpen(sessionId: string, active = true, hostId?: string) {
	const conversation = useConversation(sessionId, hostId);
	useQuery({ queryKey: workspaceQueryKey, staleTime: Infinity, queryFn: async () => { await get("/api/v1/sessions"); return []; } });
	useChatViewRegistration(sessionId, active, hostId);
	return conversation;
}
function requests(path: string) { return get.mock.calls.filter(([url]) => url === path); }
const conversationPath = "/api/v1/sessions/{sessionId}/conversation";
const sessionPath = "/api/v1/sessions/{sessionId}";
beforeEach(() => {
	get.mockReset().mockImplementation(async (path, input) => path === conversationPath
		? { data: wire(input.params.path.sessionId) }
		: path === sessionPath ? { data: { session: { id: input.params.path.sessionId, projectId: "project", mode: "chat", harness: "codex", status: "idle", activity: { state: "idle" } } } }
		: { data: { sessions: [] } });
	post.mockReset().mockResolvedValue({ error: undefined });
});
afterEach(() => { for (const client of clients.splice(0)) client.clear(); vi.useRealTimers(); });

describe("stopped Chat registration request budget", () => {
	it.each([false, true])("opens five chats without registration duplicates (hover prefetch=%s)", async (prefetched) => {
		const measurements = [];
		for (let index = 1; index <= 5; index++) {
			get.mockClear(); post.mockClear();
			const { client, wrapper } = setup();
			const id = `stopped-${index}`;
			if (prefetched) await client.prefetchInfiniteQuery(conversationQueryOptions(id));
			const prefetchedRequests = requests(conversationPath).length;
			const registration = deferred<{ error: undefined }>();
			post.mockImplementationOnce(() => registration.promise);
			const view = renderHook(() => useOpen(id), { wrapper });
			await waitFor(() => expect(view.result.current.snapshot?.controller.state).toBe("stopped"));
			await act(async () => { registration.resolve({ error: undefined }); });
			await waitFor(() => expect(client.isFetching()).toBe(0));
			const conversationRequests = requests(conversationPath).length;
			const listRequests = requests("/api/v1/sessions").length;
			const payloads = await Promise.all(get.mock.results.map((result) => result.value));
			const payloadBytes = payloads.reduce((total, response) => total + new TextEncoder().encode(JSON.stringify(response?.data ?? null)).length, 0);
			measurements.push({ id, conversationRequests, onOpen: conversationRequests - prefetchedRequests, listRequests, payloadBytes });
			view.unmount();
		}
		console.info("stopped-open-request-counts", { prefetched, measurements });
		for (const measured of measurements) {
			expect(measured.conversationRequests).toBe(1);
			expect(measured.onOpen).toBe(prefetched ? 0 : 1);
			expect(measured.listRequests).toBe(0);
		}
	});

	it("retains loaded history pages without refetching any page", async () => {
		const { client, wrapper } = setup();
		const live = { ...toSnapshot(wire("chat")), hasMoreBefore: true, oldestSequence: 10, latestSequence: 20 };
		const older = { ...live, oldestSequence: 1, latestSequence: 9, hasMoreBefore: false };
		const cached = { pages: [live, older], pageParams: [undefined, 10] };
		client.setQueryData(conversationQueryKey("chat"), cached);
		const view = renderHook(() => useOpen("chat"), { wrapper });
		await waitFor(() => expect(requests(sessionPath)).toHaveLength(1));
		expect(requests(conversationPath)).toHaveLength(0);
		expect(client.getQueryData(conversationQueryKey("chat"))).toEqual(cached);
		view.unmount();
	});
});

it("patches only the registered session row without reloading the workspace", async () => {
	const { client, wrapper } = setup();
	const view = renderHook(() => useOpen("chat"), { wrapper });
	await waitFor(() => expect(client.getQueryData(workspaceQueryKey)).toEqual([
		expect.objectContaining({ sessions: [expect.objectContaining({ id: "chat", mode: "chat", status: "idle" })] }),
	]));
	expect(requests(sessionPath)).toHaveLength(1);
	expect(requests("/api/v1/sessions")).toHaveLength(0);
	view.unmount();
});

it("does not reload history or the list when refreshing the row fails", async () => {
	const { client, wrapper } = setup();
	get.mockImplementation(async (path, input) => path === conversationPath ? { data: wire(input.params.path.sessionId) } : { error: { code: "SESSION_NOT_FOUND" } });
	const view = renderHook(() => useOpen("chat"), { wrapper });
	await waitFor(() => expect(view.result.current.snapshot).toBeDefined());
	await waitFor(() => expect(requests(sessionPath)).toHaveLength(1));
	expect(requests(conversationPath)).toHaveLength(1);
	expect(requests("/api/v1/sessions")).toHaveLength(0);
	expect(client.getQueryData(workspaceQueryKey)).toEqual([expect.objectContaining({ sessions: [] })]);
	view.unmount();
});

it("releases immediately and again when an old navigation registration finishes late", async () => {
	const { wrapper } = setup();
	const registration = deferred<{ error: undefined }>();
	post.mockImplementationOnce(() => registration.promise);
	const view = renderHook(({ id }) => useChatViewRegistration(id, true), { wrapper, initialProps: { id: "old" } });
	await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
	const viewId = post.mock.calls[0][1].body.viewId;
	view.rerender({ id: "new" });
	await waitFor(() => expect(post).toHaveBeenCalledWith(expect.any(String), { params: { path: { sessionId: "old" } }, body: { viewId, active: false } }));
	await act(async () => { registration.resolve({ error: undefined }); });
	expect(post.mock.calls.filter(([, input]) => input.params.path.sessionId === "old" && !input.body.active)).toHaveLength(2);
	expect(requests(sessionPath).some(([, input]) => input.params.path.sessionId === "old")).toBe(false);
	view.unmount();
});

it("does not patch a row whose detail response arrives after navigation", async () => {
	const { client, wrapper } = setup();
	const detail = deferred<unknown>();
	get.mockImplementationOnce(() => detail.promise);
	const view = renderHook(() => useChatViewRegistration("old", true), { wrapper });
	await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
	view.unmount();
	await act(async () => { detail.resolve({ data: { session: { id: "old", projectId: "project", mode: "chat" } } }); });
	expect(client.getQueryData(workspaceQueryKey)).toEqual([expect.objectContaining({ sessions: [] })]);
});

it("does not overwrite a newer row with a delayed registration detail response", async () => {
	const { client, wrapper } = setup();
	const detail = deferred<unknown>();
	get.mockImplementationOnce(() => detail.promise);
	const view = renderHook(() => useChatViewRegistration("chat", true), { wrapper });
	await waitFor(() => expect(requests(sessionPath)).toHaveLength(1));
	const row = { id: "chat", projectId: "project", mode: "chat", harness: "codex", status: "idle", activity: { state: "idle" } } as const;
	patchWorkspaceSession(client, { ...row, title: "Newer event" });
	await act(async () => { detail.resolve({ data: { session: { ...row, title: "Stale registration" } } }); });
	expect(client.getQueryData(workspaceQueryKey)).toEqual([
		expect.objectContaining({ sessions: [expect.objectContaining({ id: "chat", title: "Newer event" })] }),
	]);
	expect(requests("/api/v1/sessions")).toHaveLength(0);
	view.unmount();
});

it("renews the same lease without repeatedly reading the session row", async () => {
	vi.useFakeTimers();
	const { wrapper } = setup();
	const view = renderHook(() => useChatViewRegistration("chat", true), { wrapper });
	await act(async () => {});
	await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
	expect(post.mock.calls.filter(([, input]) => input.body.active)).toHaveLength(4);
	expect(new Set(post.mock.calls.map(([, input]) => input.body.viewId)).size).toBe(1);
	expect(requests(sessionPath)).toHaveLength(1);
	view.unmount();
});

it("does not register an inactive tab or perform local row reads for a remote tab", async () => {
	const { wrapper } = setup();
	const view = renderHook(({ active }) => useChatViewRegistration("chat", active, "remote"), { wrapper, initialProps: { active: false } });
	expect(post).not.toHaveBeenCalled();
	view.rerender({ active: true });
	await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
	expect(get).not.toHaveBeenCalled();
	view.unmount();
});


it("keeps permanent conversation error details without registration retrying history", async () => {
	const { wrapper } = setup();
	get.mockImplementation(async (path) => path === conversationPath
		? { error: { code: "SESSION_MODE_MISMATCH", message: "This session is in Terminal UI." } }
		: { data: {} });
	const view = renderHook(() => useOpen("chat"), { wrapper });
	await waitFor(() => expect(view.result.current.unavailable).toEqual({ code: "SESSION_MODE_MISMATCH", message: "This session is in Terminal UI." }));
	await waitFor(() => expect(requests(sessionPath)).toHaveLength(1));
	expect(requests(conversationPath)).toHaveLength(1);
	view.unmount();
});

it("recovers a hibernated snapshot from a change event and shows new rows after send", async () => {
	const { client, wrapper } = setup();
	let snapshot = wire("chat", "hibernated");
	get.mockImplementation(async (path) => path === conversationPath ? { data: snapshot } : { data: {} });
	post.mockImplementation(async (path) => {
		if (path.endsWith("/conversation/messages")) {
			snapshot = { ...wire("chat", "ready"), latestSequence: 2, messages: [{ id: "reply", kind: "message", role: "assistant", origin: "provider", text: "New reply", sequence: 2, createdAt: "2026-10-26T00:00:00Z", editAvailable: false, revision: 1, streaming: false }] };
			return { data: { turnId: "new-turn" } };
		}
		return { error: undefined };
	});
	const view = renderHook(() => ({ conversation: useOpen("chat"), commands: useConversationCommands("chat") }), { wrapper });
	await waitFor(() => expect(view.result.current.conversation.snapshot?.controller.state).toBe("hibernated"));
	snapshot = wire("chat", "ready");
	// The existing event transport owns conversation refresh; registration does not.
	await act(async () => { await client.invalidateQueries({ queryKey: conversationQueryKey("chat") }); });
	await waitFor(() => expect(view.result.current.conversation.snapshot?.controller.state).toBe("ready"));
	await act(async () => { await view.result.current.commands.send("Hello"); });
	await waitFor(() => expect(view.result.current.conversation.snapshot?.items).toContainEqual(expect.objectContaining({ id: "reply", text: "New reply" })));
	view.unmount();
});

it("releases StrictMode registrations and performs one row refresh for the surviving view", async () => {
	const { wrapper } = setup(true);
	const view = renderHook(() => useChatViewRegistration("chat", true), { wrapper });
	await waitFor(() => expect(requests(sessionPath)).toHaveLength(1));
	const activeIds = post.mock.calls.filter(([, input]) => input.body.active).map(([, input]) => input.body.viewId);
	view.unmount();
	for (const viewId of activeIds) {
		expect(post).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ body: { viewId, active: false } }));
	}
});

it("refreshes after a failed lease and can register again when reopened", async () => {
	const { wrapper } = setup();
	const registration = deferred<{ error: { code: string } }>();
	post.mockImplementationOnce(() => registration.promise);
	const view = renderHook(({ active }) => useOpen("chat", active), { wrapper, initialProps: { active: true } });
	await waitFor(() => expect(view.result.current.snapshot?.controller.state).toBe("stopped"));
	await act(async () => { registration.resolve({ error: { code: "CHAT_RESUME_FAILED" } }); });
	await waitFor(() => expect(requests(conversationPath)).toHaveLength(2));
	expect(requests("/api/v1/sessions")).toHaveLength(0);
	view.rerender({ active: false });
	view.rerender({ active: true });
	await waitFor(() => expect(post.mock.calls.filter(([, input]) => input.body.active)).toHaveLength(2));
	await waitFor(() => expect(requests(sessionPath)).toHaveLength(1));
	view.unmount();
});

it.each(["unloaded", "missing-project"])("does not fall back to a list fetch when the workspace is %s", async (state) => {
	const { client, wrapper } = setup();
	if (state === "unloaded") client.removeQueries({ queryKey: workspaceQueryKey });
	else client.setQueryData(workspaceQueryKey, [{ id: "other-project", name: "Other", sessions: [] }]);
	const before = client.getQueryData(workspaceQueryKey);
	const view = renderHook(() => useChatViewRegistration("chat", true), { wrapper });
	await waitFor(() => expect(requests(sessionPath)).toHaveLength(1));
	expect(requests("/api/v1/sessions")).toHaveLength(0);
	expect(client.getQueryData(workspaceQueryKey)).toEqual(before);
	view.unmount();
});


it("refreshes a hibernated snapshot again when the clear-marker event precedes a failed wake", async () => {
	const { client, wrapper } = setup();
	let snapshot = wire("chat", "hibernated");
	const registration = deferred<{ error: { code: string } }>();
	post.mockImplementationOnce(() => registration.promise);
	get.mockImplementation(async (path) => path === conversationPath ? { data: snapshot } : { data: {} });
	const view = renderHook(() => useOpen("chat"), { wrapper });
	await waitFor(() => expect(view.result.current.snapshot?.controller.state).toBe("hibernated"));
	// Clearing HibernatedAt emits CDC before native resume finishes. Its read
	// still sees isWaking and caches hibernated with staleTime: Infinity.
	await act(async () => { await client.invalidateQueries({ queryKey: conversationQueryKey("chat") }); });
	expect(requests(conversationPath)).toHaveLength(2);
	expect(view.result.current.snapshot?.controller.state).toBe("hibernated");
	// No completion event follows the failed native resume.
	snapshot = wire("chat", "stopped");
	await act(async () => { registration.resolve({ error: { code: "CHAT_RESUME_FAILED" } }); });
	await waitFor(() => expect(view.result.current.snapshot?.controller.state).toBe("stopped"));
	expect(requests(conversationPath)).toHaveLength(3);
	expect(requests("/api/v1/sessions")).toHaveLength(0);
	view.unmount();
});

it.each(["left", "remote"])("does not refresh local history after a failed wake for a %s view", async (state) => {
	const { client, wrapper } = setup();
	const registration = deferred<{ error: { code: string } }>();
	post.mockImplementationOnce(() => registration.promise);
	const invalidate = vi.spyOn(client, "invalidateQueries");
	const view = renderHook(() => useChatViewRegistration("chat", true, state === "remote" ? "remote" : undefined), { wrapper });
	await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
	if (state === "left") view.unmount();
	await act(async () => { registration.resolve({ error: { code: "CHAT_RESUME_FAILED" } }); });
	expect(invalidate).not.toHaveBeenCalled();
	if (state === "remote") view.unmount();
});

it("refreshes the conversation after a renewal failure without a completion event", async () => {
	const { wrapper } = setup();
	let snapshot = wire("chat", "ready");
	const renewal = deferred<{ error: { code: string } }>();
	post.mockResolvedValueOnce({ error: undefined }).mockImplementationOnce(() => renewal.promise);
	get.mockImplementation(async (path) => path === conversationPath ? { data: snapshot } : { data: {} });
	const intervals = vi.spyOn(window, "setInterval");
	const view = renderHook(() => useOpen("chat"), { wrapper });
	try {
		await waitFor(() => expect(view.result.current.snapshot?.controller.state).toBe("ready"));
		expect(requests(conversationPath)).toHaveLength(1);
		const renew = intervals.mock.calls.find(([, delay]) => delay === 10_000)?.[0] as () => void;
		expect(renew).toBeTypeOf("function");
		act(() => { renew(); });
		await waitFor(() => expect(post.mock.calls.filter(([, input]) => input.body.active)).toHaveLength(2));
		snapshot = wire("chat", "stopped");
		await act(async () => { renewal.resolve({ error: { code: "CHAT_RESUME_FAILED" } }); });
		await waitFor(() => expect(view.result.current.snapshot?.controller.state).toBe("stopped"));
		expect(requests(conversationPath)).toHaveLength(2);
		expect(requests("/api/v1/sessions")).toHaveLength(0);
	} finally {
		view.unmount();
		intervals.mockRestore();
	}
});
