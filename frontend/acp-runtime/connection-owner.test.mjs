// @vitest-environment node
import { spawn } from "node:child_process";
import { EventEmitter } from "node:events";
import { afterEach, describe, expect, it, vi } from "vitest";
import { onUncaughtException, onUnhandledRejection, ownedSpawn, release, runAsChat, track } from "./connection-owner.mjs";

// Node runs an uncaughtException listener in the async context of the callback
// that threw, so calling the handler from inside that callback is the same case.
const chat = (sessionId = "ao-7") => ({ sessionId, resources: new Set(), released: false, close: vi.fn(), onSpawn: vi.fn() });
const errors = () => vi.spyOn(console, "error").mockImplementation(() => {});

afterEach(() => vi.restoreAllMocks());

describe("onUncaughtException", () => {
	it("closes only the chat whose timer threw", async () => {
		const log = errors();
		const [a, b] = [chat("ao-a"), chat("ao-b")];
		const exit = vi.fn();
		await new Promise((done) => runAsChat(a, () => setTimeout(() => (onUncaughtException(new Error("boom"), exit), done()))));
		expect(a.close).toHaveBeenCalledOnce();
		expect(b.close).not.toHaveBeenCalled();
		expect(exit).not.toHaveBeenCalled();
		expect(log.mock.calls[0][0]).toContain("ao-a");
	});

	it("finds the chat after promise continuations", async () => {
		errors();
		const a = chat();
		const exit = vi.fn();
		await runAsChat(a, async () => {
			await Promise.resolve();
			await new Promise((resolve) => setTimeout(resolve, 1));
			onUncaughtException(new Error("after await"), exit);
		});
		expect(a.close).toHaveBeenCalledOnce();
		expect(exit).not.toHaveBeenCalled();
	});

	it("exits when no chat owns the throw", () => {
		const log = errors();
		const exit = vi.fn();
		onUncaughtException(new Error("no owner"), exit);
		expect(exit).toHaveBeenCalledWith(1);
		expect(log.mock.calls[0][0]).toContain("outside any chat");
	});
});

describe("onUnhandledRejection", () => {
	it("logs the rejection with its chat and closes nothing", async () => {
		const log = errors();
		const a = chat("ao-r");
		await runAsChat(a, async () => {
			await Promise.resolve();
			onUnhandledRejection(new Error("rejected"));
		});
		expect(a.close).not.toHaveBeenCalled();
		expect(log.mock.calls[0][0]).toContain("ao-r");
	});
});

describe("track and release", () => {
	it("records a chat's processes until release and its watchers until they close", () => {
		const a = chat();
		const child = Object.assign(new EventEmitter(), { kill: vi.fn() });
		const watcher = Object.assign(new EventEmitter(), { close: vi.fn() });
		runAsChat(a, () => {
			track(child);
			track(watcher);
		});
		track(new EventEmitter());
		expect([...a.resources]).toEqual([child, watcher]);
		child.emit("exit");
		watcher.emit("close");
		expect([...a.resources]).toEqual([child]);
	});

	it("closes watchers and signals each process group", () => {
		vi.useFakeTimers();
		const kill = vi.spyOn(process, "kill").mockImplementation(() => true);
		const watcher = { close: vi.fn() };
		const a = chat();
		a.resources = new Set([{ pid: 4321, kill: vi.fn() }, watcher]);
		release(a, 2_000);
		expect(watcher.close).toHaveBeenCalledOnce();
		expect(kill).toHaveBeenCalledWith(-4321, "SIGTERM");
		vi.advanceTimersByTime(2_000);
		expect(kill).toHaveBeenCalledWith(-4321, "SIGKILL");
		vi.useRealTimers();
	});

	it("stops what a released chat starts later, such as a session/new that fails after release", async () => {
		vi.useFakeTimers();
		const kill = vi.spyOn(process, "kill").mockImplementation(() => true);
		const a = chat();
		const watcher = Object.assign(new EventEmitter(), { close: vi.fn() });
		const child = Object.assign(new EventEmitter(), { pid: 5555, kill: vi.fn() });
		const late = runAsChat(a, async () => {
			await new Promise((resolve) => setTimeout(resolve, 5_000));
			track(watcher);
			track(child);
			throw new Error("session/new failed");
		});
		release(a);
		await vi.advanceTimersByTimeAsync(5_000);
		await expect(late).rejects.toThrow("session/new failed");
		expect(watcher.close).toHaveBeenCalledOnce();
		expect(kill).toHaveBeenCalledWith(-5555, "SIGTERM");
		expect(a.resources.size).toBe(0);
		vi.useRealTimers();
	});

	it.skipIf(process.platform === "win32")("reports each spawned process group to its chat", () => {
		const a = chat();
		const child = runAsChat(a, () => ownedSpawn(spawn)("true", [], { stdio: "ignore" }));
		expect(a.onSpawn).toHaveBeenCalledWith(child);
	});

	it.skipIf(process.platform === "win32")("stops a chat's surviving grandchild and leaves another chat running", async () => {
		const alive = (pid) => {
			try {
				process.kill(pid, 0);
				return true;
			} catch {
				return false;
			}
		};
		const start = ownedSpawn(spawn);
		const [a, b] = [chat("ao-a"), chat("ao-b")];
		// sh starts a background sleep and prints its pid; killing sh leaves the
		// sleep as an orphan in chat A's process group.
		const parent = runAsChat(a, () => start("sh", ["-c", "sleep 30 & echo $!; wait"], { stdio: ["ignore", "pipe", "ignore"] }));
		const grandchild = Number(await new Promise((resolve) => parent.stdout.once("data", (data) => resolve(String(data).trim()))));
		const other = runAsChat(b, () => start("sleep", ["30"], { stdio: "ignore" }));
		try {
			parent.kill("SIGKILL");
			await new Promise((resolve) => parent.once("exit", resolve));
			expect(alive(grandchild)).toBe(true);
			release(a, 100);
			await new Promise((resolve) => setTimeout(resolve, 500));
			expect(alive(grandchild)).toBe(false);
			expect(alive(other.pid)).toBe(true);
		} finally {
			release(b, 0);
			try {
				process.kill(grandchild, "SIGKILL");
			} catch {
				// Already stopped by release.
			}
		}
	});
});
