import { afterEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "./bridge";
import { themeStorageKey } from "./theme";

describe("apply-initial-theme", () => {
	afterEach(() => {
		window.localStorage.clear();
		delete document.documentElement.dataset.theme;
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
});
