import { afterEach, describe, expect, it, vi } from "vitest";
import { CONTENT_HEIGHT_SCRIPT, checkRender } from "./render-check";

const url = "http://127.0.0.1:3001/api/v1/sessions/p-1/renders/check-id-001";

function fakes(options: { loadError?: Error; measureNeverReturns?: boolean; emptyImage?: boolean; manualPaint?: boolean } = {}) {
	const events: string[] = [];
	const listeners = new Map<string, (...args: unknown[]) => void>();
	const paintListeners: Array<() => void> = [];
	const paint = () => {
		events.push("paint");
		for (const listener of paintListeners.splice(0)) listener();
	};
	const permissionRequest = vi.fn();
	const contents = {
		session: {
			setPermissionRequestHandler: (handler: (...args: unknown[]) => void) => permissionRequest.mockImplementation(handler),
			setPermissionCheckHandler: vi.fn(),
		},
		setWindowOpenHandler: vi.fn(),
		on: (event: string, listener: (...args: unknown[]) => void) => listeners.set(event, listener),
		once: (event: string, listener: () => void) => {
			if (event === "paint") paintListeners.push(listener);
		},
		loadURL: vi.fn(async () => {
			if (options.loadError) throw options.loadError;
			listeners.get("console-message")?.({}, 3, "Uncaught ReferenceError: d3 is not defined", 1, url);
		}),
		executeJavaScript: vi.fn(() => (options.measureNeverReturns ? new Promise<number>(() => {}) : Promise.resolve(412))),
		// An offscreen page repaints on invalidate(); manualPaint holds that frame back.
		invalidate: vi.fn(() => {
			events.push("invalidate");
			if (!options.manualPaint) queueMicrotask(paint);
		}),
		capturePage: vi.fn(async () => {
			events.push("capture");
			return { isEmpty: () => Boolean(options.emptyImage), toPNG: () => Buffer.from("png-bytes") };
		}),
	};
	const window = {
		webContents: contents,
		setContentSize: vi.fn((width: number, height: number) => events.push(`resize ${width}x${height}`)),
		destroy: vi.fn(),
	};
	// A function, not an arrow: checkRender calls it with `new` (vitest 4 rejects arrow constructors).
	const BrowserWindow = vi.fn(function (_options: Record<string, unknown>) {
		return window;
	});
	return { contents, window, BrowserWindow, permissionRequest, events, paint };
}

const png = Buffer.from("png-bytes").toString("base64");

describe("checkRender", () => {
	it("loads only daemon render-check URLs at a supported width, before creating anything", async () => {
		const f = fakes();
		await expect(checkRender(f as never, { url: "https://example.com/", width: 720 })).rejects.toThrow(/render-check URL/);
		await expect(checkRender(f as never, { url: url.replace("check-", ""), width: 720 })).rejects.toThrow(/render-check URL/);
		await expect(checkRender(f as never, { url, width: 100 })).rejects.toThrow(/width/);
		expect(f.BrowserWindow).not.toHaveBeenCalled();
	});

	it("returns the screenshot, height, and console, from a hidden offscreen sandboxed window", async () => {
		const f = fakes();
		const result = await checkRender(f as never, { url, width: 390 });
		expect(result).toEqual({
			data: png,
			width: 390,
			height: 412,
			contentHeight: 412,
			consoleMessages: [{ level: "error", text: "Uncaught ReferenceError: d3 is not defined" }],
		});
		// Offscreen so it paints without ever being on screen; one fixed partition
		// with no "persist:" prefix, so checks share one in-memory session.
		expect(f.BrowserWindow).toHaveBeenCalledWith({
			show: false,
			width: 390,
			height: 800,
			webPreferences: {
				offscreen: true,
				sandbox: true,
				contextIsolation: true,
				nodeIntegration: false,
				backgroundThrottling: false,
				partition: "ao-render-check",
			},
		});
		const decide = vi.fn();
		f.permissionRequest({}, "media", decide);
		expect(decide).toHaveBeenCalledWith(false);
		expect(f.contents.executeJavaScript).toHaveBeenCalledWith(CONTENT_HEIGHT_SCRIPT);
		expect(f.window.destroy).toHaveBeenCalled();
	});

	it("captures only once the page has painted at the measured size", async () => {
		const f = fakes({ manualPaint: true });
		const result = checkRender(f as never, { url, width: 390 });
		await vi.waitFor(() => expect(f.contents.invalidate).toHaveBeenCalled());
		await new Promise((resolve) => setTimeout(resolve, 20));
		expect(f.contents.capturePage).not.toHaveBeenCalled();
		f.paint();
		await expect(result).resolves.toMatchObject({ data: png, height: 412 });
		expect(f.events).toEqual(["resize 390x412", "invalidate", "paint", "capture"]);
	});

	it("caps the captured height at 2000 px", async () => {
		const f = fakes();
		f.contents.executeJavaScript.mockResolvedValue(5_000);
		await expect(checkRender(f as never, { url, width: 390 })).resolves.toMatchObject({ height: 2_000, contentHeight: 5_000 });
		expect(f.window.setContentSize).toHaveBeenCalledWith(390, 2_000);
	});

	it("fails on an empty capture instead of returning a blank image", async () => {
		const f = fakes({ emptyImage: true });
		await expect(checkRender(f as never, { url, width: 720 })).rejects.toMatchObject({
			code: "BROWSER_COMMAND_FAILED",
			message: expect.stringMatching(/empty/),
		});
		expect(f.window.destroy).toHaveBeenCalled();
	});

	it("destroys the window when the page fails to load", async () => {
		const f = fakes({ loadError: new Error("ERR_CONNECTION_REFUSED") });
		await expect(checkRender(f as never, { url, width: 720 })).rejects.toThrow(/ERR_CONNECTION_REFUSED/);
		expect(f.window.destroy).toHaveBeenCalled();
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

	it("gives up on a page that stops answering after it loads, and destroys the window", async () => {
		vi.useFakeTimers();
		const f = fakes({ measureNeverReturns: true });
		const result = checkRender(f as never, { url, width: 720 });
		const rejected = expect(result).rejects.toMatchObject({
			code: "BROWSER_COMMAND_FAILED",
			message: expect.stringMatching(/timed out after 20000 ms while measuring the page/),
		});
		await vi.advanceTimersByTimeAsync(20_000);
		await rejected;
		expect(f.window.destroy).toHaveBeenCalled();
	});

	it("gives up on a page that never paints after the resize, and destroys the window", async () => {
		vi.useFakeTimers();
		const f = fakes({ manualPaint: true });
		const result = checkRender(f as never, { url, width: 720 });
		const rejected = expect(result).rejects.toMatchObject({
			code: "BROWSER_COMMAND_FAILED",
			message: expect.stringMatching(/timed out after 20000 ms while capturing the screenshot/),
		});
		await vi.advanceTimersByTimeAsync(20_000);
		await rejected;
		expect(f.contents.capturePage).not.toHaveBeenCalled();
		expect(f.window.destroy).toHaveBeenCalled();
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
		expect(f.window.destroy).toHaveBeenCalled();
	});

	it("refuses to start for a signal that is already aborted", async () => {
		vi.useFakeTimers();
		const f = fakes();
		const result = checkRender(f as never, { url, width: 720 }, AbortSignal.abort());
		await expect(result).rejects.toMatchObject({ code: "BROWSER_COMMAND_CANCELED" });
		expect(f.window.destroy).toHaveBeenCalled();
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
