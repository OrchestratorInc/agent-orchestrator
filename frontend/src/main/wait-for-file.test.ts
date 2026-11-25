import { describe, expect, it } from "vitest";
import { waitForFile } from "./wait-for-file";

function clock() {
	let time = 0;
	return {
		now: () => time,
		sleep: async (ms: number) => {
			time += ms;
		},
	};
}

describe("waitForFile", () => {
	it("returns at once when the file is there", async () => {
		const { now, sleep } = clock();
		let checks = 0;
		await expect(waitForFile("/app/ao.exe", { timeoutMs: 10_000, now, sleep, exists: () => (checks++, true) })).resolves.toBe(true);
		expect(checks).toBe(1);
	});

	it("waits for a file that appears before the timeout", async () => {
		const { now, sleep } = clock();
		await expect(waitForFile("/app/ao.exe", { timeoutMs: 10_000, intervalMs: 500, now, sleep, exists: () => now() >= 2_000 })).resolves.toBe(true);
	});

	it("gives up when the file never appears", async () => {
		const { now, sleep } = clock();
		await expect(waitForFile("/app/ao.exe", { timeoutMs: 10_000, intervalMs: 500, now, sleep, exists: () => false })).resolves.toBe(false);
		expect(now()).toBe(10_000);
	});
});
