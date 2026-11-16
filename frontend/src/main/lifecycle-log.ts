import { closeSync, constants, fchmodSync, fstatSync, mkdirSync, openSync, readFileSync, renameSync, rmSync, writeFileSync, writeSync } from "node:fs";
import path from "node:path";

// Local, crash-survivable record of why Electron main and its daemon child
// started, stopped and were respawned (logs/desktop-lifecycle.jsonl beside
// running.json, normally ~/.ao/logs next to updater.jsonl). It exists because
// a Linux user lost the app with no log, core dump or Sentry event to say why.
// Never forwarded anywhere: it is read only when a user attaches it to a bug
// report.
//
// Durability: every record is a synchronous open/append/close, so it is in
// the kernel's page cache before record() returns and survives the process
// dying on the next instruction (not a power loss; there is no fsync).
//
// Privacy: call sites pass small scalar facts only (codes, signals, pids,
// reasons). Free text (error messages, stacks) goes through scrubText, which
// drops URLs, credentials, long opaque tokens and long quoted strings, and
// folds the home directory to "~". Prompts and terminal output never reach
// this module.

export const LIFECYCLE_LOG_FILE = "desktop-lifecycle.jsonl";
export const LIFECYCLE_RUN_MARKER_FILE = "desktop-run.json";
export const LIFECYCLE_LOG_MAX_BYTES = 2 * 1024 * 1024;
/** Rotated generations kept next to the live file (.1 .. .N). */
export const LIFECYCLE_LOG_KEEP = 2;
const MAX_LINE_BYTES = 16 * 1024;
const EVENT_NAME = /^[a-z][a-z0-9_]{0,63}$/;
const FIELD_NAME = /^[A-Za-z][A-Za-z0-9_]{0,63}$/;

export type LifecycleFields = Record<string, string | number | boolean | null | undefined>;
export type LifecycleRecord = (event: string, fields?: LifecycleFields, error?: unknown) => void;

