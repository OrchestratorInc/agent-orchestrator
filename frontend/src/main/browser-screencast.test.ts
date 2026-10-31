import { describe, expect, it, vi } from "vitest";
import { BrowserScreencast, jpegDimensions } from "./browser-screencast";

function jpeg(width = 640, height = 360): Buffer {
	const data = Buffer.alloc(21);
	data.set([0xff, 0xd8, 0xff, 0xc0, 0x00, 0x11, 0x08]);
	data.writeUInt16BE(height, 7);
	data.writeUInt16BE(width, 9);
	return data;
}

describe("BrowserScreencast", () => {
	it("reads JPEG dimensions without decoding the image", () => {
		expect(jpegDimensions(jpeg(1280, 720))).toEqual({ width: 1280, height: 720 });
	});

	it("starts bounded JPEG capture and acknowledges frames", async () => {
		let listener: ((event: unknown, method: string, params: Record<string, unknown>) => void) | undefined;
		const sendCommand = vi.fn(async () => undefined);
		const emit = vi.fn();
		const debug = {
			on: (_event: "message", next: typeof listener) => { listener = next; },
			off: vi.fn(),
			sendCommand,
		};
		const cast = new BrowserScreencast(debug, emit, vi.fn());
		await cast.start();
		expect(sendCommand).toHaveBeenCalledWith("Page.startScreencast", expect.objectContaining({ format: "jpeg", maxWidth: 1280, maxHeight: 720 }));
		listener?.({}, "Page.screencastFrame", { sessionId: 9, data: jpeg().toString("base64") });
		await vi.waitFor(() => expect(emit).toHaveBeenCalledWith(expect.objectContaining({ width: 640, height: 360 })));
		expect(sendCommand).toHaveBeenCalledWith("Page.screencastFrameAck", { sessionId: 9 });
		await cast.stop();
		expect(sendCommand).toHaveBeenCalledWith("Page.stopScreencast");
	});
	it("captures bounded frames when the native surface stops painting and stops on teardown", async () => {
		vi.useFakeTimers();
		try {
			const emit = vi.fn();
			let finishCapture: ((value: { data: string }) => void) | undefined;
			const sendCommand = vi.fn(async (method: string) => {
				if (method === "Page.getLayoutMetrics") return { cssVisualViewport: { pageX: 0, pageY: 400 } };
				if (method === "Page.captureScreenshot") return new Promise<{ data: string }>((resolve) => { finishCapture = resolve; });
				return undefined;
			});
			const cast = new BrowserScreencast({ on: vi.fn(), off: vi.fn(), sendCommand }, emit, vi.fn(), () => ({ width: 1920, height: 1080 }));
			await cast.start();
			await vi.advanceTimersByTimeAsync(1500);
			expect(sendCommand.mock.calls.filter(([method]) => method === "Page.captureScreenshot")).toHaveLength(1);
			expect(sendCommand).toHaveBeenCalledWith("Page.captureScreenshot", expect.objectContaining({
				captureBeyondViewport: true, clip: { x: 0, y: 400, width: 1920, height: 1080, scale: 2 / 3 },
			}));
			finishCapture?.({ data: jpeg(1280, 720).toString("base64") });
			await vi.advanceTimersByTimeAsync(0);
			expect(emit).toHaveBeenCalledWith(expect.objectContaining({ width: 1280, height: 720 }));
			await vi.advanceTimersByTimeAsync(500);
			await cast.stop();
			finishCapture?.({ data: jpeg().toString("base64") });
			await vi.advanceTimersByTimeAsync(1500);
			expect(emit).toHaveBeenCalledTimes(1);
			expect(sendCommand.mock.calls.filter(([method]) => method === "Page.captureScreenshot")).toHaveLength(2);
		} finally { vi.useRealTimers(); }
	});

	it("suppresses duplicate images, backs off to five seconds, and wakes on input", async () => {
		vi.useFakeTimers();
		try {
			const emit = vi.fn();
			let listener: ((event: unknown, method: string, params: Record<string, unknown>) => void) | undefined;
			let image = jpeg().toString("base64");
			const captureTimes: number[] = [];
			const sendCommand = vi.fn(async (method: string) => {
				if (method === "Page.captureScreenshot") { captureTimes.push(Date.now()); return { data: image }; }
				return undefined;
			});
			const cast = new BrowserScreencast({ on: (_event, next) => { listener = next; }, off: vi.fn(), sendCommand }, emit, vi.fn());
			await cast.start();
			await vi.advanceTimersByTimeAsync(13000);
			expect(emit).toHaveBeenCalledTimes(1);
			expect(captureTimes.slice(1).map((time, i) => time - captureTimes[i])).toEqual([500, 1000, 2000, 4000, 5000]);
			listener?.({}, "Page.screencastFrame", { sessionId: 7, data: image });
			await vi.advanceTimersByTimeAsync(1000);
			expect(captureTimes).toHaveLength(6);
			image = jpeg(800, 600).toString("base64");
			cast.requestFrame();
			await vi.advanceTimersByTimeAsync(500);
			expect(emit).toHaveBeenCalledTimes(2);
			expect(emit).toHaveBeenLastCalledWith(expect.objectContaining({ sequence: 2n, width: 800 }));
			await cast.stop();
		} finally { vi.useRealTimers(); }
	});

	it("acknowledges duplicate screencast frames without retransmitting them", async () => {
		let listener: ((event: unknown, method: string, params: Record<string, unknown>) => void) | undefined;
		const sendCommand = vi.fn(async () => undefined);
		const emit = vi.fn();
		const cast = new BrowserScreencast({ on: (_event, next) => { listener = next; }, off: vi.fn(), sendCommand }, emit, vi.fn());
		await cast.start();
		for (const sessionId of [1, 2]) listener?.({}, "Page.screencastFrame", { sessionId, data: jpeg().toString("base64") });
		expect(emit).toHaveBeenCalledTimes(1);
		expect(sendCommand).toHaveBeenCalledWith("Page.screencastFrameAck", { sessionId: 1 });
		expect(sendCommand).toHaveBeenCalledWith("Page.screencastFrameAck", { sessionId: 2 });
		await cast.stop();
	});

	it("cancels a pending hidden capture when the desktop becomes visible", async () => {
		vi.useFakeTimers();
		try {
			let hidden = true;
			let finishMetrics: ((value: unknown) => void) | undefined;
			const sendCommand = vi.fn(async (method: string) => method === "Page.getLayoutMetrics"
				? new Promise((resolve) => { finishMetrics = resolve; }) : undefined);
			const cast = new BrowserScreencast({ on: vi.fn(), off: vi.fn(), sendCommand }, vi.fn(), vi.fn(), () => ({ width: 800, height: 600 }), () => hidden);
			await cast.start();
			await vi.advanceTimersByTimeAsync(500);
			hidden = false;
			finishMetrics?.({});
			await vi.advanceTimersByTimeAsync(1000);
			expect(sendCommand).not.toHaveBeenCalledWith("Page.captureScreenshot", expect.anything());
			await cast.stop();
		} finally { vi.useRealTimers(); }
	});

	it("leaves visible static pages untouched and resumes fallback only while hidden", async () => {
		vi.useFakeTimers();
		try {
			let hidden = false;
			const sendCommand = vi.fn(async () => undefined);
			const cast = new BrowserScreencast({ on: vi.fn(), off: vi.fn(), sendCommand }, vi.fn(), vi.fn(), () => ({ width: 800, height: 600 }), () => hidden);
			await cast.start();
			await vi.advanceTimersByTimeAsync(30000);
			expect(sendCommand).not.toHaveBeenCalledWith("Page.getLayoutMetrics");
			expect(sendCommand).not.toHaveBeenCalledWith("Page.captureScreenshot", expect.anything());
			hidden = true;
			await vi.advanceTimersByTimeAsync(500);
			expect(sendCommand).toHaveBeenCalledWith("Page.captureScreenshot", expect.objectContaining({ captureBeyondViewport: true }));
			sendCommand.mockClear();
			hidden = false;
			await vi.advanceTimersByTimeAsync(30000);
			expect(sendCommand).not.toHaveBeenCalledWith("Page.captureScreenshot", expect.anything());
			await cast.stop();
		} finally { vi.useRealTimers(); }
	});

});
