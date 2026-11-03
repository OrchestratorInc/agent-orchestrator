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
