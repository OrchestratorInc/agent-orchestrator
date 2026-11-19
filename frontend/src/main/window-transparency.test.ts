import { afterEach, describe, expect, it, vi } from "vitest";
import { supportsWindowTransparency, WindowTransparency } from "./window-transparency";

const appearance = { dark: true, reducedTransparency: false, highContrast: false };
const host = () => ({ isDestroyed: () => false, setBackgroundColor: vi.fn(), setBackgroundMaterial: vi.fn(), setVibrancy: vi.fn() });

describe("native window transparency", () => {
	afterEach(() => vi.restoreAllMocks());

	it.each([
		["win32", "10.0.19045", false], ["win32", "10.0.22000", false],
		["win32", "10.0.22621", true], ["win32", "10.0.26300", true],
		["win32", "invalid", false], ["linux", "6.8.0", false], ["darwin", "25.0.0", true],
	] as const)("checks %s %s capability", (platform, release, supported) => {
		expect(supportsWindowTransparency(platform, release)).toBe(supported);
	});

	it("leaves fresh and default-off Windows material untouched through reloads, resizes, and appearance changes", () => {
		const native = new WindowTransparency("win32", "10.0.22621");
		const window = host();
		native.sync(window, "#123456", appearance, true);
		native.request(false);
		native.sync(window, "#123456", appearance, true);
		native.sync(window, "#123456", appearance);
		native.sync(window, "#ffffff", { ...appearance, dark: false });
		native.sync(window, "#123456", appearance, true);
		expect(window.setBackgroundMaterial).not.toHaveBeenCalled();
		expect(window.setBackgroundColor).toHaveBeenLastCalledWith("#123456");
		const reopened = host();
		native.sync(reopened, "#123456", appearance, true);
		expect(reopened.setBackgroundMaterial).not.toHaveBeenCalled();
	});

	it("does not clear Windows material when an initial enable request is system-suppressed", () => {
		const native = new WindowTransparency("win32", "10.0.22621");
		const window = host();
		native.request(true);
		expect(native.sync(window, "#123456", { ...appearance, reducedTransparency: true }, true).reason).toBe("system");
		expect(window.setBackgroundMaterial).not.toHaveBeenCalled();
		native.sync(window, "#123456", appearance);
		expect(window.setBackgroundMaterial).toHaveBeenCalledExactlyOnceWith("acrylic");
	});

	it("opts in to Acrylic and restores the current palette when disabled", () => {
		const native = new WindowTransparency("win32", "10.0.22621");
		const window = host();
		expect(native.sync(window, "#123456", appearance).enabled).toBe(false);
		native.request(true);
		expect(native.sync(window, "#123456", appearance).enabled).toBe(true);
		expect(window.setBackgroundColor).toHaveBeenLastCalledWith("#00000000");
		expect(window.setBackgroundMaterial).toHaveBeenLastCalledWith("acrylic");
		expect(window.setVibrancy).not.toHaveBeenCalled();
		native.request(false);
		native.sync(window, "#abcdef", appearance);
		expect(window.setBackgroundColor).toHaveBeenLastCalledWith("#abcdef");
		expect(window.setBackgroundMaterial.mock.calls).toEqual([["acrylic"], ["none"]]);
		native.sync(window, "#abcdef", appearance, true);
		native.request(false);
		native.sync(window, "#ffffff", { ...appearance, dark: false }, true);
		expect(window.setBackgroundMaterial).toHaveBeenCalledTimes(2);
		const reopened = host();
		native.sync(reopened, "#ffffff", appearance, true);
		expect(reopened.setBackgroundMaterial).not.toHaveBeenCalled();
	});

	it("keeps unsupported Windows builds opaque despite a saved request", () => {
		const native = new WindowTransparency("win32", "10.0.22000");
		const window = host();
		native.request(true);
		expect(native.sync(window, "#abcdef", appearance)).toEqual({ supported: false, enabled: false, reason: "unsupported" });
		expect(window.setBackgroundMaterial).not.toHaveBeenCalled();
		expect(window.setBackgroundColor).toHaveBeenCalledWith("#abcdef");
	});

	it.each(["reducedTransparency", "highContrast"] as const)("suppresses and resumes the saved preference for %s", (key) => {
		const native = new WindowTransparency("win32", "10.0.26300");
		const window = host();
		native.request(true);
		native.sync(window, "#123456", appearance);
		expect(native.sync(window, "#abcdef", { ...appearance, [key]: true })).toEqual({ supported: true, enabled: false, reason: "system" });
		expect(window.setBackgroundColor).toHaveBeenLastCalledWith("#abcdef");
		native.sync(window, "#abcdef", { ...appearance, [key]: true }, true);
		expect(window.setBackgroundMaterial.mock.calls).toEqual([["acrylic"], ["none"]]);
		expect(native.sync(window, "#abcdef", appearance).enabled).toBe(true);
		expect(window.setBackgroundMaterial.mock.calls).toEqual([["acrylic"], ["none"], ["acrylic"]]);
	});

	it("skips resize duplicates, forces reloads, and initializes recreated windows", () => {
		const native = new WindowTransparency("win32", "10.0.26300");
		const window = host();
		native.request(true);
		native.sync(window, "#123456", appearance);
		native.sync(window, "#123456", appearance);
		expect(window.setBackgroundMaterial).toHaveBeenCalledTimes(1);
		native.sync(window, "#123456", appearance, true);
		expect(window.setBackgroundMaterial).toHaveBeenCalledTimes(2);
		native.sync(window, "#ffffff", { ...appearance, dark: false });
		expect(window.setBackgroundMaterial).toHaveBeenCalledTimes(3);
		const reopened = host();
		native.sync(reopened, "#ffffff", appearance);
		expect(reopened.setBackgroundMaterial).toHaveBeenCalledWith("acrylic");
	});

	it("falls back after native failure and only retries a new request", () => {
		vi.spyOn(console, "warn").mockImplementation(() => undefined);
		const native = new WindowTransparency("win32", "10.0.26300");
		const window = host();
		window.setBackgroundMaterial.mockImplementationOnce(() => { throw new Error("DWM unavailable"); });
		native.request(true);
		expect(native.sync(window, "#abcdef", appearance).reason).toBe("error");
		expect(window.setBackgroundColor).toHaveBeenLastCalledWith("#abcdef");
		expect(window.setBackgroundMaterial.mock.calls).toEqual([["acrylic"], ["none"]]);
		expect(native.sync(window, "#abcdef", appearance, true).enabled).toBe(false);
		expect(window.setBackgroundMaterial).toHaveBeenCalledTimes(2);
		native.request(true);
		expect(native.sync(window, "#abcdef", appearance).enabled).toBe(true);
		expect(window.setBackgroundMaterial).toHaveBeenLastCalledWith("acrylic");
	});

	it("retains Windows material ownership when failure cleanup throws and retries cleanup on the next sync", () => {
		vi.spyOn(console, "warn").mockImplementation(() => undefined);
		const native = new WindowTransparency("win32", "10.0.26300");
		const window = host();
		window.setBackgroundMaterial
			.mockImplementationOnce(() => { throw new Error("Partial enable failure"); })
			.mockImplementationOnce(() => { throw new Error("Cleanup unavailable"); });
		native.request(true);
		expect(native.sync(window, "#abcdef", appearance).reason).toBe("error");
		expect(window.setBackgroundColor).toHaveBeenLastCalledWith("#abcdef");
		native.sync(window, "#abcdef", appearance, true);
		expect(window.setBackgroundMaterial.mock.calls).toEqual([["acrylic"], ["none"], ["none"]]);
		native.sync(window, "#abcdef", appearance, true);
		expect(window.setBackgroundMaterial).toHaveBeenCalledTimes(3);
		native.request(true);
		const reopened = host();
		expect(native.sync(reopened, "#abcdef", appearance, true).enabled).toBe(true);
		expect(reopened.setBackgroundMaterial).toHaveBeenCalledExactlyOnceWith("acrylic");
	});

	it("does not touch fresh Windows material when painting its default opaque background fails", () => {
		vi.spyOn(console, "warn").mockImplementation(() => undefined);
		const native = new WindowTransparency("win32", "10.0.26300");
		const window = host();
		window.setBackgroundColor.mockImplementationOnce(() => { throw new Error("Background unavailable"); });
		expect(native.sync(window, "#abcdef", appearance, true).reason).toBe("error");
		expect(window.setBackgroundColor).toHaveBeenLastCalledWith("#abcdef");
		expect(window.setBackgroundMaterial).not.toHaveBeenCalled();
	});

	it("still removes owned Windows material if painting the opaque fallback throws", () => {
		vi.spyOn(console, "warn").mockImplementation(() => undefined);
		const native = new WindowTransparency("win32", "10.0.26300");
		const window = host();
		native.request(true);
		native.sync(window, "#abcdef", appearance);
		window.setBackgroundColor.mockImplementation(() => { throw new Error("Background unavailable"); });
		native.request(false);
		expect(native.sync(window, "#abcdef", appearance).enabled).toBe(false);
		expect(window.setBackgroundMaterial.mock.calls).toEqual([["acrylic"], ["none"]]);
	});

	it("preserves macOS vibrancy without Windows material calls", () => {
		const native = new WindowTransparency("darwin", "25.0.0");
		const window = host();
		native.request(true);
		native.sync(window, "#123456", appearance);
		expect(window.setVibrancy).toHaveBeenCalledWith("under-window");
		native.sync(window, "#123456", appearance, true);
		expect(window.setVibrancy).toHaveBeenCalledTimes(2);
		native.sync(window, "#123456", { ...appearance, highContrast: true });
		expect(window.setVibrancy).toHaveBeenLastCalledWith(null);
		native.sync(window, "#123456", appearance);
		expect(window.setVibrancy).toHaveBeenLastCalledWith("under-window");
		native.request(false);
		native.sync(window, "#123456", appearance, true);
		native.sync(window, "#123456", appearance, true);
		expect(window.setVibrancy).toHaveBeenCalledTimes(6);
		expect(window.setVibrancy).toHaveBeenLastCalledWith(null);
		expect(window.setBackgroundMaterial).not.toHaveBeenCalled();
	});
});
