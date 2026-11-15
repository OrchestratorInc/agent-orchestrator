import { execFile } from "node:child_process";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

const PS_TIMEOUT_MS = 2_000;

export function parseElapsedMs(text: string): number | null {
	const match = /^(?:(?:(\d+)-)?(\d+):)?(\d+):(\d+)$/.exec(text.trim());
	if (!match) return null;
	const [, days = "0", hours = "0", minutes, seconds] = match;
	return (((Number(days) * 24 + Number(hours)) * 60 + Number(minutes)) * 60 + Number(seconds)) * 1000;
}

export type ProcessStartDeps = {
	platform: NodeJS.Platform;
	now: () => number;
	ps: (pid: number) => Promise<string>;
};

const defaultDeps: ProcessStartDeps = {
	platform: process.platform,
	now: Date.now,
	ps: async (pid) => {
		const { stdout } = await execFileAsync("ps", ["-o", "etime=", "-p", String(pid)], { timeout: PS_TIMEOUT_MS });
		return stdout;
	},
};

export async function readProcessStartMs(pid: number, deps: ProcessStartDeps = defaultDeps): Promise<number | null> {
	if (!Number.isSafeInteger(pid) || pid <= 1 || deps.platform === "win32") return null;
	const now = deps.now();
	try {
		const elapsedMs = parseElapsedMs(await deps.ps(pid));
		return elapsedMs === null ? null : now - elapsedMs;
	} catch {
		return null;
	}
}
