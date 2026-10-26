import { execFile } from "node:child_process";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

const PS_TIMEOUT_MS = 2_000;

/**
 * Parse `ps -o etime=` output, `[[dd-]hh:]mm:ss`, into milliseconds. Returns
 * null for anything else. Unlike `lstart`, etime has no locale, month names or
 * timezone to get wrong.
 */
export function parseElapsedMs(text: string): number | null {
	const match = /^(?:(?:(\d+)-)?(\d+):)?(\d+):(\d+)$/.exec(text.trim());
	if (!match) return null;
	const [, days = "0", hours = "0", minutes, seconds] = match;
	return (((Number(days) * 24 + Number(hours)) * 60 + Number(minutes)) * 60 + Number(seconds)) * 1000;
}

export type ProcessStartDeps = {
	platform: NodeJS.Platform;
	now: () => number;
	/** stdout of `ps -o etime= -p <pid>`; rejects when ps exits non-zero (no such process). */
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

/**
 * When the live process with this PID started (epoch ms), or null when it is
 * not running or the OS gives no way to ask. Callers use it to tell the process
 * that wrote a run-file from a later one that reused its PID, so null always
 * means "cannot vouch for it", never a reason to signal.
 *
 * ponytail: Windows has no ps, so it reports null there and a hung daemon that
 * stopped answering /healthz is no longer force-killed; the replacement daemon
 * then starts beside it (see daemon-takeover.ts). Add a Windows reader through
 * PowerShell Get-Process once it can be verified on a Windows host.
 */
export async function readProcessStartMs(pid: number, deps: ProcessStartDeps = defaultDeps): Promise<number | null> {
	if (!Number.isSafeInteger(pid) || pid <= 1 || deps.platform === "win32") return null;
	// Sampled just before ps runs. ps reports whole seconds, truncated, and reads
	// the process table slightly after this clock reading, so the estimate is off by
	// up to about a second in either direction; the caller's skew allowance covers it.
	const now = deps.now();
	try {
		const elapsedMs = parseElapsedMs(await deps.ps(pid));
		return elapsedMs === null ? null : now - elapsedMs;
	} catch {
		return null;
	}
}
