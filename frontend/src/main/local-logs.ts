import { closeSync, constants, fchmodSync, fstatSync, mkdirSync, openSync, renameSync, rmSync, writeSync } from "node:fs";
import path from "node:path";
import { formatWithOptions } from "node:util";

// Local, size-capped logs under <AO state dir>/logs (~/.ao/logs, or
// ~/.ao/dev/logs for a dev build). The daemon writes daemon.log itself, so it
// is captured in every launch mode; this module owns main.log, the Electron
// main process's console output. A packaged app has no console, so without the
// file a failure leaves nothing behind.
export const LOGS_DIRECTORY = "logs";
export const MAIN_LOG_FILE = "main.log";
export const DAEMON_LOG_FILE = "daemon.log";
export const MAIN_LOG_MAX_BYTES = 5 * 1024 * 1024;
export const MAIN_LOG_BACKUPS = 3;
const MAX_ENTRY_BYTES = 64 * 1024;

/** The logs folder for an AO state dir: the directory holding running.json. */
export function logsDirectory(stateDir: string): string {
	return path.join(stateDir, LOGS_DIRECTORY);
}

/** A logs folder for display, with the home directory shortened to "~". */
export function displayLogsDirectory(directory: string, homeDir: string): string {
	if (!homeDir) return directory;
	if (directory === homeDir) return "~";
	return directory.startsWith(homeDir + path.sep) ? `~${directory.slice(homeDir.length)}` : directory;
}

const REDACTED = "[redacted]";

