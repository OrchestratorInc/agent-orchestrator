import { chmod, lstat, mkdtemp, readFile, symlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TelemetryPolicyAuthority, nodeTelemetryPolicyFileSystem, resolveDesktopDataDir } from "./telemetry-policy-file";

const dirs: string[] = [];
const makeDir = async () => {
	const dir = await mkdtemp(path.join(os.tmpdir(), "ao-policy-"));
	dirs.push(dir);
	return dir;
};

/** Windows 不提供 POSIX 权限位和目录 fsync；只在已有 I/O 边界模拟这两项，读写仍使用真实磁盘。 */
function posixFileSystem(mode = 0o600) {
	const base = nodeTelemetryPolicyFileSystem;
	return {
		...base,
		lstat: async (target: string) => {
			const metadata = await base.lstat(target);
			return { ...metadata, mode: process.platform === "win32" ? mode : metadata.mode,
				isFile: () => metadata.isFile(), isSymbolicLink: () => metadata.isSymbolicLink() };
		},
		readFile: vi.fn(base.readFile),
		syncDirectory: vi.fn(async (target: string) => {
			if (process.platform !== "win32") await base.syncDirectory(target);
		}),
	};
}

afterEach(async () => {
	const { rm } = await import("node:fs/promises");
	await Promise.all(dirs.splice(0).map((dir) => rm(dir, { recursive: true, force: true })));
});

