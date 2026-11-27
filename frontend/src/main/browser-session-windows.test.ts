import { describe, expect, it, vi } from "vitest";
import { browserWindowFrameName } from "../shared/browser-window";
import { createBrowserSessionWindows } from "./browser-session-windows";

function fakeWindow(options: Record<string, unknown> = {}) {
	const listeners = new Map<string, () => void>();
	let destroyed = false;
	let contentSize: [number, number] = [Number(options.width ?? 1200), Number(options.height ?? 820)];
	let resizable = options.resizable !== false;
	let minimumSize: [number, number] = [Number(options.minWidth ?? 0), Number(options.minHeight ?? 0)];
	const window = {
		options,
		showInactive: vi.fn(),
		getContentSize: () => contentSize,
		setContentSize: vi.fn((width: number, height: number) => {
			// Like the OS, a window does not go under its minimum size.
			contentSize = [Math.max(width, minimumSize[0]), Math.max(height, minimumSize[1])];
		}),
		getMinimumSize: () => minimumSize,
		setMinimumSize: vi.fn((width: number, height: number) => {
			minimumSize = [width, height];
		}),
		isResizable: () => resizable,
		setResizable: vi.fn((value: boolean) => {
			resizable = value;
		}),
		contentView: { addChildView: vi.fn(), removeChildView: vi.fn() },
		getContentBounds: () => ({ x: 0, y: 0, width: 1200, height: 820 }),
		isDestroyed: () => destroyed,
		isFocused: () => true,
		isMinimized: vi.fn(() => false),
		isVisible: () => true,
		focus: vi.fn(),
		show: vi.fn(),
		restore: vi.fn(),
		setTitle: vi.fn(),
		close: vi.fn(() => {
			listeners.get("close")?.();
			destroyed = true;
			listeners.get("closed")?.();
		}),
		on: (event: string, listener: () => void) => listeners.set(event, listener),
	};
	return window;
}

function fakeShell() {
	const listeners = new Map<string, (...args: unknown[]) => void>();
	return {
		on: (event: string, listener: (...args: unknown[]) => void) => listeners.set(event, listener),
		once: (event: string, listener: (...args: unknown[]) => void) => listeners.set(event, listener),
		emit: (event: string, ...args: unknown[]) => listeners.get(event)?.(...args),
	};
}

function setup({
	attach = true,
	recordingSize,
	contentArea = { width: 1500, height: 900 },
}: { attach?: boolean; recordingSize?: { width: number; height: number }; contentArea?: { width: number; height: number } } = {}) {
	const windows: ReturnType<typeof fakeWindow>[] = [];
	const compositions: Array<{ restackShell: ReturnType<typeof vi.fn>; dispose: ReturnType<typeof vi.fn> }> = [];
	const host = {
		canAttachWindow: vi.fn(() => true),
		attachWindow: vi.fn(() => attach),
		releaseWindow: vi.fn(),
		recordingWindowSize: vi.fn(() => recordingSize),
	};
	const notify = vi.fn();
	const sessionWindows = createBrowserSessionWindows({
		createWindow: (windowOptions) => {
			const window = fakeWindow(windowOptions as Record<string, unknown>);
			windows.push(window);
			return window as never;
		},
		compose: () => {
			const composition = { restackShell: vi.fn(), dispose: vi.fn() };
			compositions.push(composition);
			return composition;
		},
		host,
		notify,
		contentArea: () => contentArea,
	});
	const open = (viewId: string) => {
		const response = sessionWindows.handleWindowOpen({ url: "about:blank", frameName: browserWindowFrameName(viewId) });
		if (response?.action !== "allow") return { response, shell: undefined };
		const shell = fakeShell();
		const returned = response.createWindow!({ webContents: shell } as never);
		return { response, shell, returned };
	};
	return { compositions, host, notify, open, sessionWindows, windows };
}

