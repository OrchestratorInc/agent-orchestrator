import { access, link, mkdir, readFile, rmdir, stat, unlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { randomUUID } from "node:crypto";
import type { TelemetryPolicySnapshot } from "./telemetry-policy";

export type TelemetryBootstrap = {
	distinctId: string;
	installId: string;
	appVersion: string;
	platform: NodeJS.Platform;
	/**
	 * Event streams the renderer must not send, from
	 * `AO_TELEMETRY_DISABLED_EVENTS`.
	 *
	 * The daemon enforces the same list on its own billed sink, but renderer
	 * events go straight to PostHog and never pass through it. Without carrying
	 * the policy across the boundary, denying `ao.v2.app.active` would silence
	 * the CLI producer while the renderer kept sending under the same exported
	 * name, which is a kill switch that only half works.
	 */
	disabledEvents: string[];
	eventsEnabled: boolean;
	consentGeneration: string;
};

/** Parses the comma-separated deny list. Never throws: an unusable entry is inert. */
export function parseDisabledEvents(raw: string | undefined): string[] {
	if (!raw) return [];
	return raw
		.split(",")
		.map((name) => name.trim())
		.filter((name) => name !== "");
}

/**
 * Whether the renderer may export to PostHog.
 *
 * Unpackaged builds are off by default so a developer's ordinary session does
 * not land in the production project as a real install. Setting
 * `AO_TELEMETRY_RENDERER=on` opts a dev build back in for deliberate testing.
 */
export function rendererTelemetryEnabled(
	env: Record<string, string | undefined>,
	isPackaged: boolean,
): boolean {
	const explicit = env.AO_TELEMETRY_RENDERER?.trim().toLowerCase();
	if (explicit === "on") return true;
	if (explicit === "off") return false;
	return isPackaged;
}

export function defaultDataDir(
	platform: NodeJS.Platform,
	env: Record<string, string | undefined>,
	homeDir: string,
): string | null {
	void platform;
	if (env.AO_DATA_DIR) return env.AO_DATA_DIR;
	if (!homeDir) return null;
	return path.join(homeDir, ".ao", "data");
}

export async function loadOrCreateTelemetryInstallId(dataDir: string): Promise<string> {
	const file = path.join(dataDir, "telemetry_install_id");
	await mkdir(dataDir, { recursive: true });
	for (let attempt = 0; attempt < 5; attempt++) {
		try {
			const existing = (await readFile(file, "utf8")).trim();
			if (existing) return existing;
			return recoverEmptyTelemetryInstallId(dataDir, file);
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
		}

		const distinctId = `ins_${randomUUID()}`;
		const temporary = path.join(dataDir, `.telemetry_install_id_${randomUUID()}`);
		try {
			await writeFile(temporary, `${distinctId}\n`, { mode: 0o600, flag: "wx" });
			await link(temporary, file);
			return distinctId;
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error;
		} finally {
			await unlink(temporary).catch(() => {});
		}
	}
	throw new Error("telemetry install id creation did not settle");
}

// Both the daemon and Electron use this lock before repairing a legacy empty
// file. Re-read under the lock so a second repair cannot erase the first ID.
async function recoverEmptyTelemetryInstallId(dataDir: string, file: string): Promise<string> {
	const lock = `${file}.recovery`;
	const deadline = Date.now() + 20_000;
	for (;;) {
		try {
			await mkdir(lock, { mode: 0o700 });
			break;
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "EEXIST") throw error;
			let age: number;
			try {
				age = Date.now() - (await stat(lock)).mtimeMs;
			} catch (statError) {
				if ((statError as NodeJS.ErrnoException).code === "ENOENT") continue;
				throw statError;
			}
			if (age > 10_000) {
				await rmdir(lock).catch(() => {});
				continue;
			}
			if (Date.now() > deadline) throw new Error("timed out waiting for telemetry install id recovery");
			await new Promise((resolve) => setTimeout(resolve, 20));
		}
	}
	try {
		try {
			const existing = (await readFile(file, "utf8")).trim();
			if (existing) return existing;
			await unlink(file);
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
		}
		return loadOrCreateTelemetryInstallId(dataDir);
	} finally {
		await rmdir(lock).catch(() => {});
	}
}

/**
 * The ID the renderer's PostHog client starts on. The daemon keeps
 * `telemetry_bound_user` while the install ID may be linked to an AO Cloud
 * account (identify() merged it into that person). Whether that account is still
 * signed in is only known once the session resolves, after init has already
 * captured its first events, so a linked (or uninspectable) ID is withheld: the
 * client starts on a throwaway anonymous ID, and a signed-in user's identify()
 * merges it into the person anyway.
 *
 * The marker is checked before the anonymous ID is read. The daemon writes a
 * replacement anonymous ID before removing the marker. The installation ID
 * remains fixed for event attribution and must not be reused as a linked
 * anonymous distinct ID after sign-out.
 */
export async function bootstrapDistinctId(
	dataDir: string,
	io: { markerMissing: (file: string) => Promise<boolean>; loadId: (dir: string) => Promise<string> } = {
		// Only a confirmed missing marker proves the ID is unlinked; any other
		// failure (EACCES, EIO, ELOOP) means it could not be inspected, so fail closed.
		markerMissing: (file) =>
			access(file).then(
				() => false,
				(error: NodeJS.ErrnoException) => error.code === "ENOENT",
			),
		loadId: loadOrCreateTelemetryInstallId,
	},
): Promise<string> {
	const unlinked = await io.markerMissing(path.join(dataDir, "telemetry_bound_user"));
	if (!unlinked) return `anon_${randomUUID()}`;
	try {
		const anonymousId = (await readFile(path.join(dataDir, "telemetry_anonymous_id"), "utf8")).trim();
		if (anonymousId) return anonymousId;
		throw new Error("empty telemetry anonymous id");
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
	}
	return io.loadId(dataDir);
}

export async function buildTelemetryBootstrap(
	env: Record<string, string | undefined>,
	appVersion: string,
	platform: NodeJS.Platform,
	homeDir = os.homedir(),
	isPackaged = true,
	policy?: TelemetryPolicySnapshot,
): Promise<TelemetryBootstrap | null> {
	// Returning null is the whole switch: initTelemetry() bails on a null
	// bootstrap, so the renderer never constructs a PostHog client at all. That
	// is stronger than gating each capture, because it also removes the SDK's
	// own background requests.
	if (!rendererTelemetryEnabled(env, isPackaged)) return null;
	const dataDir = defaultDataDir(platform, env, homeDir);
	if (!dataDir) return null;
	if (!policy) return null;
	// This policy controls failure reporting only. Existing anonymous PostHog
	// product analytics retain their own packaged/env gate above.
	const eventsEnabled = policy.eventsEnabled && policy.acknowledged;
	return {
		distinctId: await bootstrapDistinctId(dataDir),
		installId: await loadOrCreateTelemetryInstallId(dataDir),
		appVersion,
		platform,
		disabledEvents: parseDisabledEvents(env.AO_TELEMETRY_DISABLED_EVENTS),
		eventsEnabled,
		consentGeneration: policy.consentGeneration,
	};
}
