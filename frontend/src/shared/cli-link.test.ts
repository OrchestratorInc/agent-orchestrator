import { chmod, lstat, mkdir, mkdtemp, readFile, readlink, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { nodeCliLinkIo } from "../main/cli-link-fs";
import {
	CLI_LINK_DIRECTORY,
	CLI_LINK_MARKER_FILE,
	inspectCliLink,
	linkCli,
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
		expect(countedHolder.symlinks).toBe(1);
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
		const repaired = await linkCli(io, { ...req, confirmReplacement: true });
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
		const req = request({ appCliPath, linkDirectory, dataDir, confirmReplacement: true });
		expect(await linkCli(io, req)).toMatchObject({ ok: true, version: "5.0.0" });
		expect(await unlinkCli(io, req)).toMatchObject({ ok: true, changed: true });
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
});
