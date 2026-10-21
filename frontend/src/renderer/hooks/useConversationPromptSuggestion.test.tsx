import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { chatFixtureEmpty } from "../lib/chat-fixture";
import type { ConversationMessage, ConversationSnapshot } from "../types/conversation";

const { clientForSessionHost, post } = vi.hoisted(() => ({ clientForSessionHost: vi.fn(), post: vi.fn() }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost }));
import { useConversationPromptSuggestion } from "./useConversationPromptSuggestion";

const settled: ConversationSnapshot = {
	...chatFixtureEmpty,
	conversationId: "conversation-1",
	sessionId: "session-1",
	activeBranchId: "branch-1",
	controller: { state: "ready" },
	turns: [{ id: "turn-1", state: "completed", requestedAt: "2026-10-12T00:00:00Z" }],
};

function wrapperFor(client = new QueryClient()) {
	return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

beforeEach(() => {
	clientForSessionHost.mockReset().mockReturnValue({ POST: post });
	post.mockReset().mockResolvedValue({ data: { turnId: "turn-1", prompt: "  Explain the fix.  " } });
});

describe("useConversationPromptSuggestion", () => {
	it("withdraws a cached suggestion when its source answer changes, but reuses unchanged replay", async () => {
		const message: ConversationMessage = {
			kind: "message", id: "reply-1", role: "assistant", origin: "provider", turnId: "turn-1",
			sequence: 1, revision: 1, text: "Fixed the race.", streaming: false, createdAt: "",
		};
		const initial: ConversationSnapshot = { ...settled, items: [message] };
		const { result, rerender } = renderHook(({ snapshot }: { snapshot: ConversationSnapshot }) => useConversationPromptSuggestion(snapshot, true), {
			wrapper: wrapperFor(), initialProps: { snapshot: initial },
		});
		await waitFor(() => expect(result.current?.text).toBe("Explain the fix."));
		const firstId = result.current?.id;
		rerender({ snapshot: { ...initial, items: [{ ...message, revision: 2 }, {
			kind: "activity", id: "usage-1", sequence: 2, revision: 1, activityKind: "usage",
			status: "completed", summary: "Updated usage", createdAt: "",
		}] } });
		expect(result.current?.id).toBe(firstId);
		expect(post).toHaveBeenCalledOnce();
		post.mockResolvedValue({ data: { turnId: "turn-1", prompt: "Fix the remaining race." } });
		rerender({ snapshot: { ...initial, items: [{ ...message, revision: 3, text: "The race is still present." }] } });
		expect(result.current).toBeUndefined();
		await waitFor(() => expect(result.current?.text).toBe("Fix the remaining race."));
		expect(result.current?.id).not.toBe(firstId);
		expect(post).toHaveBeenCalledTimes(2);
	});

	it("uses a compact settled-answer version and invalidates a correction outside the old truncation boundary", async () => {
		const reply: ConversationMessage = {
			kind: "message", id: "reply-1", role: "assistant", text: `Original beginning${"😀".repeat(20_000)}`,
			origin: "provider", sequence: 1, revision: 1, streaming: false, createdAt: "",
		};
		const client = new QueryClient();
		const { result, rerender } = renderHook(({ snapshot }) => useConversationPromptSuggestion(snapshot, true), {
			wrapper: wrapperFor(client), initialProps: { snapshot: { ...settled, items: [reply] } },
		});
		await waitFor(() => expect(result.current?.text).toBe("Explain the fix."));
		expect(JSON.stringify(client.getQueryCache().getAll()[0].queryKey).length).toBeLessThan(300);
		expect(client.getQueryCache().getAll()[0].gcTime).toBeLessThan(Infinity);
		const firstId = result.current?.id;
		rerender({ snapshot: { ...settled, items: [{ ...reply, text: `Corrected beginning${"😀".repeat(20_000)}`, revision: 2 }] } });
		expect(result.current).toBeUndefined();
		await waitFor(() => expect(result.current?.text).toBe("Explain the fix."));
		expect(result.current?.id).not.toBe(firstId);
		expect(post).toHaveBeenCalledTimes(2);
	});

	it("does not retain a query per streamed text refresh", () => {
		const client = new QueryClient();
		const running = { ...settled, turns: [{ ...settled.turns[0], state: "running" as const }] };
		const { rerender } = renderHook(({ snapshot }) => useConversationPromptSuggestion(snapshot, true), {
			wrapper: wrapperFor(client), initialProps: { snapshot: running },
		});
		for (let revision = 1; revision <= 100; revision++) {
			rerender({ snapshot: { ...running, items: [{ kind: "message", id: "reply-1", role: "assistant", origin: "provider", text: "Streaming".repeat(revision), sequence: 1, revision, streaming: true, createdAt: "" }] } });
		}
		expect(client.getQueryCache().getAll()).toHaveLength(1);
		expect(client.getQueryCache().getAll()[0].gcTime).toBeLessThan(Infinity);
		expect(post).not.toHaveBeenCalled();
	});

	it("requests once per settled context and reuses it when the chat returns", async () => {
		const wrapper = wrapperFor();
		const first = renderHook(() => useConversationPromptSuggestion(settled, true), { wrapper });
		await waitFor(() => expect(first.result.current?.text).toBe("Explain the fix."));
		expect(post).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/conversation/prompt-suggestion", {
			params: { path: { sessionId: "session-1" } }, body: { turnId: "turn-1" }, signal: expect.any(AbortSignal),
		});
		first.rerender();
		first.unmount();
		const second = renderHook(() => useConversationPromptSuggestion(settled, true), { wrapper });
		expect(second.result.current?.text).toBe("Explain the fix.");
		expect(post).toHaveBeenCalledTimes(1);
	});

	it.each<ConversationSnapshot>([
		{ ...settled, turns: [] },
		{ ...settled, controller: { state: "busy" } },
		{ ...settled, mode: "tui" },
		{ ...settled, turns: [...settled.turns, { id: "turn-2", state: "failed", requestedAt: "" }] },
		{ ...settled, turns: [...settled.turns, { id: "turn-2", state: "queued", requestedAt: "" }] },
		{ ...settled, turns: [...settled.turns, { id: "turn-2", state: "running", requestedAt: "" }] },
		{ ...settled, turns: [{ ...settled.turns[0], rolledBack: true }] },
		{ ...settled, items: [{ kind: "message", id: "reply-1", role: "assistant", origin: "provider", turnId: "turn-1", sequence: 1, revision: 1, text: "Working", streaming: true, createdAt: "" }] },
	])("omits suggestions before a settled active conversation is available %#", (snapshot) => {
		const { result } = renderHook(() => useConversationPromptSuggestion(snapshot, true), { wrapper: wrapperFor() });
		expect(result.current).toBeUndefined();
		expect(post).not.toHaveBeenCalled();
	});

	it("selects the latest visible turn after rollback", async () => {
		const snapshot: ConversationSnapshot = {
			...settled,
			turns: [...settled.turns, { id: "discarded", state: "completed", rolledBack: true, requestedAt: "" }],
		};
		const { result } = renderHook(() => useConversationPromptSuggestion(snapshot, true), { wrapper: wrapperFor() });
		await waitFor(() => expect(result.current?.text).toBe("Explain the fix."));
		expect(post.mock.calls[0][1].body).toEqual({ turnId: "turn-1" });
	});

	it("does not start while hidden but preserves an in-flight suggestion when temporarily hidden", async () => {
		let signal: AbortSignal | undefined;
		let resolve!: (value: unknown) => void;
		post.mockImplementation((_path, options: { signal: AbortSignal }) => {
			signal = options.signal;
			return new Promise((complete) => { resolve = complete; });
		});
		const { result, rerender } = renderHook(({ enabled }) => useConversationPromptSuggestion(settled, enabled), {
			wrapper: wrapperFor(), initialProps: { enabled: false },
		});
		expect(post).not.toHaveBeenCalled();
		rerender({ enabled: true });
		await waitFor(() => expect(post).toHaveBeenCalledOnce());
		rerender({ enabled: false });
		expect(signal?.aborted).toBe(false);
		expect(result.current).toBeUndefined();
		await act(async () => resolve({ data: { turnId: "turn-1", prompt: "Explain the fix." } }));
		rerender({ enabled: true });
		await waitFor(() => expect(result.current?.text).toBe("Explain the fix."));
		expect(post).toHaveBeenCalledOnce();
	});

	it("keeps dismissal across remount and query eviction until another reply", async () => {
		const client = new QueryClient();
		const wrapper = wrapperFor(client);
		const first = renderHook(() => useConversationPromptSuggestion(settled, true), { wrapper });
		await waitFor(() => expect(first.result.current?.text).toBe("Explain the fix."));
		act(() => first.result.current?.onDismiss());
		await waitFor(() => expect(first.result.current).toBeUndefined());
		first.unmount();
		client.removeQueries({ queryKey: ["conversation-prompt-suggestion"] });
		const second = renderHook(({ snapshot }) => useConversationPromptSuggestion(snapshot, true), { wrapper, initialProps: { snapshot: settled } });
		expect(second.result.current).toBeUndefined();
		expect(post).toHaveBeenCalledOnce();
		post.mockResolvedValue({ data: { turnId: "turn-2", prompt: "Review the new reply." } });
		second.rerender({ snapshot: { ...settled, turns: [...settled.turns, { id: "turn-2", state: "completed", requestedAt: "" }] } });
		await waitFor(() => expect(second.result.current?.text).toBe("Review the new reply."));
		expect(post).toHaveBeenCalledTimes(2);
	});

	it("retries only typed busy responses twice and does not cache them as an empty success", async () => {
		post.mockResolvedValue({ error: { code: "CHAT_PROMPT_SUGGESTION_BUSY" } });
		const client = new QueryClient();
		const wrapper = wrapperFor(client);
		const first = renderHook(() => useConversationPromptSuggestion(settled, true), { wrapper });
		await waitFor(() => expect(post).toHaveBeenCalledTimes(3), { timeout: 3500 });
		await waitFor(() => expect(client.isFetching()).toBe(0));
		expect(first.result.current).toBeUndefined();
		expect(client.getQueryCache().getAll()[0].state.status).toBe("error");
		first.unmount();
		post.mockResolvedValue({ data: { turnId: "turn-1", prompt: "Retry succeeded." } });
		const second = renderHook(() => useConversationPromptSuggestion(settled, true), { wrapper });
		await waitFor(() => expect(second.result.current?.text).toBe("Retry succeeded."));
		expect(post).toHaveBeenCalledTimes(4);
	});

	it("cancels an obsolete turn and uses the owning remote daemon", async () => {
		let resolveFirst!: (value: unknown) => void;
		let firstSignal: AbortSignal | undefined;
		post.mockImplementationOnce((_path, options: { signal: AbortSignal }) => {
			firstSignal = options.signal;
			return new Promise((resolve) => { resolveFirst = resolve; });
		}).mockResolvedValue({ data: { turnId: "turn-2", prompt: "Review the tests." } });
		const { result, rerender } = renderHook(({ snapshot }) => useConversationPromptSuggestion(snapshot, true, "host-2"), {
			wrapper: wrapperFor(), initialProps: { snapshot: settled },
		});
		await waitFor(() => expect(post).toHaveBeenCalledOnce());
		rerender({ snapshot: { ...settled, turns: [...settled.turns, { id: "turn-2", state: "completed", requestedAt: "" }] } });
		expect(firstSignal?.aborted).toBe(true);
		await waitFor(() => expect(result.current?.text).toBe("Review the tests."));
		await act(async () => resolveFirst({ data: { turnId: "turn-1", prompt: "Obsolete." } }));
		expect(result.current?.text).toBe("Review the tests.");
		expect(clientForSessionHost).toHaveBeenCalledWith("host-2");
	});

	it("keeps sessions, conversations, branches, and hosts in separate suggestion contexts", async () => {
		const { result, rerender } = renderHook(({ snapshot, hostId }) => useConversationPromptSuggestion(snapshot, true, hostId), {
			wrapper: wrapperFor(), initialProps: { snapshot: settled, hostId: "host-1" },
		});
		await waitFor(() => expect(result.current?.text).toBe("Explain the fix."));
		const ids = new Set([result.current?.id]);
		for (const props of [
			{ snapshot: { ...settled, sessionId: "session-2" }, hostId: "host-1" },
			{ snapshot: { ...settled, conversationId: "conversation-2" }, hostId: "host-1" },
			{ snapshot: { ...settled, activeBranchId: "branch-2" }, hostId: "host-1" },
			{ snapshot: settled, hostId: "host-2" },
		]) {
			rerender(props);
			expect(result.current).toBeUndefined();
			await waitFor(() => expect(result.current?.text).toBe("Explain the fix."));
			ids.add(result.current?.id);
		}
		expect(post).toHaveBeenCalledTimes(5);
		expect(ids.size).toBe(5);
	});

	it.each([
		{ error: { message: "No model configured" } },
		{ error: { code: "RATE_LIMITED" }, response: { status: 429 } },
		{ data: { turnId: "turn-1", prompt: " " } },
		{ data: { turnId: "other-turn", prompt: "Wrong context." } },
		new Error("Disconnected"),
	])("quietly caches failures or empty suggestions without retries %#", async (response) => {
		if (response instanceof Error) post.mockRejectedValue(response);
		else post.mockResolvedValue(response);
		const client = new QueryClient();
		const wrapper = wrapperFor(client);
		const first = renderHook(() => useConversationPromptSuggestion(settled, true), { wrapper });
		await waitFor(() => expect(client.isFetching()).toBe(0));
		expect(first.result.current).toBeUndefined();
		first.unmount();
		const second = renderHook(() => useConversationPromptSuggestion(settled, true), { wrapper });
		expect(second.result.current).toBeUndefined();
		expect(post).toHaveBeenCalledOnce();
	});
});
