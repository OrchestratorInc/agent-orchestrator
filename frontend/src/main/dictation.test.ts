import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import { afterAll, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

type Handler = (event: { sender: FakeContents }, ...args: unknown[]) => unknown;

const electron = vi.hoisted(() => ({
	handlers: new Map<string, Handler>(),
}));

vi.mock("electron", () => ({
	app: { on: vi.fn() },
	ipcMain: { handle: (channel: string, handler: Handler) => electron.handlers.set(channel, handler) },
}));

/** A fake helper process: lines written to `stdout` reach main as the helper's events. */
class FakeHelper extends EventEmitter {
	stdin = Object.assign(new EventEmitter(), { write: vi.fn() });
	stdout = new PassThrough();
	kill = vi.fn();
	say(event: object): void {
		this.stdout.write(`${JSON.stringify(event)}\n`);
	}
}

const helpers: FakeHelper[] = [];
vi.mock("node:child_process", () => {
	const spawn = vi.fn(() => {
		const helper = new FakeHelper();
		helpers.push(helper);
		return helper;
	});
	return { spawn, default: { spawn } };
});

class FakeContents extends EventEmitter {
	send = vi.fn();
	isDestroyed = () => false;
}

const { registerDictationIpc } = await import("./dictation");

const call = (channel: string, sender: FakeContents, ...args: unknown[]) =>
	electron.handlers.get(channel)!({ sender }, ...args);
const lines = () => new Promise((resolve) => setImmediate(resolve));

describe("registerDictationIpc", () => {
	const platform = process.platform;
	let shell: FakeContents;
	let page: FakeContents;
	let stranger: FakeContents;

	beforeAll(() => Object.defineProperty(process, "platform", { value: "darwin" }));
	afterAll(() => Object.defineProperty(process, "platform", { value: platform }));

	beforeEach(() => {
		helpers.length = 0;
		shell = new FakeContents();
		page = new FakeContents();
		stranger = new FakeContents();
		registerDictationIpc("/helper", (sender) => sender === (shell as never) || sender === (page as never));
	});

	it("starts only for allowed senders, and says whether it started", () => {
		expect(call("dictation:start", stranger, 1)).toBe(false);
		expect(helpers).toHaveLength(0);
		expect(call("dictation:start", page, 1)).toBe(true);
		expect(helpers).toHaveLength(1);
	});

	it("tags the helper's events with the owner's run", async () => {
		call("dictation:start", page, 4);
		helpers[0]!.say({ type: "text", text: "hello" });
		await lines();
		expect(page.send).toHaveBeenCalledWith("dictation:event", { type: "text", text: "hello", run: 4 });
	});

	it("lets only the owner stop or cancel", () => {
		call("dictation:start", page, 1);
		const helper = helpers[0]!;

		call("dictation:stop", shell);
		call("dictation:cancel", shell);
		expect(helper.stdin.write).not.toHaveBeenCalled();

		call("dictation:stop", page);
		expect(helper.stdin.write).toHaveBeenLastCalledWith("stop\n");
		call("dictation:cancel", page);
		expect(helper.stdin.write).toHaveBeenLastCalledWith("cancel\n");
	});

	it("tells the previous owner its run was superseded, and drops the old helper's events", async () => {
		call("dictation:start", shell, 7);
		call("dictation:start", page, 3);

		expect(shell.send).toHaveBeenCalledWith("dictation:event", { type: "superseded", run: 7 });
		expect(helpers[0]!.kill).toHaveBeenCalled();
		helpers[0]!.say({ type: "text", text: "late" });
		helpers[1]!.say({ type: "listening" });
		await lines();
		expect(shell.send).not.toHaveBeenCalledWith("dictation:event", expect.objectContaining({ text: "late" }));
		expect(page.send).toHaveBeenCalledWith("dictation:event", { type: "listening", run: 3 });
	});

	it("does not supersede a run when the same surface starts another", () => {
		call("dictation:start", shell, 1);
		call("dictation:start", shell, 2);
		expect(shell.send).not.toHaveBeenCalledWith("dictation:event", expect.objectContaining({ type: "superseded" }));
	});

	it.each(["destroyed", "render-process-gone", "did-navigate"])(
		"cancels the owner's dictation when it is %s",
		(event) => {
			call("dictation:start", page, 1);
			const helper = helpers[0]!;

			page.emit(event);

			expect(helper.stdin.write).toHaveBeenCalledWith("cancel\n");
			// The page no longer owns anything to stop.
			helper.stdin.write.mockClear();
			call("dictation:stop", page);
			expect(helper.stdin.write).not.toHaveBeenCalled();
		},
	);

	it("leaves the current owner's dictation alone when a former owner goes away", () => {
		call("dictation:start", page, 1);
		call("dictation:start", shell, 1);
		const current = helpers[1]!;

		page.emit("render-process-gone");

		expect(current.stdin.write).not.toHaveBeenCalled();
	});
});
