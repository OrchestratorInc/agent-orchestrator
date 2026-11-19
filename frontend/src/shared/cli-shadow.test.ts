import { chmod, lstat, mkdir, mkdtemp, readdir, readFile, readlink, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { nodeCliShadowIo } from "../main/cli-link-fs";
import { cliShadowEnabled, inspectCliShadow, replaceCliShadow as replaceCliShadowImpl, type CliShadowIo, type CliShadowRequest } from "./cli-shadow";

async function replaceCliShadow(io: CliShadowIo, req: CliShadowRequest, expected: { path: string; target: string; identity?: string }) {
	return replaceCliShadowImpl(io, req, { ...expected, identity: expected.identity ?? await io.identity(expected.path) ?? "missing" });
}

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

const request = (shellPath: string | undefined, bundledPath: string | undefined, platform: NodeJS.Platform = "darwin", isPackaged = true): CliShadowRequest =>
	({ platform, shellPath, bundledPath, isPackaged });

const inspect = async (shellPath: string | undefined, bundledPath: string | undefined, platform: NodeJS.Platform = "darwin", isPackaged = true) => {
	const io = nodeCliShadowIo();
	const probeIdentity = shellPath ? await io.identity(path.join(shellPath.split(":")[0] ?? "", "ao")) : null;
	return inspectCliShadow(io, { ...request(shellPath, bundledPath, platform, isPackaged), probeIdentity: probeIdentity ?? undefined });
};

describe("cliShadowEnabled", () => {
	it("applies only to an installed app on its default profile", () => {
		expect(cliShadowEnabled({}, true)).toBe(true);
	});

	it.each([
		["a dev build", {}, false],
		["an isolated data dir", { AO_DATA_DIR: "/tmp/scratch" }, true],
		["an isolated run file", { AO_RUN_FILE: "/tmp/running.json" }, true],
	] as const)("stays off in %s", (_name, env, isPackaged) => {
		expect(cliShadowEnabled(env, isPackaged)).toBe(false);
	});

	it("treats a blank override as unset", () => {
		expect(cliShadowEnabled({ AO_DATA_DIR: "  ", AO_RUN_FILE: "" }, true)).toBe(true);
	});
});

describe("inspectCliShadow", () => {
	it("does not execute a different file using consent for an earlier identity", async () => {
		const { shadowDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "old");
		const real = nodeCliShadowIo();
		const identity = await real.identity(shadowPath) as string;
		await writeFile(shadowPath, "#!/bin/sh\necho changed\n");
		const executed: string[] = [];
		const io = { ...real, version: async (file: string) => { executed.push(file); return real.version(file); } };
		await expect(inspectCliShadow(io, { ...request(shadowDir, bundledPath), probeIdentity: identity })).resolves.toMatchObject({ versionChecked: false });
		expect(executed).not.toContain(shadowPath);
	});

	it("does not execute the other ao during passive inspection", async () => {
		const { shadowDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "old");
		const real = nodeCliShadowIo();
		const executed: string[] = [];
		const io = { ...real, version: async (file: string) => { executed.push(file); return real.version(file); } };
		await inspectCliShadow(io, { ...request(shadowDir, bundledPath), probeIdentity: undefined });
		expect(executed).not.toContain(shadowPath);
	});
	it("does not suggest a global uninstall for a local npm executable", async () => {
		const { root, shadowDir, bundledPath } = await layout();
		const pkgDir = path.join(root, "project/node_modules/ao-cli/bin");
		await mkdir(pkgDir, { recursive: true });
		await symlink(await executable(pkgDir, "old"), path.join(shadowDir, "ao"));
		await expect(inspect(shadowDir, bundledPath)).resolves.toMatchObject({ fix: { kind: "manual", command: `rm ${path.join(shadowDir, "ao")}` } });
	});
	it("names the other ao, its version, and the bundled path when another ao wins", async () => {
		const { shadowDir, appDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "ao version dev");
		await expect(inspect(`${shadowDir}:${appDir}`, bundledPath)).resolves.toMatchObject({
			kind: "shadowed",
			shadowPath,
			shadowVersion: "ao version dev",
			bundledPath,
			bundledVersion: "app-version",
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

describe("inspectCliShadow version relation", () => {
	it.each([
		["0.13.0 commit abc built 2026-01-01", "0.14.8", "older", "0.13.0"],
		["0.14.8", "0.14.8 commit abc", "same", "0.14.8"],
		["0.15.0", "0.14.8", "newer", "0.15.0"],
		["ao version 0.13.0", "0.14.8", "older", "0.13.0"],
		["v0.13.0", "0.14.8", "older", "0.13.0"],
		["0.14.9-nightly.202611030413 commit 2895333c5d7e", "0.14.9", "older", "0.14.9-nightly.202611030413"],
		["dev", "0.14.8", "different", "dev"],
		["0.14.8", "dev", "different", "0.14.8"],
		["dev", "dev", "different", "dev"],
	])("compares %s with %s as %s", async (shadowLine, bundledLine, relation, shadowVersion) => {
		const { shadowDir, appDir } = await layout();
		await executable(shadowDir, shadowLine);
		const bundledPath = await executable(appDir, bundledLine);
		await expect(inspect(shadowDir, bundledPath)).resolves.toMatchObject({ relation, shadowVersion });
	});

	it("reads as different when the other ao cannot answer", async () => {
		const { shadowDir, appDir } = await layout();
		await executable(shadowDir, "broken", 1);
		const bundledPath = await executable(appDir, "0.14.8");
		await expect(inspect(shadowDir, bundledPath)).resolves.toMatchObject({ relation: "different", shadowVersion: null, bundledVersion: "0.14.8" });
	});
});

describe("inspectCliShadow fix", () => {
	async function linkedInto(relative: string) {
		const fixture = await layout();
		const target = path.join(fixture.root, relative);
		await mkdir(path.dirname(target), { recursive: true });
		await writeFile(target, "#!/bin/sh\necho 0.1.0\n");
		await chmod(target, 0o755);
		await symlink(target, path.join(fixture.shadowDir, "ao"));
		return fixture;
	}

	it("offers to replace a writable symlink", async () => {
		const { root, shadowDir, bundledPath } = await layout();
		const oldDir = path.join(root, "old");
		await mkdir(oldDir);
		await symlink(await executable(oldDir, "old"), path.join(shadowDir, "ao"));
		await expect(inspect(shadowDir, bundledPath)).resolves.toMatchObject({ fix: { kind: "replace", replaces: "link" } });
	});

	it("offers to replace a writable file, and says it is a file", async () => {
		const { shadowDir, bundledPath } = await layout();
		await executable(shadowDir, "old");
		await expect(inspect(shadowDir, bundledPath)).resolves.toMatchObject({ fix: { kind: "replace", replaces: "file" } });
	});

	it.each([
		["Cellar/ao/0.1.0/bin/ao", "brew uninstall ao"],
		["Caskroom/ao-app/0.1.0/ao", "brew uninstall --cask ao-app"],
	])("leaves a writable package-manager install at %s to its manager", async (relative, command) => {
		const { shadowDir, bundledPath } = await linkedInto(relative);
		await expect(inspect(shadowDir, bundledPath)).resolves.toMatchObject({ fix: { kind: "manual", command } });
	});

	it("gives a sudo command when the directory is not writable", async () => {
		const { shadowDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "old");
		const io: CliShadowIo = { ...nodeCliShadowIo(), canWrite: async () => false };
		await expect(inspectCliShadow(io, request(shadowDir, bundledPath))).resolves.toMatchObject({ fix: { kind: "manual", command: `sudo rm ${shadowPath}` } });
	});

	it("does not offer a link from a build that is not installed", async () => {
		const { shadowDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "old");
		await expect(inspect(shadowDir, bundledPath, "darwin", false)).resolves.toMatchObject({ fix: { kind: "manual", command: `rm ${shadowPath}` } });
	});

	it("quotes a path with spaces in the command", async () => {
		const { root, bundledPath } = await layout();
		const spaced = path.join(root, "it's here");
		await mkdir(spaced);
		const shadowPath = await executable(spaced, "old");
		const status = await inspect(spaced, bundledPath, "darwin", false);
		expect(status).toMatchObject({ fix: { kind: "manual", command: `rm '${shadowPath.replace("'", "'\\''")}'` } });
	});
});

describe("replaceCliShadow", () => {
	it("refuses a regular file edited after inspection", async () => {
		const { shadowDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "old");
		const io = nodeCliShadowIo();
		const identity = await io.identity(shadowPath) as string;
		await writeFile(shadowPath, "#!/bin/sh\necho updated\n");
		await expect(replaceCliShadow(io, request(shadowDir, bundledPath), { path: shadowPath, target: await io.realpath(shadowPath) as string, identity })).resolves.toMatchObject({ ok: false, code: "changed" });
		await expect(readFile(shadowPath, "utf8")).resolves.toContain("updated");
	});
	it("refuses a file changed while the staged link is verified", async () => {
		const { shadowDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "old");
		const real = nodeCliShadowIo();
		const identity = await real.identity(shadowPath) as string;
		const io: CliShadowIo = { ...real, version: async (file) => {
			if (file.endsWith(".tmp")) await writeFile(shadowPath, "#!/bin/sh\necho updated\n");
			return real.version(file);
		} };
		await expect(replaceCliShadow(io, request(shadowDir, bundledPath), { path: shadowPath, target: await real.realpath(shadowPath) as string, identity })).resolves.toMatchObject({ ok: false, code: "changed" });
		await expect(readFile(shadowPath, "utf8")).resolves.toContain("updated");
		await expect(readdir(shadowDir)).resolves.toEqual(["ao"]);
	});

	it("replaces a symlink and leaves the file it pointed to", async () => {
		const { root, shadowDir, bundledPath } = await layout();
		const oldDir = path.join(root, "old");
		await mkdir(oldDir);
		const oldTarget = await executable(oldDir, "old");
		const shadowPath = path.join(shadowDir, "ao");
		await symlink(oldTarget, shadowPath);
		const io = nodeCliShadowIo();
		await expect(replaceCliShadow(io, request(shadowDir, bundledPath), { path: shadowPath, target: await io.realpath(oldTarget) as string })).resolves.toEqual({ ok: true });
		await expect(readlink(shadowPath)).resolves.toBe(bundledPath);
		await expect(readFile(oldTarget, "utf8")).resolves.toContain("old");
		await expect(inspect(shadowDir, bundledPath)).resolves.toEqual({ kind: "match" });
		await expect(readdir(shadowDir)).resolves.toEqual(["ao"]);
	});

	it("replaces a regular file", async () => {
		const { shadowDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "old");
		const io = nodeCliShadowIo();
		await expect(replaceCliShadow(io, request(shadowDir, bundledPath), { path: shadowPath, target: await io.realpath(shadowPath) as string })).resolves.toEqual({ ok: true });
		expect((await lstat(shadowPath)).isSymbolicLink()).toBe(true);
	});

	it("refuses a path that is not the current other ao", async () => {
		const { root, shadowDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "old");
		const unrelated = path.join(root, "unrelated");
		await writeFile(unrelated, "keep me");
		await expect(replaceCliShadow(nodeCliShadowIo(), request(shadowDir, bundledPath), { path: unrelated, target: unrelated })).resolves.toMatchObject({ ok: false, code: "changed" });
		await expect(readFile(unrelated, "utf8")).resolves.toBe("keep me");
		await expect(readFile(shadowPath, "utf8")).resolves.toContain("old");
	});

	it("refuses when the file changed after the user confirmed", async () => {
		const { shadowDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "old");
		await expect(replaceCliShadow(nodeCliShadowIo(), request(shadowDir, bundledPath), { path: shadowPath, target: "/somewhere/else/ao" })).resolves.toMatchObject({ ok: false, code: "changed" });
		expect((await lstat(shadowPath)).isSymbolicLink()).toBe(false);
	});

	it("refuses a package-manager install even when asked directly", async () => {
		const { root, shadowDir, bundledPath } = await layout();
		const target = path.join(root, "Cellar/ao/0.1.0/bin/ao");
		await mkdir(path.dirname(target), { recursive: true });
		await writeFile(target, "#!/bin/sh\necho 0.1.0\n");
		await chmod(target, 0o755);
		const shadowPath = path.join(shadowDir, "ao");
		await symlink(target, shadowPath);
		const io = nodeCliShadowIo();
		await expect(replaceCliShadow(io, request(shadowDir, bundledPath), { path: shadowPath, target: await io.realpath(target) as string })).resolves.toMatchObject({ ok: false, code: "changed" });
		await expect(readlink(shadowPath)).resolves.toBe(target);
	});

	it("keeps the other ao and cleans up when the new link does not run", async () => {
		const { shadowDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "old");
		const real = nodeCliShadowIo();
		const io: CliShadowIo = { ...real, version: async (file) => { if (file.endsWith(".tmp")) throw new Error("bad link"); return real.version(file); } };
		await expect(replaceCliShadow(io, request(shadowDir, bundledPath), { path: shadowPath, target: await real.realpath(shadowPath) as string })).resolves.toMatchObject({ ok: false, code: "version" });
		await expect(readdir(shadowDir)).resolves.toEqual(["ao"]);
		expect((await lstat(shadowPath)).isSymbolicLink()).toBe(false);
	});

	it("reports a permission failure and leaves no staged link", async () => {
		const { shadowDir, bundledPath } = await layout();
		const shadowPath = await executable(shadowDir, "old");
		const real = nodeCliShadowIo();
		const io: CliShadowIo = { ...real, rename: async () => { throw Object.assign(new Error("denied"), { code: "EACCES" }); } };
		await expect(replaceCliShadow(io, request(shadowDir, bundledPath), { path: shadowPath, target: await real.realpath(shadowPath) as string })).resolves.toMatchObject({ ok: false, code: "permission" });
		await expect(readdir(shadowDir)).resolves.toEqual(["ao"]);
	});
});
