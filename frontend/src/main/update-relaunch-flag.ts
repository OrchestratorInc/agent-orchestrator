import { randomUUID } from "node:crypto";
import { mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import path from "node:path";

const RELAUNCH_FLAG_FILE = "relaunch-flag.json";
const FLAG_LIFETIME_MS = 30 * 60_000;

type RelaunchFlag = {
	version: string;
	fromPID: number;
	startedAt: number;
};

function flagDir(stateDir: string): string {
	return path.join(stateDir, "update-restart");
}

async function atomicJSON(file: string, value: unknown): Promise<void> {
	const temporary = `${file}.${randomUUID()}.tmp`;
	await writeFile(temporary, JSON.stringify(value), { mode: 0o600 });
	await rename(temporary, file);
}

export async function markUpdateRelaunch(options: {
	stateDir: string;
	version: string;
	pid?: number;
	now?: number;
}): Promise<void> {
	const dir = flagDir(options.stateDir);
	await mkdir(dir, { recursive: true, mode: 0o700 });
	const flag: RelaunchFlag = {
		version: options.version,
		fromPID: options.pid ?? process.pid,
		startedAt: options.now ?? Date.now(),
	};
	await atomicJSON(path.join(dir, RELAUNCH_FLAG_FILE), flag);
}

export async function consumeUpdateRelaunchFlag(options: {
	stateDir: string;
	version: string;
	pid?: number;
	now?: number;
}): Promise<boolean> {
	const file = path.join(flagDir(options.stateDir), RELAUNCH_FLAG_FILE);
	let contents: string;
	try {
		contents = await readFile(file, "utf8");
	} catch {
		return false;
	}
	await rm(file, { force: true }).catch(() => undefined);
	let parsed: unknown;
	try {
		parsed = JSON.parse(contents);
	} catch {
		return false;
	}
	if (typeof parsed !== "object" || parsed === null) {
		return false;
	}
	const flag = parsed as Partial<RelaunchFlag>;
	if (typeof flag.startedAt !== "number") {
		return false;
	}
	const age = (options.now ?? Date.now()) - flag.startedAt;
	if (
		typeof flag.version !== "string" ||
		typeof flag.fromPID !== "number" ||
		!Number.isInteger(flag.fromPID) ||
		flag.fromPID <= 0 ||
		!Number.isFinite(age) ||
		age < 0 ||
		age > FLAG_LIFETIME_MS ||
		flag.version !== options.version
	) {
		return false;
	}
	return true;
}