describe("TelemetryPolicyAuthority", () => {
	it("materializes the packaged default with owner-only creation and POSIX durability", async () => {
		const dataDir = await makeDir();
		const fs = posixFileSystem();
		const openExclusive = vi.spyOn(fs, "openExclusive");
		const authority = new TelemetryPolicyAuthority({ dataDir, packagedDefault: true, platform: "linux", fs });
		const snapshot = await authority.load();
		const policyPath = path.join(dataDir, "telemetry_policy.json");
		expect(snapshot.eventsEnabled).toBe(true);
		expect(snapshot.acknowledged).toBe(true);
		expect(openExclusive).toHaveBeenCalledWith(expect.any(String), 0o600);
		expect(fs.syncDirectory).toHaveBeenCalledWith(dataDir);
		if (process.platform !== "win32") expect((await lstat(policyPath)).mode & 0o777).toBe(0o600);
		expect(JSON.parse(await readFile(policyPath, "utf8"))).toEqual({
			schema_version: 1,
			events_enabled: true,
			consent_generation: snapshot.consentGeneration,
			updated_at: snapshot.updatedAt,
		});
	});

	it("keeps a version 1 opt-in enabled without rewriting it", async () => {
		const dataDir = await makeDir();
		const policyPath = path.join(dataDir, "telemetry_policy.json");
		const raw = JSON.stringify({
			schema_version: 1,
			events_enabled: true,
			consent_generation: "7f80c8a9-ec67-4a16-a067-a444ffcc5cca",
			updated_at: "2026-08-28T10:15:30.000Z",
		});
		await writeFile(policyPath, raw, { mode: 0o600 });
		const snapshot = await new TelemetryPolicyAuthority({ dataDir, packagedDefault: true }).load();
		expect(snapshot).toMatchObject({ eventsEnabled: true, acknowledged: true });
		expect(await readFile(policyPath, "utf8")).toBe(raw);
	});

	it("keeps a legacy version 2 opt-out despite its obsolete production flag", async () => {
		const dataDir = await makeDir();
		await writeFile(path.join(dataDir, "telemetry_policy.json"), JSON.stringify({
			schema_version: 2,
			events_enabled: false,
			consent_generation: "7f80c8a9-ec67-4a16-a067-a444ffcc5cca",
			consent_production_enabled: true,
			updated_at: "2026-08-28T10:15:30.000Z",
		}), { mode: 0o600 });
		const snapshot = await new TelemetryPolicyAuthority({ dataDir, packagedDefault: true }).load();
		expect(snapshot).toMatchObject({ eventsEnabled: false, acknowledged: true });
	});

	it.each(["symlink", "group-readable", "malformed"])("fails closed without replacing an unsafe %s authority", async (kind) => {
		const dataDir = await makeDir();
		const policyPath = path.join(dataDir, "telemetry_policy.json");
		const valid = JSON.stringify({ schema_version: 1, events_enabled: true,
			consent_generation: "7f80c8a9-ec67-4a16-a067-a444ffcc5cca", updated_at: "2026-08-28T10:15:30.000Z" });
		const fs = posixFileSystem(kind === "group-readable" ? 0o640 : 0o600);
		if (kind === "symlink") {
			const target = path.join(dataDir, "target.json");
			await writeFile(target, valid, { mode: 0o600 });
			if (process.platform === "win32") {
				await writeFile(policyPath, valid, { mode: 0o600 });
				const original = fs.lstat;
				fs.lstat = async (file) => ({ ...await original(file), isSymbolicLink: () => file === policyPath });
			} else await symlink(target, policyPath);
		} else {
			await writeFile(policyPath, kind === "malformed" ? "not-json" : valid, { mode: 0o600 });
			if (kind === "group-readable") await chmod(policyPath, 0o640);
		}
		const before = await readFile(policyPath, "utf8");
		const authority = new TelemetryPolicyAuthority({ dataDir, packagedDefault: true, platform: "linux", fs });
		const snapshot = await authority.load();
		expect(snapshot.eventsEnabled).toBe(false);
		expect(snapshot.acknowledged).toBe(false);
		expect(fs.readFile).toHaveBeenCalledTimes(kind === "malformed" ? 1 : 0);
		await expect(authority.setEventsEnabled(true)).rejects.toThrow("unsafe");
		expect(await readFile(policyPath, "utf8")).toBe(before);
	});

	it("does not acknowledge a replace until the containing directory is synced", async () => {
		const dataDir = await makeDir();
		const base = posixFileSystem();
		const authority = new TelemetryPolicyAuthority({
			dataDir,
			packagedDefault: false,
			platform: "linux",
			fs: { ...base, syncDirectory: async () => { throw new Error("directory fsync failed"); } },
		});
		await expect(authority.load()).rejects.toThrow("directory fsync failed");
		expect(authority.snapshot().eventsEnabled).toBe(false);
		expect(authority.snapshot().acknowledged).toBe(false);
	});

	it("retries an unacknowledged replacement without changing its generation", async () => {
		const dataDir = await makeDir();
		const base = posixFileSystem();
		let syncAttempts = 0;
		const authority = new TelemetryPolicyAuthority({
			dataDir,
			packagedDefault: false,
			platform: "linux",
			fs: {
				...base,
				syncDirectory: async (target) => {
					syncAttempts++;
					if (syncAttempts === 1) throw new Error("directory fsync failed");
					await base.syncDirectory(target);
				},
			},
		});
		await expect(authority.load()).rejects.toThrow("directory fsync failed");
		const pending = authority.snapshot();
		expect(pending.acknowledged).toBe(false);

		const retried = await authority.retryPendingReplacement();

		expect(retried).toMatchObject({
			eventsEnabled: pending.eventsEnabled,
			consentGeneration: pending.consentGeneration,
			updatedAt: pending.updatedAt,
			acknowledged: true,
		});
		expect(syncAttempts).toBe(2);
	});

	it("supports Windows defaults and durable opt-out replacements", async () => {
		const dataDir = await makeDir();
		const syncDirectory = vi.fn(nodeTelemetryPolicyFileSystem.syncDirectory);
		const authority = new TelemetryPolicyAuthority({ dataDir, packagedDefault: true, platform: "win32",
			fs: { ...nodeTelemetryPolicyFileSystem, syncDirectory } });
		expect(authority.durabilitySupported).toBe(true);
		expect(await authority.load()).toMatchObject({ eventsEnabled: true, acknowledged: true });
		expect(await authority.setEventsEnabled(false)).toMatchObject({ eventsEnabled: false, acknowledged: true });
		expect(await new TelemetryPolicyAuthority({ dataDir, packagedDefault: true, platform: "win32" }).load()).toMatchObject({ eventsEnabled: false, acknowledged: true });
		expect(syncDirectory).not.toHaveBeenCalled();
	});

	it.each(["EPERM", "EBUSY"])("retries a Windows %s replacement without changing the policy generation", async (code) => {
		const dataDir = await makeDir();
		const base = nodeTelemetryPolicyFileSystem;
		let attempts = 0;
		const authority = new TelemetryPolicyAuthority({
			dataDir, packagedDefault: true, platform: "win32",
			fs: { ...base, rename: async (from, to) => {
				attempts++;
				if (attempts === 2) throw Object.assign(new Error("locked"), { code });
				await base.rename(from, to);
			} },
		});
		await authority.load();
		const disabled = await authority.setEventsEnabled(false);
		expect(disabled).toMatchObject({ eventsEnabled: false, acknowledged: true });
		expect(attempts).toBe(3);
		expect((await new TelemetryPolicyAuthority({ dataDir, packagedDefault: true, platform: "win32" }).load()).consentGeneration).toBe(disabled.consentGeneration);
	});

});

it("resolves one absolute data directory against the daemon launch cwd", () => {
	const homeDir = path.resolve("/home/ao");
	const launchWorkingDirectory = path.resolve("/work/checkout");
	expect(resolveDesktopDataDir({ AO_DATA_DIR: "relative-data" }, homeDir, launchWorkingDirectory, false)).toBe(path.join(launchWorkingDirectory, "relative-data"));
	expect(resolveDesktopDataDir({}, homeDir, launchWorkingDirectory, true)).toBe(path.join(homeDir, ".ao", "data"));
	expect(resolveDesktopDataDir({}, homeDir, launchWorkingDirectory, false)).toBe(path.join(homeDir, ".ao", "dev", "data"));
});
