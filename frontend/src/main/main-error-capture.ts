import type { IpcMain, IpcMainInvokeEvent } from "electron";

// Electron main's handled failures (daemon start and exit, auto-update, IPC
// handlers, hung windows) used to reach only the console and PostHog. This
// module is the single path from those sites to the consented Sentry
// transport. Only fixed categories and reasons cross it; error text never does.

export const MAIN_ERROR_CATEGORIES = ["daemon", "update", "ipc", "window", "acp_runtime"] as const;
export type MainErrorCategory = (typeof MAIN_ERROR_CATEGORIES)[number];

export const MAIN_ERROR_REASONS = [
	// daemon
	"binary_missing", "spawn_failed", "exited", "port_unconfirmed", "identity_mismatch", "datadir_unwritable",
	// update
	"check", "download", "stage", "restart_prep", "install", "return",
	// ipc
	"handler",
	// window
	"unresponsive", "did_fail_load", "render_process_gone",
	// acp_runtime
	"rebuild",
] as const;
export type MainErrorReason = (typeof MAIN_ERROR_REASONS)[number];

export type MainErrorCapture = {
	category: MainErrorCategory;
	reason: MainErrorReason;
	// Only the error's constructor name and stack function names are used.
	error?: unknown;
	// A short fixed detail such as "signal:SIGKILL" or "code:1".
	detail?: string;
};

const CATEGORIES = new Set<string>(MAIN_ERROR_CATEGORIES);
const REASONS = new Set<string>(MAIN_ERROR_REASONS);
const SAFE_DETAIL = /^(?:signal:SIG[A-Z0-9]{1,10}|code:-?\d{1,5}|channel:[a-z][A-Za-z0-9:_-]{0,63}|kind:[a-z-]{1,24})$/;
const PENDING_LIMIT = 20;

type Sink = (capture: MainErrorCapture) => void;

let sink: Sink | null = null;
let pending: MainErrorCapture[] = [];

export function isSafeMainErrorDetail(value: unknown): value is string {
	return typeof value === "string" && SAFE_DETAIL.test(value);
}

export function sanitizeMainErrorCapture(capture: MainErrorCapture): MainErrorCapture | null {
	if (!CATEGORIES.has(capture.category) || !REASONS.has(capture.reason)) return null;
	return {
		category: capture.category,
		reason: capture.reason,
		...(capture.error === undefined ? {} : { error: capture.error }),
		...(isSafeMainErrorDetail(capture.detail) ? { detail: capture.detail } : {}),
	};
}

// Failures before the telemetry policy loads are held (bounded) and handed to
// the sink once it is set. The sink applies consent, so a held capture is
// dropped then if the user opted out.
export function setMainErrorSink(next: Sink | null): void {
	sink = next;
	if (!next) return;
	const held = pending;
	pending = [];
	for (const capture of held) next(capture);
}

export function captureMainError(capture: MainErrorCapture): void {
	const safe = sanitizeMainErrorCapture(capture);
	if (!safe) return;
	if (sink) {
		try { sink(safe); } catch { /* reporting must never break the caller */ }
		return;
	}
	if (pending.length < PENDING_LIMIT) pending.push(safe);
}




let ipcCaptureInstalled = false;

// Wraps ipcMain.handle once so every handler's unexpected throw is reported
// under its fixed channel name. The error still rejects back to the renderer
// exactly as before. Telemetry channels are skipped so a failing capture can
// never report itself.
export function installIpcErrorCapture(ipcMain: Pick<IpcMain, "handle">): void {
	if (ipcCaptureInstalled) return;
	ipcCaptureInstalled = true;
	const original = ipcMain.handle.bind(ipcMain);
	ipcMain.handle = (channel, listener) => {
		if (channel.startsWith("telemetry:")) return original(channel, listener);
		const wrapped = async (event: IpcMainInvokeEvent, ...args: unknown[]) => {
			try {
				return await listener(event, ...args);
			} catch (error) {
				captureMainError({ category: "ipc", reason: "handler", error, detail: `channel:${channel}` });
				throw error;
			}
		};
		return original(channel, wrapped);
	};
}

export function resetMainErrorCaptureForTests(): void {
	sink = null;
	pending = [];
	ipcCaptureInstalled = false;
}
