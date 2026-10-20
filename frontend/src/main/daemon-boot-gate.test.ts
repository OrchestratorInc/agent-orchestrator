import { describe, expect, it } from "vitest";
import { bootDaemonAndWindow, DAEMON_BOOT_TIMEOUT_MS } from "./daemon-boot-gate";

function deferred(): { promise: Promise<void>; resolve: () => void } {
	let resolve!: () => void;
	const promise = new Promise<void>((r) => {
		resolve = r;
	});
	return { promise, resolve };
}

describe("bootDaemonAndWindow", () => {
	it("waits for the daemon to settle before creating the window (daemon-first boot)", async () => {
		const order: string[] = [];
		const start = deferred();
		const startDaemon = async () => {
			await start.promise;
			order.push("daemon");
		};
		const createWindow = async () => {
			order.push("window");
		};
		const boot = bootDaemonAndWindow({ startDaemon, createWindow, isWindows: false });
		await new Promise((r) => setTimeout(r, 10));
		expect(order).toEqual([]);
		start.resolve();
		await boot;
		expect(order).toEqual(["daemon", "window"]);
	});

	it("opens the window anyway when the daemon start exceeds the boot timeout", async () => {
		const order: string[] = [];
		const startDaemon = () => new Promise<void>(() => {});
		const createWindow = async () => {
			order.push("window");
		};
		await bootDaemonAndWindow({ startDaemon, createWindow, isWindows: false, timeoutMs: 20 });
		expect(order).toEqual(["window"]);
	});

	it("starts the daemon after the window on Windows", async () => {
		const order: string[] = [];
		const startDaemon = async () => {
			order.push("daemon");
		};
		const createWindow = async () => {
			order.push("window");
		};
		await bootDaemonAndWindow({ startDaemon, createWindow, isWindows: true });
		expect(order).toEqual(["window", "daemon"]);
	});

	it("does not start the daemon after an aborted Windows boot", async () => {
		let started = 0;
		const startDaemon = async () => {
			started++;
		};
		await bootDaemonAndWindow({
			startDaemon,
			createWindow: async () => {},
			isWindows: true,
			shouldAbort: () => true,
		});
		expect(started).toBe(0);
	});

	it("exports a bounded default timeout", () => {
		expect(DAEMON_BOOT_TIMEOUT_MS).toBeLessThanOrEqual(20_000);
		expect(DAEMON_BOOT_TIMEOUT_MS).toBeGreaterThan(0);
	});
});
