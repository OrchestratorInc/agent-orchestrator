import { afterEach, beforeEach, expect, it } from "vitest";
import { vi } from "vitest";
import { waitForConfig } from "./waitForConfig";

beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => { vi.useRealTimers(); });

it("answers at once with a connection", async () => {
	await expect(waitForConfig(() => "lan", 15_000)).resolves.toBe("lan");
});

// An app switch drops the connection for a moment: a page asked for then waits.
it("waits for the connection to come back", async () => {
	let config: string | null = null;
	const waiting = waitForConfig(() => config, 15_000);
	await vi.advanceTimersByTimeAsync(1_000);
	config = "tunnel";
	await vi.advanceTimersByTimeAsync(250);
	await expect(waiting).resolves.toBe("tunnel");
});

it("gives up after the timeout", async () => {
	const waiting = waitForConfig(() => null, 15_000);
	await vi.advanceTimersByTimeAsync(15_250);
	await expect(waiting).resolves.toBeNull();
});

it("stops when its caller aborts", async () => {
	const controller = new AbortController();
	const waiting = waitForConfig(() => null, 15_000, controller.signal);
	controller.abort();
	await expect(waiting).resolves.toBeNull();
	expect(vi.getTimerCount()).toBe(0);
});
