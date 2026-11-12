import { closeSync, constants, fchmodSync, fstatSync, mkdirSync, openSync, renameSync, rmSync, writeSync } from "node:fs";
import path from "node:path";

export const DAEMON_OUTPUT_LOG_FILE = "daemon.log";
export const DAEMON_OUTPUT_LOG_MAX_BYTES = 4 * 1024 * 1024;

// The app-owned daemon writes its slog lines and, when it dies, the Go runtime's
// panic or fatal-error stack to stdout/stderr. Those pipes end in this process,
// so without a file the only record of why a daemon died is lost with the app.
// Writes are synchronous and bounded (one previous file is kept), and like the
// updater record they redact URLs and authorization values and fail open.
function redact(text: string): string {
	return text
		.replace(/https?:\/\/[^\s<>"']+/gi, "[redacted-url]")
		.replace(/\b(?:authorization["']?\s*[:=]\s*["']?|bearer\s+)[^\r\n]+/gi, "[redacted-authorization]");
}

export function createDaemonOutputLog(stateDir: string, maxBytes = DAEMON_OUTPUT_LOG_MAX_BYTES) {
	const directory = path.join(stateDir, "logs");
	const file = path.join(directory, DAEMON_OUTPUT_LOG_FILE);
	const append = (text: string): void => {
		if (!text) return;
		let fd: number | undefined;
		try {
			const data = redact(text);
			const bytes = Buffer.byteLength(data);
			mkdirSync(directory, { recursive: true, mode: 0o700 });
			const open = () => openSync(file, constants.O_CREAT | constants.O_APPEND | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
			fd = open();
			const stat = fstatSync(fd);
			if (!stat.isFile()) return;
			if (stat.size > 0 && stat.size + bytes > maxBytes) {
				closeSync(fd);
				fd = undefined;
				rmSync(`${file}.1`, { force: true });
				renameSync(file, `${file}.1`);
				fd = open();
			}
			fchmodSync(fd, 0o600);
			writeSync(fd, data);
		} catch {
			// Logging must never affect the daemon or the app.
		} finally {
			if (fd !== undefined) {
				try { closeSync(fd); } catch { /* best effort */ }
			}
		}
	};
	return {
		/** Raw daemon stdout/stderr, as received. */
		output: append,
		/** One AO marker line, so a reader can tell where each daemon starts and ends. */
		mark: (message: string): void => append(`--- ${new Date().toISOString()} AO: ${message}\n`),
	};
}