export function scrubText(value: string, homeDir: string, limit = 300): string {
	let out = value;
	if (homeDir.length > 1) out = out.split(homeDir).join("~");
	out = out
		.replace(/[a-z][a-z0-9+.-]*:\/\/[^\s<>"'`)]+/gi, "[url]")
		.replace(/\b(?:bearer|basic)\s+[^\s"']+/gi, "[credential]")
		// A quoted value is redacted through its closing quote, spaces included.
		.replace(/\b([A-Za-z_]*(?:token|secret|password|passwd|apikey|api_key|api-key|authorization|cookie|credential)[A-Za-z_]*)(["']?\s*[:=]\s*)(?:"[^"\n]*"|'[^'\n]*'|[^\s"',;]+)/gi, "$1$2[redacted]")
		.replace(/(["'`])[^"'`\n]{40,}\1/g, "$1[text]$1")
		.replace(/[A-Za-z0-9+_=-]{32,}/g, "[opaque]");
	return out.length > limit ? `${out.slice(0, limit)}…` : out;
}

export function errorFacts(error: unknown, homeDir: string): Record<string, unknown> {
	if (!(error instanceof Error)) {
		return { type: typeof error, ...(typeof error === "string" ? { message: scrubText(error, homeDir) } : {}) };
	}
	const code = (error as { code?: unknown }).code;
	const frames = (error.stack ?? "")
		.split("\n")
		.filter((line) => /^\s*at /.test(line))
		.slice(0, 12)
		.map((line) => scrubText(line.trim(), homeDir, 240));
	return {
		name: scrubText(error.name, homeDir, 80),
		message: scrubText(error.message, homeDir),
		...(typeof code === "string" || typeof code === "number" ? { code: scrubText(String(code), homeDir, 64) } : {}),
		...(frames.length ? { frames } : {}),
	};
}

export type LifecycleLogOptions = {
	directory: string;
	version: string;
	homeDir: string;
	now?: () => Date;
	pid?: number;
	maxBytes?: number;
};

export function createLifecycleLog(options: LifecycleLogOptions): LifecycleRecord {
	const file = path.join(options.directory, LIFECYCLE_LOG_FILE);
	const maxBytes = options.maxBytes ?? LIFECYCLE_LOG_MAX_BYTES;
	const now = options.now ?? (() => new Date());
	const pid = options.pid ?? process.pid;
	return (event, fields = {}, error) => {
		if (!EVENT_NAME.test(event)) return;
		let fd: number | undefined;
		try {
			const safe: Record<string, unknown> = {};
			for (const [key, value] of Object.entries(fields)) {
				if (!FIELD_NAME.test(key) || value === undefined) continue;
				if (typeof value === "string") safe[key] = scrubText(value, options.homeDir, 200);
				else if (typeof value === "number") safe[key] = Number.isFinite(value) ? value : null;
				else safe[key] = value;
			}
			const line = `${JSON.stringify({
				ts: now().toISOString(),
				pid,
				version: scrubText(options.version, options.homeDir, 64),
				event,
				...safe,
				...(error === undefined ? {} : { error: errorFacts(error, options.homeDir) }),
			})}\n`;
			if (Buffer.byteLength(line) > MAX_LINE_BYTES) return;
			mkdirSync(options.directory, { recursive: true, mode: 0o700 });
			const open = () => openSync(file, constants.O_CREAT | constants.O_APPEND | constants.O_WRONLY | constants.O_NOFOLLOW, 0o600);
			fd = open();
			const stat = fstatSync(fd);
			if (!stat.isFile()) return;
			if (stat.size + Buffer.byteLength(line) > maxBytes) {
				closeSync(fd);
				fd = undefined;
				rotate(file);
				fd = open();
			}
			fchmodSync(fd, 0o600);
			writeSync(fd, line);
		} catch {
			// Logging must never change app behavior or recurse into console.
		} finally {
			if (fd !== undefined) {
				try { closeSync(fd); } catch { /* best effort */ }
			}
		}
	};
}

function rotate(file: string): void {
	rmSync(`${file}.${LIFECYCLE_LOG_KEEP}`, { force: true });
	for (let generation = LIFECYCLE_LOG_KEEP - 1; generation >= 1; generation--) {
		try { renameSync(`${file}.${generation}`, `${file}.${generation + 1}`); } catch { /* generation absent */ }
	}
	renameSync(file, `${file}.1`);
}

type RunMarker = { pid: number; startedAt: string; version: string };

/**
 * Records this run in a marker file and reports a previous run that never
 * reached a clean process exit (crash, SIGKILL, power loss). Returns a function
 * that removes the marker; call it from process "exit".
 */
export function markRunStarted(options: {
	directory: string;
	record: LifecycleRecord;
	version: string;
	pid?: number;
	now?: () => Date;
	processAlive: (pid: number) => boolean;
}): () => void {
	const marker = path.join(options.directory, LIFECYCLE_RUN_MARKER_FILE);
	const pid = options.pid ?? process.pid;
	try {
		const previous = JSON.parse(readFileSync(marker, "utf8")) as Partial<RunMarker>;
		if (typeof previous.pid === "number" && previous.pid !== pid) {
			options.record("previous_run_unclean", {
				previousPid: previous.pid,
				previousStartedAt: typeof previous.startedAt === "string" ? previous.startedAt : undefined,
				previousVersion: typeof previous.version === "string" ? previous.version : undefined,
				previousPidAlive: options.processAlive(previous.pid),
			});
		}
	} catch {
		// No marker: the previous run exited cleanly or this is the first run.
	}
	try {
		mkdirSync(options.directory, { recursive: true, mode: 0o700 });
		const body: RunMarker = { pid, startedAt: (options.now ?? (() => new Date()))().toISOString(), version: options.version };
		writeFileSync(marker, JSON.stringify(body), { mode: 0o600 });
	} catch {
		// Without a marker the next run just cannot report an unclean exit.
	}
	return () => {
		try {
			const current = JSON.parse(readFileSync(marker, "utf8")) as Partial<RunMarker>;
			if (current.pid === pid) rmSync(marker, { force: true });
		} catch { /* already gone */ }
	};
}

/** Linux facts that explain who can take the app down with it. */
export function linuxProcessContext(readText: (file: string) => string = (file) => readFileSync(file, "utf8")): LifecycleFields {
	const fields: LifecycleFields = {};
	try {
		const unified = readText("/proc/self/cgroup").split("\n").find((line) => line.startsWith("0::"));
		if (unified) fields.cgroup = unified.slice(3).trim().slice(0, 200);
	} catch { /* not Linux or /proc unavailable */ }
	try {
		const adj = Number.parseInt(readText("/proc/self/oom_score_adj").trim(), 10);
		if (Number.isFinite(adj)) fields.oomScoreAdj = adj;
	} catch { /* unavailable */ }
	return fields;
}

type ProcessEmit = (event: string | symbol, ...args: unknown[]) => boolean;

/**
 * Observes unhandled promise rejections without changing what Node does with
 * them. Adding an "unhandledRejection" listener would mark every rejection as
 * handled and turn a crash into a silent continue (or the reverse if the
 * listener rethrew), so this wraps process.emit and returns its result as is.
 */
export function observeUnhandledRejections(proc: { emit: ProcessEmit }, onRejection: (reason: unknown) => void): () => void {
	const original = proc.emit;
	const wrapped: ProcessEmit = function (this: unknown, event, ...args) {
		if (event === "unhandledRejection") {
			try { onRejection(args[0]); } catch { /* observation only */ }
		}
		return original.apply(this, [event, ...args]);
	};
	proc.emit = wrapped;
	return () => {
		if (proc.emit === wrapped) proc.emit = original;
	};
}
