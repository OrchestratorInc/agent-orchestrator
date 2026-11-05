import { describe, expect, it, vi } from "vitest";
import { requestDesktopShutdown } from "./desktop-shutdown";

function setup() {
	return {
		keepAlive: false,
		appRunId: "run-1",
		status: { state: "ready" as const, pid: 123, port: 3002 },
		preserveAgents: vi.fn(() => false),
		readRunInfo: vi.fn(async () => ({ pid: 123, port: 3002, startedAtMs: 0, owner: "app", appRunId: "run-1" })),
		requestShutdown: vi.fn(async () => ({ ok: true, status: 202 })),
	};
}

describe("desktop daemon shutdown", () => {
	it("requests agent shutdown for full desktop exit", async () => {
		const options = setup();
		expect(await requestDesktopShutdown(options)).toBe(true);
		expect(options.requestShutdown).toHaveBeenCalledWith(3002, { stopAgents: true, appRunId: "run-1" });
	});

	it("preserves agents for a planned update or relaunch", async () => {
		const options = setup();
		options.preserveAgents.mockReturnValue(true);
		expect(await requestDesktopShutdown(options)).toBe(true);
		expect(options.requestShutdown).toHaveBeenCalledWith(3002, { stopAgents: false, appRunId: "run-1" });
	});

	it("leaves keep-alive daemons alone", async () => {
		const options = setup();
		options.keepAlive = true;
		expect(await requestDesktopShutdown(options)).toBe(false);
		expect(options.readRunInfo).not.toHaveBeenCalled();
		expect(options.requestShutdown).not.toHaveBeenCalled();
	});

	it.each(["persistent", ""])("leaves a daemon with owner %s alone", async (owner) => {
		const options = setup();
		options.readRunInfo.mockResolvedValue({ pid: 123, port: 3002, startedAtMs: 0, owner, appRunId: "run-1" });
		expect(await requestDesktopShutdown(options)).toBe(false);
		expect(options.requestShutdown).not.toHaveBeenCalled();
	});

	it.each([{ port: 4000, appRunId: "run-1" }, { port: 3002, appRunId: "run-2" }])("rejects a replaced daemon %j", async (identity) => {
		const options = setup();
		options.readRunInfo.mockResolvedValue({ pid: 123, startedAtMs: 0, owner: "app", ...identity });
		await expect(requestDesktopShutdown(options)).rejects.toThrow("ownership changed");
		expect(options.requestShutdown).not.toHaveBeenCalled();
	});

	it("uses the app run identity when a configured shell command has a different PID", async () => {
		const options = setup();
		options.status.pid = 456;
		expect(await requestDesktopShutdown(options)).toBe(true);
		expect(options.requestShutdown).toHaveBeenCalledWith(3002, { stopAgents: true, appRunId: "run-1" });
	});

	it("does not accept a failed shutdown request", async () => {
		const options = setup();
		options.requestShutdown.mockResolvedValue({ ok: false, status: 409 });
		await expect(requestDesktopShutdown(options)).rejects.toThrow("HTTP 409");
	});
});
