// @vitest-environment node
import { spawn } from "node:child_process";
import { once } from "node:events";
import { describe, expect, it, vi } from "vitest";
import { parseElapsedMs, readProcessStartMs } from "./process-start";

describe("parseElapsedMs", () => {
	it.each([
		["00:05", 5_000],
		["12:34", 754_000],
		["01:02:03", 3_723_000],
		["19-04:25:54", (((19 * 24 + 4) * 60 + 25) * 60 + 54) * 1000],
		["   00:01\n", 1_000],
	])("parses ps etime %j", (text, expected) => {
		expect(parseElapsedMs(text)).toBe(expected);
	});

	it.each(["", "   ", "abc", "12", "1:2:3:4", "-1:00", "00:05 extra", "1-02"])("rejects %j", (text) => {
		expect(parseElapsedMs(text)).toBeNull();
	});
});

describe("readProcessStartMs", () => {
	it("subtracts the elapsed time ps reports from the clock reading taken before asking", async () => {
		const ps = vi.fn(async () => "01:00\n");
		const start = await readProcessStartMs(4242, { platform: "darwin", now: () => 1_000_000, ps });
		expect(start).toBe(1_000_000 - 60_000);
		expect(ps).toHaveBeenCalledWith(4242);
	});

	it("returns null when ps fails, as it does for a process that is not running", async () => {
		const ps = vi.fn(async () => {
			throw new Error("ps exited with code 1");
		});
		await expect(readProcessStartMs(4242, { platform: "linux", now: () => 1, ps })).resolves.toBeNull();
	});

	it("returns null for output it cannot read", async () => {
		const ps = vi.fn(async () => "not a time");
		await expect(readProcessStartMs(4242, { platform: "linux", now: () => 1, ps })).resolves.toBeNull();
	});

	it.each([0, 1, -5, 1.5, Number.NaN])("returns null for PID %s without asking the OS", async (pid) => {
		const ps = vi.fn(async () => "00:05");
		await expect(readProcessStartMs(pid, { platform: "darwin", now: () => 1, ps })).resolves.toBeNull();
		expect(ps).not.toHaveBeenCalled();
	});

	it("returns null on Windows, which has no ps to ask", async () => {
		const ps = vi.fn(async () => "00:05");
		await expect(readProcessStartMs(4242, { platform: "win32", now: () => 1, ps })).resolves.toBeNull();
		expect(ps).not.toHaveBeenCalled();
	});

	it.skipIf(process.platform === "win32")("reads the real start time of a live process", async () => {
		const spawnedAt = Date.now();
		const child = spawn("sleep", ["30"], { stdio: "ignore" });
		try {
			await once(child, "spawn");
			const start = await readProcessStartMs(child.pid!);
			expect(start).not.toBeNull();
			// ps reports whole seconds; allow generous scheduling slack on a busy runner.
			expect(Math.abs(start! - spawnedAt)).toBeLessThan(5_000);
		} finally {
			child.kill();
		}
	});

	it.skipIf(process.platform === "win32")("returns null for the PID of a process that has exited", async () => {
		const child = spawn(process.execPath, ["-e", ""], { stdio: "ignore" });
		await once(child, "exit");
		await expect(readProcessStartMs(child.pid!)).resolves.toBeNull();
	});
});
