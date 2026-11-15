import { access, link, lstat, mkdir, readFile, rmdir, stat, unlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { randomInt, randomUUID } from "node:crypto";
import type { TelemetryPolicySnapshot } from "./telemetry-policy";
import { detectCiEnvironment, installDay, normalizeInstallSource, safeOsVersion } from "./install-environment";

export type TelemetryInstallContext = {
	arch: string;
	osVersion?: string;
	installSource: string;
	installedAt?: string;
	isCi: boolean;
	isContainer: boolean;
};

export type TelemetryBootstrap = {
	distinctId: string;
	installId: string;
	daysSinceInstall?: number;
	installTimestampMs?: number;
	lifetimeSessions?: number;
	cloudUserId?: string;
	appVersion: string;
	platform: NodeJS.Platform;
	disabledEvents: string[];
	eventsEnabled: boolean;
	consentGeneration: string;
	install?: TelemetryInstallContext;
	experimentCohort?: number;
};

export async function readTelemetryInstallContext(input: {
	stateDir: string | null;
	arch: string;
	osVersion: string;
	env: Record<string, string | undefined>;
	exists: (file: string) => boolean;
}): Promise<TelemetryInstallContext> {
	let state: { installSource?: unknown; installedAt?: unknown } = {};
	if (input.stateDir) {
		try {
			const parsed: unknown = JSON.parse(await readFile(path.join(input.stateDir, "app-state.json"), "utf8"));
			if (parsed && typeof parsed === "object") state = parsed as typeof state;
		} catch {
		}
	}
	const ci = detectCiEnvironment(input.env, input.exists);
	const installedAt = installDay(typeof state.installedAt === "string" ? state.installedAt : undefined);
	const osVersion = safeOsVersion(input.osVersion);
	return {
		arch: input.arch,
		...(osVersion ? { osVersion } : {}),
		installSource: normalizeInstallSource(typeof state.installSource === "string" ? state.installSource : undefined),
		...(installedAt ? { installedAt } : {}),
		isCi: ci.isCi,
		isContainer: ci.isContainer,
	};
}

export function parseDisabledEvents(raw: string | undefined): string[] {
	if (!raw) return [];
	return raw
		.split(",")
		.map((name) => name.trim())
		.filter((name) => name !== "");
}

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

export const EXPERIMENT_COHORT_BUCKETS = 100;

export async function loadOrAssignExperimentCohort(dataDir: string): Promise<number | undefined> {
	const file = path.join(dataDir, "telemetry_experiment_cohort");
	for (let attempt = 0; attempt < 3; attempt++) {
		try {
			const value = Number((await readFile(file, "utf8")).trim());
			return Number.isInteger(value) && value >= 0 && value < EXPERIMENT_COHORT_BUCKETS ? value : undefined;
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "ENOENT") return undefined;
		}
		const cohort = randomInt(EXPERIMENT_COHORT_BUCKETS);
		const temporary = path.join(dataDir, `.telemetry_experiment_cohort_${randomUUID()}`);
		try {
			await mkdir(dataDir, { recursive: true });
			await writeFile(temporary, `${cohort}\n`, { mode: 0o600, flag: "wx" });
			await link(temporary, file);
			return cohort;
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "EEXIST") return undefined;
		} finally {
			await unlink(temporary).catch(() => {});
		}
	}
	return undefined;
}

const CLOUD_USER_ID_PATTERN = /^[A-Za-z0-9_-]{1,64}$/;

export async function boundCloudUserId(dataDir: string): Promise<string | null> {
	const file = path.join(dataDir, "telemetry_bound_user");
	try {
		if (!(await lstat(file)).isFile()) return null;
		const value = (await readFile(file, "utf8")).trim();
		return CLOUD_USER_ID_PATTERN.test(value) ? value : null;
	} catch {
		return null;
	}
}

export type FirstOpenClaim = "claimed" | "ineligible" | "unknown";

export async function claimFirstOpen(dataDir: string, facts: Pick<TelemetryBootstrap, "daysSinceInstall" | "lifetimeSessions">): Promise<FirstOpenClaim> {
	if (facts.daysSinceInstall !== 0) return "ineligible";
	if (facts.lifetimeSessions === undefined) return "unknown";
	if (facts.lifetimeSessions > 0) return "ineligible";
	try {
		await writeFile(path.join(dataDir, "telemetry_first_open_emitted"), "", { mode: 0o600, flag: "wx" });
		return "claimed";
	} catch {
		return "ineligible";
	}
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

export async function bootstrapDistinctId(
	dataDir: string,
	io: { markerMissing: (file: string) => Promise<boolean>; loadId: (dir: string) => Promise<string> } = {
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
	install?: TelemetryInstallContext,
): Promise<TelemetryBootstrap | null> {
	if (!rendererTelemetryEnabled(env, isPackaged)) return null;
	const dataDir = defaultDataDir(platform, env, homeDir);
	if (!dataDir) return null;
	if (!policy) return null;
	const eventsEnabled = policy.eventsEnabled && policy.acknowledged;
	const experimentCohort = await loadOrAssignExperimentCohort(dataDir);
	const installId = await loadOrCreateTelemetryInstallId(dataDir);
	let daysSinceInstall: number | undefined;
	let installTimestampMs: number | undefined;
	try {
		const installedAt = (await stat(path.join(dataDir, "telemetry_install_id"))).mtimeMs;
		if (installedAt > 0 && installedAt <= Date.now()) {
			installTimestampMs = installedAt;
			daysSinceInstall = Math.floor((Date.now() - installedAt) / 86_400_000);
		}
	} catch {
	}
	return {
		distinctId: await bootstrapDistinctId(dataDir),
		installId,
		...(daysSinceInstall === undefined ? {} : { daysSinceInstall }),
		...(installTimestampMs === undefined ? {} : { installTimestampMs }),
		appVersion,
		platform,
		disabledEvents: parseDisabledEvents(env.AO_TELEMETRY_DISABLED_EVENTS),
		eventsEnabled,
		consentGeneration: policy.consentGeneration,
		...(install ? { install } : {}),
		...(experimentCohort === undefined ? {} : { experimentCohort }),
	};
}
