import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";
import type { UpdateErrorInfo } from "../shared/update-error-message";

export type UpdateInstallResult = void | {
	state: "confirmation-required";
	version: string;
	releaseNotes?: string;
};

export type UpdateChannel = "latest" | "nightly";

export interface FeaturePin {
	pr: number;
}

export interface UpdateSettings {
	enabled: boolean;
	channel: UpdateChannel;
	nightlyAck: boolean;
	feature: FeaturePin | null;
	macDifferentialUpdates?: boolean;
}

export type UpdateState =
	"idle" | "checking" | "available" | "not-available" | "downloading" | "preparing" | "downloaded" | "error" | "unsupported";

export interface UpdateStatus {
	state: UpdateState;
	version?: string;
	percent?: number;
	transferred?: number;
	total?: number;
	bytesPerSecond?: number;
	message?: string;
	checkedAt?: number;
	requestId?: string;
	stagedAt?: number;
	escalated?: boolean;
	releaseNotes?: string;
	staged?: { version?: string; stagedAt: number; escalated: boolean; ready?: boolean; releaseNotes?: string };
	staleCheckNudge?: boolean;
	checksFailing?: boolean;
	checkError?: string;
	errorInfo?: UpdateErrorInfo;
	checkErrorInfo?: UpdateErrorInfo;
	netError?: boolean;
}

export const UPDATE_SETTINGS_FILE_NAME = "update-settings.json";

const DEFAULTS: UpdateSettings = {
	enabled: false,
	channel: "latest",
	nightlyAck: false,
	feature: null,
	macDifferentialUpdates: false,
};
let settingsOperationQueue: Promise<void> = Promise.resolve();

function coerceFeature(raw: unknown): FeaturePin | null {
	if (raw === null || raw === undefined) return null;
	if (typeof raw !== "object") return null;
	const o = raw as Record<string, unknown>;
	const pr = typeof o.pr === "number" && Number.isInteger(o.pr) && o.pr > 0 ? o.pr : null;
	return pr !== null ? { pr } : null;
}

function coerce(raw: unknown): UpdateSettings {
	const o = (raw ?? {}) as Record<string, unknown>;
	return {
		enabled: o.enabled === true,
		channel: o.channel === "nightly" ? "nightly" : "latest",
		nightlyAck: o.nightlyAck === true,
		feature: coerceFeature(o.feature),
		macDifferentialUpdates: o.macDifferentialUpdates === true && (o.feature === null || coerceFeature(o.feature) !== null),
	};
}

export function macDifferentialUpdatesEnabled(input: {
	platform: NodeJS.Platform;
	settings: Pick<UpdateSettings, "channel" | "feature" | "macDifferentialUpdates">;
}): boolean {
	return (
		input.platform === "darwin" &&
		input.settings.channel === "nightly" &&
		input.settings.feature === null &&
		input.settings.macDifferentialUpdates === true
	);
}

async function readUpdateSettingsUnlocked(stateDir: string): Promise<UpdateSettings> {
	let raw: string;
	try {
		raw = await readFile(path.join(stateDir, UPDATE_SETTINGS_FILE_NAME), "utf8");
	} catch {
		return { ...DEFAULTS };
	}
	try {
		return coerce(JSON.parse(raw));
	} catch {
		return { ...DEFAULTS };
	}
}

async function writeUpdateSettingsUnlocked(stateDir: string, settings: UpdateSettings): Promise<void> {
	await mkdir(stateDir, { recursive: true, mode: 0o750 });
	const file = path.join(stateDir, UPDATE_SETTINGS_FILE_NAME);
	const data = `${JSON.stringify(coerce(settings), null, 2)}\n`;
	const tmp = path.join(stateDir, `.update-settings-${process.pid}-${Date.now()}.json`);
	await writeFile(tmp, data, { mode: 0o600 });
	await rename(tmp, file);
}

async function runSettingsOperation<T>(operation: () => Promise<T>): Promise<T> {
	const queued = settingsOperationQueue.then(operation, operation);
	settingsOperationQueue = queued.then(
		() => undefined,
		() => undefined,
	);
	return queued;
}

export async function readUpdateSettings(stateDir: string): Promise<UpdateSettings> {
	return readUpdateSettingsUnlocked(stateDir);
}

export async function writeUpdateSettings(stateDir: string, settings: UpdateSettings): Promise<void> {
	await runSettingsOperation(() => writeUpdateSettingsUnlocked(stateDir, settings));
}

export async function updateUpdateSettings(
	stateDir: string,
	update: (current: UpdateSettings) => UpdateSettings | Promise<UpdateSettings>,
): Promise<UpdateSettings> {
	return runSettingsOperation(async () => {
		const current = await readUpdateSettingsUnlocked(stateDir);
		const candidate = await update(current);
		if (candidate === current) return current;
		const next = coerce(candidate);
		await writeUpdateSettingsUnlocked(stateDir, next);
		return next;
	});
}
