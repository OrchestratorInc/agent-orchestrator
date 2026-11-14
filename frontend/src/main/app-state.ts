import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";
import { type InstallSource, resolveInstallSource } from "../shared/install-environment";

export type MigrationStatus = "pending" | "completed" | "declined" | "failed";

export interface MigrationState {
	status: MigrationStatus;
	lastAttemptAt?: string;
	completedAt?: string;
	report?: { projectsImported: number; projectsSkipped: number };
	error?: string;
}

export interface AppStateMarker {
	updateRestartProtocol?: 1;
	schemaVersion: number;
	appPath: string;
	version: string;
	installedAt: string;
	lastReconciledAt: string;
	installSource: string;
	migration?: MigrationState;
	firstRunProjectPrompt?: "pending" | "seen";
	[key: string]: unknown;
}

const SCHEMA_VERSION = 2;

export const APP_STATE_FILE_NAME = "app-state.json";

export interface WriteAppStateOptions {
	updateRestartProtocol?: 1;
	stateDir: string;
	appPath: string;
	version: string;
	installedVia?: string;
	detectedInstallSource?: InstallSource | null;
	now: () => Date;
}

async function readExisting(file: string): Promise<AppStateMarker | null> {
	let raw: string;
	try {
		raw = await readFile(file, "utf8");
	} catch {
		return null;
	}
	try {
		return JSON.parse(raw) as AppStateMarker;
	} catch {
		return null;
	}
}

async function atomicWriteMarker(stateDir: string, marker: AppStateMarker): Promise<void> {
	await mkdir(stateDir, { recursive: true, mode: 0o750 });
	const file = path.join(stateDir, APP_STATE_FILE_NAME);
	const data = `${JSON.stringify(marker, null, 2)}\n`;
	const tmp = path.join(stateDir, `.app-state-${process.pid}-${Date.now()}.json`);
	await writeFile(tmp, data, { mode: 0o600 });
	await rename(tmp, file);
}

export async function writeAppStateMarker(opts: WriteAppStateOptions): Promise<void> {
	const file = path.join(opts.stateDir, APP_STATE_FILE_NAME);
	const existing = await readExisting(file);
	const nowIso = opts.now().toISOString();
	const { updateRestartProtocol: _previousProtocol, ...carried } =
		existing && typeof existing === "object" && !Array.isArray(existing) ? existing : ({} as Partial<AppStateMarker>);

	const marker: AppStateMarker = {
		...carried,
		schemaVersion: SCHEMA_VERSION,
		...(opts.updateRestartProtocol ? { updateRestartProtocol: opts.updateRestartProtocol } : {}),
		appPath: opts.appPath,
		version: opts.version,
		installedAt: existing?.installedAt ?? nowIso,
		lastReconciledAt: nowIso,
		installSource: resolveInstallSource(existing?.installSource, opts.installedVia, opts.detectedInstallSource ?? null),
		...(existing?.migration !== undefined ? { migration: existing.migration } : {}),
		firstRunProjectPrompt: existing?.firstRunProjectPrompt ?? (existing ? "seen" : "pending"),
	};

	await atomicWriteMarker(opts.stateDir, marker);
}

export interface UpdateMigrationOptions {
	stateDir: string;
	migration: MigrationState;
	now: () => Date;
}

export async function updateMigration(opts: UpdateMigrationOptions): Promise<void> {
	const file = path.join(opts.stateDir, APP_STATE_FILE_NAME);
	const existing = await readExisting(file);
	const nowIso = opts.now().toISOString();
	const marker: AppStateMarker = existing
		? { ...existing, migration: opts.migration }
		: {
				schemaVersion: SCHEMA_VERSION,
				appPath: "",
				version: "",
				installedAt: nowIso,
				lastReconciledAt: nowIso,
				installSource: "unknown",
				migration: opts.migration,
			};
	await atomicWriteMarker(opts.stateDir, marker);
}

export async function readMigrationState(stateDir: string): Promise<MigrationState> {
	const existing = await readExisting(path.join(stateDir, APP_STATE_FILE_NAME));
	return existing?.migration ?? { status: "pending" };
}

export async function readFirstRunProjectPrompt(stateDir: string): Promise<"pending" | "seen"> {
	const existing = await readExisting(path.join(stateDir, APP_STATE_FILE_NAME));
	return existing?.firstRunProjectPrompt === "pending" ? "pending" : "seen";
}

export async function claimFirstRunExperimentExposure(stateDir: string): Promise<boolean> {
	try {
		await writeFile(path.join(stateDir, "first-run-project-experiment-exposed"), "1\n", { mode: 0o600, flag: "wx" });
		return true;
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code === "EEXIST") return false;
		throw error;
	}
}

export async function markFirstRunProjectPromptSeen(stateDir: string): Promise<void> {
	const existing = await readExisting(path.join(stateDir, APP_STATE_FILE_NAME));
	if (!existing) throw new Error("App state marker is unavailable");
	if (existing.firstRunProjectPrompt === "seen") return;
	await atomicWriteMarker(stateDir, { ...existing, firstRunProjectPrompt: "seen" });
}
