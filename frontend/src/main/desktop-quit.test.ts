import { describe, expect, it, vi } from "vitest";
import { createDesktopQuitController } from "./desktop-quit";

type QuitOptions = Parameters<typeof createDesktopQuitController>[0];

function setup(overrides: Partial<QuitOptions> = {}) {
	const options: QuitOptions = {
		platform: "darwin",
		hasTray: () => true,
		isUpdateRestartRequested: () => false,
		closeWindow: vi.fn(),
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

	it("keeps repeated quits in the background even when the window is already gone", () => {
		const { options, controller, event } = setup();
		controller.handleBeforeQuit(event);
		controller.handleBeforeQuit(event);
		expect(event.preventDefault).toHaveBeenCalledTimes(2);
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
