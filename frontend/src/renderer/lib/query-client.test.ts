import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./telemetry", () => ({ captureRendererException: vi.fn() }));

// AO's window is often hidden (behind other apps, or minimized) while agents
// work. Reads must keep running then: a cloud approval nobody sees times out.
let visibility: DocumentVisibilityState = "hidden";
beforeEach(() => {
	visibility = "hidden";
	vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
	vi.resetModules();
});
afterEach(() => {
	vi.restoreAllMocks();
	vi.useRealTimers();
});

async function freshClient() {
	const { queryClient } = await import("./query-client");
	const { focusManager: manager, QueryObserver } = await import("@tanstack/react-query");
	// As QueryClientProvider does in the app.
	queryClient.mount();
	return { queryClient, manager, QueryObserver };
}

describe("query client in a hidden window", () => {
	it("retries a failed read without waiting for the window to be shown", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const { queryClient } = await freshClient();
		let calls = 0;
		const read = queryClient.fetchQuery({
			queryKey: ["hidden-retry"],
			retry: 1,
			retryDelay: 100,
			queryFn: async () => {
				calls += 1;
				if (calls === 1) throw new Error("first read fails");
				return "ok";
			},
		});
		await vi.advanceTimersByTimeAsync(1_000);
		await expect(read).resolves.toBe("ok");
		expect(calls).toBe(2);
		queryClient.clear();
	});

	it("keeps polling while hidden", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		const { queryClient, QueryObserver } = await freshClient();
		const queryFn = vi.fn(async () => Date.now());
		const observer = new QueryObserver(queryClient, { queryKey: ["hidden-poll"], queryFn, refetchInterval: 1_000 });
		const stop = observer.subscribe(() => undefined);
		await vi.advanceTimersByTimeAsync(5_500);
		expect(queryFn.mock.calls.length).toBeGreaterThanOrEqual(5);
		stop();
		queryClient.clear();
	});

	it("still refetches a focus-refetching read when the window is shown again", async () => {
		const { queryClient, manager, QueryObserver } = await freshClient();
		const queryFn = vi.fn(async () => "value");
		const observer = new QueryObserver(queryClient, { queryKey: ["on-show"], queryFn, refetchOnWindowFocus: "always" });
		const stop = observer.subscribe(() => undefined);
		await vi.waitFor(() => expect(queryFn).toHaveBeenCalledTimes(1));
		visibility = "visible";
		document.dispatchEvent(new Event("visibilitychange", { bubbles: true }));
		await vi.waitFor(() => expect(queryFn).toHaveBeenCalledTimes(2));
		expect(manager.isFocused()).toBe(true);
		stop();
		queryClient.clear();
	});
});

