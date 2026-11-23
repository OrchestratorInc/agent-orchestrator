import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DEVICE_SUPPORT_CHECK_MS, LEASE_FAILED_MS, LEASE_HELD_MS, LEASE_IDLE_MS, startSessionDevicePoll } from "./sessionDevicePoll";

describe("startSessionDevicePoll", () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	const settle = () => vi.advanceTimersByTimeAsync(0);

	it("reads no lease while device support is off, and checks support rarely", async () => {
		const supportOn = vi.fn(async () => false);
		const lease = vi.fn(async () => null);
		const seen: unknown[] = [];
		const stop = startSessionDevicePoll({ supportOn, lease }, (next) => seen.push(next), () => Date.now());
		await settle();
		await vi.advanceTimersByTimeAsync(DEVICE_SUPPORT_CHECK_MS - 1);
		expect(supportOn).toHaveBeenCalledTimes(1);
		expect(lease).not.toHaveBeenCalled();
		expect(seen).toEqual([null]);
		await vi.advanceTimersByTimeAsync(1);
		expect(supportOn).toHaveBeenCalledTimes(2);
		stop();
	});

	it("polls quickly while a device is held and slowly while none is", async () => {
		let held: string | null = "lease";
		const lease = vi.fn(async () => held);
		const stop = startSessionDevicePoll({ supportOn: async () => true, lease }, () => {}, () => Date.now());
		await settle();
		expect(lease).toHaveBeenCalledTimes(1);
		await vi.advanceTimersByTimeAsync(LEASE_HELD_MS);
		expect(lease).toHaveBeenCalledTimes(2);
		held = null;
		await vi.advanceTimersByTimeAsync(LEASE_HELD_MS);
		expect(lease).toHaveBeenCalledTimes(3);
		await vi.advanceTimersByTimeAsync(LEASE_IDLE_MS - 1);
		expect(lease).toHaveBeenCalledTimes(3);
		await vi.advanceTimersByTimeAsync(1);
		expect(lease).toHaveBeenCalledTimes(4);
		stop();
	});

	it("backs off after a failure and never overlaps reads", async () => {
		let release: () => void = () => {};
		const lease = vi.fn()
			.mockImplementationOnce(() => new Promise<string>((resolve) => { release = () => resolve("lease"); }))
			.mockRejectedValueOnce(new Error("offline"))
			.mockResolvedValue("lease");
		const stop = startSessionDevicePoll({ supportOn: async () => true, lease }, () => {}, () => Date.now());
		await vi.advanceTimersByTimeAsync(LEASE_HELD_MS * 5);
		expect(lease).toHaveBeenCalledTimes(1); // the slow read is still out
		release();
		await vi.advanceTimersByTimeAsync(LEASE_HELD_MS);
		expect(lease).toHaveBeenCalledTimes(2); // fails
		await vi.advanceTimersByTimeAsync(LEASE_FAILED_MS - 1);
		expect(lease).toHaveBeenCalledTimes(2);
		await vi.advanceTimersByTimeAsync(1);
		expect(lease).toHaveBeenCalledTimes(3);
		stop();
	});

	it("reports nothing once stopped", async () => {
		let release: () => void = () => {};
		const onLease = vi.fn();
		const stop = startSessionDevicePoll({ supportOn: async () => true, lease: () => new Promise<string>((resolve) => { release = () => resolve("lease"); }) }, onLease, () => Date.now());
		await settle();
		stop();
		release();
		await settle();
		expect(onLease).not.toHaveBeenCalled();
	});
});
