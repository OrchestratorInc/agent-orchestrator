import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { subscribeBrowserOverlaySync } from "./browser-overlay-sync";

describe("subscribeBrowserOverlaySync", () => {
	let setOverlayOpen: ReturnType<typeof vi.fn>;
	let original: typeof window.ao;

	beforeEach(() => {
		original = window.ao;
		setOverlayOpen = vi.fn();
		window.ao = { ...(original ?? {}), browser: { ...(original?.browser ?? {}), setOverlayOpen } } as typeof window.ao;
	});

	afterEach(() => {
		document.body.innerHTML = "";
		window.ao = original;
	});

	const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

	it("keeps the shell raised until the last browser subscriber leaves", async () => {
		const first = subscribeBrowserOverlaySync();
		const second = subscribeBrowserOverlaySync();
		const dialog = document.createElement("div");
		dialog.setAttribute("role", "dialog");
		dialog.setAttribute("data-state", "open");
		document.body.append(dialog);
		await flush();
		expect(setOverlayOpen).toHaveBeenLastCalledWith(true);

		// One browser hook unmounting must not lower the shell under the dialog.
		first();
		first();
		expect(setOverlayOpen).not.toHaveBeenCalledWith(false);

		dialog.setAttribute("data-state", "closed");
		await flush();
		expect(setOverlayOpen).toHaveBeenLastCalledWith(false);

		dialog.setAttribute("data-state", "open");
		await flush();
		expect(setOverlayOpen).toHaveBeenLastCalledWith(true);

		second();
		expect(setOverlayOpen).toHaveBeenLastCalledWith(false);
	});

	it("raises the shell for an overlay that was already open on subscribe", () => {
		const notice = document.createElement("aside");
		notice.setAttribute("data-browser-native-overlay", "true");
		notice.setAttribute("data-state", "open");
		document.body.append(notice);
		const unsubscribe = subscribeBrowserOverlaySync();
		expect(setOverlayOpen).toHaveBeenCalledExactlyOnceWith(true);
		unsubscribe();
	});
});
