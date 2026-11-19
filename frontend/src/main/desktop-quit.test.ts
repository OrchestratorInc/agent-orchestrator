import { describe, expect, it, vi } from "vitest";
import { createDesktopQuitController } from "./desktop-quit";

type QuitOptions = Parameters<typeof createDesktopQuitController>[0];

function setup(overrides: Partial<QuitOptions> = {}) {
	const options: QuitOptions = {
		platform: "darwin",
		hasTray: () => true,
		isUpdateRestartRequested: () => false,
		hasWindow: () => true,
		closeWindow: vi.fn(),
		hideDock: vi.fn(),
		quit: vi.fn(),
		...overrides,
	};
	return { options, controller: createDesktopQuitController(options), event: { preventDefault: vi.fn() } };
}

describe("desktop quit", () => {
	it("backgrounds an ordinary Windows Quit without touching the Dock", () => {
		const { options, controller, event } = setup({ platform: "win32" });
		vi.mocked(options.quit).mockImplementation(() => {
			expect(controller.handleBeforeQuit(event)).toBe(true);
		});
		controller.backgroundQuit();
		controller.windowClosed();
		expect(options.closeWindow).toHaveBeenCalledOnce();
		expect(options.hideDock).not.toHaveBeenCalled();
		expect(event.preventDefault).toHaveBeenCalledOnce();
		// An external quit or cleanup continuation is a separate request.
		expect(controller.handleBeforeQuit({ preventDefault: vi.fn() })).toBe(false);
	});

	it("keeps Windows backgrounded when ordinary Quit has no window", () => {
		const { options, controller, event } = setup({ platform: "win32", hasWindow: () => false });
		vi.mocked(options.quit).mockImplementation(() => {
			expect(controller.handleBeforeQuit(event)).toBe(true);
		});
		controller.backgroundQuit();
		expect(options.closeWindow).not.toHaveBeenCalled();
		expect(options.hideDock).not.toHaveBeenCalled();
	});

	it("clears Windows background intent even if Quit throws", () => {
		const { options, controller, event } = setup({ platform: "win32" });
		vi.mocked(options.quit).mockImplementation(() => { throw new Error("quit failed"); });
		expect(() => controller.backgroundQuit()).toThrow("quit failed");
		expect(controller.handleBeforeQuit(event)).toBe(false);
	});

	it.each(["no tray", "update", "full quit", "linux"])("does not background ordinary Quit during %s", (reason) => {
		const { options, controller, event } = setup({
			platform: reason === "linux" ? "linux" : "win32",
			hasTray: () => reason !== "no tray",
			isUpdateRestartRequested: () => reason === "update",
		});
		if (reason === "full quit") controller.quitCompletely();
		vi.mocked(options.quit).mockImplementation(() => {
			expect(controller.handleBeforeQuit(event)).toBe(false);
		});
		controller.backgroundQuit();
		expect(event.preventDefault).not.toHaveBeenCalled();
	});

	it("restores Windows close-to-tray after canceled full quit and draft close", () => {
		const { options, controller, event } = setup({ platform: "win32" });
		controller.quitCompletely();
		controller.cancelQuit();
		vi.mocked(options.quit).mockImplementation(() => {
			expect(controller.handleBeforeQuit(event)).toBe(true);
			controller.windowCloseCanceled();
		});
		controller.backgroundQuit();
		controller.backgroundQuit();
		expect(options.closeWindow).toHaveBeenCalledTimes(2);
		expect(controller.handleBeforeQuit(event)).toBe(false);
	});

	it("closes the macOS window without quitting the tray or daemon owner", () => {
		const { options, controller, event } = setup();
		expect(controller.handleBeforeQuit(event)).toBe(true);
		expect(event.preventDefault).toHaveBeenCalledOnce();
		expect(options.closeWindow).toHaveBeenCalledOnce();
		expect(options.quit).not.toHaveBeenCalled();
	});

	it("leaves the Dock once the quit-closed window is gone", () => {
		const { options, controller, event } = setup();
		controller.handleBeforeQuit(event);
		expect(options.hideDock).not.toHaveBeenCalled();
		controller.windowClosed();
		expect(options.hideDock).toHaveBeenCalledOnce();
	});

	it("keeps the Dock icon when the draft guard cancels the close", () => {
		const { options, controller, event } = setup();
		controller.handleBeforeQuit(event);
		controller.windowCloseCanceled();
		controller.windowClosed();
		expect(options.hideDock).not.toHaveBeenCalled();
	});

	it("keeps the Dock icon when the window is closed without quitting", () => {
		const { options, controller } = setup();
		controller.windowClosed();
		expect(options.hideDock).not.toHaveBeenCalled();
	});

	it("leaves the Dock right away when quitting with no window, without exiting", () => {
		const { options, controller, event } = setup({ hasWindow: () => false });
		expect(controller.handleBeforeQuit(event)).toBe(true);
		expect(event.preventDefault).toHaveBeenCalledOnce();
		expect(options.hideDock).toHaveBeenCalledOnce();
		expect(options.closeWindow).not.toHaveBeenCalled();
		expect(options.quit).not.toHaveBeenCalled();
	});

	it("allows explicit full quit and its cleanup continuation", () => {
		const { options, controller, event } = setup();
		controller.quitCompletely();
		expect(options.quit).toHaveBeenCalledOnce();
		expect(controller.handleBeforeQuit(event)).toBe(false);
		expect(controller.handleBeforeQuit(event)).toBe(false);
		expect(event.preventDefault).not.toHaveBeenCalled();
		expect(options.closeWindow).not.toHaveBeenCalled();
	});

	it("restores ordinary background quit after a full-quit draft warning is canceled", () => {
		const { options, controller, event } = setup();
		controller.quitCompletely();
		controller.cancelQuit();
		expect(controller.handleBeforeQuit(event)).toBe(true);
		expect(options.closeWindow).toHaveBeenCalledOnce();
	});

	it("allows update restart, then backgrounds again if the update is canceled", () => {
		let restarting = true;
		const { controller, event } = setup({ isUpdateRestartRequested: () => restarting });
		expect(controller.handleBeforeQuit(event)).toBe(false);
		restarting = false;
		expect(controller.handleBeforeQuit(event)).toBe(true);
	});

	it.each<NodeJS.Platform>(["win32", "linux"])("keeps %s quit behavior unchanged", (platform) => {
		const { controller, event } = setup({ platform });
		expect(controller.handleBeforeQuit(event)).toBe(false);
		expect(event.preventDefault).not.toHaveBeenCalled();
	});

	it("really exits if the tray could not be created, including early startup", () => {
		const { controller, event } = setup({ hasTray: () => false });
		expect(controller.handleBeforeQuit(event)).toBe(false);
		expect(event.preventDefault).not.toHaveBeenCalled();
	});

	it("does not intercept a full-quit cleanup continuation after background eligibility is revoked", () => {
		let canBackground = true;
		const { controller, event } = setup({ hasTray: () => canBackground });
		canBackground = false;
		expect(controller.handleBeforeQuit(event)).toBe(false);
		expect(event.preventDefault).not.toHaveBeenCalled();
	});
});
