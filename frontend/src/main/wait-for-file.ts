import { existsSync } from "node:fs";

type WaitForFileOptions = {
	timeoutMs: number;
	intervalMs?: number;
	exists?: (filePath: string) => boolean;
	sleep?: (ms: number) => Promise<void>;
	now?: () => number;
};

/**
 * Whether a file exists now or appears within the timeout. On Windows an
 * installer that is still copying files, or an antivirus scan holding a fresh
 * executable, can hide a bundled binary for a few seconds after launch.
 */
export async function waitForFile(filePath: string, options: WaitForFileOptions): Promise<boolean> {
	const exists = options.exists ?? existsSync;
	const sleep = options.sleep ?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)));
	const now = options.now ?? Date.now;
	const interval = options.intervalMs ?? 500;
	const deadline = now() + options.timeoutMs;
	while (!exists(filePath)) {
		if (now() >= deadline) return false;
		await sleep(interval);
	}
	return true;
}
