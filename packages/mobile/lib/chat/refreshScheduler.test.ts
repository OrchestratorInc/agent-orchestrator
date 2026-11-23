import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { createRefreshScheduler } from "./refreshScheduler";

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

/** A read that settles only when the test says so. */
function deferredReads() {
	const pending: Array<() => void> = [];
	const run = vi.fn(() => new Promise<void>((resolve) => pending.push(resolve)));
	return { run, settle: async (index: number) => { pending[index]?.(); await vi.advanceTimersByTimeAsync(0); } };
}

// A connection change cancels the scheduler while a read to the old connection
// is still out, possibly until its timeout. Events for the new connection must
// not wait on that read.
it("does not let a cancelled read hold back reads after it", async () => {
	const { run, settle } = deferredReads();
	const scheduler = createRefreshScheduler(run, 120);

	scheduler.request();
	await vi.advanceTimersByTimeAsync(120);
	expect(run).toHaveBeenCalledTimes(1); // never settles: a host that stopped answering

	scheduler.cancel();
	scheduler.request();
	await vi.advanceTimersByTimeAsync(120);
	expect(run).toHaveBeenCalledTimes(2);

	// The stale read settling late must not end the current one's turn: a
	// request now still waits for the current read instead of racing it.
	await settle(0);
	scheduler.request();
	await vi.advanceTimersByTimeAsync(500);
	expect(run).toHaveBeenCalledTimes(2);

	// The current read settling runs the catch-up it owes.
	await settle(1);
	await vi.advanceTimersByTimeAsync(120);
	expect(run).toHaveBeenCalledTimes(3);
});

it("drops a catch-up owed before cancel", async () => {
	const { run, settle } = deferredReads();
	const scheduler = createRefreshScheduler(run, 120);

	scheduler.request();
	await vi.advanceTimersByTimeAsync(120);
	scheduler.request(); // earns a catch-up
	scheduler.cancel();
	await settle(0);
	await vi.advanceTimersByTimeAsync(1_000);
	expect(run).toHaveBeenCalledTimes(1);
});
