import { describe, expect, it } from "vitest";
import { createRefreshQueue } from "./refreshQueue";

function deferred() {
	let resolve!: () => void;
	const promise = new Promise<void>((done) => { resolve = done; });
	return { promise, resolve };
}

describe("refreshQueue", () => {
	it("runs one refresh at a time and folds every request made meanwhile into one more", async () => {
		const queue = createRefreshQueue();
		const runs: Array<ReturnType<typeof deferred>> = [];
		const task = () => { const run = deferred(); runs.push(run); return run.promise; };

		const first = queue.run(task);
		const second = queue.run(task);
		const third = queue.run(task);
		expect(runs).toHaveLength(1);
		expect(second).toBe(third);

		runs[0].resolve();
		await first;
		await Promise.resolve();
		expect(runs).toHaveLength(2);
		runs[1].resolve();
		await second;
		expect(queue.busy()).toBe(false);
	});

	it("never runs two refreshes at once when asked again right after the running one settles", async () => {
		const queue = createRefreshQueue();
		const runs: Array<ReturnType<typeof deferred>> = [];
		let active = 0;
		let maxActive = 0;
		const task = async () => {
			const run = deferred();
			runs.push(run);
			active += 1;
			maxActive = Math.max(maxActive, active);
			try { await run.promise; } finally { active -= 1; }
		};

		const first = queue.run(task);
		const queued = queue.run(task);
		runs[0].resolve();
		await first;
		// No extra microtask wait: the running refresh is over but the queued one
		// has not started yet. This request must join it, not start beside it.
		const again = queue.run(task);
		expect(again).toBe(queued);
		await Promise.resolve();
		await Promise.resolve();
		expect(runs).toHaveLength(2);
		runs[1].resolve();
		await queued;
		expect(maxActive).toBe(1);
		expect(queue.busy()).toBe(false);
	});

	it("tells a run from an ended generation not to publish, and drops what was queued behind it", async () => {
		const queue = createRefreshQueue();
		const current: boolean[] = [];
		const running = deferred();
		let queuedRan = false;
		const first = queue.run(async (isCurrent) => { await running.promise; current.push(isCurrent()); });
		const queued = queue.run(async () => { queuedRan = true; });

		queue.invalidate();
		const fresh = queue.run(async (isCurrent) => { current.push(isCurrent()); });
		await fresh;
		running.resolve();
		await first;
		await queued;

		expect(current).toEqual([true, false]);
		expect(queuedRan).toBe(false);
	});

	it("still runs the queued refresh after one that failed", async () => {
		const queue = createRefreshQueue();
		const failing = deferred();
		let ran = false;
		const first = queue.run(() => failing.promise.then(() => { throw new Error("offline"); }));
		const next = queue.run(async () => { ran = true; });
		failing.resolve();
		await expect(first).rejects.toThrow("offline");
		await next;
		expect(ran).toBe(true);
	});
});
