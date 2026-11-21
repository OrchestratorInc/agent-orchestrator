import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRequestGate } from "../chat/requestGate";
import { startSerialPoll } from "./serialPoll";
import { STARTUP_POLL_MS } from "./startupPoll";

/**
 * The conversation refresh as useConversation runs it: each call opens a request
 * on the latest-request gate and applies its response only if no newer request
 * began meanwhile.
 */
function gatedRefresh(latencyMs: number) {
	const gate = createRequestGate();
	let applied = 0;
	let inFlight = 0;
	let maxInFlight = 0;
	const refresh = async () => {
		const request = gate.begin();
		inFlight += 1;
		maxInFlight = Math.max(maxInFlight, inFlight);
		await new Promise((resolve) => setTimeout(resolve, latencyMs));
		inFlight -= 1;
		if (gate.isCurrent(request)) applied += 1;
	};
	return { refresh, stats: () => ({ applied, maxInFlight }) };
}

describe("startSerialPoll", () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it("applies every read when reads are slower than the poll interval", async () => {
		const { refresh, stats } = gatedRefresh(1_500);
		const stop = startSerialPoll(refresh, STARTUP_POLL_MS);
		await vi.advanceTimersByTimeAsync(10_000);
		stop();
		// One read at a time, each one landing: 1.5s read + 1s gap.
		expect(stats().maxInFlight).toBe(1);
		expect(stats().applied).toBe(4);
	});

	it("is the fix for a fixed interval, which discards every slow read", async () => {
		const { refresh, stats } = gatedRefresh(1_500);
		const timer = setInterval(() => void refresh(), STARTUP_POLL_MS);
		await vi.advanceTimersByTimeAsync(10_000);
		clearInterval(timer);
		expect(stats().maxInFlight).toBeGreaterThan(1);
		expect(stats().applied).toBe(0);
	});

	it("keeps polling after a failed read and stops cleanly", async () => {
		let calls = 0;
		const stop = startSerialPoll(async () => {
			calls += 1;
			if (calls === 1) throw new Error("network");
		}, STARTUP_POLL_MS);
		await vi.advanceTimersByTimeAsync(2_500);
		expect(calls).toBe(3);
		stop();
		await vi.advanceTimersByTimeAsync(5_000);
		expect(calls).toBe(3);
	});
});
