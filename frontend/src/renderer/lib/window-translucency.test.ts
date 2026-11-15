import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "./bridge";
import { isMacPlatform, isWindowsPlatform } from "./platform";
import type { WindowTransparencyState } from "../../shared/window-transparency";

vi.mock("./platform", () => ({ isMacPlatform: vi.fn(() => false), isWindowsPlatform: vi.fn(() => true) }));

const nativeState = (enabled: boolean): WindowTransparencyState => ({ supported: true, enabled, reason: null });
function deferredState() {
	let resolve!: (value: WindowTransparencyState) => void;
	const promise = new Promise<WindowTransparencyState>((done) => { resolve = done; });
	return { promise, resolve };
}
let module: typeof import("./window-translucency");

beforeEach(async () => {
	vi.resetModules();
	vi.mocked(isMacPlatform).mockReturnValue(false);
	vi.mocked(isWindowsPlatform).mockReturnValue(true);
	module = await import("./window-translucency");
});
afterEach(() => {
	module.disposeWindowTranslucency();
	window.localStorage.clear();
	delete document.documentElement.dataset.windowTranslucent;
	delete document.documentElement.dataset.windowMaterial;
	vi.restoreAllMocks();
});

describe("window translucency", () => {
	it("restores the macOS preference independently of Developer Mode and defaults Windows off", () => {
		expect(module.readStoredWindowTranslucency()).toBe(false);
		vi.mocked(isMacPlatform).mockReturnValue(true);
		vi.mocked(isWindowsPlatform).mockReturnValue(false);
		expect(module.readStoredWindowTranslucency()).toBe(true);
		window.localStorage.setItem(module.developerModeStorageKey, "false");
		expect(module.readStoredWindowTranslucency()).toBe(true);
		window.localStorage.setItem(module.windowTranslucencyStorageKey, "false");
		expect(module.readStoredWindowTranslucency()).toBe(false);
		window.localStorage.setItem(module.windowTranslucencyStorageKey, "true");
		expect(module.readStoredWindowTranslucency()).toBe(true);
		vi.mocked(isMacPlatform).mockReturnValue(false);
		expect(module.readStoredWindowTranslucency()).toBe(false);
		vi.mocked(isWindowsPlatform).mockReturnValue(true);
		expect(module.readStoredWindowTranslucency()).toBe(true);
	});

	it("starts with an unknown Windows capability rather than an unsupported verdict", () => {
		const { result } = renderHook(module.useWindowTransparency);
		expect(result.current).toEqual({ supported: null, enabled: false, reason: null });
	});

	it("keeps Windows opaque until main answers, including repeated requests", async () => {
		const pending = deferredState();
		const set = vi.spyOn(aoBridge.theme, "setWindowTranslucent")
			.mockReturnValueOnce(pending.promise).mockImplementation(async (enabled) => nativeState(enabled));
		module.initializeWindowTranslucency(true);
		const request = module.applyWindowTranslucency(true);
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
		pending.resolve(nativeState(true));
		await request;
		expect(document.documentElement.dataset.windowMaterial).toBe("acrylic");
		await module.applyWindowTranslucency(true);
		expect(set).toHaveBeenCalledTimes(2);
		await module.applyWindowTranslucency(false);
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
	});

	it.each([false, true])("preserves macOS's synchronous first paint for preference %s and reconciles suppression", async (enabled) => {
		vi.mocked(isMacPlatform).mockReturnValue(true);
		vi.mocked(isWindowsPlatform).mockReturnValue(false);
		module.initializeWindowTranslucency(enabled);
		expect(document.documentElement.dataset.windowTranslucent).toBe(enabled ? "true" : undefined);
		expect(document.documentElement.dataset.windowMaterial).toBe(enabled ? "vibrancy" : undefined);
		vi.spyOn(aoBridge.theme, "setWindowTranslucent").mockResolvedValue({ supported: true, enabled: false, reason: "system" });
		await module.applyWindowTranslucency(enabled);
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
		module.initializeWindowTranslucency(true);
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
	});

	it.each(["unsupported", "system", "error"] as const)("stays opaque when main reports %s without altering the preference", async (reason) => {
		window.localStorage.setItem(module.windowTranslucencyStorageKey, "true");
		vi.spyOn(aoBridge.theme, "setWindowTranslucent").mockResolvedValue({ supported: reason !== "unsupported", enabled: false, reason });
		await module.applyWindowTranslucency(true);
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
		expect(window.localStorage.getItem(module.windowTranslucencyStorageKey)).toBe("true");
	});

	it("ignores a stale enable response after a newer disable request", async () => {
		const first = deferredState();
		vi.spyOn(aoBridge.theme, "setWindowTranslucent").mockReturnValueOnce(first.promise).mockResolvedValue(nativeState(false));
		const old = module.applyWindowTranslucency(true);
		await module.applyWindowTranslucency(false);
		first.resolve(nativeState(true));
		await old;
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
	});

	it("does not repaint an old enable push while a newer disable is in flight", async () => {
		let receive!: (next: WindowTransparencyState) => void;
		vi.spyOn(aoBridge.theme, "onWindowTransparency").mockImplementation((listener) => { receive = listener; return vi.fn(); });
		const on = deferredState();
		const off = deferredState();
		vi.spyOn(aoBridge.theme, "setWindowTranslucent").mockReturnValueOnce(on.promise).mockReturnValueOnce(off.promise);
		const first = module.applyWindowTranslucency(true);
		const second = module.applyWindowTranslucency(false);
		receive(nativeState(true));
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
		receive(nativeState(false));
		off.resolve(nativeState(false));
		await second;
		on.resolve(nativeState(true));
		await first;
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
	});

	it("rapid on/off/on changes publish only the latest accepted native appearance", async () => {
		let receive!: (next: WindowTransparencyState) => void;
		vi.spyOn(aoBridge.theme, "onWindowTransparency").mockImplementation((listener) => { receive = listener; return vi.fn(); });
		const calls = [deferredState(), deferredState(), deferredState()];
		const set = vi.spyOn(aoBridge.theme, "setWindowTranslucent");
		for (const call of calls) set.mockReturnValueOnce(call.promise);
		const requests = [module.applyWindowTranslucency(true), module.applyWindowTranslucency(false), module.applyWindowTranslucency(true)];
		for (const enabled of [true, false, true]) {
			receive(nativeState(enabled));
			expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
		}
		calls[2].resolve(nativeState(true));
		await requests[2];
		expect(document.documentElement.dataset.windowTranslucent).toBe("true");
		calls[0].resolve(nativeState(true));
		calls[1].resolve(nativeState(false));
		await Promise.all(requests);
		expect(document.documentElement.dataset.windowTranslucent).toBe("true");
	});

	it("immediately follows live suppression during a pending setter and does not restore its stale response", async () => {
		let receive!: (next: WindowTransparencyState) => void;
		vi.spyOn(aoBridge.theme, "onWindowTransparency").mockImplementation((listener) => { receive = listener; return vi.fn(); });
		const pending = deferredState();
		vi.spyOn(aoBridge.theme, "setWindowTranslucent").mockResolvedValueOnce(nativeState(true)).mockReturnValueOnce(pending.promise);
		window.localStorage.setItem(module.windowTranslucencyStorageKey, "true");
		await module.applyWindowTranslucency(true);
		const request = module.applyWindowTranslucency(true);
		receive({ supported: true, enabled: false, reason: "system" });
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
		pending.resolve(nativeState(true));
		await request;
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
		receive(nativeState(true));
		expect(document.documentElement.dataset.windowTranslucent).toBe("true");
		expect(window.localStorage.getItem(module.windowTranslucencyStorageKey)).toBe("true");
	});

	it.each([false, true])("allows startup-error retry when capability refresh succeeds: %s", async (readSucceeds) => {
		const set = vi.spyOn(aoBridge.theme, "setWindowTranslucent").mockRejectedValueOnce(new Error("IPC unavailable"));
		const get = vi.spyOn(aoBridge.theme, "getWindowTransparency");
		if (readSucceeds) get.mockResolvedValue(nativeState(false));
		else get.mockRejectedValue(new Error("IPC unavailable"));
		const { result } = renderHook(module.useWindowTransparency);
		await act(() => module.applyWindowTranslucency(true));
		expect(result.current).toEqual({ supported: readSucceeds ? true : null, enabled: false, reason: "error" });
		set.mockResolvedValue(nativeState(true));
		await act(() => module.applyWindowTranslucency(true));
		expect(result.current).toEqual(nativeState(true));
	});

	it("retains one native subscription and disposes it with outstanding requests on hot replacement", async () => {
		let receive!: (next: WindowTransparencyState) => void;
		const unsubscribe = vi.fn();
		const listen = vi.spyOn(aoBridge.theme, "onWindowTransparency").mockImplementation((listener) => { receive = listener; return unsubscribe; });
		const pending = deferredState();
		vi.spyOn(aoBridge.theme, "setWindowTranslucent").mockResolvedValueOnce(nativeState(false)).mockReturnValueOnce(pending.promise);
		await module.applyWindowTranslucency(false);
		const request = module.applyWindowTranslucency(true);
		expect(listen).toHaveBeenCalledTimes(1);
		module.disposeWindowTranslucency();
		expect(unsubscribe).toHaveBeenCalledTimes(1);
		receive(nativeState(true));
		pending.resolve(nativeState(true));
		await request;
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
	});
});