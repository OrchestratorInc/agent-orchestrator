import type { Terminal } from "@xterm/xterm";
import { afterEach, describe, expect, it, vi } from "vitest";

const addons = vi.hoisted(() => [] as Array<{ disposed: boolean; loseContext: () => void }>);
const webgl = vi.hoisted(() => ({ unavailable: false }));

vi.mock("@xterm/addon-webgl", () => ({
	WebglAddon: class FakeWebglAddon {
		disposed = false;
		private lossListener: (() => void) | null = null;
		constructor() {
			if (webgl.unavailable) throw new Error("WebGL2 not supported");
			addons.push(this as never);
		}
		onContextLoss(listener: () => void) {
			this.lossListener = listener;
			return { dispose: () => undefined };
		}
		loseContext() {
			this.lossListener?.();
		}
		dispose() {
			this.disposed = true;
		}
	},
}));

import { MAX_WEBGL_TERMINALS, leaseWebglRenderer, type WebglLease } from "./terminal-webgl-pool";

const leases: WebglLease[] = [];

function lease(visible: { value: boolean }) {
	const term = { loadAddon: vi.fn() } as unknown as Terminal;
	const result = leaseWebglRenderer(term, () => visible.value);
	leases.push(result);
	return { term, lease: result };
}

function liveAddons() {
	return addons.filter((addon) => !addon.disposed).length;
}

afterEach(() => {
	for (const item of leases.splice(0)) item.release();
	addons.length = 0;
	webgl.unavailable = false;
	vi.restoreAllMocks();
});

describe("terminal WebGL pool", () => {
	it("keeps contexts only for the most recently shown terminals", () => {
		const hidden = { value: false };
		const terminals = Array.from({ length: MAX_WEBGL_TERMINALS + 4 }, () => lease(hidden));
		for (const { lease: item } of terminals) item.activate();

		expect(addons).toHaveLength(MAX_WEBGL_TERMINALS + 4);
		expect(liveAddons()).toBe(MAX_WEBGL_TERMINALS);
		// The oldest four were parked back on the DOM renderer.
		expect(addons.slice(0, 4).every((addon) => addon.disposed)).toBe(true);
	});

	it("gives a parked terminal a fresh context when it is shown again", () => {
		const hidden = { value: false };
		const terminals = Array.from({ length: MAX_WEBGL_TERMINALS + 1 }, () => lease(hidden));
		for (const { lease: item } of terminals) item.activate();
		expect(addons[0].disposed).toBe(true);

		terminals[0].lease.activate();

		expect(terminals[0].term.loadAddon).toHaveBeenCalledTimes(2);
		expect(addons.at(-1)?.disposed).toBe(false);
		expect(liveAddons()).toBe(MAX_WEBGL_TERMINALS);
		// The next least recently shown terminal gave up its context instead.
		expect(addons[1].disposed).toBe(true);
	});

	it("never takes the context from a visible terminal", () => {
		const shown = { value: true };
		const hidden = { value: false };
		const visible = lease(shown);
		visible.lease.activate();
		for (let i = 0; i < MAX_WEBGL_TERMINALS; i++) lease(hidden).lease.activate();

		expect(addons[0].disposed).toBe(false);
		expect(addons[1].disposed).toBe(true);
	});

	it("re-creates a lost context on the next activation", () => {
		vi.spyOn(console, "warn").mockImplementation(() => undefined);
		const shown = { value: true };
		const { term, lease: item } = lease(shown);
		item.activate();
		item.activate();
		expect(term.loadAddon).toHaveBeenCalledTimes(1);

		addons[0].loseContext();
		expect(addons[0].disposed).toBe(true);

		item.activate();
		expect(term.loadAddon).toHaveBeenCalledTimes(2);
		expect(addons[1].disposed).toBe(false);
	});

	it("ignores activation after release", () => {
		const { term, lease: item } = lease({ value: true });
		item.release();
		item.activate();
		expect(term.loadAddon).not.toHaveBeenCalled();
	});

	it("stops trying once WebGL is unavailable", async () => {
		// The failure is remembered for the module's lifetime, so use a fresh copy.
		vi.resetModules();
		const pool = await import("./terminal-webgl-pool");
		const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
		webgl.unavailable = true;
		const term = { loadAddon: vi.fn() } as unknown as Terminal;
		const item = pool.leaseWebglRenderer(term, () => true);
		leases.push(item);

		item.activate();
		item.activate();
		pool.leaseWebglRenderer(term, () => true).activate();

		expect(warn).toHaveBeenCalledTimes(1);
		expect(term.loadAddon).not.toHaveBeenCalled();
	});
});
