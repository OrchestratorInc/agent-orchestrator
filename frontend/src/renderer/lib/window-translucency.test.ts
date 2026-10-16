import { afterEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "./bridge";
import { isMacPlatform } from "./platform";
import {
	applyWindowTranslucency,
	developerModeStorageKey,
	readStoredWindowTranslucency,
	windowTranslucencyStorageKey,
} from "./window-translucency";

vi.mock("./platform", () => ({ isMacPlatform: vi.fn(() => true) }));

describe("window translucency", () => {
	afterEach(() => {
		window.localStorage.clear();
		delete document.documentElement.dataset.windowTranslucent;
		vi.mocked(isMacPlatform).mockReturnValue(true);
		vi.restoreAllMocks();
	});

	it("is on only when Developer Mode and the flag are both saved on macOS", () => {
		window.localStorage.setItem(windowTranslucencyStorageKey, "true");
		expect(readStoredWindowTranslucency()).toBe(false);

		window.localStorage.setItem(developerModeStorageKey, "true");
		expect(readStoredWindowTranslucency()).toBe(true);

		vi.mocked(isMacPlatform).mockReturnValue(false);
		expect(readStoredWindowTranslucency()).toBe(false);
	});

	it("marks the document and asks main for vibrancy every time", () => {
		const setWindowTranslucent = vi.spyOn(aoBridge.theme, "setWindowTranslucent");

		applyWindowTranslucency(true);
		applyWindowTranslucency(true);
		expect(document.documentElement.dataset.windowTranslucent).toBe("true");
		expect(setWindowTranslucent).toHaveBeenNthCalledWith(1, true);
		expect(setWindowTranslucent).toHaveBeenNthCalledWith(2, true);

		applyWindowTranslucency(false);
		expect(document.documentElement.dataset.windowTranslucent).toBeUndefined();
		expect(setWindowTranslucent).toHaveBeenLastCalledWith(false);
	});
});
