import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CloudCpError, type CloudCpClientEvent } from "../lib/cloud-cp";
import { SCRIPT_STEP_EVENT } from "../lib/cloud-script-runs";
import { useCloudScriptRuns, type CloudScriptRunsLifecycle } from "./useCloudScriptRuns";

const mocks = vi.hoisted(() => ({
	client: { listChatEvents: vi.fn() },
	subscribe: vi.fn(),
}));

vi.mock("./useCloudCp", () => ({
	useCloudCp: () => ({ client: mocks.client, ready: true, baseUrl: "https://cloud.example.test" }),
}));
vi.mock("../lib/cloud-cp/stream-bridge", () => ({
	subscribeSessionEventsBridged: (options: unknown) => mocks.subscribe(options),
}));

function step(sequence: number, payload: Record<string, unknown>): CloudCpClientEvent {
	return {
		sessionId: "s1", sequence, type: SCRIPT_STEP_EVENT, createdAt: "2026-10-18T10:00:00Z",
		payload: { phase: "setup", runId: "run-1", total: 1, step: 1, command: "make", repository: "", ...payload },
	};
}

function page(events: CloudCpClientEvent[], hasMore = false) {
	return { events, hasMore, nextAfter: events.at(-1)?.sequence ?? 0 };
}

function setup(lifecycle?: CloudScriptRunsLifecycle) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
	const hook = renderHook((props: { lifecycle?: CloudScriptRunsLifecycle }) => useCloudScriptRuns("org-1", "s1", { lifecycle: props.lifecycle }), {
		wrapper, initialProps: { lifecycle },
	});
	return { ...hook, queryClient };
}

beforeEach(() => {
	// A fresh client per test: the refused-filter memory is per client.
	mocks.client = { listChatEvents: vi.fn() };
	mocks.subscribe.mockReset();
	mocks.subscribe.mockResolvedValue(undefined);
});

