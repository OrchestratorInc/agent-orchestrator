import { describe, expect, it } from "vitest";
import type { ChromeApi } from "./chrome-api";
import { TrustedInput } from "./trusted-input";

type Call = { method: string; params: Record<string, unknown> };

// A debugger whose commands can be held open, to cancel mid-gesture.
function fakeDebugger() {
	const calls: Call[] = [];
	let attaches = 0;
	let detaches = 0;
	const gates = new Map<string, () => void>();
	const gateOn = (key: string) => new Promise<void>((resolve) => gates.set(key, resolve));
	let blocked: { key: string; wait: Promise<void> } | undefined;
	const chrome = {
		scripting: {
			executeScript: async ({ args }: { args: string[] }) => {
				calls.push({ method: "capture", params: { action: args[0] } });
				return [{ result: args[0] === "observed" ? 47 : true }];
			},
		},
		debugger: {
			attach: async () => void attaches++,
			detach: async () => void detaches++,
			sendCommand: async (_target: unknown, method: string, params: Record<string, unknown> = {}) => {
				calls.push({ method, params });
				const key = `${method}:${String(params.type ?? "")}`;
				if (blocked && blocked.key === key) {
					const wait = blocked.wait;
					blocked = undefined;
					await wait;
				}
				return {};
			},
			onEvent: { addListener: () => undefined, removeListener: () => undefined },
			onDetach: { addListener: () => undefined, removeListener: () => undefined },
		},
	} as unknown as ChromeApi;
	return {
		chrome,
		calls,
		counts: () => ({ attaches, detaches }),
		/** Hold the next command matching method/type until release(key). */
		block(method: string, type = "") {
			const key = `${method}:${type}`;
			blocked = { key, wait: gateOn(key) };
			return () => gates.get(key)?.();
		},
		inputTypes: () => calls.filter((call) => call.method.startsWith("Input.")).map((call) => String(call.params.type ?? call.method)),
	};
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

describe("TrustedInput cancellation", () => {
	it("sweeps the observed pointer ID into frames whose observers saw no event", async () => {
		const fake = fakeDebugger();
		let swept: number[] | undefined;
		fake.chrome.scripting.executeScript = async (injection) => {
			if (!("args" in injection)) return [];
			if (injection.args[0] === "observed") return [{ result: 47 }, { result: "idle" }];
			if (injection.args[0] === "release") swept = injection.args[1];
			return [{ result: true }, { result: true }];
		};
		const controller = new AbortController();
		const release = fake.block("Input.dispatchMouseEvent", "mousePressed");
		const click = new TrustedInput(fake.chrome).click(1, { x: 10, y: 20 }, { signal: controller.signal });
		await tick();
		controller.abort();
		release();
		await expect(click).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		expect(swept).toEqual([47]);
		expect(fake.inputTypes()).toEqual(["mouseMoved", "mousePressed", "mouseMoved", "mouseReleased"]);
	});
	it("dispatches no click after the command is cancelled mid-movement", async () => {
		const fake = fakeDebugger();
		const input = new TrustedInput(fake.chrome);
		const controller = new AbortController();
		const release = fake.block("Input.dispatchMouseEvent", "mouseMoved");
		const click = input.click(1, { x: 10, y: 20 }, { signal: controller.signal });
		await tick();
		controller.abort();
		release();
		await expect(click).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		expect(fake.inputTypes()).toEqual(["mouseMoved"]);
		expect(fake.counts().detaches).toBe(1);
	});

	it("releases a pressed button when cancelled mid-click", async () => {
		const fake = fakeDebugger();
		const input = new TrustedInput(fake.chrome);
		const controller = new AbortController();
		const release = fake.block("Input.dispatchMouseEvent", "mousePressed");
		const click = input.click(1, { x: 10, y: 20 }, { clicks: 2, signal: controller.signal });
		await tick();
		controller.abort();
		release();
		await expect(click).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		expect(fake.inputTypes()).toEqual(["mouseMoved", "mousePressed", "mouseMoved", "mouseReleased"]);
		const released = fake.calls.find((call) => call.params.type === "mouseReleased");
		expect(released?.params).toMatchObject({ x: -1, y: -1, button: "left", buttons: 0 });
		const capture = fake.calls.findIndex((call) => call.method === "capture" && call.params.action === "release");
		const releaseIndex = fake.calls.findIndex((call) => call.params.type === "mouseReleased");
		expect(capture).toBeGreaterThan(-1);
		expect(capture).toBeLessThan(releaseIndex);
	});

	it("does not release onto a target if capture cleanup cannot be verified", async () => {
		const fake = fakeDebugger();
		fake.chrome.scripting.executeScript = async (injection) => {
			if ("args" in injection && injection.args[0] === "release") throw new Error("Frame unavailable");
			return [{ result: "args" in injection && injection.args[0] === "observed" ? 47 : true }];
		};
		const input = new TrustedInput(fake.chrome);
		const controller = new AbortController();
		const release = fake.block("Input.dispatchMouseEvent", "mousePressed");
		const click = input.click(1, { x: 10, y: 20 }, { signal: controller.signal });
		await tick();
		controller.abort();
		release();
		await expect(click).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		expect(fake.inputTypes()).toEqual(["mouseMoved", "mousePressed"]);
		expect(fake.counts().detaches).toBe(1);
	});

	it("never attaches when cancelled while waiting for the tab", async () => {
		const fake = fakeDebugger();
		const input = new TrustedInput(fake.chrome);
		const release = fake.block("Input.dispatchMouseEvent", "mouseMoved");
		const first = input.click(1, { x: 1, y: 1 });
		const controller = new AbortController();
		const queued = input.press(1, "Enter", controller.signal);
		await tick();
		controller.abort();
		release();
		await first;
		await expect(queued).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		expect(fake.counts().attaches).toBe(1);
		expect(fake.inputTypes()).not.toContain("keyDown");
	});

	it("lets go of held keys when a chord is cancelled", async () => {
		const fake = fakeDebugger();
		const input = new TrustedInput(fake.chrome);
		const controller = new AbortController();
		const release = fake.block("Input.dispatchKeyEvent", "rawKeyDown");
		const press = input.press(1, "Control+A", controller.signal);
		await tick();
		controller.abort();
		release();
		await expect(press).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		const keys = fake.calls.filter((call) => call.method === "Input.dispatchKeyEvent").map((call) => `${call.params.type}:${call.params.key}`);
		expect(keys).toEqual(["rawKeyDown:Control", "keyUp:Control"]);
	});

	it("rejects at once when already cancelled", async () => {
		const fake = fakeDebugger();
		const input = new TrustedInput(fake.chrome);
		const controller = new AbortController();
		controller.abort();
		await expect(input.hover(1, { x: 1, y: 1 }, true, controller.signal)).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		expect(fake.counts().attaches).toBe(0);
	});
});
