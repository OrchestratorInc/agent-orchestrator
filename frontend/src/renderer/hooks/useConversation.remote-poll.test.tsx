import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { conversationQueryKey, toSnapshot, useConversation } from "./useConversation";
import type { components } from "../../api/schema";

const mocks = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/host-clients", async (original) => ({
	...await original<typeof import("../lib/host-clients")>(),
	clientForSessionHost: (hostId?: string) => ({ GET: (...args: unknown[]) => mocks.get(hostId, ...args) }),
}));
const wire: components["schemas"]["ConversationSnapshotResponse"] = { conversationId: "conversation", sessionId: "same", harness: "opencode", mode: "chat", controller: "ready", latestSequence: 0, hasMoreBefore: false, branchedFromEarlierMessage: false, nativeForkAvailableAfterSequence: 0, settings: { model: "test" }, turns: [], messages: [], activities: [] };
function Observer({ hostId, id }: { hostId: string; id: string }) { useConversation(id, hostId); return null; }
function client() {
	const query = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity, gcTime: Infinity, retry: false } } });
	for (const hostId of ["box-a", "box-b"]) for (const id of ["same", "other"])
		query.setQueryData(conversationQueryKey(id, hostId), { pages: [toSnapshot(wire)], pageParams: [undefined] });
	return query;
}
beforeEach(() => { vi.useFakeTimers(); mocks.get.mockReset().mockResolvedValue({ data: wire }); });
afterEach(() => { vi.useRealTimers(); });

it("shares one fallback poll across a Grid header and Chat surface and frees it after the last observer", async () => {
	const query = client();
	const tree = (count: number) => <QueryClientProvider client={query}>{Array.from({ length: count }, (_, index) => <Observer key={index} hostId="box-a" id="same" />)}</QueryClientProvider>;
	const view = render(tree(2));
	await act(() => vi.advanceTimersByTimeAsync(2_000));
	expect(mocks.get).toHaveBeenCalledTimes(1);
	view.rerender(tree(1));
	await act(() => vi.advanceTimersByTimeAsync(2_000));
	expect(mocks.get).toHaveBeenCalledTimes(2);
	view.unmount();
	await act(() => vi.advanceTimersByTimeAsync(6_000));
	expect(mocks.get).toHaveBeenCalledTimes(2);
	query.clear();
});

it("keeps equal session IDs on different hosts independent and routes each poll to its owner", async () => {
	const query = client();
	const view = render(<QueryClientProvider client={query}><Observer hostId="box-a" id="same" /><Observer hostId="box-b" id="same" /><Observer hostId="box-a" id="other" /></QueryClientProvider>);
	await act(() => vi.advanceTimersByTimeAsync(2_000));
	expect(mocks.get.mock.calls.map(([hostId, , options]) => [hostId, options.params.path.sessionId])).toEqual([["box-a", "same"], ["box-b", "same"], ["box-a", "other"]]);
	view.unmount(); query.clear();
});

it("does not overlap slow network polls", async () => {
	let finish!: (value: { data: typeof wire }) => void;
	mocks.get.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
	const query = client();
	const view = render(<QueryClientProvider client={query}><Observer hostId="box-a" id="same" /><Observer hostId="box-a" id="same" /></QueryClientProvider>);
	await act(() => vi.advanceTimersByTimeAsync(6_000));
	expect(mocks.get).toHaveBeenCalledTimes(1);
	await act(async () => { finish({ data: wire }); });
	await act(() => vi.advanceTimersByTimeAsync(2_000));
	expect(mocks.get).toHaveBeenCalledTimes(2);
	view.unmount(); query.clear();
});
