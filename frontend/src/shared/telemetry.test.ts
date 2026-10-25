import { access, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, expect, test } from "vitest";
import {
	bootstrapDistinctId,
	buildTelemetryBootstrap,
	defaultDataDir,
	loadOrCreateTelemetryInstallId,
	parseDisabledEvents,
	rendererTelemetryEnabled,
} from "./telemetry";

const tempDirs: string[] = [];
const enabledPolicy = { eventsEnabled: true, consentGeneration: "7f80c8a9-ec67-4a16-a067-a444ffcc5cca", updatedAt: "2026-08-28T10:15:30.000Z", acknowledged: true, consentRenewalRequired: false };

afterEach(async () => {
	await Promise.all(
		tempDirs
			.splice(0)
			.map((dir) => import("node:fs/promises").then(({ rm }) => rm(dir, { recursive: true, force: true }))),
	);
});

test("defaultDataDir prefers AO_DATA_DIR", () => {
	expect(defaultDataDir("linux", { AO_DATA_DIR: "/tmp/custom" }, "/home/test")).toBe("/tmp/custom");
});

test("loadOrCreateTelemetryInstallId persists a stable install id", async () => {
	const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
	tempDirs.push(dir);

	const first = await loadOrCreateTelemetryInstallId(dir);
	const second = await loadOrCreateTelemetryInstallId(dir);
	const stored = (await readFile(path.join(dir, "telemetry_install_id"), "utf8")).trim();

	expect(first).toMatch(/^ins_/);
	expect(second).toBe(first);
	expect(stored).toBe(first);
});

test("loadOrCreateTelemetryInstallId heals an empty file and publishes one id to concurrent callers", async () => {
	const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
	tempDirs.push(dir);
	const file = path.join(dir, "telemetry_install_id");
	await writeFile(file, " \n");
	const recovered = await loadOrCreateTelemetryInstallId(dir);
	expect(recovered).toMatch(/^ins_/);
	expect((await readFile(file, "utf8")).trim()).toBe(recovered);

	for (const legacyEmpty of [false, true]) {
		const concurrentDir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
		tempDirs.push(concurrentDir);
		if (legacyEmpty) await writeFile(path.join(concurrentDir, "telemetry_install_id"), "\n");
		const ids = await Promise.all(Array.from({ length: 16 }, () => loadOrCreateTelemetryInstallId(concurrentDir)));
		expect(new Set(ids).size).toBe(1);
		expect((await readFile(path.join(concurrentDir, "telemetry_install_id"), "utf8")).trim()).toBe(ids[0]);
	}
});

test("buildTelemetryBootstrap returns null when no home dir is available", async () => {
	await expect(buildTelemetryBootstrap({}, "1.2.3", "linux", "", true, enabledPolicy)).resolves.toBeNull();
});

test("renderer telemetry is off on unpackaged builds unless explicitly opted in", () => {
	expect(rendererTelemetryEnabled({}, false)).toBe(false);
	expect(rendererTelemetryEnabled({}, true)).toBe(true);
	expect(rendererTelemetryEnabled({ AO_TELEMETRY_RENDERER: " ON " }, false)).toBe(true);
	expect(rendererTelemetryEnabled({ AO_TELEMETRY_RENDERER: "off" }, true)).toBe(false);
});

// A null bootstrap is the switch itself: initTelemetry bails on it, so an
// unpackaged build never constructs a PostHog client at all.
test("buildTelemetryBootstrap withholds the bootstrap on an unpackaged build", async () => {
	const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
	tempDirs.push(dir);

	const disabled = await buildTelemetryBootstrap({}, "0.11.2", "linux", dir, false, enabledPolicy);
	expect(disabled).toBeNull();
	const optedIn = await buildTelemetryBootstrap({ AO_TELEMETRY_RENDERER: "on" }, "0.11.2", "linux", dir, false, enabledPolicy);
	expect(optedIn?.appVersion).toBe("0.11.2");
});

test("failure-reporting opt-out does not withhold PostHog identity", async () => {
	const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
	tempDirs.push(dir);
	const bootstrap = await buildTelemetryBootstrap({}, "0.11.2", "linux", dir, true, {
		...enabledPolicy,
		eventsEnabled: false,
	});
	expect(bootstrap?.eventsEnabled).toBe(false);
	expect(bootstrap?.distinctId).toMatch(/^ins_/);
});

