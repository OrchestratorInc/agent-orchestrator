import { describe, expect, it, vi } from "vitest";
import { createCliShadowShellPath } from "./cli-shadow-shell-path";

function setup(probe: () => Promise<string | undefined>, startupPath?: string) {
	const settleStartupProbes = vi.fn(async () => undefined);
	const probeMock = vi.fn(probe);
	return {
		settleStartupProbes,
		probe: probeMock,
		shellPath: createCliShadowShellPath({ settleStartupProbes, startupPath: () => startupPath, probe: probeMock }),
	};
}

describe("createCliShadowShellPath", () => {
	it("uses the startup probe's answer without a second shell", async () => {
		const { shellPath, probe, settleStartupProbes } = setup(async () => "/slow", "/startup");
		await expect(shellPath()).resolves.toBe("/startup");
		expect(settleStartupProbes).toHaveBeenCalledOnce();
		expect(probe).not.toHaveBeenCalled();
	});

	it("caches a successful answer", async () => {
		const { shellPath, probe } = setup(async () => "/usr/bin");
		await expect(shellPath()).resolves.toBe("/usr/bin");
		await expect(shellPath()).resolves.toBe("/usr/bin");
		expect(probe).toHaveBeenCalledOnce();
	});

	it("shares one probe between concurrent callers", async () => {
		const { shellPath, probe } = setup(async () => "/usr/bin");
		await Promise.all([shellPath(), shellPath()]);
		expect(probe).toHaveBeenCalledOnce();
	});

	it("does not cache a failed answer, so the next caller retries", async () => {
		const { shellPath, probe } = setup(async () => undefined);
		probe.mockResolvedValueOnce(undefined).mockResolvedValueOnce("/usr/bin");
		await expect(shellPath()).resolves.toBeUndefined();
		await expect(shellPath()).resolves.toBe("/usr/bin");
		await expect(shellPath()).resolves.toBe("/usr/bin");
		expect(probe).toHaveBeenCalledTimes(2);
	});

	it("does not cache a thrown probe", async () => {
		const { shellPath, probe } = setup(async () => "/usr/bin");
		probe.mockRejectedValueOnce(new Error("timed out"));
		await expect(shellPath()).rejects.toThrow("timed out");
		await expect(shellPath()).resolves.toBe("/usr/bin");
	});
});
