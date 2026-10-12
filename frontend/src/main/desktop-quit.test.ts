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
