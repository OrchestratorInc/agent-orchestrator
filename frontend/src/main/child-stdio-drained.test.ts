import { EventEmitter } from "node:events";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { childStdioDrained } from "./child-stdio-drained";

function stream(ended: boolean) {
	return Object.assign(new EventEmitter(), { readableEnded: ended });
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("childStdioDrained", () => {
	it("returns nothing when no pipe is still open", () => {
		expect(childStdioDrained({ stdout: stream(true), stderr: null }, 250)).toBeUndefined();
		expect(childStdioDrained({}, 250)).toBeUndefined();
	});

	it("resolves once every open pipe closes", async () => {
		const stdout = stream(false);
		const stderr = stream(false);
		const done = vi.fn();
		void childStdioDrained({ stdout, stderr }, 250)!.then(done);
		stdout.emit("close");
		await Promise.resolve();
		expect(done).not.toHaveBeenCalled();
		stderr.emit("close");
		await vi.advanceTimersByTimeAsync(0);
		expect(done).toHaveBeenCalledOnce();
	});

	it("gives up after the timeout when a pipe stays open", async () => {
		const done = vi.fn();
		void childStdioDrained({ stderr: stream(false) }, 250)!.then(done);
		await vi.advanceTimersByTimeAsync(249);
		expect(done).not.toHaveBeenCalled();
		await vi.advanceTimersByTimeAsync(1);
		expect(done).toHaveBeenCalledOnce();
	});
});