describe("createBrowserSessionWindows", () => {
	it("leaves every other window.open to the app's own handler", () => {
		const { sessionWindows } = setup();
		expect(sessionWindows.handleWindowOpen({ url: "https://example.com/", frameName: "" })).toBeNull();
		expect(sessionWindows.handleWindowOpen({ url: "https://example.com/", frameName: browserWindowFrameName("1:s") })).toBeNull();
		expect(sessionWindows.handleWindowOpen({ url: "about:blank", frameName: "other" })).toBeNull();
	});

	it("opens a bridge-less window around the shell's document and moves the pages into it", () => {
		const { host, notify, open, windows } = setup();
		const { response, shell, returned } = open("1:sess-1");
		expect(response).toMatchObject({
			action: "allow",
			overrideBrowserWindowOptions: { webPreferences: { preload: undefined, sandbox: true, contextIsolation: true, nodeIntegration: false } },
		});
		expect(returned).toBe(shell);
		expect(windows).toHaveLength(1);
		expect(host.attachWindow).toHaveBeenCalledWith("1:sess-1", expect.objectContaining({ getContentBounds: expect.any(Function) }));
		expect(notify).toHaveBeenCalledWith({ viewId: "1:sess-1", open: true });
	});

	it("refuses a second window for a session, and one for a panel the host does not have", () => {
		const { host, open, sessionWindows } = setup();
		open("1:sess-1");
		expect(sessionWindows.handleWindowOpen({ url: "about:blank", frameName: browserWindowFrameName("1:sess-1") })).toEqual({ action: "deny" });
		host.canAttachWindow.mockReturnValueOnce(false);
		expect(sessionWindows.handleWindowOpen({ url: "about:blank", frameName: browserWindowFrameName("1:sess-2") })).toEqual({ action: "deny" });
	});

	it("gives the pages back and tells the shell when the window closes", () => {
		const { compositions, host, notify, open, sessionWindows, windows } = setup();
		open("1:sess-1");
		sessionWindows.close("1:sess-1");
		expect(host.releaseWindow).toHaveBeenCalledWith("1:sess-1");
		expect(host.releaseWindow.mock.invocationCallOrder[0]).toBeLessThan(compositions[0]!.dispose.mock.invocationCallOrder[0]!);
		expect(notify).toHaveBeenLastCalledWith({ viewId: "1:sess-1", open: false });
		expect(windows[0]!.isDestroyed()).toBe(true);
		// A new window may open for the session again.
		expect(sessionWindows.handleWindowOpen({ url: "about:blank", frameName: browserWindowFrameName("1:sess-1") })?.action).toBe("allow");
	});

	it("closes the window when its document goes away", () => {
		const { host, open, windows } = setup();
		const { shell } = open("1:sess-1");
		shell!.emit("destroyed");
		expect(windows[0]!.close).toHaveBeenCalled();
		expect(host.releaseWindow).toHaveBeenCalledWith("1:sess-1");
	});

	it("closes a window whose panel went away while it was made", () => {
		const { notify, open, windows } = setup({ attach: false });
		open("1:sess-1");
		expect(windows[0]!.close).toHaveBeenCalled();
		expect(notify).not.toHaveBeenCalled();
	});

	it("restores and focuses a minimized window, names it after its page, and closes all", () => {
		const { open, sessionWindows, windows } = setup();
		const { shell } = open("1:sess-1");
		open("1:sess-2");
		windows[0]!.isMinimized.mockReturnValue(true);
		sessionWindows.focus("1:sess-1");
		expect(windows[0]!.restore).toHaveBeenCalled();
		expect(windows[0]!.focus).toHaveBeenCalled();
		shell!.emit("page-title-updated", {}, "Example · Browser");
		expect(windows[0]!.setTitle).toHaveBeenCalledWith("Example · Browser");
		sessionWindows.closeAll();
		expect(windows.every((window) => window.isDestroyed())).toBe(true);
	});

	it("opens a recording's window at its size, fixed, without taking focus", () => {
		const { open, windows } = setup({ recordingSize: { width: 390, height: 844 } });
		open("1:sess-1");
		expect(windows[0]!.options).toMatchObject({ width: 390, height: 844, useContentSize: true, resizable: false, show: false });
		expect(windows[0]!.showInactive).toHaveBeenCalled();
	});

	it("scales a recording's window that would not fit the screen", () => {
		const { open, windows } = setup({ recordingSize: { width: 1920, height: 1080 }, contentArea: { width: 1454, height: 908 } });
		open("1:sess-1");
		// The same shape, as large as fits.
		expect(windows[0]!.options).toMatchObject({ width: 1454, height: 818 });
	});

	it("sizes an open window for a recording and gives its size back after", () => {
		const { open, sessionWindows, windows } = setup();
		open("1:sess-1");
		const window = windows[0]!;
		sessionWindows.fitToRecording("1:sess-1", { width: 390, height: 844 });
		expect(window.setResizable).toHaveBeenLastCalledWith(false);
		expect(window.getContentSize()).toEqual([390, 844]);
		sessionWindows.fitToRecording("1:sess-1", null);
		expect(window.setResizable).toHaveBeenLastCalledWith(true);
		expect(window.getContentSize()).toEqual([1200, 820]);
		expect(window.getMinimumSize()).toEqual([480, 360]);
	});

	it("fits a phone recording into a window the person opened, under its minimum size", () => {
		const { open, sessionWindows, windows } = setup();
		open("1:sess-1");
		const window = windows[0]!;
		sessionWindows.fitToRecording("1:sess-1", { width: 320, height: 300 });
		expect(window.getContentSize()).toEqual([320, 300]);
	});
});
