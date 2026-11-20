// @vitest-environment node
import { EventEmitter } from "node:events";
import { afterEach, describe, expect, it, vi } from "vitest";
import { onUncaughtException, onUnhandledRejection, release, runAsChat, track } from "./connection-owner.mjs";

// Node runs an uncaughtException listener in the async context of the callback
// that threw, so calling the handler from inside that callback is the same case.
const chat = (sessionId = "ao-7") => ({ sessionId, resources: new Set(), close: vi.fn() });
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
	it("records a chat's process and watcher until they end", () => {
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
		expect([...a.resources]).toEqual([watcher]);
	});

	it("closes watchers and stops processes that ignore SIGTERM", () => {
		vi.useFakeTimers();
		const child = { kill: vi.fn(), exitCode: null, signalCode: null };
		const watcher = { close: vi.fn() };
		release(new Set([child, watcher]), 2_000);
		expect(watcher.close).toHaveBeenCalledOnce();
		expect(child.kill).toHaveBeenCalledWith("SIGTERM");
		vi.advanceTimersByTime(2_000);
		expect(child.kill).toHaveBeenCalledWith("SIGKILL");
		vi.useRealTimers();
	});
});
