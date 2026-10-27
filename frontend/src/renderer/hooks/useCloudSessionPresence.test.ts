import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PRESENCE_INTERVAL_MS, useCloudSessionPresence } from "./useCloudSessionPresence";

const touch = vi.hoisted(() => vi.fn());
vi.mock("./useCloudCp", () => ({
	useCloudCp: () => ({ client: { touchSessionPresence: touch }, ready: true, baseUrl: "https://cp.test" }),
}));

describe("useCloudSessionPresence", () => {
	beforeEach(() => {
		vi.useFakeTimers();
		touch.mockReset().mockResolvedValue(undefined);
		vi.spyOn(document, "hasFocus").mockReturnValue(true);
	});
	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
	});

	it("beats on mount and every interval while the window is focused", () => {
		const { unmount } = renderHook(() => useCloudSessionPresence("org-1", "session-1", true));
		expect(touch).toHaveBeenCalledTimes(1);
		expect(touch).toHaveBeenLastCalledWith("org-1", "session-1", expect.anything());
		vi.advanceTimersByTime(PRESENCE_INTERVAL_MS);
		expect(touch).toHaveBeenCalledTimes(2);
		unmount();
		vi.advanceTimersByTime(PRESENCE_INTERVAL_MS * 3);
		expect(touch).toHaveBeenCalledTimes(2);
	});

	it("stays quiet when the window is not focused or the session is not cloud", () => {
		vi.mocked(document.hasFocus).mockReturnValue(false);
		const unfocused = renderHook(() => useCloudSessionPresence("org-1", "session-1", true));
		vi.advanceTimersByTime(PRESENCE_INTERVAL_MS * 2);
		expect(touch).not.toHaveBeenCalled();
		unfocused.unmount();

		vi.mocked(document.hasFocus).mockReturnValue(true);
		renderHook(() => useCloudSessionPresence(undefined, undefined, false));
		vi.advanceTimersByTime(PRESENCE_INTERVAL_MS);
		expect(touch).not.toHaveBeenCalled();
	});
});