test("buildTelemetryBootstrap carries the deny list across the process boundary", async () => {
	const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
	tempDirs.push(dir);

	const bootstrap = await buildTelemetryBootstrap(
		{ AO_TELEMETRY_DISABLED_EVENTS: "ao.v2.app.active, ao.renderer.* ,, " },
		"0.11.2",
		"linux",
		dir,
		true,
		enabledPolicy,
	);
	expect(bootstrap?.disabledEvents).toEqual(["ao.v2.app.active", "ao.renderer.*"]);
});

test("parseDisabledEvents treats absent or blank policy as no policy", () => {
	expect(parseDisabledEvents(undefined)).toEqual([]);
	expect(parseDisabledEvents(" , , ")).toEqual([]);
});

test("buildTelemetryBootstrap withholds an install id linked to an AO Cloud account", async () => {
	const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
	tempDirs.push(dir);
	const env = { AO_DATA_DIR: dir };
	const installId = await loadOrCreateTelemetryInstallId(dir);

	const unlinked = await buildTelemetryBootstrap(env, "1.2.3", "linux", "/home/test", true, enabledPolicy);
	expect(unlinked?.distinctId).toBe(installId);
	expect(unlinked?.installId).toBe(installId);

	await writeFile(path.join(dir, "telemetry_bound_user"), "user_A\n");
	const linked = await buildTelemetryBootstrap(env, "1.2.3", "linux", "/home/test", true, enabledPolicy);
	const again = await buildTelemetryBootstrap(env, "1.2.3", "linux", "/home/test", true, enabledPolicy);
	expect(linked?.distinctId).toMatch(/^anon_/);
	expect(linked?.distinctId).not.toBe(installId);
	expect(again?.distinctId).not.toBe(linked?.distinctId); // throwaway, never reused across launches
	expect(linked?.installId).toBe(installId);
	expect(again?.installId).toBe(installId);
	expect(await readFile(path.join(dir, "telemetry_install_id"), "utf8")).toContain(installId);
});

test("buildTelemetryBootstrap withholds the install id when the bound-user marker cannot be inspected", async () => {
	const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
	tempDirs.push(dir);
	const installId = await loadOrCreateTelemetryInstallId(dir);
	// A self-referencing symlink fails with ELOOP, not ENOENT.
	await symlink("telemetry_bound_user", path.join(dir, "telemetry_bound_user"));

	const bootstrap = await buildTelemetryBootstrap({ AO_DATA_DIR: dir }, "1.2.3", "linux", "/home/test", true, enabledPolicy);
	expect(bootstrap?.distinctId).toMatch(/^anon_/);
	expect(bootstrap?.distinctId).not.toBe(installId);
});

// The daemon retires an ID by writing the replacement, then removing the marker.
async function rotate(dir: string, next: string): Promise<void> {
	await writeFile(path.join(dir, "telemetry_anonymous_id"), `${next}\n`);
	await rm(path.join(dir, "telemetry_bound_user"), { force: true });
}

test("bootstrapDistinctId never returns an ID retired between the marker check and the ID read", async () => {
	const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
	tempDirs.push(dir);
	const retired = await loadOrCreateTelemetryInstallId(dir);
	await writeFile(path.join(dir, "telemetry_bound_user"), "user_A\n");

	// Rotation completes before the marker check: a missing marker means the ID
	// read afterwards is already the replacement.
	const afterRotation = await bootstrapDistinctId(dir, {
		markerMissing: async (file) => {
			await rotate(dir, "anon_replacement");
			return access(file).then(() => false, () => true);
		},
		loadId: loadOrCreateTelemetryInstallId,
	});
	expect(afterRotation).toBe("anon_replacement");
	expect(afterRotation).not.toBe(retired);
	expect(await loadOrCreateTelemetryInstallId(dir)).toBe(retired);

	// Rotation after the marker check: the marker was present, so the linked ID is withheld.
	await writeFile(path.join(dir, "telemetry_bound_user"), "user_A\n");
	const beforeRotation = await bootstrapDistinctId(dir, {
		markerMissing: (file) => access(file).then(() => false, () => true),
		loadId: loadOrCreateTelemetryInstallId,
	});
	expect(beforeRotation).toMatch(/^anon_/);
	expect(beforeRotation).not.toBe(retired);
});
