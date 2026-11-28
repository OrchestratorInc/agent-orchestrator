import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "./bridge";
import { themeStorageKey } from "./theme";
import { isMacPlatform, isWindowsPlatform } from "./platform";
import type { WindowTransparencyState } from "../../shared/window-transparency";

vi.mock("./platform", () => ({ isMacPlatform: vi.fn(() => false), isWindowsPlatform: vi.fn(() => true) }));

describe("apply-initial-theme", () => {
	beforeEach(() => {
		vi.resetModules();
		vi.mocked(isMacPlatform).mockReturnValue(false);
		vi.mocked(isWindowsPlatform).mockReturnValue(true);
	});
	afterEach(async () => {
		(await import("./window-translucency")).disposeWindowTranslucency();
		window.localStorage.clear();
		delete document.documentElement.dataset.theme;
		delete document.documentElement.dataset.windowTranslucent;
		delete document.documentElement.dataset.windowMaterial;
		vi.restoreAllMocks();
		vi.resetModules();
	});

	it("sends the stored theme preference to main before the shell mounts", async () => {
		// The vibrancy material follows nativeTheme, so the startup loader needs
		// main to know the app theme before the shell's own theme effect runs.
		window.localStorage.setItem(themeStorageKey, "light");
		const setTheme = vi.spyOn(aoBridge.theme, "set");

		await import("./apply-initial-theme");

		expect(document.documentElement.dataset.theme).toBe("light");
		expect(setTheme).toHaveBeenCalledWith("light");
	});

	it.each(["mac", "windows"] as const)("paints %s before the native reply and reconciles system suppression", async (platform) => {
		vi.mocked(isMacPlatform).mockReturnValue(platform === "mac");
		vi.mocked(isWindowsPlatform).mockReturnValue(platform === "windows");
		window.localStorage.setItem("ao.windowTranslucency", "true");
		let resolve!: (state: WindowTransparencyState) => void;
		const pending = new Promise<WindowTransparencyState>((done) => { resolve = done; });
		const setTheme = vi.spyOn(aoBridge.theme, "set");
		const setTransparency = vi.spyOn(aoBridge.theme, "setWindowTranslucent").mockReturnValue(pending);
		await import("./apply-initial-theme");
		expect(document.documentElement.dataset.windowTranslucent).toBe(platform === "mac" ? "true" : undefined);
		expect(document.documentElement.dataset.windowMaterial).toBe(platform === "mac" ? "vibrancy" : undefined);
		expect(setTheme.mock.invocationCallOrder[0]).toBeLessThan(setTransparency.mock.invocationCallOrder[0]!);
		resolve({ supported: true, enabled: false, reason: "system" });
		await pending;
		await Promise.resolve();
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
	});

	it("keeps a saved macOS opt-out opaque on first paint", async () => {
		vi.mocked(isMacPlatform).mockReturnValue(true);
		vi.mocked(isWindowsPlatform).mockReturnValue(false);
		window.localStorage.setItem("ao.windowTranslucency", "false");
		vi.spyOn(aoBridge.theme, "setWindowTranslucent").mockReturnValue(new Promise(() => {}));
		await import("./apply-initial-theme");
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
	});
});
