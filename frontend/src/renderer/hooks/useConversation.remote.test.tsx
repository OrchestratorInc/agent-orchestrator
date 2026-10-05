import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, type ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import type { components } from "../../api/schema";

const { localPost, remotePost, remoteGet } = vi.hoisted(() => ({
	localPost: vi.fn(), remotePost: vi.fn(), remoteGet: vi.fn(),
}));
vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: (hostId?: string) => hostId
		? { POST: remotePost, GET: remoteGet } : { POST: localPost },
}));
import { conversationQueryKey, toSnapshot, useConversationCommands } from "./useConversation";

const sessionId = "same-session";
const hostId = "remote-host";
const input = { text: "deliver once", clientMessageId: "same-client-id" };
const wire: components["schemas"]["ConversationSnapshotResponse"] = {
	conversationId: "remote-conversation", sessionId, harness: "codex",
	mode: "chat", controller: "ready", latestSequence: 0,
	messages: [], activities: [], turns: [], settings: {},
	branchedFromEarlierMessage: false, hasMoreBefore: false, nativeForkAvailableAfterSequence: 0,
};

function mountCommands() {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	const wrapper = ({ children }: { children: ReactNode }) => (
		<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
	);
	return { ...renderHook(() => ({
		local: useConversationCommands(sessionId),
		remote: useConversationCommands(sessionId, hostId),
	}), { wrapper }), queryClient };
}

beforeEach(() => {
	localPost.mockReset().mockResolvedValue({ data: { turnId: "local-turn" } });
	remotePost.mockReset().mockResolvedValue({ data: { turnId: "remote-turn" } });
	remoteGet.mockReset().mockResolvedValue({ data: wire });
});

it("accepts and acknowledges matching client IDs independently on local and remote hosts", async () => {
	const { result } = mountCommands();
	await act(async () => {
		await Promise.all([result.current.local.send(input), result.current.remote.send(input)]);
	});
	await waitFor(() => {
		expect(result.current.local.localEchos).toMatchObject([{ ...input, turnId: "local-turn" }]);
		expect(result.current.remote.localEchos).toMatchObject([{ ...input, turnId: "remote-turn" }]);
	});
	act(() => result.current.remote.acknowledgeLocalEcho(input.clientMessageId));
	await waitFor(() => expect(result.current.remote.localEchos).toEqual([]));
	expect(result.current.local.localEchos).toMatchObject([{ ...input, turnId: "local-turn" }]);
	act(() => result.current.local.acknowledgeLocalEcho(input.clientMessageId));
	await waitFor(() => expect(result.current.local.localEchos).toEqual([]));
});

it("retains only the remote uncertain echo when a retry receives a definitive rejection", async () => {
	remotePost.mockResolvedValueOnce({ error: { code: "CHAT_SEND_FAILED" } })
		.mockResolvedValueOnce({ error: { code: "CHAT_CONTROLLER_NOT_READY" } });
	const { result } = mountCommands();
	await act(async () => {
		await result.current.local.send(input);
		await result.current.remote.send(input).catch(() => {});
		await result.current.remote.send(input).catch(() => {});
	});
	expect(result.current.remote.localEchos).toMatchObject([{ ...input, delivery: "uncertain" }]);
	expect(result.current.local.localEchos).toMatchObject([{ ...input, delivery: "accepted" }]);
	await waitFor(() => expect(result.current.remote.busy).toBe(false));
});

it.each([true, false])("reconciles a remote duplicate against its own snapshot (observed=%s)", async (observed) => {
	remotePost.mockResolvedValue({ data: { duplicate: true } });
	remoteGet.mockResolvedValue({ data: {
		...wire, messages: observed ? [{
			id: "remote-message", clientMessageId: input.clientMessageId,
			sequence: 1, revision: 1, role: "user", origin: "human",
			text: input.text, streaming: false, createdAt: "2026-10-04T00:00:00Z",
		}] : [],
	} });
	const { result, queryClient } = mountCommands();
	// Opposite acknowledgement locally catches a read of the wrong host's snapshot.
	queryClient.setQueryData(conversationQueryKey(sessionId), {
		pages: [{ ...toSnapshot({ ...wire, messages: [], activities: [], turns: [] }), items: observed ? [] : [{
			kind: "message", clientMessageId: input.clientMessageId,
		}] }], pageParams: [undefined],
	});
	await act(async () => {
		await result.current.local.send(input);
		await result.current.remote.send(input);
	});
	await waitFor(() => expect(result.current.remote.localEchos).toHaveLength(observed ? 1 : 0));
	if (observed) expect(result.current.remote.localEchos[0]).toMatchObject({ ...input, delivery: "accepted" });
	expect(result.current.local.localEchos).toMatchObject([{ ...input, turnId: "local-turn" }]);
	await waitFor(() => expect(result.current.remote.busy).toBe(false));
});
