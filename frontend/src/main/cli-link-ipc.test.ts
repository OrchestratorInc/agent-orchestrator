import { chmod, lstat, mkdir, mkdtemp, readlink, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ handlers: new Map<string, (...args: unknown[]) => Promise<unknown>>(), home: "" }));

vi.mock("electron", () => ({
	ipcMain: {
		handle: (channel: string, handler: (...args: unknown[]) => Promise<unknown>) => state.handlers.set(channel, handler),
	},
}));

import { registerCliLinkIpc } from "./cli-link-ipc";
import { bundledDaemonBinaryName } from "../shared/daemon-launch";

describe.skipIf(process.platform === "win32")("registerCliLinkIpc", () => {
	let root = "";

	beforeEach(async () => {
		state.handlers.clear();
		root = await mkdtemp(path.join(tmpdir(), "ao-cli-link-ipc-"));
		state.home = path.join(root, "home");
		vi.stubEnv("HOME", state.home);
		await mkdir(path.join(state.home, ".local", "bin"), { recursive: true });
		const daemon = path.join(root, "resources", "daemon");
		await mkdir(daemon, { recursive: true });
		const cli = path.join(daemon, bundledDaemonBinaryName(process.platform));
		await writeFile(cli, "#!/bin/sh\nprintf '%s\\n' '1.0.0'\n");
		await chmod(cli, 0o755);
	});

	afterEach(async () => {
		vi.unstubAllEnvs();
		await rm(root, { recursive: true, force: true });
	});

	it("links once on a first launch and does not link again after Unlink", async () => {
		const bin = path.join(state.home, ".local", "bin");
		const link = path.join(bin, "ao");
		let pending = true;
		const firstLaunch = {
			pending: vi.fn(async () => pending),
			spend: vi.fn(async () => {
				pending = false;
			}),
		};
		const linkOnFirstLaunch = registerCliLinkIpc({
			isPackaged: () => true,
			resourcesPath: path.join(root, "resources"),
			appImage: undefined,
			dataDir: path.join(root, "data"),
			appVersion: "1.0.0",
			shellPath: () => bin,
			firstLaunch,
		});

		expect(await linkOnFirstLaunch(undefined)).toBeNull();
		expect(firstLaunch.pending).not.toHaveBeenCalled();
		expect(await linkOnFirstLaunch(bin)).toMatchObject({ ok: true, changed: true, linkPath: link });
		const target = await readlink(link);

		const unlink = state.handlers.get("cliLink:unlink");
		expect(await unlink?.({}, true, target)).toMatchObject({ ok: true });
		expect(await lstat(link).catch(() => null)).toBeNull();
		expect(await linkOnFirstLaunch(bin)).toBeNull();
		expect(await lstat(link).catch(() => null)).toBeNull();
	});

	it("spends the first launch attempt when a link made by hand in Settings is removed", async () => {
		const bin = path.join(state.home, ".local", "bin");
		const firstLaunch = { pending: vi.fn(async () => true), spend: vi.fn(async () => undefined) };
		registerCliLinkIpc({
			isPackaged: () => true,
			resourcesPath: path.join(root, "resources"),
			appImage: undefined,
			dataDir: path.join(root, "data"),
			appVersion: "1.0.0",
			shellPath: () => bin,
			firstLaunch,
		});
		expect(await state.handlers.get("cliLink:link")?.({}, false, null)).toMatchObject({ ok: true, changed: true });
		expect(firstLaunch.spend).not.toHaveBeenCalled();
		expect(await state.handlers.get("cliLink:unlink")?.({}, true, await readlink(path.join(bin, "ao")))).toMatchObject({ ok: true });
		expect(firstLaunch.spend).toHaveBeenCalledTimes(1);
	});
});
