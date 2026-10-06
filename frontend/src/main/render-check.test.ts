import { afterEach, describe, expect, it, vi } from "vitest";
import { CONTENT_HEIGHT_SCRIPT, checkRender } from "./render-check";

const url = "http://127.0.0.1:3001/api/v1/sessions/p-1/renders/check-id-001";

function fakes(options: { loadError?: Error; measureNeverReturns?: boolean } = {}) {
	const listeners = new Map<string, (...args: unknown[]) => void>();
	const permissionRequest = vi.fn();
	const contents = {
		session: {
			setPermissionRequestHandler: (handler: (...args: unknown[]) => void) => permissionRequest.mockImplementation(handler),
			setPermissionCheckHandler: vi.fn(),
		},
		setWindowOpenHandler: vi.fn(),
		on: (event: string, listener: (...args: unknown[]) => void) => listeners.set(event, listener),
		loadURL: vi.fn(async () => {
			if (options.loadError) throw options.loadError;
			listeners.get("console-message")?.({}, 3, "Uncaught ReferenceError: d3 is not defined", 1, url);
		}),
		executeJavaScript: vi.fn(() => (options.measureNeverReturns ? new Promise<number>(() => {}) : Promise.resolve(412))),
		debugger: {
			attach: vi.fn(),
			sendCommand: vi.fn(async () => ({ data: "iVBORw0KGgo=" })),
			detach: vi.fn(),
		},
		close: vi.fn(),
	};
	const view = { webContents: contents, setBounds: vi.fn() };
	// A function, not an arrow: checkRender calls it with `new` (vitest 4 rejects arrow constructors).
	const WebContentsView = vi.fn(function (_options: { webPreferences: Record<string, unknown> }) {
		return view;
	});
	const window = { contentView: { addChildView: vi.fn(), removeChildView: vi.fn() } };
	return { contents, view, WebContentsView, window, permissionRequest };
}

describe("checkRender", () => {
	it("loads only daemon render-check URLs", async () => {
		const f = fakes();
		await expect(checkRender(f as never, { url: "https://example.com/", width: 720 })).rejects.toThrow(/render-check URL/);
		await expect(checkRender(f as never, { url: url.replace("check-", ""), width: 720 })).rejects.toThrow(/render-check URL/);
		expect(f.WebContentsView).not.toHaveBeenCalled();
	});

	it("returns the screenshot, height, and console, in a sandboxed throwaway view", async () => {
		const f = fakes();
		const result = await checkRender(f as never, { url, width: 390 });
		expect(result).toEqual({
			data: "iVBORw0KGgo=",
			width: 390,
			height: 412,
			contentHeight: 412,
			consoleMessages: [{ level: "error", text: "Uncaught ReferenceError: d3 is not defined" }],
		});
		const prefs = f.WebContentsView.mock.calls[0][0].webPreferences;
		expect(prefs).toMatchObject({ sandbox: true, contextIsolation: true, nodeIntegration: false });
		expect(prefs.partition).toMatch(/^ao-render-check-/);
		const decide = vi.fn();
		f.permissionRequest({}, "media", decide);
		expect(decide).toHaveBeenCalledWith(false);
		expect(f.window.contentView.removeChildView).toHaveBeenCalledWith(f.view);
		expect(f.contents.close).toHaveBeenCalled();
		expect(f.contents.executeJavaScript).toHaveBeenCalledWith(CONTENT_HEIGHT_SCRIPT);
	});

	it("removes the view when the page fails to load", async () => {
		const f = fakes({ loadError: new Error("ERR_CONNECTION_REFUSED") });
		await expect(checkRender(f as never, { url, width: 720 })).rejects.toThrow(/ERR_CONNECTION_REFUSED/);
		expect(f.window.contentView.removeChildView).toHaveBeenCalledWith(f.view);
		expect(f.contents.close).toHaveBeenCalled();
	});
});

describe("CONTENT_HEIGHT_SCRIPT", () => {
	const measure = (documentElement: { scrollHeight: number; clientHeight: number; rectHeight: number }) =>
		new Function("document", `return ${CONTENT_HEIGHT_SCRIPT}`)({
			documentElement: {
				scrollHeight: documentElement.scrollHeight,
				clientHeight: documentElement.clientHeight,
				getBoundingClientRect: () => ({ height: documentElement.rectHeight }),
			},
		});

	it("reports a short page's own height, not the viewport's", () => {
		expect(measure({ scrollHeight: 800, clientHeight: 800, rectHeight: 300 })).toBe(300);
	});

	it("reports a tall page's scroll height", () => {
		expect(measure({ scrollHeight: 1500, clientHeight: 800, rectHeight: 1500 })).toBe(1500);
	});

	it("rounds a fractional height up", () => {
		expect(measure({ scrollHeight: 800, clientHeight: 800, rectHeight: 300.2 })).toBe(301);
	});
});

describe("checkRender deadline and cancellation", () => {
	afterEach(() => vi.useRealTimers());

	it("gives up on a page that stops answering after it loads, and removes the view", async () => {
		vi.useFakeTimers();
		const f = fakes({ measureNeverReturns: true });
		const result = checkRender(f as never, { url, width: 720 });
		const rejected = expect(result).rejects.toMatchObject({
			code: "BROWSER_COMMAND_FAILED",
			message: expect.stringMatching(/timed out after 20000 ms while measuring the page/),
		});
		await vi.advanceTimersByTimeAsync(20_000);
		await rejected;
		expect(f.window.contentView.removeChildView).toHaveBeenCalledWith(f.view);
		expect(f.contents.close).toHaveBeenCalled();
	});

	it("stops when the daemon cancels during the settle wait", async () => {
		vi.useFakeTimers();
		const f = fakes();
		const controller = new AbortController();
		const result = checkRender(f as never, { url, width: 720 }, controller.signal);
		const rejected = expect(result).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		await vi.advanceTimersByTimeAsync(100);
		controller.abort();
		await rejected;
		expect(f.contents.executeJavaScript).not.toHaveBeenCalled();
		expect(f.window.contentView.removeChildView).toHaveBeenCalledWith(f.view);
		expect(f.contents.close).toHaveBeenCalled();
	});

	it("refuses to start for a signal that is already aborted", async () => {
		vi.useFakeTimers();
		const f = fakes();
		const result = checkRender(f as never, { url, width: 720 }, AbortSignal.abort());
		await expect(result).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		expect(f.window.contentView.removeChildView).toHaveBeenCalledWith(f.view);
		expect(f.contents.close).toHaveBeenCalled();
	});

	it("leaves no timer or abort listener behind after a successful check", async () => {
		vi.useFakeTimers();
		const f = fakes();
		const controller = new AbortController();
		const removeListener = vi.spyOn(controller.signal, "removeEventListener");
		const result = checkRender(f as never, { url, width: 720 }, controller.signal);
		await vi.advanceTimersByTimeAsync(300);
		await expect(result).resolves.toMatchObject({ contentHeight: 412 });
		expect(vi.getTimerCount()).toBe(0);
		expect(removeListener).toHaveBeenCalledWith("abort", expect.any(Function));
	});
});
