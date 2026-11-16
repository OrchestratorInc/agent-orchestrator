import { chmod, lstat, mkdir, mkdtemp, readFile, readlink, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { nodeCliLinkIo } from "../main/cli-link-fs";
import {
	CLI_LINK_DIRECTORY,
	CLI_LINK_MARKER_FILE,
	inspectCliLink,
	linkCli,
	selectCliLinkDirectory,
	unlinkCli,
	type CliLinkIo,
	type CliLinkRequest,
} from "./cli-link";

const roots: string[] = [];

afterEach(async () => {
	await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

async function layout() {
	const root = await mkdtemp(path.join(tmpdir(), "ao-cli-link-"));
	roots.push(root);
	const linkDirectory = path.join(root, "bin");
	const dataDir = path.join(root, "data");
	const apps = path.join(root, "apps");
	await mkdir(linkDirectory);
	await mkdir(apps, { recursive: true });
	return { root, linkDirectory, dataDir, apps };
}

async function executable(directory: string, name: string, version: string): Promise<string> {
	const file = path.join(directory, name);
	await mkdir(directory, { recursive: true });
	await writeFile(
		file,
		`#!/bin/sh\nif [ -n "$HOME" ]; then exit 4; fi\nif [ "$1" != version ]; then exit 2; fi\nprintf '%s\\n' '${version}'\n`,
	);
	await chmod(file, 0o755);
	return file;
}

function request(partial: Partial<CliLinkRequest> & Pick<CliLinkRequest, "appCliPath" | "linkDirectory" | "dataDir">): CliLinkRequest {
	return {
		developerMode: true,
		platform: "darwin",
		isPackaged: true,
		...partial,
	};
}

function counting(io: CliLinkIo): CliLinkIo & { symlinks: number } {
	const counted = { symlinks: 0 };
	return {
		...io,
		symlinks: 0,
		symlink: async (target, link) => {
			counted.symlinks += 1;
			countedHolder.symlinks = counted.symlinks;
			await io.symlink(target, link);
		},
	};
}

const countedHolder = { symlinks: 0 };

describe("cli link", () => {
	it("refuses the action when developer mode is off without touching the filesystem", async () => {
		const io: CliLinkIo = {
			...nodeCliLinkIo(),
			lstat: () => Promise.reject(new Error("filesystem touched")),
			version: () => Promise.reject(new Error("filesystem touched")),
		};
		const result = await inspectCliLink(io, request({
			developerMode: false,
			appCliPath: "/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao",
			linkDirectory: CLI_LINK_DIRECTORY,
			dataDir: "/tmp/does-not-matter",
		}));
		expect(result).toMatchObject({ ok: false, code: "developer-mode" });
	});

	it("refuses to inspect a real global bin directory from tests", async () => {
		await expect(inspectCliLink(nodeCliLinkIo(), request({
			appCliPath: "/tmp/ao",
			linkDirectory: CLI_LINK_DIRECTORY,
			dataDir: "/tmp/ao-cli-link-data",
		}))).rejects.toThrow(/real global CLI directory/);
	});

	it("explains unsupported Windows, AppImage, and unpackaged checkouts", async () => {
		const { linkDirectory, dataDir, apps } = await layout();
		const appCliPath = await executable(path.join(apps, "current"), "ao", "1.0.0");
		const base = request({ appCliPath, linkDirectory, dataDir });
		expect(await inspectCliLink(nodeCliLinkIo(), { ...base, platform: "win32" })).toMatchObject({
			ok: false,
			code: "unsupported",
		});
		expect((await inspectCliLink(nodeCliLinkIo(), { ...base, appImage: "/opt/AO.AppImage" })).ok).toBe(false);
		expect(await inspectCliLink(nodeCliLinkIo(), { ...base, isPackaged: false })).toMatchObject({ code: "unsupported" });
		expect(await lstat(path.join(linkDirectory, "ao")).catch(() => null)).toBeNull();
	});

	it("links a missing command, reports version from a clean environment, and is idempotent", async () => {
		const { linkDirectory, dataDir, apps } = await layout();
		const appCliPath = await executable(path.join(apps, "current"), "ao", "2.4.0");
		const io = counting(nodeCliLinkIo());
		countedHolder.symlinks = 0;
		const req = request({ appCliPath, linkDirectory, dataDir });
		const missing = await inspectCliLink(io, req);
		expect(missing).toMatchObject({ ok: true, status: { current: { kind: "missing" }, proposedVersion: "2.4.0" } });
		const linked = await linkCli(io, req);
		expect(linked).toMatchObject({ ok: true, changed: true, version: "2.4.0", targetPath: appCliPath });
		expect(await readlink(path.join(linkDirectory, "ao"))).toBe(appCliPath);
		const again = await linkCli(io, { ...req, confirmReplacement: true });
		expect(again).toMatchObject({ ok: true, changed: false, version: "2.4.0" });
		expect(countedHolder.symlinks).toBe(2);
		expect(await readFile(path.join(dataDir, CLI_LINK_MARKER_FILE), "utf8")).toContain(appCliPath);
	});

	it("does not replace an unrelated executable and will not unlink it", async () => {
		const { linkDirectory, dataDir, apps } = await layout();
		const appCliPath = await executable(path.join(apps, "current"), "ao", "3.0.0");
		const foreign = path.join(linkDirectory, "ao");
		await writeFile(foreign, "#!/bin/sh\nprintf 'other\\n'\n");
		await chmod(foreign, 0o755);
		const before = await readFile(foreign, "utf8");
		const req = request({ appCliPath, linkDirectory, dataDir, confirmReplacement: true });
		expect(await linkCli(nodeCliLinkIo(), req)).toMatchObject({ ok: false, code: "foreign" });
		expect(await unlinkCli(nodeCliLinkIo(), req)).toMatchObject({ ok: false, code: "foreign" });
		expect(await readFile(foreign, "utf8")).toBe(before);
	});

	it("requires confirmation before repairing a stale AO link and keeps the old binary", async () => {
		const { linkDirectory, dataDir, apps } = await layout();
		const oldCli = await executable(path.join(apps, "old"), "ao", "1.0.0");
		const appCliPath = await executable(path.join(apps, "current"), "ao", "4.0.0");
		const linkPath = path.join(linkDirectory, "ao");
		const io = nodeCliLinkIo();
		await io.symlink(oldCli, linkPath);
		await io.writeText(path.join(dataDir, CLI_LINK_MARKER_FILE), `${JSON.stringify({ version: 1, linkPath, targetPath: oldCli })}\n`);
		const req = request({ appCliPath, linkDirectory, dataDir });
		expect(await inspectCliLink(io, req)).toMatchObject({ ok: true, status: { current: { kind: "stale", target: oldCli }, proposedVersion: "4.0.0" } });
		expect(await linkCli(io, req)).toMatchObject({ ok: false, code: "confirmation-required" });
		expect(await readlink(linkPath)).toBe(oldCli);
		const repaired = await linkCli(io, { ...req, confirmReplacement: true, expectedTarget: oldCli });
		expect(repaired).toMatchObject({ ok: true, changed: true, version: "4.0.0", targetPath: appCliPath });
		expect(await readlink(linkPath)).toBe(appCliPath);
		expect(await readFile(oldCli, "utf8")).toContain("1.0.0");
	});

	it("repairs a dangling AO link and unlinks only that symlink", async () => {
		const { linkDirectory, dataDir, apps } = await layout();
		const appCliPath = await executable(path.join(apps, "current"), "ao", "5.0.0");
		const linkPath = path.join(linkDirectory, "ao");
		const gone = path.join(apps, "gone", "ao");
		const io = nodeCliLinkIo();
		await io.symlink(gone, linkPath);
		await io.writeText(path.join(dataDir, CLI_LINK_MARKER_FILE), `${JSON.stringify({ version: 1, linkPath, targetPath: gone })}\n`);
		const req = request({ appCliPath, linkDirectory, dataDir, confirmReplacement: true, expectedTarget: gone });
		expect(await linkCli(io, req)).toMatchObject({ ok: true, version: "5.0.0" });
		expect(await unlinkCli(io, { ...req, expectedTarget: appCliPath })).toMatchObject({ ok: true, changed: true });
		expect(await lstat(linkPath).catch(() => null)).toBeNull();
		expect(await lstat(appCliPath)).toBeTruthy();
		expect(await readFile(path.join(dataDir, CLI_LINK_MARKER_FILE), "utf8").catch(() => null)).toBeNull();
	});

	it("reports a permission failure and leaves the directory untouched", async () => {
		if (typeof process.getuid === "function" && process.getuid() === 0) return;
		const { linkDirectory, dataDir, apps } = await layout();
		const appCliPath = await executable(path.join(apps, "current"), "ao", "6.0.0");
		await chmod(linkDirectory, 0o555);
		try {
			const result = await linkCli(nodeCliLinkIo(), request({ appCliPath, linkDirectory, dataDir, confirmReplacement: true }));
			expect(result).toMatchObject({ ok: false, code: "permission" });
			expect(await lstat(path.join(linkDirectory, "ao")).catch(() => null)).toBeNull();
		} finally {
			await chmod(linkDirectory, 0o755);
		}
	});

	it("fails when the app CLI is missing", async () => {
		const { linkDirectory, dataDir, apps } = await layout();
		const result = await linkCli(nodeCliLinkIo(), request({
			appCliPath: path.join(apps, "missing", "ao"),
			linkDirectory,
			dataDir,
			confirmReplacement: true,
		}));
		expect(result).toMatchObject({ ok: false, code: "missing-app" });
	});

	it("never claims a user made symlink to the app CLI", async () => {
		const { linkDirectory, dataDir, apps } = await layout();
		const appCliPath = await executable(apps, "ao", "1.0.0");
		const linkPath = path.join(linkDirectory, "ao");
		await symlink(appCliPath, linkPath);
		const req = request({ appCliPath, linkDirectory, dataDir, confirmReplacement: true, expectedTarget: appCliPath });
		expect(await linkCli(nodeCliLinkIo(), req)).toMatchObject({ ok: false, code: "foreign" });
		expect(await unlinkCli(nodeCliLinkIo(), req)).toMatchObject({ ok: false, code: "foreign" });
		expect(await readlink(linkPath)).toBe(appCliPath);
		expect(await readFile(path.join(dataDir, CLI_LINK_MARKER_FILE)).catch(() => null)).toBeNull();
	});

	it("refuses a link replaced by the user after AO wrote its marker", async () => {
		const { linkDirectory, dataDir, apps } = await layout();
		const appCliPath = await executable(path.join(apps, "current"), "ao", "1.0.0");
		const userCli = await executable(path.join(apps, "user"), "ao", "2.0.0");
		const linkPath = path.join(linkDirectory, "ao");
		const io = nodeCliLinkIo();
		const req = request({ appCliPath, linkDirectory, dataDir });
		expect((await linkCli(io, req)).ok).toBe(true);
		await rm(linkPath);
		await symlink(userCli, linkPath);
		expect(await inspectCliLink(io, req)).toMatchObject({ ok: true, status: { current: { kind: "foreign", target: userCli } } });
		expect(await linkCli(io, { ...req, confirmReplacement: true, expectedTarget: userCli })).toMatchObject({ code: "foreign" });
		expect(await unlinkCli(io, { ...req, confirmReplacement: true, expectedTarget: userCli })).toMatchObject({ code: "foreign" });
		expect(await readlink(linkPath)).toBe(userCli);
	});

	it("keeps the old link and marker when replacement fails", async () => {
		const { linkDirectory, dataDir, apps } = await layout();
		const oldCli = await executable(path.join(apps, "old"), "ao", "1.0.0");
		const appCliPath = await executable(path.join(apps, "new"), "ao", "2.0.0");
		const io = nodeCliLinkIo();
		const linkPath = path.join(linkDirectory, "ao");
		await io.symlink(oldCli, linkPath);
		await io.writeText(path.join(dataDir, CLI_LINK_MARKER_FILE), JSON.stringify({ version: 1, linkPath, targetPath: oldCli }));
		const failing: CliLinkIo = { ...io, rename: async () => { throw Object.assign(new Error("disk failed"), { code: "EIO" }); } };
		const result = await linkCli(failing, request({ appCliPath, linkDirectory, dataDir, confirmReplacement: true, expectedTarget: oldCli }));
		expect(result).toMatchObject({ ok: false, code: "io" });
		expect(await readlink(linkPath)).toBe(oldCli);
		expect(await readFile(path.join(dataDir, CLI_LINK_MARKER_FILE), "utf8")).toContain(oldCli);
	});

	it("does not mutate the link when staged version verification fails", async () => {
		const { linkDirectory, dataDir, apps } = await layout();
		const appCliPath = await executable(apps, "ao", "1.0.0");
		const io = nodeCliLinkIo();
		let calls = 0;
		const failing: CliLinkIo = { ...io, version: async (file) => {
			if (++calls === 2) throw new Error("timeout");
			return io.version(file);
		} };
		expect(await linkCli(failing, request({ appCliPath, linkDirectory, dataDir }))).toMatchObject({ ok: false, code: "version" });
		expect(await lstat(path.join(linkDirectory, "ao")).catch(() => null)).toBeNull();
	});

	it("does not create a link when writing its marker fails", async () => {
		const { linkDirectory, dataDir, apps } = await layout();
		const appCliPath = await executable(apps, "ao", "1.0.0");
		const io = nodeCliLinkIo();
		const failing: CliLinkIo = { ...io, writeText: async () => { throw Object.assign(new Error("marker denied"), { code: "EACCES" }); } };
		expect(await linkCli(failing, request({ appCliPath, linkDirectory, dataDir }))).toMatchObject({ ok: false, code: "permission" });
		expect(await lstat(path.join(linkDirectory, "ao")).catch(() => null)).toBeNull();
	});

	it("refuses translocated and mounted macOS app bundles", async () => {
		const { linkDirectory, dataDir } = await layout();
		for (const appCliPath of ["/private/var/folders/tmp/AppTranslocation/id/AO.app/Contents/Resources/daemon/ao", "/Volumes/AO/AO.app/Contents/Resources/daemon/ao"]) {
			expect(await inspectCliLink(nodeCliLinkIo(), request({ appCliPath, linkDirectory, dataDir }))).toMatchObject({ ok: false, code: "unsupported" });
		}
	});

	it("reports the executable a fresh login shell finds first", async () => {
		const { root, linkDirectory, dataDir, apps } = await layout();
		const appCliPath = await executable(apps, "ao", "1.0.0");
		const earlier = path.join(root, "earlier");
		await executable(earlier, "ao", "other");
		const result = await inspectCliLink(nodeCliLinkIo(), request({
			appCliPath, linkDirectory, dataDir, shellPath: `${earlier}:${linkDirectory}`,
		}));
		expect(result.ok && result.status.message).toContain(path.join(earlier, "ao"));
		expect(await linkCli(nodeCliLinkIo(), request({
			appCliPath, linkDirectory, dataDir, shellPath: `${earlier}:${linkDirectory}`,
		}))).toMatchObject({ ok: false, code: "unsupported" });
		expect(await lstat(path.join(linkDirectory, "ao")).catch(() => null)).toBeNull();
	});

	it("selects a writable supported directory on the login PATH without changing the protected directory", async () => {
		const { root, linkDirectory, dataDir, apps } = await layout();
		const protectedDirectory = path.join(root, "protected");
		await mkdir(protectedDirectory);
		const io = nodeCliLinkIo();
		const restricted: CliLinkIo = { ...io, canWrite: async (directory) => directory === protectedDirectory ? false : io.canWrite(directory) };
		const shellPath = `${protectedDirectory}:${linkDirectory}`;
		expect(await selectCliLinkDirectory(restricted, dataDir, shellPath, [protectedDirectory, linkDirectory])).toBe(linkDirectory);
		const appCliPath = await executable(apps, "ao", "1.0.0");
		expect(await linkCli(restricted, request({ appCliPath, linkDirectory, dataDir, shellPath }))).toMatchObject({ ok: true, linkPath: path.join(linkDirectory, "ao") });
		expect(await lstat(path.join(protectedDirectory, "ao")).catch(() => null)).toBeNull();
	});

	it("keeps an existing AO managed link in its original directory for repair and unlink", async () => {
		const { root, linkDirectory, dataDir, apps } = await layout();
		const alternative = path.join(root, "alternative");
		await mkdir(alternative);
		const appCliPath = await executable(apps, "ao", "1.0.0");
		const io = nodeCliLinkIo();
		const req = request({ appCliPath, linkDirectory, dataDir, shellPath: `${linkDirectory}:${alternative}` });
		expect((await linkCli(io, req)).ok).toBe(true);
		expect(await selectCliLinkDirectory(io, dataDir, `${alternative}:${linkDirectory}`, [alternative, linkDirectory])).toBe(linkDirectory);
		expect(await unlinkCli(io, { ...req, confirmReplacement: true, expectedTarget: appCliPath })).toMatchObject({ ok: true, changed: true });
		expect(await lstat(path.join(alternative, "ao")).catch(() => null)).toBeNull();
	});

	it("refuses to link when the selected directory is absent from the login PATH", async () => {
		const { root, linkDirectory, dataDir, apps } = await layout();
		const appCliPath = await executable(apps, "ao", "1.0.0");
		const req = request({ appCliPath, linkDirectory, dataDir, shellPath: path.join(root, "other") });
		expect(await linkCli(nodeCliLinkIo(), req)).toMatchObject({ ok: false, code: "unsupported" });
		expect(await lstat(path.join(linkDirectory, "ao")).catch(() => null)).toBeNull();
	});

	it("rejects a stale target changed since the confirmation dialog opened", async () => {
		const { linkDirectory, dataDir, apps } = await layout();
		const oldCli = await executable(path.join(apps, "old"), "ao", "1.0.0");
		const appCliPath = await executable(path.join(apps, "new"), "ao", "2.0.0");
		const linkPath = path.join(linkDirectory, "ao");
		const io = nodeCliLinkIo();
		await io.symlink(oldCli, linkPath);
		await io.writeText(path.join(dataDir, CLI_LINK_MARKER_FILE), JSON.stringify({ version: 1, linkPath, targetPath: oldCli }));
		const req = request({ appCliPath, linkDirectory, dataDir, confirmReplacement: true, expectedTarget: "a different target" });
		expect(await linkCli(io, req)).toMatchObject({ ok: false, code: "confirmation-required" });
		expect(await unlinkCli(io, req)).toMatchObject({ ok: false, code: "confirmation-required" });
		expect(await readlink(linkPath)).toBe(oldCli);
	});

	it("reports a missing link directory as IO instead of permission denial", async () => {
		const { root, dataDir, apps } = await layout();
		const appCliPath = await executable(apps, "ao", "1.0.0");
		const result = await linkCli(nodeCliLinkIo(), request({ appCliPath, dataDir, linkDirectory: path.join(root, "absent") }));
		expect(result).toMatchObject({ ok: false, code: "io" });
		expect(result.ok || result.message).toMatch(/does not exist/);
	});
});