describe("useCloudScriptRuns", () => {
	it("reads only the step events when the control plane filters by type, and does not listen for a finished run", async () => {
		mocks.client.listChatEvents.mockResolvedValueOnce(page([step(4, { status: "succeeded", exitCode: 0 })]));
		const { result } = setup({ desiredState: "running", observedState: "running" });
		await waitFor(() => expect(result.current).toHaveLength(1));
		expect(result.current[0].status).toBe("succeeded");
		expect(mocks.client.listChatEvents).toHaveBeenCalledTimes(1);
		const [, , query] = mocks.client.listChatEvents.mock.calls[0];
		expect(query).toMatchObject({ after: 0, limit: 500 });
		expect(query.types).toEqual(expect.arrayContaining([SCRIPT_STEP_EVENT, "workspace.setup", "agent.ready"]));
		expect(mocks.subscribe).not.toHaveBeenCalled();
	});

	it("pages every event without the filter once the control plane refuses it", async () => {
		mocks.client.listChatEvents
			.mockRejectedValueOnce(new CloudCpError("unknown parameter types", { status: 400 }))
			.mockResolvedValueOnce(page([{ sessionId: "s1", sequence: 1, type: "chat.user_message", payload: {}, createdAt: "" }, step(2, { status: "running" })], true))
			.mockResolvedValueOnce(page([step(3, { status: "succeeded", exitCode: 0 })]));
		const { result, queryClient } = setup();
		await waitFor(() => expect(result.current[0]?.status).toBe("succeeded"));
		const calls = mocks.client.listChatEvents.mock.calls.map(([, , query]) => query);
		expect(calls[0].types).toBeDefined();
		expect(calls[1]).toMatchObject({ after: 0, types: undefined });
		expect(calls[2]).toMatchObject({ after: 2, types: undefined });
		// The cache holds the newest step event only, not the other event types.
		expect(queryClient.getQueryData<CloudCpClientEvent[]>(["cloud-script-steps", "org-1", "s1"])?.map((event) => event.sequence)).toEqual([3]);
		// Later reads skip the filter rather than being refused again.
		mocks.client.listChatEvents.mockResolvedValueOnce(page([]));
		await act(() => queryClient.invalidateQueries({ queryKey: ["cloud-script-steps", "org-1", "s1"] }));
		expect(mocks.client.listChatEvents.mock.calls.at(-1)?.[2]).toMatchObject({ after: 3, types: undefined });
	});

	it("listens on the session stream only while a run is in progress", async () => {
		mocks.client.listChatEvents.mockResolvedValueOnce(page([step(5, { status: "running" })]));
		const { result, unmount } = setup({ desiredState: "running", observedState: "running" });
		await waitFor(() => expect(mocks.subscribe).toHaveBeenCalledTimes(1));
		const options = mocks.subscribe.mock.calls[0][0] as { after?: number; signal: AbortSignal; onEvent: (event: CloudCpClientEvent) => void };
		expect(options.after).toBe(5);
		act(() => options.onEvent(step(6, { status: "succeeded", exitCode: 0 })));
		await waitFor(() => expect(result.current[0].status).toBe("succeeded"));
		// The run ended: the stream closes and is not opened again.
		expect(options.signal.aborted).toBe(true);
		expect(mocks.subscribe).toHaveBeenCalledTimes(1);
		expect(mocks.client.listChatEvents).toHaveBeenCalledTimes(1);
		unmount();
	});

	it("reads again when the session's state changes and listens while it is being deleted", async () => {
		mocks.client.listChatEvents.mockResolvedValueOnce(page([step(5, { status: "succeeded", exitCode: 0 })]));
		const { result, rerender } = setup({ desiredState: "running", observedState: "running" });
		await waitFor(() => expect(result.current).toHaveLength(1));
		expect(mocks.subscribe).not.toHaveBeenCalled();
		// The same state again does not read again.
		rerender({ lifecycle: { desiredState: "running", observedState: "running" } });
		expect(mocks.client.listChatEvents).toHaveBeenCalledTimes(1);

		mocks.client.listChatEvents.mockResolvedValueOnce(page([step(9, { phase: "cleanup", runId: "c", total: 2, status: "succeeded", exitCode: 0 })]));
		rerender({ lifecycle: { desiredState: "deleted", observedState: "deleting" } });
		await waitFor(() => expect(result.current).toHaveLength(2));
		expect(mocks.client.listChatEvents.mock.calls[1][2]).toMatchObject({ after: 5 });
		await waitFor(() => expect(mocks.subscribe).toHaveBeenCalledTimes(1));
		const stream = mocks.subscribe.mock.calls[0][0] as { after?: number; signal: AbortSignal };
		// From the newest event held when the deletion was seen.
		expect(stream.after).toBeGreaterThanOrEqual(5);

		// Deleted: Cleanup is over, the stream closes, and its unfinished run ends.
		mocks.client.listChatEvents.mockResolvedValueOnce(page([]));
		rerender({ lifecycle: { desiredState: "deleted", observedState: "deleted" } });
		await waitFor(() => expect(mocks.client.listChatEvents).toHaveBeenCalledTimes(3));
		expect(result.current[1].status).toBe("unknown");
		await waitFor(() => expect(stream.signal.aborted).toBe(true));
		expect(mocks.subscribe).toHaveBeenCalledTimes(1);
	});

	it("does not poll an idle session", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			mocks.client.listChatEvents.mockResolvedValue(page([step(1, { status: "succeeded", exitCode: 0 })]));
			const { result } = setup({ desiredState: "running", observedState: "running" });
			await waitFor(() => expect(result.current).toHaveLength(1));
			await act(async () => { await vi.advanceTimersByTimeAsync(10 * 60_000); });
			expect(mocks.client.listChatEvents).toHaveBeenCalledTimes(1);
			expect(mocks.subscribe).not.toHaveBeenCalled();
		} finally {
			vi.useRealTimers();
		}
	});
});
