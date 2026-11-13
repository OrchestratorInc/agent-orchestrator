import { afterEach, describe, expect, it, vi } from "vitest";
import { captureMainError, reportDaemonFailure, installIpcErrorCapture, resetMainErrorCaptureForTests, sanitizeMainErrorCapture, setMainErrorSink, type MainErrorCapture } from "./main-error-capture";

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
		for (let i = 0; i < 50; i++) captureMainError({ category: "daemon", reason: "exited", detail: `code:${i}` });
		const sink = vi.fn();
		setMainErrorSink(sink);
		expect(sink).toHaveBeenCalledTimes(20);
	});

	it.each(["daemon", "update", "ipc", "window", "acp_runtime", "side_chat"] as const)("bounds fresh errors for %s independently of object identity", (category) => {
		const sink = vi.fn(); setMainErrorSink(sink);
		for (let i = 0; i < 20; i++) captureMainError({ category, reason: "handler", error: new Error(`private ${i}`), detail: "channel:cloudCp:request" });
		expect(sink).toHaveBeenCalledTimes(5);
		captureMainError({ category, reason: "handler", detail: "channel:other" });
		captureMainError({ category, reason: "check", detail: "channel:cloudCp:request" });
		expect(sink).toHaveBeenCalledTimes(7);
		captureMainError({ category: category === "daemon" ? "update" : "daemon", reason: "handler", detail: "channel:cloudCp:request" });
		expect(sink).toHaveBeenCalledTimes(8);
	});

	it("applies budgets before consent and retains them across sink changes", () => {
		for (let i = 0; i < 20; i++) captureMainError({ category: "ipc", reason: "handler", error: new Error("retry") });
		const sink = vi.fn(); setMainErrorSink(sink);
		expect(sink).toHaveBeenCalledTimes(5);
		setMainErrorSink(null); captureMainError({ category: "ipc", reason: "handler" }); setMainErrorSink(sink);
		expect(sink).toHaveBeenCalledTimes(5);
	});

	it("bounds unique budget keys until the next UTC day", () => {
		const clock = vi.spyOn(Date, "now").mockReturnValue(Date.UTC(2026, 10, 6));
		try {
			const sink = vi.fn(); setMainErrorSink(sink);
			for (let i = 0; i < 1100; i++) captureMainError({ category: "ipc", reason: "handler", detail: `code:${i}` });
			expect(sink).toHaveBeenCalledTimes(1024);
			clock.mockReturnValue(Date.UTC(2026, 10, 7));
			captureMainError({ category: "ipc", reason: "handler", detail: "code:1100" });
			expect(sink).toHaveBeenCalledTimes(1025);
		} finally { clock.mockRestore(); }
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

	it.each(["daemon", "update", "ipc", "window", "acp_runtime", "side_chat"] as const)("caps %s across minutes until the next day", (category) => {
		const clock = vi.spyOn(Date, "now");
		try {
			const base = Date.UTC(2026, 10, 4, 0, 0, 0);
			const sink = vi.fn(); setMainErrorSink(sink);
			for (let i = 0; i <= 200; i++) {
				clock.mockReturnValue(base + i * 60_000);
				captureMainError({ category, reason: "recovery" });
			}
			expect(sink).toHaveBeenCalledTimes(200);
			clock.mockReturnValue(base + 86_400_000);
			captureMainError({ category, reason: "recovery" });
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


describe("daemon failure reporting", () => {
	it("reports failure transitions with safe exit detail, but not repeated refreshes", () => {
		const sink = vi.fn(); setMainErrorSink(sink);
		reportDaemonFailure({ state: "ready" }, { state: "error", code: "exited", signal: "SIGKILL", message: "/private/path" });
		reportDaemonFailure({ state: "error", code: "exited" }, { state: "error", code: "exited" });
		reportDaemonFailure({ state: "ready" }, { state: "error", code: "exited", exitCode: 7 });
		expect(sink.mock.calls.map(([event]) => event)).toEqual([
			{ category: "daemon", reason: "exited", detail: "signal:SIGKILL" },
			{ category: "daemon", reason: "exited", detail: "code:7" },
		]);
	});
	it("ignores normal and transitional states", () => {
		const sink = vi.fn(); setMainErrorSink(sink);
		for (const code of ["not_ready", "not_configured", "daemon_unreachable"] as const) reportDaemonFailure({ state: "ready" }, { state: "error", code });
		reportDaemonFailure({ state: "ready" }, { state: "stopped" });
		expect(sink).not.toHaveBeenCalled();
	});
});
