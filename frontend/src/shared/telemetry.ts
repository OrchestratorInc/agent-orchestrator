import { access, mkdir, readFile, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { randomUUID } from "node:crypto";
import type { TelemetryPolicySnapshot } from "./telemetry-policy";

export type TelemetryBootstrap = {
	distinctId: string;
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
	try {
		const existing = (await readFile(file, "utf8")).trim();
		if (existing) return existing;
	} catch {
		// Create the id on first use.
	}
	await mkdir(dataDir, { recursive: true });
	const distinctId = `ins_${randomUUID()}`;
	await writeFile(file, `${distinctId}\n`, { mode: 0o600 });
	return distinctId;
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
 * The marker is checked before the ID is read. The daemon retires an ID by
 * writing the replacement and only then removing the marker, so a missing marker
 * guarantees that any ID read afterwards is the replacement, never one that was
 * linked. Reading the ID first would let a rotation land between the two reads
 * and return the retired ID with no marker left to flag it.
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
	const installId = await io.loadId(dataDir);
	return unlinked ? installId : `anon_${randomUUID()}`;
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
		appVersion,
		platform,
		disabledEvents: parseDisabledEvents(env.AO_TELEMETRY_DISABLED_EVENTS),
		eventsEnabled,
		consentGeneration: policy.consentGeneration,
	};
}
