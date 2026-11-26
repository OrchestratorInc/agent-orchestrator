import { QueryClient, QueryObserver, focusManager } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { recoverWhileHidden } from "./hidden-window";

// The window is hidden from startup, as when AO reloads behind other apps.
let visibility: DocumentVisibilityState = "hidden";
let client: QueryClient;
beforeEach(() => {
	visibility = "hidden";
	vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
	vi.useFakeTimers({ shouldAdvanceTime: true });
	focusManager.setFocused(undefined);
	client = new QueryClient();
	client.mount();
});
afterEach(() => {
	client.clear();
	client.unmount();
	vi.restoreAllMocks();
	vi.useRealTimers();
});

function observe(options: ConstructorParameters<typeof QueryObserver>[1]) {
	const observer = new QueryObserver(client, options);
	const stop = observer.subscribe(() => undefined);
	return { observer, stop };
}

describe("reads in a hidden window", () => {
	it("recovers a read that failed while hidden, without the window being shown", async () => {
		let calls = 0;
		const { observer, stop } = observe({
			queryKey: ["recover"],
			queryFn: async () => {
				calls += 1;
				if (calls === 1) throw new Error("first read fails");
				return "ok";
			},
			...recoverWhileHidden(),
		});
		await vi.advanceTimersByTimeAsync(16_000);
		expect(observer.getCurrentResult().data).toBe("ok");
		stop();
	});

	it("does not poll while hidden once the read succeeds", async () => {
		const queryFn = vi.fn(async () => "ok");
		const { stop } = observe({ queryKey: ["quiet"], queryFn, ...recoverWhileHidden(1_000) });
		await vi.advanceTimersByTimeAsync(10_000);
		expect(queryFn).toHaveBeenCalledTimes(1);
		stop();
	});

	it("leaves every other read paused while hidden, as React Query does", async () => {
		const queryFn = vi.fn(async () => "ok");
		const { stop } = observe({ queryKey: ["plain"], queryFn, refetchInterval: 1_000 });
		await vi.advanceTimersByTimeAsync(10_000);
		expect(queryFn).toHaveBeenCalledTimes(1);
		stop();
	});

	it("polls at its own interval while the window is visible", async () => {
		visibility = "visible";
		const queryFn = vi.fn(async () => "ok");
		const { stop } = observe({ queryKey: ["visible"], queryFn, ...recoverWhileHidden(1_000) });
		await vi.advanceTimersByTimeAsync(5_500);
		expect(queryFn.mock.calls.length).toBeGreaterThanOrEqual(5);
		stop();
	});
});
