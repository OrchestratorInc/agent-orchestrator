import {
	createWriteStream,
	mkdirSync,
	renameSync,
	statSync,
	type WriteStream,
} from "node:fs";
import path from "node:path";

// Durable daemon log for the desktop-owned launch (issue #2689). Without it the
// daemon's stderr — including a panic stack — lives only in the Electron console
// and dies with the app, so a crash leaves nothing to correlate with the request
// ID the API handed out. Keep-daemon mode redirects stdio to this same file at
// spawn time instead, so this writer is only for the piped path. The log is
// written in packaged builds too — a crash in the installed app is exactly the
// case with nothing else to look at; dev runs keep theirs under ~/.ao/dev/ so
// the two states don't mix.
export const DAEMON_LOG_MAX_BYTES = 8 * 1024 * 1024;

let daemonLogStream: WriteStream | undefined;
let daemonLogBytes = 0;
let daemonLogTarget: string | undefined;
let daemonLogMaxBytes = DAEMON_LOG_MAX_BYTES;
// Flush of a stream a rotation already replaced; closeDaemonLog waits for it
// too, so a close-then-read (tests, quit hooks) never races the old stream's
// last write to disk.
let daemonLogFlush: Promise<void> = Promise.resolve();

export function openDaemonLog(logPath: string, maxBytes: number = DAEMON_LOG_MAX_BYTES): void {
	void closeDaemonLog();
	daemonLogTarget = logPath;
	daemonLogMaxBytes = maxBytes;
	try {
		mkdirSync(path.dirname(logPath), { recursive: true });
		// Rotate before appending so one long-lived install cannot grow the log
		// without bound; one generation back is enough to survive a crash loop.
		let size = 0;
		try {
			size = statSync(logPath).size;
		} catch {
			size = 0; // absent on first run
		}
		if (size >= maxBytes) {
			try {
				renameSync(logPath, `${logPath}.1`);
				size = 0;
			} catch {
				// Rotation is best-effort; appending to an oversized log still beats
				// losing the crash output entirely.
			}
		}
		daemonLogBytes = size;
		const stream = createWriteStream(logPath, { flags: "a" });
		stream.on("error", () => {
			// A failed log write must never take the app down with it. Only the
			// stream that is still current culls itself: a late error from a
			// stream an earlier rotation already replaced must not disable the
			// replacement (PR #3892 review).
			if (daemonLogStream === stream) daemonLogStream = undefined;
		});
		daemonLogStream = stream;
	} catch {
		console.warn(`AO: daemon log unavailable: ${logPath}`);
		daemonLogStream = undefined;
	}
}

// Resolves once the current stream has flushed what it was given. The app never
// awaits this; the promise exists so tests can close, then assert on the file.
export function closeDaemonLog(): Promise<void> {
	const stream = daemonLogStream;
	daemonLogStream = undefined;
	daemonLogBytes = 0;
	const previous = daemonLogFlush;
	if (!stream) return previous;
	const ended = new Promise<void>((resolve) => {
		stream.end(() => resolve());
	});
	daemonLogFlush = ended;
	return ended.then(() => {
		if (daemonLogFlush === ended) daemonLogFlush = Promise.resolve();
		return previous;
	});
}

export function writeDaemonLog(text: string): void {
	if (!daemonLogStream) return;
	daemonLogStream.write(text);
	daemonLogBytes += Buffer.byteLength(text);
	if (daemonLogTarget && daemonLogBytes >= daemonLogMaxBytes) {
		openDaemonLog(daemonLogTarget, daemonLogMaxBytes);
	}
}

// Test-only view of the module state; production code goes through the three
// functions above. Exposed so a test can hold a replaced stream and fire a late
// error at it (the PR #3892 regression).
export function daemonLogStreamForTest(): WriteStream | undefined {
	return daemonLogStream;
}
