import { beforeEach, describe, expect, it, vi } from "vitest";

const handlers = vi.hoisted(() => new Map<string, (...args: unknown[]) => Promise<unknown>>());

vi.mock("electron", () => ({
	ipcMain: {
		handle: (channel: string, handler: (...args: unknown[]) => Promise<unknown>) => handlers.set(channel, handler),
	},
}));

import { registerCliShadowIpc } from "./cli-shadow-ipc";

function register(enabled: boolean) {
	const shellPath = vi.fn(async (): Promise<string | undefined> => undefined);
	const bundledPath = vi.fn((): string | undefined => undefined);
	const reveal = vi.fn();
	registerCliShadowIpc({ enabled: () => enabled, shellPath, bundledPath, isPackaged: () => enabled, appImage: undefined, reveal });
	return { shellPath, bundledPath, reveal };
}

const invoke = (channel: string, ...args: unknown[]) => {
	const handler = handlers.get(channel);
	if (!handler) throw new Error(`${channel} is not registered`);
	return handler({}, ...args);
};

describe("registerCliShadowIpc", () => {
	beforeEach(() => handlers.clear());

	it("answers unknown without probing the login shell when disabled", async () => {
		const { shellPath, bundledPath } = register(false);
		await expect(invoke("cliShadow:inspect", "identity-1")).resolves.toEqual({ kind: "unknown" });
		expect(shellPath).not.toHaveBeenCalled();
		expect(bundledPath).not.toHaveBeenCalled();
	});

	it("refuses a replace when disabled, even with a well-formed request", async () => {
		const { shellPath } = register(false);
		await expect(invoke("cliShadow:replace", "/usr/local/bin/ao", "/opt/old/ao", "identity-1")).resolves.toMatchObject({ ok: false, code: "changed" });
		expect(shellPath).not.toHaveBeenCalled();
	});

	it("reveals nothing when disabled", async () => {
		const { shellPath, reveal } = register(false);
		await invoke("cliShadow:reveal");
		expect(shellPath).not.toHaveBeenCalled();
		expect(reveal).not.toHaveBeenCalled();
	});

	it("runs the probe on an installed build", async () => {
		const { shellPath } = register(true);
		await expect(invoke("cliShadow:inspect")).resolves.toEqual({ kind: "unknown" });
		expect(shellPath).toHaveBeenCalledOnce();
	});
});
