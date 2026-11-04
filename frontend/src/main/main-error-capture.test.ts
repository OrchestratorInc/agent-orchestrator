import { afterEach, describe, expect, it, vi } from "vitest";
import { captureMainError, installIpcErrorCapture, resetMainErrorCaptureForTests, sanitizeMainErrorCapture, setMainErrorSink, type MainErrorCapture } from "./main-error-capture";

afterEach(() => resetMainErrorCaptureForTests());

describe("main error capture", () => {
	it("holds captures until the sink is set, then hands them over in order", () => {
		captureMainError({ category: "daemon", reason: "spawn_failed" });
		captureMainError({ category: "window", reason: "unresponsive" });
		const sink = vi.fn<(capture: MainErrorCapture) => void>();
		setMainErrorSink(sink);
		expect(sink.mock.calls.map(([capture]) => capture.reason)).toEqual(["spawn_failed", "unresponsive"]);
		captureMainError({ category: "update", reason: "download" });
		expect(sink).toHaveBeenCalledTimes(3);
	});

	it("bounds the pre-consent queue", () => {
		for (let i = 0; i < 50; i++) captureMainError({ category: "daemon", reason: "exited" });
		const sink = vi.fn();
		setMainErrorSink(sink);
		expect(sink).toHaveBeenCalledTimes(20);
	});

	it("deduplicates side-chat recovery errors across IPC and bounds retry storms", () => {
		const sink = vi.fn(); setMainErrorSink(sink);
		const error = new TypeError("private failure");
		captureMainError({ category: "side_chat", reason: "recovery", error });
		captureMainError({ category: "ipc", reason: "handler", error });
		expect(sink).toHaveBeenCalledOnce();
		for (let i = 0; i < 10; i++) captureMainError({ category: "side_chat", reason: "recovery", error: new Error("retry") });
		expect(sink).toHaveBeenCalledTimes(5);
	});

	it("caps side-chat retries across minutes until the next day", () => {
		const clock = vi.spyOn(Date, "now");
		try {
			const base = Date.UTC(2026, 10, 4, 0, 0, 0);
			const sink = vi.fn(); setMainErrorSink(sink);
			for (let i = 0; i <= 200; i++) {
				clock.mockReturnValue(base + i * 60_000);
				captureMainError({ category: "side_chat", reason: "recovery" });
			}
			expect(sink).toHaveBeenCalledTimes(200);
			clock.mockReturnValue(base + 86_400_000);
			captureMainError({ category: "side_chat", reason: "recovery" });
			expect(sink).toHaveBeenCalledTimes(201);
		} finally { clock.mockRestore(); }
	});

	it("drops unknown categories and reasons and any free-text detail", () => {
		expect(sanitizeMainErrorCapture({ category: "made_up" as never, reason: "exited" })).toBeNull();
		expect(sanitizeMainErrorCapture({ category: "daemon", reason: "made_up" as never })).toBeNull();
		expect(sanitizeMainErrorCapture({ category: "daemon", reason: "exited", detail: "/Users/dave/secret" })).toEqual({ category: "daemon", reason: "exited" });
		for (const detail of ["signal:SIGKILL", "code:1", "code:-3", "channel:app:chooseDirectory", "kind:invalid-signature"]) {
			expect(sanitizeMainErrorCapture({ category: "daemon", reason: "exited", detail })?.detail).toBe(detail);
		}
	});

	it("never lets a failing sink break the caller", () => {
		setMainErrorSink(() => { throw new Error("transport closed"); });
		expect(() => captureMainError({ category: "daemon", reason: "exited" })).not.toThrow();
	});

	it("continues flushing held errors when the sink throws", () => {
		captureMainError({ category: "daemon", reason: "spawn_failed" });
		captureMainError({ category: "window", reason: "unresponsive" });
		const sink = vi.fn(() => { throw new Error("transport closed"); });
		expect(() => setMainErrorSink(sink)).not.toThrow();
		expect(sink).toHaveBeenCalledTimes(2);
	});

	it("reports IPC handler throws under the channel and still rejects to the renderer", async () => {
		const handlers = new Map<string, (event: never, ...args: unknown[]) => unknown>();
		const ipcMain = { handle: (channel: string, listener: (event: never, ...args: unknown[]) => unknown) => { handlers.set(channel, listener); } };
		installIpcErrorCapture(ipcMain as never);
		const sink = vi.fn<(capture: MainErrorCapture) => void>();
		setMainErrorSink(sink);
		ipcMain.handle("app:chooseDirectory", () => { throw new TypeError("boom"); });
		ipcMain.handle("app:getVersion", () => "1.0.0");
		ipcMain.handle("telemetry:capture", () => { throw new Error("must not self-report"); });
		await expect(handlers.get("app:chooseDirectory")?.({} as never)).rejects.toThrow("boom");
		await expect(handlers.get("app:getVersion")?.({} as never)).resolves.toBe("1.0.0");
		expect(() => handlers.get("telemetry:capture")?.({} as never)).toThrow("must not self-report");
		expect(sink).toHaveBeenCalledOnce();
		expect(sink.mock.calls[0]?.[0]).toMatchObject({ category: "ipc", reason: "handler", detail: "channel:app:chooseDirectory" });
	});
});
