import { afterEach, describe, expect, it, vi } from "vitest";
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

function setupDock(overrides: Partial<QuitOptions> = {}) {
	let visible = true;
	const dock = {
		hide: vi.fn(() => { visible = false; }),
		show: vi.fn(async () => { visible = true; }),
		isVisible: () => visible,
	};
	return { dock, ...setup({ dock, ...overrides }) };
}

afterEach(() => vi.useRealTimers());

describe("desktop quit", () => {
	it("does not hide the Dock when the draft guard cancels window close", () => {
		const { dock, controller, event } = setupDock();
		controller.handleBeforeQuit(event);
		expect(dock.hide).not.toHaveBeenCalled();
		expect(dock.isVisible()).toBe(true);
	});

	it("hides the Dock only after the window closes, without quitting the background owner", () => {
		const { dock, options, controller, event } = setupDock();
		controller.handleBeforeQuit(event);
		controller.handleWindowClosed();
		expect(dock.hide).toHaveBeenCalledOnce();
		expect(dock.isVisible()).toBe(false);
		expect(options.quit).not.toHaveBeenCalled();
	});

	it("restores the Dock before reopening and skips redundant shows", async () => {
		const { dock, controller } = setupDock();
		await controller.beforeWindowShow();
		expect(dock.show).not.toHaveBeenCalled();
		controller.handleWindowClosed();
		await controller.beforeWindowShow();
		expect(dock.show).toHaveBeenCalledOnce();
		expect(dock.isVisible()).toBe(true);
	});

	it("waits for one in-flight Dock show and allows retry after failure", async () => {
		const { dock, controller } = setupDock();
		controller.handleWindowClosed();
		let rejectShow!: (reason: Error) => void;
		dock.show.mockImplementationOnce(() => new Promise<void>((_resolve, reject) => { rejectShow = reject; }));
		const first = controller.beforeWindowShow();
		const second = controller.beforeWindowShow();
		expect(dock.show).toHaveBeenCalledOnce();
		const failures = Promise.all([expect(first).rejects.toThrow("show failed"), expect(second).rejects.toThrow("show failed")]);
		rejectShow(new Error("show failed"));
		await failures;
		await controller.beforeWindowShow();
		expect(dock.show).toHaveBeenCalledTimes(2);
	});

	it("retries once when Electron ignores a hide immediately after Dock show", () => {
		vi.useFakeTimers();
		const { dock, controller } = setupDock();
		dock.hide.mockImplementationOnce(() => {});
		controller.handleWindowClosed();
		expect(dock.isVisible()).toBe(true);
		vi.advanceTimersByTime(1100);
		expect(dock.hide).toHaveBeenCalledTimes(2);
		expect(dock.isVisible()).toBe(false);
		expect(vi.getTimerCount()).toBe(0);
	});

	it("does not arm a retry when the Dock hides immediately", () => {
		vi.useFakeTimers();
		const { controller } = setupDock();
		controller.handleWindowClosed();
		expect(vi.getTimerCount()).toBe(0);
	});

	it("cancels a pending Dock hide when reopening, even if it is already visible", async () => {
		vi.useFakeTimers();
		const { dock, controller } = setupDock();
		dock.hide.mockImplementationOnce(() => {});
		controller.handleWindowClosed();
		await controller.beforeWindowShow();
		vi.advanceTimersByTime(1100);
		expect(dock.hide).toHaveBeenCalledOnce();
		expect(dock.isVisible()).toBe(true);
	});

	it.each(["full quit", "update restart", "tray removed"])("cancels or skips a pending hide on %s", (reason) => {
		vi.useFakeTimers();
		let restarting = false;
		let hasTray = true;
		const { dock, controller, event } = setupDock({
			isUpdateRestartRequested: () => restarting,
			hasTray: () => hasTray,
		});
		dock.hide.mockImplementationOnce(() => {});
		controller.handleWindowClosed();
		if (reason === "full quit") controller.quitCompletely();
		if (reason === "update restart") {
			restarting = true;
			controller.handleBeforeQuit(event);
		}
		if (reason === "tray removed") hasTray = false;
		vi.advanceTimersByTime(1100);
		expect(dock.hide).toHaveBeenCalledOnce();
	});

	it.each<NodeJS.Platform>(["win32", "linux"])("does not change Dock state on %s", async (platform) => {
		const { dock, controller } = setupDock({ platform });
		controller.handleWindowClosed();
		await controller.beforeWindowShow();
		expect(dock.hide).not.toHaveBeenCalled();
		expect(dock.show).not.toHaveBeenCalled();
	});

	it("does not hide the only launcher when no tray is available", () => {
		const { dock, controller } = setupDock({ hasTray: () => false });
		controller.handleWindowClosed();
		expect(dock.hide).not.toHaveBeenCalled();
	});

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
