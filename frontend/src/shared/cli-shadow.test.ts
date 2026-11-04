import { chmod, mkdir, mkdtemp, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { nodeCliShadowIo } from "../main/cli-link-fs";
import { inspectCliShadow } from "./cli-shadow";

const roots: string[] = [];

afterEach(async () => {
	await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

async function layout() {
	const root = await mkdtemp(path.join(tmpdir(), "ao-cli-shadow-"));
	roots.push(root);
	const shadowDir = path.join(root, "shadow");
	const appDir = path.join(root, "app");
	await mkdir(shadowDir);
	await mkdir(appDir);
	return { root, shadowDir, appDir, bundledPath: await executable(appDir, "app-version") };
}

async function executable(directory: string, version: string, status = 0): Promise<string> {
	const file = path.join(directory, "ao");
	await writeFile(file, `#!/bin/sh\necho '${version}'\nexit ${status}\n`);
	await chmod(file, 0o755);
	return file;
}

const inspect = (shellPath: string | undefined, bundledPath: string | undefined, platform: NodeJS.Platform = "darwin") =>
	inspectCliShadow(nodeCliShadowIo(), { platform, shellPath, bundledPath });

describe("inspectCliShadow", () => {
	it("names the other ao, its version, and the bundled path when another ao wins", async () => {
		const { shadowDir, appDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "ao version dev");
		await expect(inspect(`${shadowDir}:${appDir}`, bundledPath)).resolves.toEqual({
			kind: "shadowed",
			shadowPath,
			shadowVersion: "ao version dev",
			bundledPath,
		});
	});

	it("warns when the bundled directory is not on PATH at all, as in a regular terminal", async () => {
		const { shadowDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "old");
		await expect(inspect(shadowDir, bundledPath)).resolves.toMatchObject({ kind: "shadowed", shadowPath });
	});

	it("still warns when the other ao cannot report a version", async () => {
		const { shadowDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "no prebuilt binary", 1);
		await expect(inspect(shadowDir, bundledPath)).resolves.toMatchObject({ kind: "shadowed", shadowPath, shadowVersion: null });
	});

	it("matches when the bundled ao comes first", async () => {
		const { shadowDir, appDir, bundledPath } = await layout();
		await executable(shadowDir, "old");
		await expect(inspect(`${appDir}:${shadowDir}`, bundledPath)).resolves.toEqual({ kind: "match" });
	});

	it("matches a symlink to the bundled ao, such as the Developer Mode CLI link", async () => {
		const { shadowDir, bundledPath } = await layout();
		await symlink(bundledPath, path.join(shadowDir, "ao"));
		await expect(inspect(shadowDir, bundledPath)).resolves.toEqual({ kind: "match" });
	});

	it("flags a symlink to a different binary", async () => {
		const { root, shadowDir, bundledPath } = await layout();
		const oldDir = path.join(root, "old");
		await mkdir(oldDir);
		const target = await executable(oldDir, "old");
		await symlink(target, path.join(shadowDir, "ao"));
		await expect(inspect(shadowDir, bundledPath)).resolves.toMatchObject({ kind: "shadowed", shadowPath: path.join(shadowDir, "ao") });
	});

	it("reports absent when no ao is on PATH", async () => {
		const { shadowDir, bundledPath } = await layout();
		await expect(inspect(shadowDir, bundledPath)).resolves.toEqual({ kind: "absent" });
	});

	it("skips what a shell would skip: non-executable files, directories, and relative or empty entries", async () => {
		const { root, shadowDir, appDir, bundledPath } = await layout();
		await writeFile(path.join(shadowDir, "ao"), "not executable");
		const withDirectory = path.join(root, "dir-entry");
		await mkdir(path.join(withDirectory, "ao"), { recursive: true });
		await expect(inspect(`:relative:${shadowDir}:${withDirectory}:${appDir}`, bundledPath)).resolves.toEqual({ kind: "match" });
	});

	it("answers unknown instead of guessing when the probe failed", async () => {
		const { bundledPath } = await layout();
		await expect(inspect(undefined, bundledPath)).resolves.toEqual({ kind: "unknown" });
	});

	it("answers unknown without a bundled binary to compare against", async () => {
		const { root, shadowDir } = await layout();
		await executable(shadowDir, "old");
		await expect(inspect(shadowDir, undefined)).resolves.toEqual({ kind: "unknown" });
		await expect(inspect(shadowDir, path.join(root, "missing", "ao"))).resolves.toEqual({ kind: "unknown" });
	});

	it("answers unknown on Windows", async () => {
		const { shadowDir, bundledPath } = await layout();
		await executable(shadowDir, "old");
		await expect(inspect(shadowDir, bundledPath, "win32")).resolves.toEqual({ kind: "unknown" });
	});
});