// Mirrors the daemon's daemon.log redaction (backend/internal/daemonlog/redact.go)
// so both files follow one rule: keep URLs and messages readable, drop the
// secret parts.
const REDACTIONS: Array<[RegExp, string]> = [
	// Credentials embedded in a URL: scheme://user:pass@host.
	[/\b([a-z][a-z0-9+.-]*:\/\/)[^/\s:@"']+(?::[^/\s@"']*)?@/gi, `$1${REDACTED}@`],
	// Authorization headers, quoted or not, with or without a scheme word.
	[/(authorization["']?\s*[:=]\s*["']?(?:(?:bearer|token|basic)\s+)?)[^\s"',]+/gi, `$1${REDACTED}`],
	[/\b(bearer\s+)[a-z0-9._~+/=-]+/gi, `$1${REDACTED}`],
	// Secret-bearing query parameters, including signed-URL signatures.
	[/([?&](?:api[_-]?key|key|token|access[_-]?token|refresh[_-]?token|id[_-]?token|secret|client[_-]?secret|password|auth|code|sig|signature|x-amz-signature|x-amz-credential|x-amz-security-token)=)[^&\s"'#]+/gi, `$1${REDACTED}`],
	// key=value / key: value pairs whose key names a secret. The key must end in
	// the secret word, so counters such as input_tokens=12 stay readable.
	[/(\b[a-z0-9_.-]*(?:api[_-]?key|token|secret|password|passwd)["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s"',}&]+)/gi, `$1${REDACTED}`],
	// Well-known token shapes that can appear anywhere in a message.
	[/\b(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,})\b/g, REDACTED],
	[/\bsk-[A-Za-z0-9_-]{8,}/g, REDACTED],
	[/\bxox[abprs]-[A-Za-z0-9-]{8,}/g, REDACTED],
	[/\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+/g, REDACTED],
];

/**
 * Removes credentials from a log entry and shortens the home directory to "~",
 * so a log a user attaches to a report carries neither secrets nor their
 * account name.
 */
export function redactLogText(value: string, homeDir = ""): string {
	let out = value;
	for (const [pattern, replacement] of REDACTIONS) out = out.replace(pattern, replacement);
	return homeDir.length > 1 ? out.split(homeDir).join("~") : out;
}

/**
 * Appends whole entries to `file`, owner-only (0600), rotating to
 * `file.1` ... `file.<backups>` before an entry would push it past `maxBytes`.
 * Synchronous so the last entries survive an immediate quit; every failure is
 * swallowed, because logging must never break the app or recurse into console.
 */
export function createRotatingLogWriter(file: string, options: { maxBytes: number; backups: number }): (entry: string) => void {
	const directory = path.dirname(file);
	const open = () => openSync(file, constants.O_CREAT | constants.O_APPEND | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
	return (entry) => {
		let fd: number | undefined;
		try {
			const bytes = Buffer.byteLength(entry);
			mkdirSync(directory, { recursive: true, mode: 0o700 });
			fd = open();
			const stat = fstatSync(fd);
			if (!stat.isFile()) return;
			if (stat.size > 0 && stat.size + bytes > options.maxBytes) {
				closeSync(fd);
				fd = undefined;
				rmSync(`${file}.${options.backups}`, { force: true });
				for (let i = options.backups - 1; i >= 1; i--) {
					try {
						renameSync(`${file}.${i}`, `${file}.${i + 1}`);
					} catch {
						// A missing backup is the normal case on a young log.
					}
				}
				renameSync(file, `${file}.1`);
				fd = open();
			}
			fchmodSync(fd, 0o600);
			writeSync(fd, entry);
		} catch {
			// Best effort: no console recursion, no effect on the caller.
		} finally {
			if (fd !== undefined) {
				try {
					closeSync(fd);
				} catch {
					// Best effort.
				}
			}
		}
	};
}

type ConsoleLevel = "log" | "info" | "warn" | "error";
const CONSOLE_LEVELS: ConsoleLevel[] = ["log", "info", "warn", "error"];
const LEVEL_LABEL: Record<ConsoleLevel | "fatal", string> = {
	log: "INFO ",
	info: "INFO ",
	warn: "WARN ",
	error: "ERROR",
	fatal: "FATAL",
};

/** Formats one main.log entry: ISO timestamp, level, redacted message. */
export function formatMainLogEntry(level: ConsoleLevel | "fatal", args: unknown[], homeDir: string, now: Date): string {
	let message = redactLogText(formatWithOptions({ colors: false, depth: 4, breakLength: Infinity }, ...args), homeDir);
	if (Buffer.byteLength(message) > MAX_ENTRY_BYTES) message = `${message.slice(0, MAX_ENTRY_BYTES)} [truncated]`;
	return `${now.toISOString()} ${LEVEL_LABEL[level]} ${message}\n`;
}

export type MainLog = {
	/** The main.log path. */
	file: string;
	/** Runs `fn` with its console output kept out of main.log (it still reaches the console). */
	skipFile: (fn: () => void) => void;
};

/**
 * Tees the main process's console.log/info/warn/error into main.log, and
 * records uncaught exceptions without changing how they are handled. Output
 * that already has its own file (the daemon's piped stderr, which the daemon
 * writes to daemon.log itself) goes through `skipFile` so it is not stored twice.
 */
export function installMainLog(options: {
	directory: string;
	homeDir: string;
	target?: Pick<Console, ConsoleLevel>;
	processRef?: Pick<NodeJS.Process, "on">;
	now?: () => Date;
}): MainLog {
	const file = path.join(options.directory, MAIN_LOG_FILE);
	const write = createRotatingLogWriter(file, { maxBytes: MAIN_LOG_MAX_BYTES, backups: MAIN_LOG_BACKUPS });
	const target = options.target ?? console;
	const now = options.now ?? (() => new Date());
	let skipping = 0;
	const record = (level: ConsoleLevel | "fatal", args: unknown[]) => {
		if (skipping > 0) return;
		try {
			write(formatMainLogEntry(level, args, options.homeDir, now()));
		} catch {
			// Formatting a hostile value must not break the console call.
		}
	};
	for (const level of CONSOLE_LEVELS) {
		const original = target[level].bind(target);
		target[level] = (...args: unknown[]) => {
			original(...args);
			record(level, args);
		};
	}
	(options.processRef ?? process).on("uncaughtExceptionMonitor", (error: Error, origin: string) => {
		record("fatal", [origin, error]);
	});
	return {
		file,
		skipFile: (fn) => {
			skipping++;
			try {
				fn();
			} finally {
				skipping--;
			}
		},
	};
}
