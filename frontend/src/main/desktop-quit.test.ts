import { afterEach, describe, expect, it, vi } from "vitest";
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
		exit: vi.fn(),
		externalQuitStarted: vi.fn(),
		...overrides,
	};
	const event = { preventDefault: vi.fn() };
	const controller = createDesktopQuitController(options);
	// app.quit() emits before-quit synchronously, so the in-app Quit is modeled
	// as backgroundQuit() delivering the event to handleBeforeQuit.
	let handled: boolean | undefined;
	options.quit = vi.fn(() => { handled = controller.handleBeforeQuit(event); });
	const inAppQuit = () => { handled = undefined; controller.backgroundQuit(); return handled; };
	return { options, controller, event, inAppQuit };
}

afterEach(() => { vi.useRealTimers(); });

describe("desktop quit", () => {
	it("closes the macOS window on AO's own Quit without quitting the tray or daemon owner", () => {
		const { options, event, inAppQuit } = setup();
		expect(inAppQuit()).toBe(true);
		expect(event.preventDefault).toHaveBeenCalledOnce();
		expect(options.closeWindow).toHaveBeenCalledOnce();
		expect(options.externalQuitStarted).not.toHaveBeenCalled();
		expect(options.exit).not.toHaveBeenCalled();
	});

	it("leaves the Dock once the quit-closed window is gone", () => {
		const { options, controller, inAppQuit } = setup();
		inAppQuit();
		expect(options.hideDock).not.toHaveBeenCalled();
		controller.windowClosed();
		expect(options.hideDock).toHaveBeenCalledOnce();
	});

	it("keeps the Dock icon when the draft guard cancels the close", () => {
		const { options, controller, inAppQuit } = setup();
		inAppQuit();
		controller.windowCloseCanceled();
		controller.windowClosed();
		expect(options.hideDock).not.toHaveBeenCalled();
	});

	it("keeps the Dock icon when the window is closed without quitting", () => {
		const { options, controller } = setup();
		controller.windowClosed();
		expect(options.hideDock).not.toHaveBeenCalled();
	});

	it("leaves the Dock right away when AO's own Quit runs with no window, without exiting", () => {
		const { options, event, inAppQuit } = setup({ hasWindow: () => false });
		expect(inAppQuit()).toBe(true);
		expect(event.preventDefault).toHaveBeenCalledOnce();
		expect(options.hideDock).toHaveBeenCalledOnce();
		expect(options.closeWindow).not.toHaveBeenCalled();
	});

	it("exits on an outside quit (Apple Event, Dock, SIGTERM) and keeps agents", () => {
		const { options, controller, event } = setup();
		expect(controller.handleBeforeQuit(event)).toBe(false);
		expect(event.preventDefault).not.toHaveBeenCalled();
		expect(options.closeWindow).not.toHaveBeenCalled();
		expect(options.hideDock).not.toHaveBeenCalled();
		expect(options.externalQuitStarted).toHaveBeenCalledOnce();
		// The cleanup continuation's app.quit() goes through too.
		expect(controller.handleBeforeQuit(event)).toBe(false);
		expect(options.externalQuitStarted).toHaveBeenCalledOnce();
	});

	it("exits on an outside quit even after AO's own Quit has backgrounded the app", () => {
		const { options, controller, event, inAppQuit } = setup({ hasWindow: () => false });
		expect(inAppQuit()).toBe(true);
		event.preventDefault.mockClear();
		expect(controller.handleBeforeQuit(event)).toBe(false);
		expect(event.preventDefault).not.toHaveBeenCalled();
		expect(options.externalQuitStarted).toHaveBeenCalledOnce();
	});

	it("forces the exit when outside-quit cleanup outlives the deadline", () => {
		vi.useFakeTimers();
		const { options, controller, event } = setup({ externalQuitDeadlineMs: 5_000 });
		controller.handleBeforeQuit(event);
		controller.startCleanupDeadline();
		vi.advanceTimersByTime(4_999);
		expect(options.exit).not.toHaveBeenCalled();
		vi.advanceTimersByTime(1);
		expect(options.exit).toHaveBeenCalledOnce();
	});

	it("gives cleanup the full deadline after a slow draft confirmation", () => {
		vi.useFakeTimers();
		const { options, controller, event } = setup();
		controller.handleBeforeQuit(event);
		vi.advanceTimersByTime(60_000);
		expect(options.exit).not.toHaveBeenCalled();
		controller.handleBeforeQuit(event);
		controller.startCleanupDeadline();
		vi.advanceTimersByTime(4_999);
		expect(options.exit).not.toHaveBeenCalled();
		vi.advanceTimersByTime(1);
		expect(options.exit).toHaveBeenCalledOnce();
	});

	it("does not extend the deadline on cleanup continuations", () => {
		vi.useFakeTimers();
		const { options, controller, event } = setup();
		controller.handleBeforeQuit(event);
		controller.startCleanupDeadline();
		vi.advanceTimersByTime(4_000);
		controller.handleBeforeQuit(event);
		controller.startCleanupDeadline();
		vi.advanceTimersByTime(1_000);
		expect(options.exit).toHaveBeenCalledOnce();
	});

	it("drops the deadline and backgrounds again when the draft guard cancels an outside quit", () => {
		vi.useFakeTimers();
		const { options, controller, event, inAppQuit } = setup();
		controller.handleBeforeQuit(event);
		controller.startCleanupDeadline();
		controller.cancelQuit();
		controller.startCleanupDeadline();
		vi.advanceTimersByTime(60_000);
		expect(options.exit).not.toHaveBeenCalled();
		expect(inAppQuit()).toBe(true);
		expect(options.closeWindow).toHaveBeenCalledOnce();
	});

	it("does not arm a canceled outside quit when later full-quit cleanup starts", () => {
		vi.useFakeTimers();
		const { options, controller, event } = setup();
		controller.handleBeforeQuit(event);
		controller.cancelQuit();
		controller.quitCompletely();
		controller.startCleanupDeadline();
		vi.advanceTimersByTime(60_000);
		expect(options.exit).not.toHaveBeenCalled();
	});

	it("does not leave the in-app mark set when before-quit never ran", () => {
		const { options, controller, event } = setup();
		options.quit = vi.fn();
		controller.backgroundQuit();
		expect(controller.handleBeforeQuit(event)).toBe(false);
		expect(options.externalQuitStarted).toHaveBeenCalledOnce();
	});

	it("allows explicit full quit and its cleanup continuation", () => {
		const { options, controller, event } = setup();
		controller.quitCompletely();
		expect(options.quit).toHaveBeenCalledOnce();
		expect(controller.handleBeforeQuit(event)).toBe(false);
		expect(controller.handleBeforeQuit(event)).toBe(false);
		expect(event.preventDefault).not.toHaveBeenCalled();
		expect(options.closeWindow).not.toHaveBeenCalled();
		expect(options.externalQuitStarted).not.toHaveBeenCalled();
	});

	it("restores ordinary background quit after a full-quit draft warning is canceled", () => {
		const { options, controller, inAppQuit } = setup();
		controller.quitCompletely();
		controller.cancelQuit();
		expect(inAppQuit()).toBe(true);
		expect(options.closeWindow).toHaveBeenCalledOnce();
	});

	it("allows update restart, then backgrounds again if the update is canceled", () => {
		let restarting = true;
		const { inAppQuit } = setup({ isUpdateRestartRequested: () => restarting });
		expect(inAppQuit()).toBe(false);
		restarting = false;
		expect(inAppQuit()).toBe(true);
	});

	it.each<NodeJS.Platform>(["win32", "linux"])("keeps %s quit behavior unchanged", (platform) => {
		const { options, controller, event, inAppQuit } = setup({ platform });
		expect(inAppQuit()).toBe(false);
		expect(controller.handleBeforeQuit(event)).toBe(false);
		expect(event.preventDefault).not.toHaveBeenCalled();
		expect(options.externalQuitStarted).not.toHaveBeenCalled();
	});

	it("really exits if the tray could not be created, including early startup", () => {
		const { controller, event, inAppQuit } = setup({ hasTray: () => false });
		expect(inAppQuit()).toBe(false);
		expect(controller.handleBeforeQuit(event)).toBe(false);
		expect(event.preventDefault).not.toHaveBeenCalled();
	});

	it("does not intercept a full-quit cleanup continuation after background eligibility is revoked", () => {
		let canBackground = true;
		const { inAppQuit, event } = setup({ hasTray: () => canBackground });
		canBackground = false;
		expect(inAppQuit()).toBe(false);
		expect(event.preventDefault).not.toHaveBeenCalled();
	});
});
