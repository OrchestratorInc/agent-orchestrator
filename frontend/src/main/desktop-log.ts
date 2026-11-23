import { appendFileSync, chmodSync, existsSync, mkdirSync, renameSync, rmSync, statSync } from "node:fs";
import path from "node:path";
import { formatWithOptions } from "node:util";

const MAX_BYTES = 5 * 1024 * 1024;

export function createDesktopLogWriter(dataDir: string) {
	const file = path.join(dataDir, "desktop.log");
	mkdirSync(dataDir, { recursive: true, mode: 0o700 });
	appendFileSync(file, "", { mode: 0o600 });
	chmodSync(file, 0o600);
	if (existsSync(`${file}.1`)) chmodSync(`${file}.1`, 0o600);
	let size = statSync(file).size;
	return (level: string, args: unknown[]) => {
		const line = Buffer.from(`${new Date().toISOString()} [${level}] ${formatWithOptions({ depth: 5, maxStringLength: MAX_BYTES, maxArrayLength: 100 }, ...args)}\n`);
		const entry = line.subarray(0, MAX_BYTES);
		if (size + entry.length > MAX_BYTES) {
			rmSync(`${file}.1`, { force: true });
			renameSync(file, `${file}.1`);
			size = 0;
		}
		appendFileSync(file, entry, { mode: 0o600 });
		size += entry.length;
	};
}

export function initializeDesktopLog(dataDir: string) {
	try {
		const write = createDesktopLogWriter(dataDir);
		const record = (level: string, args: unknown[]) => {
			try { write(level, args); }
			catch (error) { process.stderr.write(`Could not write desktop.log: ${String(error)}\n`); }
		};
		for (const level of ["log", "warn", "error"] as const) {
			const original = console[level].bind(console);
			console[level] = (...args: unknown[]) => { record(level, args); original(...args); };
		}
		process.on("uncaughtExceptionMonitor", (error) => record("uncaught", [error]));
	} catch (error) { console.error("Could not initialize desktop.log", error); }
}
